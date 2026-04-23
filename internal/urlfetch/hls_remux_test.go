package urlfetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// requireFFmpeg skips the test if ffmpeg is unavailable. Matches the pattern in
// handler/stream_test.go so CI machines without ffmpeg can still run unit tests.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}
}

// makeHLSFixture produces a VOD HLS playlist + .ts segments of durationSec
// seconds inside dir and returns the playlist's basename. 1-second segments
// mean durationSec segments are written, which matters for tests that need
// the remuxer to spend enough wall time writing output to exercise the
// background watcher (progress/size cap).
func makeHLSFixture(t *testing.T, dir string, durationSec int) string {
	t.Helper()
	requireFFmpeg(t)
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "color=black:size=64x64:rate=1",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", fmt.Sprintf("%d", durationSec),
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "1",
		"-c:a", "aac",
		"-hls_time", "1",
		"-hls_segment_type", "mpegts",
		"-hls_list_size", "0",
		"-hls_playlist_type", "vod",
		"-f", "hls",
		filepath.Join(dir, "playlist.m3u8"),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("makeHLSFixture: %v\nstderr: %s", err, stderr.String())
	}
	return "playlist.m3u8"
}

// slowHLSServer wraps a directory FileServer so each .ts fetch waits at least
// perSegment before responding. Extends remux wall time enough that the 500 ms
// watcher ticks observe output growth (progress test) and can catch a cap
// breach before ffmpeg finishes (size cap test).
func slowHLSServer(t *testing.T, dir string, perSegment time.Duration) *httptest.Server {
	t.Helper()
	fs := http.FileServer(http.Dir(dir))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".ts") {
			time.Sleep(perSegment)
		}
		fs.ServeHTTP(w, r)
	}))
}

func TestRunHLSRemux_Success(t *testing.T) {
	fixtureDir := t.TempDir()
	playlistName := makeHLSFixture(t, fixtureDir, 1)

	srv := httptest.NewServer(http.FileServer(http.Dir(fixtureDir)))
	defer srv.Close()

	outDir := t.TempDir()
	tmpPath := filepath.Join(outDir, "out.mp4")

	err := runHLSRemux(context.Background(), srv.URL+"/"+playlistName, tmpPath, nil, MaxBytes)
	if err != nil {
		t.Fatalf("runHLSRemux: %v", err)
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(data) < 8 {
		t.Fatalf("output too short: %d bytes", len(data))
	}
	// MP4 container signature: bytes [4:8] must spell "ftyp".
	if string(data[4:8]) != "ftyp" {
		t.Errorf("output is not MP4 (no ftyp box): % x", data[:16])
	}
}

func TestRunHLSRemux_ContextCancel(t *testing.T) {
	requireFFmpeg(t)
	// Serve a playlist that points at a segment URL that blocks until the
	// test ends, so ffmpeg stalls on the HTTP read. We cancel ctx mid-flight
	// and verify the process terminates quickly.
	blockCh := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/playlist.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.0,\nslow.ts\n#EXT-X-ENDLIST\n"))
	})
	mux.HandleFunc("/slow.ts", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-blockCh:
		case <-r.Context().Done():
		}
	})
	srv := httptest.NewServer(mux)
	// Order matters: close blockCh FIRST so any stalled handler returns,
	// THEN Close the server. httptest.Server.Close() waits for active
	// connections, so a handler still reading blockCh would deadlock it.
	defer func() {
		close(blockCh)
		srv.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	outDir := t.TempDir()
	tmpPath := filepath.Join(outDir, "out.mp4")

	var wg sync.WaitGroup
	var err error
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = runHLSRemux(ctx, srv.URL+"/playlist.m3u8", tmpPath, nil, MaxBytes)
	}()

	// Give ffmpeg a moment to spawn and start the HTTP request.
	time.Sleep(300 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runHLSRemux did not return after ctx cancel")
	}

	if err == nil {
		t.Fatal("expected error after ctx cancel")
	}
	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

// TestWatchOutputFile_DetectsOversize verifies the size-cap enforcement path
// of the progress watcher independent of ffmpeg's buffered output: a manual
// writer grows the file past the cap and the watcher must invoke onOversize
// exactly once and return.
func TestWatchOutputFile_DetectsOversize(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "growing.bin")
	if err := os.WriteFile(tmpPath, nil, 0644); err != nil {
		t.Fatal(err)
	}

	// Writer goroutine appends 100 B every 20 ms; after ~10 rounds the file
	// exceeds the 512 B cap.
	stopWrite := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWrite:
				return
			case <-ticker.C:
				f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					return
				}
				_, _ = f.Write(bytes.Repeat([]byte("A"), 100))
				_ = f.Close()
			}
		}
	}()
	defer func() { close(stopWrite); <-writerDone }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var oversizeCalls atomic.Int64
	watchOutputFile(ctx, tmpPath, 50*time.Millisecond, 512, nil, func() {
		oversizeCalls.Add(1)
	})

	if oversizeCalls.Load() != 1 {
		t.Errorf("onOversize called %d times, want 1", oversizeCalls.Load())
	}
}

// TestWatchOutputFile_EmitsProgress verifies the progress path: as the file
// grows, cb.Progress must be invoked with monotonically non-decreasing sizes.
func TestWatchOutputFile_EmitsProgress(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "growing.bin")
	if err := os.WriteFile(tmpPath, nil, 0644); err != nil {
		t.Fatal(err)
	}

	stopWrite := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWrite:
				return
			case <-ticker.C:
				f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					return
				}
				_, _ = f.Write(bytes.Repeat([]byte("B"), 200))
				_ = f.Close()
			}
		}
	}()

	var mu sync.Mutex
	var seen []int64
	cb := &Callbacks{
		Progress: func(n int64) {
			mu.Lock()
			seen = append(seen, n)
			mu.Unlock()
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	watchOutputFile(ctx, tmpPath, 50*time.Millisecond, 1<<30, cb, func() {})

	close(stopWrite)
	<-writerDone

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("expected ≥1 progress sample, got 0")
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] < seen[i-1] {
			t.Errorf("progress non-monotonic: %v", seen)
			break
		}
	}
}

func TestRunHLSRemux_ExitError(t *testing.T) {
	requireFFmpeg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	outDir := t.TempDir()
	tmpPath := filepath.Join(outDir, "out.mp4")

	err := runHLSRemux(context.Background(), srv.URL+"/does-not-exist.m3u8", tmpPath, nil, MaxBytes)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var exitErr *ffmpegExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("got %T (%v), want *ffmpegExitError", err, err)
	}
	if exitErr.stderr == "" {
		t.Error("expected stderr capture, got empty string")
	}
}

