package thumb

import (
	"image"
	"image/color"
	"image/png"
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
