package convert

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}
}

// makeTestTS synthesizes a 1-second MPEG-2/MP2 transport stream. Mirrors
// handler/stream_test.go:makeTestTS so both suites share the same fixture
// recipe and so the remux args exercised here match production.
func makeTestTS(t *testing.T, dir string) string {
	t.Helper()
	requireFFmpeg(t)
	out := filepath.Join(dir, "clip.ts")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "color=black:size=64x64:rate=1",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", "1",
		"-c:v", "mpeg2video", "-c:a", "mp2",
		out,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("makeTestTS: %v", err)
	}
	return out
}

func assertNoTempLeft(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".convert-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestRemux_FFmpegMissing(t *testing.T) {
	// Force PATH to something with no ffmpeg. t.Setenv restores at cleanup.
	t.Setenv("PATH", "")

	dir := t.TempDir()
	src := filepath.Join(dir, "nonexistent.ts")
	// Intentionally do not create src — we must short-circuit before touching it.

	_, err := RemuxTSToMP4(context.Background(), src, dir, "out", Callbacks{})
	if !errors.Is(err, ErrFFmpegMissing) {
		t.Fatalf("want ErrFFmpegMissing, got %v", err)
	}
	assertNoTempLeft(t, dir)
}

func TestRemux_Success(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestTS(t, dir)

	res, err := RemuxTSToMP4(context.Background(), src, dir, "clip", Callbacks{})
	if err != nil {
		t.Fatalf("RemuxTSToMP4: %v", err)
	}
	if res == nil {
		t.Fatal("Result is nil")
	}
	want := filepath.Join(dir, "clip.mp4")
	if res.Path != want {
		t.Errorf("Path = %q, want %q", res.Path, want)
	}
	if res.Size <= 0 {
		t.Errorf("Size = %d, want > 0", res.Size)
	}
	fi, err := os.Stat(res.Path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", res.Path, err)
	}
	if fi.Size() != res.Size {
		t.Errorf("Result.Size (%d) != on-disk size (%d)", res.Size, fi.Size())
	}
	assertNoTempLeft(t, dir)
}

func TestRemux_OnStartCalled(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestTS(t, dir)

	srcInfo, err := os.Stat(src)
	if err != nil {
		t.Fatalf("Stat src: %v", err)
	}

	var startCalls int
	var startTotal int64
	cb := Callbacks{
		OnStart: func(total int64) {
			startCalls++
			startTotal = total
		},
	}

	if _, err := RemuxTSToMP4(context.Background(), src, dir, "clip", cb); err != nil {
		t.Fatalf("RemuxTSToMP4: %v", err)
	}
	if startCalls != 1 {
		t.Errorf("OnStart call count = %d, want 1", startCalls)
	}
	if startTotal != srcInfo.Size() {
		t.Errorf("OnStart total = %d, want %d (src size)", startTotal, srcInfo.Size())
	}
}

func TestRemux_AtomicRename(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestTS(t, dir)

	finalPath := filepath.Join(dir, "clip.mp4")
	if _, err := os.Stat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("precondition: clip.mp4 must not exist yet, got err=%v", err)
	}

	if _, err := RemuxTSToMP4(context.Background(), src, dir, "clip", Callbacks{}); err != nil {
		t.Fatalf("RemuxTSToMP4: %v", err)
	}

	if _, err := os.Stat(finalPath); err != nil {
		t.Fatalf("final .mp4 not at %q: %v", finalPath, err)
	}
	assertNoTempLeft(t, dir)
}

func TestRemux_CtxCancel(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestTS(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel almost immediately so ffmpeg has no time to finish even the
	// tiny fixture.
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	_, err := RemuxTSToMP4(ctx, src, dir, "clip", Callbacks{})
	if err == nil {
		t.Fatal("expected error from cancel, got nil")
	}
	// The final .mp4 must not exist (cancel happened mid-flight).
	if _, serr := os.Stat(filepath.Join(dir, "clip.mp4")); serr == nil {
		t.Errorf("clip.mp4 should not exist after cancel")
	}
	assertNoTempLeft(t, dir)
}

func TestRemux_NonZeroExit(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	// Empty file — ffmpeg will fail to demux.
	bogus := filepath.Join(dir, "empty.ts")
	if err := os.WriteFile(bogus, []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := RemuxTSToMP4(context.Background(), bogus, dir, "empty", Callbacks{})
	if err == nil {
		t.Fatal("expected error for empty TS input, got nil")
	}
	var exitErr *FFmpegExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("want *FFmpegExitError, got %T: %v", err, err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "empty.mp4")); serr == nil {
		t.Errorf("empty.mp4 should not exist on failure")
	}
	assertNoTempLeft(t, dir)
}

func TestRemux_StderrCaptured(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	bogus := filepath.Join(dir, "empty.ts")
	if err := os.WriteFile(bogus, []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err := RemuxTSToMP4(context.Background(), bogus, dir, "empty", Callbacks{})
	var exitErr *FFmpegExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("want *FFmpegExitError, got %T: %v", err, err)
	}
	if exitErr.Stderr == "" {
		t.Error("FFmpegExitError.Stderr is empty; expected diagnostic text from ffmpeg")
	}
}
