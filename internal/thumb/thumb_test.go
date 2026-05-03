package thumb

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}
}

func makeTestMP4(t *testing.T, dir string) string {
	t.Helper()
	requireFFmpeg(t)
	out := filepath.Join(dir, "clip.mp4")
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "color=blue:size=320x240:rate=1",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", "4",
		"-c:v", "libx264", "-c:a", "aac",
		"-shortest",
		out,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("makeTestMP4: %v", err)
	}
	return out
}

func makePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 128, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

func TestIsBlankFrame(t *testing.T) {
	makeUniform := func(w, h int, c color.RGBA) image.Image {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				img.Set(x, y, c)
			}
		}
		return img
	}

	t.Run("all black is blank", func(t *testing.T) {
		img := makeUniform(10, 10, color.RGBA{0, 0, 0, 255})
		if !IsBlankFrame(img) {
			t.Error("expected blank for all-black image")
		}
	})
	t.Run("all white is blank", func(t *testing.T) {
		img := makeUniform(10, 10, color.RGBA{255, 255, 255, 255})
		if !IsBlankFrame(img) {
			t.Error("expected blank for all-white image")
		}
	})
	t.Run("colorful is not blank", func(t *testing.T) {
		img := makeUniform(10, 10, color.RGBA{100, 100, 100, 255})
		if IsBlankFrame(img) {
			t.Error("expected non-blank for colorful image")
		}
	})
	t.Run("mostly black with one colored pixel is not blank", func(t *testing.T) {
		img := makeUniform(10, 10, color.RGBA{0, 0, 0, 255})
		img.(*image.RGBA).Set(5, 5, color.RGBA{100, 100, 100, 255})
		if IsBlankFrame(img) {
			t.Error("expected non-blank when at least one colored pixel exists")
		}
	})
}

func TestGenerateFromVideo(t *testing.T) {
	dir := t.TempDir()
	src := makeTestMP4(t, dir)
	dst := filepath.Join(dir, ".thumb", "clip.mp4.jpg")

	t.Run("creates thumbnail from mp4", func(t *testing.T) {
		if err := GenerateFromVideo(src, dst); err != nil {
			t.Fatalf("GenerateFromVideo failed: %v", err)
		}
		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("thumbnail not created: %v", err)
		}
		if fi.Size() == 0 {
			t.Error("thumbnail is empty")
		}
	})

	t.Run("thumbnail fits within 200x200", func(t *testing.T) {
		f, _ := os.Open(dst)
		defer f.Close()
		cfg, _, err := image.DecodeConfig(f)
		if err != nil {
			t.Fatalf("decode config: %v", err)
		}
		if cfg.Width > thumbWidth || cfg.Height > thumbHeight {
			t.Errorf("thumbnail %dx%d exceeds %dx%d", cfg.Width, cfg.Height, thumbWidth, thumbHeight)
		}
	})

	t.Run("missing src returns error", func(t *testing.T) {
		err := GenerateFromVideo(filepath.Join(dir, "ghost.mp4"), filepath.Join(dir, "out.jpg"))
		if err == nil {
			t.Error("expected error for missing source file")
		}
	})
}

func requireWebPMux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("webpmux"); err != nil {
		t.Skip("webpmux not found in PATH (libwebp-tools)")
	}
}

func makeAnimatedWebP(t *testing.T, dir string) string {
	t.Helper()
	requireFFmpeg(t)
	out := filepath.Join(dir, "anim.webp")
	cmd := exec.Command("ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=red:size=64x64:rate=10",
		"-t", "1",
		"-c:v", "libwebp", "-loop", "0", "-lossless", "0",
		"-q:v", "80", "-an",
		out,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("makeAnimatedWebP: %v", err)
	}
	return out
}

func TestGenerateAnimatedWebP(t *testing.T) {
	requireFFmpeg(t)
	requireWebPMux(t)
	dir := t.TempDir()
	src := makeAnimatedWebP(t, dir)
	dst := filepath.Join(dir, ".thumb", "anim.webp.jpg")

	if err := Generate(src, dst); err != nil {
		t.Fatalf("Generate(animated webp): %v", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("thumbnail not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Error("thumbnail is empty")
	}
	// 첫 프레임은 단색 빨강이라 결과 jpg를 다시 디코딩하면 대략 빨강
	// 범위의 픽셀이 나와야 한다. 여기서는 디코딩 성공 여부만 sanity check
	// 한다 — 픽셀 단위 색상 검사는 jpeg 품질에 따라 흔들려서 신뢰도가 낮다.
	f, err := os.Open(dst)
	if err != nil {
		t.Fatalf("open thumb: %v", err)
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if cfg.Width > thumbWidth || cfg.Height > thumbHeight {
		t.Errorf("thumbnail %dx%d exceeds %dx%d", cfg.Width, cfg.Height, thumbWidth, thumbHeight)
	}
}

func TestProbeDuration(t *testing.T) {
	dir := t.TempDir()
	src := makeTestMP4(t, dir) // 4초 클립

	got, err := ProbeDuration(src)
	if err != nil {
		t.Fatalf("ProbeDuration: %v", err)
	}
	if math.Abs(got-4.0) > 0.5 {
		t.Errorf("ProbeDuration = %v, want ≈4.0", got)
	}
}

func TestProbeDurationMissingFile(t *testing.T) {
	requireFFmpeg(t)
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not found")
	}
	if _, err := ProbeDuration(filepath.Join(t.TempDir(), "ghost.mp4")); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestDurationSidecarRoundTrip(t *testing.T) {
	dir := t.TempDir()
	thumbPath := filepath.Join(dir, "clip.mp4.jpg")

	if err := WriteDurationSidecar(thumbPath, 273.456); err != nil {
		t.Fatalf("WriteDurationSidecar: %v", err)
	}
	got, ok := ReadDurationSidecar(thumbPath)
	if !ok {
		t.Fatal("ReadDurationSidecar returned ok=false")
	}
	if math.Abs(got-273.456) > 0.001 {
		t.Errorf("round-trip got %v, want 273.456", got)
	}
}

func TestDurationSidecarPath(t *testing.T) {
	got := DurationSidecarPath("/foo/.thumb/bar.mp4.jpg")
	want := "/foo/.thumb/bar.mp4.jpg.dur"
	if got != want {
		t.Errorf("DurationSidecarPath = %q, want %q", got, want)
	}
}

func TestReadDurationSidecarMissing(t *testing.T) {
	_, ok := ReadDurationSidecar(filepath.Join(t.TempDir(), "nope.mp4.jpg"))
	if ok {
		t.Error("expected ok=false for missing sidecar")
	}
}

func TestReadDurationSidecarMalformed(t *testing.T) {
	dir := t.TempDir()
	thumbPath := filepath.Join(dir, "bad.mp4.jpg")
	if err := os.WriteFile(DurationSidecarPath(thumbPath), []byte("not-a-number"), 0644); err != nil {
		t.Fatal(err)
	}
	_, ok := ReadDurationSidecar(thumbPath)
	if ok {
		t.Error("expected ok=false for malformed sidecar contents")
	}
}

func TestWriteDurationSidecarRejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	thumbPath := filepath.Join(dir, "clip.mp4.jpg")

	cases := []struct {
		name string
		sec  float64
	}{
		{"NaN", math.NaN()},
		{"+Inf", math.Inf(1)},
		{"-Inf", math.Inf(-1)},
		{"zero", 0},
		{"negative", -1.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := WriteDurationSidecar(thumbPath, tc.sec); err == nil {
				t.Errorf("WriteDurationSidecar(%v) returned nil error, want error", tc.sec)
			}
			if _, err := os.Stat(DurationSidecarPath(thumbPath)); err == nil {
				t.Errorf("sidecar file should not exist after rejected write of %v", tc.sec)
				os.Remove(DurationSidecarPath(thumbPath))
			}
		})
	}
}

func TestReadDurationSidecarRejectsPoisoned(t *testing.T) {
	dir := t.TempDir()
	thumbPath := filepath.Join(dir, "bad.mp4.jpg")

	cases := []struct {
		name    string
		content string
	}{
		{"NaN", "NaN"},
		{"+Inf", "+Inf"},
		{"-Inf", "-Inf"},
		{"zero", "0"},
		{"negative", "-3.14"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(DurationSidecarPath(thumbPath), []byte(tc.content), 0644); err != nil {
				t.Fatal(err)
			}
			if _, ok := ReadDurationSidecar(thumbPath); ok {
				t.Errorf("ReadDurationSidecar accepted poisoned value %q", tc.content)
			}
		})
	}
}

func TestWriteDurationSidecarLeavesNoTempFile(t *testing.T) {
	dir := t.TempDir()
	thumbPath := filepath.Join(dir, "clip.mp4.jpg")
	if err := WriteDurationSidecar(thumbPath, 12.5); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if name != filepath.Base(DurationSidecarPath(thumbPath)) {
			t.Errorf("unexpected leftover file: %s", name)
		}
	}
}

func TestLookupDuration(t *testing.T) {
	dir := t.TempDir()
	thumbPath := filepath.Join(dir, "clip.mp4.jpg")

	if d := LookupDuration(thumbPath); d != nil {
		t.Errorf("LookupDuration on missing sidecar = %v, want nil", *d)
	}
	if err := WriteDurationSidecar(thumbPath, 99.5); err != nil {
		t.Fatal(err)
	}
	d := LookupDuration(thumbPath)
	if d == nil {
		t.Fatal("LookupDuration after write returned nil")
	}
	if math.Abs(*d-99.5) > 0.001 {
		t.Errorf("LookupDuration = %v, want 99.5", *d)
	}
}

func TestBackfillDuration(t *testing.T) {
	dir := t.TempDir()
	src := makeTestMP4(t, dir)
	thumbPath := filepath.Join(dir, ".thumb", "clip.mp4.jpg")
	if err := os.MkdirAll(filepath.Dir(thumbPath), 0755); err != nil {
		t.Fatal(err)
	}

	d := BackfillDuration(thumbPath, src)
	if d == nil {
		t.Fatal("BackfillDuration returned nil")
	}
	if math.Abs(*d-4.0) > 0.5 {
		t.Errorf("BackfillDuration = %v, want ≈4.0", *d)
	}
	cached, ok := ReadDurationSidecar(thumbPath)
	if !ok || math.Abs(cached-*d) > 0.001 {
		t.Errorf("sidecar after backfill = (%v, %v), want %v", cached, ok, *d)
	}
}

func TestBackfillDurationProbeFailure(t *testing.T) {
	requireFFmpeg(t)
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not found")
	}
	dir := t.TempDir()
	thumbPath := filepath.Join(dir, ".thumb", "ghost.mp4.jpg")
	if err := os.MkdirAll(filepath.Dir(thumbPath), 0755); err != nil {
		t.Fatal(err)
	}
	if d := BackfillDuration(thumbPath, filepath.Join(dir, "ghost.mp4")); d != nil {
		t.Errorf("BackfillDuration on missing source = %v, want nil", *d)
	}
	if _, err := os.Stat(DurationSidecarPath(thumbPath)); err == nil {
		t.Error("sidecar should not be written on probe failure")
	}
}

func TestGenerateFromVideoWritesSidecar(t *testing.T) {
	dir := t.TempDir()
	src := makeTestMP4(t, dir)
	dst := filepath.Join(dir, ".thumb", "clip.mp4.jpg")

	if err := GenerateFromVideo(src, dst); err != nil {
		t.Fatalf("GenerateFromVideo: %v", err)
	}
	sec, ok := ReadDurationSidecar(dst)
	if !ok {
		t.Fatal("expected sidecar to be created alongside thumbnail")
	}
	if math.Abs(sec-4.0) > 0.5 {
		t.Errorf("sidecar duration = %v, want ≈4.0", sec)
	}
}

func TestGenerate(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.png")
	dst := filepath.Join(dir, ".thumb", "photo.png.jpg")

	makePNG(t, src, 400, 300)

	t.Run("creates thumbnail", func(t *testing.T) {
		if err := Generate(src, dst); err != nil {
			t.Fatalf("Generate failed: %v", err)
		}
		fi, err := os.Stat(dst)
		if err != nil {
			t.Fatalf("thumbnail not created: %v", err)
		}
		if fi.Size() == 0 {
			t.Error("thumbnail is empty")
		}
	})

	t.Run("thumbnail fits within 200x200", func(t *testing.T) {
		f, _ := os.Open(dst)
		defer f.Close()
		cfg, _, err := image.DecodeConfig(f)
		if err != nil {
			t.Fatalf("decode config: %v", err)
		}
		if cfg.Width > thumbWidth || cfg.Height > thumbHeight {
			t.Errorf("thumbnail %dx%d exceeds %dx%d", cfg.Width, cfg.Height, thumbWidth, thumbHeight)
		}
	})

	t.Run("missing src returns error", func(t *testing.T) {
		err := Generate(filepath.Join(dir, "ghost.png"), filepath.Join(dir, "out.jpg"))
		if err == nil {
			t.Error("expected error for missing source file")
		}
	})
}
