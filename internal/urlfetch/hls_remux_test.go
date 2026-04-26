package urlfetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
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

// captureFfmpeg replaces runFfmpeg with a stub that records every invocation's
// argv and writes a stub MP4 (ftyp box) at the output path so the caller's
// subsequent os.Stat / atomic rename succeed. Returns a pointer to the slice
// that accumulates argv across calls. Cleanup restores the original
// runFfmpeg via t.Cleanup.
//
// IMPORTANT: tests using captureFfmpeg MUST NOT call t.Parallel() —
// runFfmpeg is a package-level var and concurrent swaps would race. Code
// review enforces this.
func captureFfmpeg(t *testing.T) *[][]string {
	t.Helper()
	var captured [][]string
	var mu sync.Mutex

	orig := runFfmpeg
	runFfmpeg = func(ctx context.Context, args []string, stderr io.Writer) error {
		mu.Lock()
		// Copy so future arg mutations cannot retroactively corrupt the record.
		captured = append(captured, append([]string(nil), args...))
		mu.Unlock()

		// Write a stub MP4 at the output path so callers' Stat/rename succeed.
		// argv pattern: ... -y <outPath> at the end.
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-y" {
				outPath := args[i+1]
				_ = os.WriteFile(outPath, []byte{
					0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p',
					'm', 'p', '4', '2', 0x00, 0x00, 0x00, 0x00,
					'i', 's', 'o', 'm', 'm', 'p', '4', '2',
				}, 0644)
				break
			}
		}
		return nil
	}
	t.Cleanup(func() { runFfmpeg = orig })

	return &captured
}

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

	err := runHLSRemux(context.Background(), srv.URL+"/"+playlistName, tmpPath, nil, testMaxBytes)
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
	// test ends, so ffmpeg stalls on the HTTP read. A `segmentHit` channel
	// signals when ffmpeg has actually reached the segment GET — cancelling
	// ctx before that point would race with the normal happy-path exit on
	// fast runners (runHLSRemux can finish before we cancel).
	blockCh := make(chan struct{})
	segmentHit := make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/playlist.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:6\n#EXTINF:6.0,\nslow.ts\n#EXT-X-ENDLIST\n"))
	})
	mux.HandleFunc("/slow.ts", func(w http.ResponseWriter, r *http.Request) {
		select {
		case segmentHit <- struct{}{}:
		default:
		}
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
		err = runHLSRemux(ctx, srv.URL+"/playlist.m3u8", tmpPath, nil, testMaxBytes)
	}()

	// Block until ffmpeg's request for the segment actually lands, so the
	// cancel reliably interrupts a stalled read rather than a not-yet-started
	// fetch.
	select {
	case <-segmentHit:
	case <-time.After(5 * time.Second):
		cancel()
		wg.Wait()
		t.Fatal("ffmpeg never fetched the segment — test setup broken or ffmpeg slow-start")
	}
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

	err := runHLSRemux(context.Background(), srv.URL+"/does-not-exist.m3u8", tmpPath, nil, testMaxBytes)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var exitErr *ffmpegExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("got %T (%v), want *ffmpegExitError", err, err)
	}
	// Assert the exit code, not stderr contents: ffmpeg's stderr wording
	// changes across versions and loglevel settings.
	if exitErr.exitCode == 0 {
		t.Errorf("exitCode = 0, want non-zero")
	}
}

// TestClassifyHLSRemuxError pins the sentinel → FetchError.Code mapping so a
// future refactor cannot silently collapse ffmpeg_missing back into
// ffmpeg_error (which hides the operator-misconfig case) or swap
// download_timeout and network_error on context failures.
func TestClassifyHLSRemuxError(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want string
	}{
		{"deadline exceeded", context.DeadlineExceeded, "download_timeout"},
		{"canceled", context.Canceled, "network_error"},
		{"too large", errHLSTooLarge, "too_large"},
		{"ffmpeg missing", errFFmpegMissing, "ffmpeg_missing"},
		{"exit error", &ffmpegExitError{exitCode: 1, stderr: "oops"}, "ffmpeg_error"},
		{"other", errors.New("random"), "ffmpeg_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ferr := classifyHLSRemuxError(tc.in)
			if ferr.Code != tc.want {
				t.Errorf("code = %q, want %q", ferr.Code, tc.want)
			}
			if ferr.Unwrap() != tc.in {
				t.Errorf("wrapped err not preserved")
			}
		})
	}
}

// TestRunFfmpegSwap_StubBypassesBinary locks the captureFfmpeg contract used
// by AC-10 / AC-11 argv invariant tests: with the stub installed, runHLSRemux
// fully replaces the production path — no real ffmpeg executes, argv is
// captured, and the stub MP4 is created at the output path so subsequent
// rename / Stat in fetchHLS succeed. This is the prerequisite that lets
// argv-checking tests run on machines without ffmpeg.
func TestRunFfmpegSwap_StubBypassesBinary(t *testing.T) {
	captured := captureFfmpeg(t)

	outPath := filepath.Join(t.TempDir(), "out.mp4")
	err := runHLSRemux(context.Background(),
		"https://example.com/playlist.m3u8", outPath, nil, testMaxBytes)
	if err != nil {
		t.Fatalf("runHLSRemux: %v", err)
	}
	if len(*captured) != 1 {
		t.Fatalf("captured calls = %d, want 1; argv = %v", len(*captured), *captured)
	}
	stat, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stub MP4 missing: %v", err)
	}
	if stat.Size() < 8 {
		t.Errorf("stub MP4 too small: %d bytes", stat.Size())
	}
	// Sanity: the variant URL appeared as the -i argument (still valid here
	// because D2 hasn't switched the production argv yet).
	args := (*captured)[0]
	for i, a := range args {
		if a == "-i" && i+1 < len(args) {
			if args[i+1] != "https://example.com/playlist.m3u8" {
				t.Errorf("-i arg = %q, want the variant URL", args[i+1])
			}
		}
	}
}

