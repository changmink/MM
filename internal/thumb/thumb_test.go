package thumb

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

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
