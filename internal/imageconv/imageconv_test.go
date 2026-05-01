package imageconv

import (
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makePNG writes a PNG fixture. If withAlpha is true the top-left quadrant
// is fully transparent and the bottom-right is 50% opaque red — those
// positions are sampled in the alpha-composite test.
func makePNG(t *testing.T, path string, w, h int, withAlpha bool) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case withAlpha && x < w/2 && y < h/2:
				img.Set(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 0}) // transparent
			case withAlpha && x >= w/2 && y >= h/2:
				img.Set(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 128}) // semi-transparent
			default:
				img.Set(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 255}) // opaque red
			}
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

func decodeJPEG(t *testing.T, path string) image.Image {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := jpeg.Decode(f)
	if err != nil {
		t.Fatalf("jpeg decode: %v", err)
	}
	return img
}

func TestConvertPNGToJPG_RGBPasses(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.png")
	dst := filepath.Join(dir, "out.jpg")
	makePNG(t, src, 32, 16, false)

	if err := ConvertPNGToJPG(src, dst, 90); err != nil {
		t.Fatalf("convert: %v", err)
	}
	out := decodeJPEG(t, dst)
	if out.Bounds().Dx() != 32 || out.Bounds().Dy() != 16 {
		t.Errorf("dimensions = %v, want 32x16", out.Bounds())
	}
}

func TestConvertPNGToJPG_AlphaCompositedToWhite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.png")
	dst := filepath.Join(dir, "out.jpg")
	makePNG(t, src, 40, 40, true)

	if err := ConvertPNGToJPG(src, dst, 90); err != nil {
		t.Fatalf("convert: %v", err)
	}
	out := decodeJPEG(t, dst)
	// Top-left was fully transparent — should composite to ~white.
	r, g, b, _ := out.At(5, 5).RGBA()
	r8, g8, b8 := r>>8, g>>8, b>>8
	if r8 < 240 || g8 < 240 || b8 < 240 {
		t.Errorf("alpha=0 px sample = (%d,%d,%d), want ~white (≥240 each)", r8, g8, b8)
	}
	// Opaque red corner stays red.
	r, g, b, _ = out.At(5, 35).RGBA()
	r8, g8, b8 = r>>8, g>>8, b>>8
	if r8 < 200 || g8 > 60 || b8 > 60 {
		t.Errorf("opaque red sample = (%d,%d,%d), want ~(255,0,0)", r8, g8, b8)
	}
}

func TestConvertPNGToJPG_CorruptPNG(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "bad.png")
	if err := os.WriteFile(src, []byte("\x89PNG\r\n\x1a\nGARBAGE"), 0644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "out.jpg")
	if err := ConvertPNGToJPG(src, dst, 90); err == nil {
		t.Fatal("expected decode error")
	}
	leftover, _ := filepath.Glob(filepath.Join(dir, ".imageconv-*"))
	if len(leftover) > 0 {
		t.Errorf("temp files remain: %v", leftover)
	}
}

func TestConvertPNGToJPG_SrcMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "missing.png")
	dst := filepath.Join(dir, "out.jpg")
	if err := ConvertPNGToJPG(src, dst, 90); err == nil {
		t.Fatal("expected error for missing src")
	}
}

func TestConvertPNGToJPG_DestDirMissing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.png")
	makePNG(t, src, 8, 8, false)
	dst := filepath.Join(dir, "nonexistent", "out.jpg")
	if err := ConvertPNGToJPG(src, dst, 90); err == nil {
		t.Fatal("expected create-temp error")
	}
}

func TestConvertPNGToJPG_QualityOutOfRange(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.png")
	dst := filepath.Join(dir, "out.jpg")
	// File doesn't exist — quality check must fire before any I/O.
	for _, q := range []int{-1, 101, 999, -100} {
		err := ConvertPNGToJPG(src, dst, q)
		if err == nil {
			t.Errorf("quality=%d: expected error", q)
		}
	}
}

func TestConvertPNGToJPG_NoTempFilesAfterSuccess(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.png")
	dst := filepath.Join(dir, "out.jpg")
	makePNG(t, src, 16, 16, false)
	if err := ConvertPNGToJPG(src, dst, 90); err != nil {
		t.Fatal(err)
	}
	leftover, _ := filepath.Glob(filepath.Join(dir, ".imageconv-*"))
	if len(leftover) > 0 {
		t.Errorf("temp files remain after success: %v", leftover)
	}
}

func TestConvertPNGToJPG_RespectsDestExt(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.png")
	makePNG(t, src, 8, 8, false)
	// Caller decides extension; package writes wherever destPath points.
	for _, ext := range []string{".jpg", ".jpeg"} {
		dst := filepath.Join(dir, "out"+ext)
		if err := ConvertPNGToJPG(src, dst, 90); err != nil {
			t.Fatalf("ext=%s: %v", ext, err)
		}
		_ = decodeJPEG(t, dst) // confirm it's a real JPEG regardless of ext
	}
}
