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

func requireFFprobe(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not found in PATH")
	}
}

// makeTestMP4는 1초짜리 64x64 H.264 영상을 합성한다. withAudio가 true면
// 무음 AAC 스테레오 트랙을 함께 넣는다. pix_fmt는 yuv420p로 강제 — 그래야
// libwebp가 별도 -vf 포맷 변환 없이 입력을 받아들인다.
func makeTestMP4(t *testing.T, dir, name string, withAudio bool) string {
	t.Helper()
	requireFFmpeg(t)
	out := filepath.Join(dir, name)
	args := []string{"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=black:size=64x64:rate=24"}
	if withAudio {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo")
	}
	args = append(args, "-t", "1",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p")
	if withAudio {
		args = append(args, "-c:a", "aac", "-b:a", "64k", "-shortest")
	}
	args = append(args, out)
	if err := exec.Command("ffmpeg", args...).Run(); err != nil {
		t.Fatalf("makeTestMP4: %v", err)
	}
	return out
}

// makeTestGIF는 1초짜리 작은 애니메이션 GIF를 합성한다.
func makeTestGIF(t *testing.T, dir string) string {
	t.Helper()
	requireFFmpeg(t)
	out := filepath.Join(dir, "clip.gif")
	cmd := exec.Command("ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=red:size=32x32:rate=10",
		"-t", "1",
		out,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("makeTestGIF: %v", err)
	}
	return out
}

func assertNoWebPTempLeft(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%q): %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".webpconvert-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// probeAudioStreamCount는 path에 들어 있는 오디오 스트림 개수를 반환한다.
// EncodeWebP가 오디오를 제거하는지 검증할 때 쓴다.
func probeAudioStreamCount(t *testing.T, path string) int {
	t.Helper()
	requireFFprobe(t)
	out, err := exec.Command("ffprobe",
		"-v", "error",
		"-select_streams", "a",
		"-show_entries", "stream=index",
		"-of", "csv=p=0",
		path,
	).Output()
	if err != nil {
		t.Fatalf("probeAudioStreamCount: %v", err)
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return 0
	}
	return len(strings.Split(s, "\n"))
}

// assertAnimatedWebP는 path가 extended 포맷의 animation flag가 설정된 WebP
// 파일이 아니면 테스트를 실패시킨다. 애니메이션 webp 컨테이너에 대해
// ffprobe의 nb_read_frames는 "image data not found"를 내기 때문에, 여기서는
// RIFF 헤더를 직접 파싱한다. 스펙 참조: WebP 컨테이너 — bytes 0..3 "RIFF",
// 8..11 "WEBP", 12..15 "VP8X"(extended), byte 20의 feature flags에서 bit
// 1(0x02)이 animation을 의미한다.
func assertAnimatedWebP(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	if len(data) < 21 {
		t.Fatalf("webp file too short (%d bytes)", len(data))
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		t.Fatalf("not a WebP file: header=%q", data[0:12])
	}
	if string(data[12:16]) != "VP8X" {
		t.Fatalf("WebP not in extended format (no VP8X chunk) — likely single-frame")
	}
	if data[20]&0x02 == 0 {
		t.Fatalf("VP8X feature flags = 0x%02x, animation bit not set", data[20])
	}
}

func TestEncodeWebP_FFmpegMissing(t *testing.T) {
	t.Setenv("PATH", "")
	dir := t.TempDir()
	src := filepath.Join(dir, "nonexistent.mp4")
	_, err := EncodeWebP(context.Background(), src, dir, "out", Callbacks{})
	if !errors.Is(err, ErrFFmpegMissing) {
		t.Fatalf("want ErrFFmpegMissing, got %v", err)
	}
	assertNoWebPTempLeft(t, dir)
}

func TestEncodeWebP_Success_MP4(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestMP4(t, dir, "clip.mp4", false)

	res, err := EncodeWebP(context.Background(), src, dir, "clip", Callbacks{})
	if err != nil {
		t.Fatalf("EncodeWebP: %v", err)
	}
	if res == nil {
		t.Fatal("Result is nil")
	}
	want := filepath.Join(dir, "clip.webp")
	if res.Path != want {
		t.Errorf("Path = %q, want %q", res.Path, want)
	}
	if res.Size <= 0 {
		t.Errorf("Size = %d, want > 0", res.Size)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("output webp missing: %v", err)
	}
	assertNoWebPTempLeft(t, dir)
}

func TestEncodeWebP_Success_GIF(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestGIF(t, dir)

	res, err := EncodeWebP(context.Background(), src, dir, "clip", Callbacks{})
	if err != nil {
		t.Fatalf("EncodeWebP: %v", err)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Fatalf("output webp missing: %v", err)
	}
	if res.Size <= 0 {
		t.Errorf("Size = %d, want > 0", res.Size)
	}
	assertNoWebPTempLeft(t, dir)
}

func TestEncodeWebP_AudioStripped(t *testing.T) {
	requireFFmpeg(t)
	requireFFprobe(t)
	dir := t.TempDir()
	src := makeTestMP4(t, dir, "audio.mp4", true)
	if probeAudioStreamCount(t, src) == 0 {
		t.Fatalf("precondition: source should have audio")
	}
	res, err := EncodeWebP(context.Background(), src, dir, "audio", Callbacks{})
	if err != nil {
		t.Fatalf("EncodeWebP: %v", err)
	}
	if got := probeAudioStreamCount(t, res.Path); got != 0 {
		t.Errorf("output webp audio streams = %d, want 0", got)
	}
}

func TestEncodeWebP_Animated(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestMP4(t, dir, "clip.mp4", false)
	res, err := EncodeWebP(context.Background(), src, dir, "clip", Callbacks{})
	if err != nil {
		t.Fatalf("EncodeWebP: %v", err)
	}
	assertAnimatedWebP(t, res.Path)
}

func TestEncodeWebP_OnStartCalled(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestMP4(t, dir, "clip.mp4", false)
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
	if _, err := EncodeWebP(context.Background(), src, dir, "clip", cb); err != nil {
		t.Fatalf("EncodeWebP: %v", err)
	}
	if startCalls != 1 {
		t.Errorf("OnStart call count = %d, want 1", startCalls)
	}
	if startTotal != srcInfo.Size() {
		t.Errorf("OnStart total = %d, want %d", startTotal, srcInfo.Size())
	}
}

func TestEncodeWebP_NonZeroExit(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	bogus := filepath.Join(dir, "empty.mp4")
	if err := os.WriteFile(bogus, []byte{}, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err := EncodeWebP(context.Background(), bogus, dir, "empty", Callbacks{})
	var exitErr *FFmpegExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("want *FFmpegExitError, got %T: %v", err, err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "empty.webp")); serr == nil {
		t.Errorf("empty.webp should not exist on failure")
	}
	assertNoWebPTempLeft(t, dir)
}

func TestEncodeWebP_CtxCancel(t *testing.T) {
	requireFFmpeg(t)
	dir := t.TempDir()
	src := makeTestMP4(t, dir, "clip.mp4", false)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	_, err := EncodeWebP(ctx, src, dir, "clip", Callbacks{})
	if err == nil {
		t.Fatal("expected error from cancel, got nil")
	}
	if _, serr := os.Stat(filepath.Join(dir, "clip.webp")); serr == nil {
		t.Errorf("clip.webp should not exist after cancel")
	}
	assertNoWebPTempLeft(t, dir)
}

func TestProbeStreamInfo_AudioPresent(t *testing.T) {
	requireFFmpeg(t)
	requireFFprobe(t)
	dir := t.TempDir()
	src := makeTestMP4(t, dir, "audio.mp4", true)
	dur, hasAudio, err := ProbeStreamInfo(src)
	if err != nil {
		t.Fatalf("ProbeStreamInfo: %v", err)
	}
	if !hasAudio {
		t.Errorf("hasAudio = false, want true")
	}
	if dur <= 0 {
		t.Errorf("durationSec = %v, want > 0", dur)
	}
}

func TestProbeStreamInfo_AudioAbsent(t *testing.T) {
	requireFFmpeg(t)
	requireFFprobe(t)
	dir := t.TempDir()
	src := makeTestMP4(t, dir, "video.mp4", false)
	_, hasAudio, err := ProbeStreamInfo(src)
	if err != nil {
		t.Fatalf("ProbeStreamInfo: %v", err)
	}
	if hasAudio {
		t.Errorf("hasAudio = true, want false")
	}
}

func TestProbeStreamInfo_GIF(t *testing.T) {
	requireFFmpeg(t)
	requireFFprobe(t)
	dir := t.TempDir()
	src := makeTestGIF(t, dir)
	_, hasAudio, err := ProbeStreamInfo(src)
	if err != nil {
		t.Fatalf("ProbeStreamInfo: %v", err)
	}
	if hasAudio {
		t.Errorf("GIF hasAudio = true, want false")
	}
	// duration은 ffprobe 빌드에 따라 0이거나 양수일 수 있다 — 여기서는
	// 오디오 부재만 확인한다.
}

func TestProbeStreamInfo_MissingFile(t *testing.T) {
	requireFFprobe(t)
	_, _, err := ProbeStreamInfo(filepath.Join(t.TempDir(), "does-not-exist.mp4"))
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}
