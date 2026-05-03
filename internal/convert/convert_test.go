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

// makeTestTS는 1초짜리 H.264 + AAC transport stream을 합성한다. 코덱 조합은
// 프로덕션 TS 캡처와 맞춘 것이다 — MP4 muxer가 요구하는 `-bsf:a
// aac_adtstoasc` 필터는 AAC만 받기 때문에, mp2 오디오면 `aac_adtstoasc`가
// "Codec not supported"로 중단된다.
func makeTestTS(t *testing.T, dir string) string {
	t.Helper()
	requireFFmpeg(t)
	out := filepath.Join(dir, "clip.ts")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "color=black:size=64x64:rate=25",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo",
		"-t", "1",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-c:a", "aac", "-b:a", "64k",
		"-f", "mpegts",
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
	// PATH를 ffmpeg가 없는 값으로 강제 설정한다. t.Setenv가 cleanup에서 복원해준다.
	t.Setenv("PATH", "")

	dir := t.TempDir()
	src := filepath.Join(dir, "nonexistent.ts")
	// src는 일부러 만들지 않는다 — 파일에 닿기 전에 short-circuit 되어야 한다.

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
	// ffmpeg가 작은 픽스처조차 끝낼 시간이 없도록 거의 즉시 취소한다.
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()

	_, err := RemuxTSToMP4(ctx, src, dir, "clip", Callbacks{})
	if err == nil {
		t.Fatal("expected error from cancel, got nil")
	}
	// 진행 중에 취소됐으므로 최종 .mp4는 존재해선 안 된다.
	if _, serr := os.Stat(filepath.Join(dir, "clip.mp4")); serr == nil {
		t.Errorf("clip.mp4 should not exist after cancel")
	}
	assertNoTempLeft(t, dir)
}

func TestRemux_NonZeroExit(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	// 빈 파일 — ffmpeg가 demux에 실패한다.
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
