package hls

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
// IMPORTANT: tests using captureFfmpeg MUST NOT call t.Parallel() ??
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
	// makeHLSFixture writes playlist.m3u8 + .ts segments into fixtureDir.
	// Post-D2 ffmpeg invocation only accepts local file paths, so we feed
	// the playlist path directly ??no httptest server in the loop.
	fixtureDir := t.TempDir()
	playlistName := makeHLSFixture(t, fixtureDir, 1)
	localPlaylist := filepath.Join(fixtureDir, playlistName)

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "out.mp4")

	err := runHLSRemux(context.Background(), localPlaylist, outPath, nil, testMaxBytes)
	if err != nil {
		t.Fatalf("runHLSRemux: %v", err)
	}

	data, err := os.ReadFile(outPath)
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

// TestRunHLSRemux_CtxCancelPropagates: previously TestRunHLSRemux_ContextCancel
// stalled ffmpeg by serving a slow segment over HTTP. Post-D2 ffmpeg only reads
// local files (no network), so we cannot stall it on I/O the same way. Test
// the wiring directly instead: a stub runFfmpeg that blocks until ctx fires
// confirms the same property ??that an external ctx cancel propagates to the
// process. ffmpeg is not required (this runs without the binary).
func TestRunHLSRemux_CtxCancelPropagates(t *testing.T) {
	orig := runFfmpeg
	runFfmpeg = func(ctx context.Context, args []string, stderr io.Writer) error {
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { runFfmpeg = orig })

	ctx, cancel := context.WithCancel(context.Background())
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "out.mp4")

	var wg sync.WaitGroup
	var err error
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = runHLSRemux(ctx, "/local/playlist.m3u8", outPath, nil, testMaxBytes)
	}()

	// Give the goroutine a moment to invoke runFfmpeg before we cancel.
	time.Sleep(50 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runHLSRemux did not return after ctx cancel")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(err, context.Canceled)", err)
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
		t.Fatal("expected ?? progress sample, got 0")
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
	// Post-D2 runHLSRemux only reads local files. Force ffmpeg to fail by
	// pointing the playlist at a segment that doesn't exist on disk.
	fixtureDir := t.TempDir()
	badPlaylist := filepath.Join(fixtureDir, "bad.m3u8")
	err := os.WriteFile(badPlaylist, []byte("#EXTM3U\n"+
		"#EXT-X-VERSION:3\n"+
		"#EXT-X-TARGETDURATION:6\n"+
		"#EXTINF:6.0,\n"+
		"nonexistent-segment.ts\n"+
		"#EXT-X-ENDLIST\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "out.mp4")

	err = runHLSRemux(context.Background(), badPlaylist, outPath, nil, testMaxBytes)
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

// TestClassifyHLSRemuxError pins the sentinel ??FetchError.Code mapping so a
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

// TestRunHLSRemux_ArgvLocalOnly: argv invariant for the runHLSRemux level
// (spec AC-10 / AC-11 partial). Captures the ffmpeg argv and asserts:
//   - -protocol_whitelist is exactly file,crypto (no http/https/tcp/tls/etc)
//   - -i input has no http://, https:// prefix
// E3 adds the fetchHLS-level integration test that exercises the full
// materialize ??runHLSRemux flow.
func TestRunHLSRemux_ArgvLocalOnly(t *testing.T) {
	captured := captureFfmpeg(t)

	outPath := filepath.Join(t.TempDir(), "out.mp4")
	err := runHLSRemux(context.Background(), "/local/playlist.m3u8", outPath, nil, testMaxBytes)
	if err != nil {
		t.Fatalf("runHLSRemux: %v", err)
	}
	if len(*captured) != 1 {
		t.Fatalf("captured calls = %d, want 1", len(*captured))
	}
	args := (*captured)[0]

	// Find and validate -protocol_whitelist value.
	whitelistIdx := -1
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-protocol_whitelist" {
			whitelistIdx = i + 1
			break
		}
	}
	if whitelistIdx == -1 {
		t.Fatalf("-protocol_whitelist not in argv: %v", args)
	}
	if args[whitelistIdx] != "file,crypto" {
		t.Errorf("-protocol_whitelist = %q, want %q", args[whitelistIdx], "file,crypto")
	}
	for _, forbidden := range []string{"http", "tcp", "tls", "udp", "rtp", "pipe", "async"} {
		if strings.Contains(args[whitelistIdx], forbidden) {
			t.Errorf("-protocol_whitelist contains forbidden token %q: %q",
				forbidden, args[whitelistIdx])
		}
	}

	// Verify -i input is local (no remote URL).
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-i" {
			input := args[i+1]
			if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
				t.Errorf("-i input is a remote URL: %q", input)
			}
		}
	}
}

// TestRunFfmpegSwap_StubBypassesBinary locks the captureFfmpeg contract used
// by AC-10 / AC-11 argv invariant tests: with the stub installed, runHLSRemux
// fully replaces the production path ??no real ffmpeg executes, argv is
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
	// Sanity: runHLSRemux forwards its first argument verbatim as the -i
	// input. Post-D2 production callers must already have a local playlist
	// path here ??TestRunHLSRemux_ArgvLocalOnly enforces that no remote URL
	// reaches ffmpeg in real usage. This test only verifies the swap
	// mechanism, hence the dummy URL.
	args := (*captured)[0]
	for i, a := range args {
		if a == "-i" && i+1 < len(args) {
			if args[i+1] != "https://example.com/playlist.m3u8" {
				t.Errorf("-i arg = %q, want the dummy first arg passed in", args[i+1])
			}
		}
	}
}
