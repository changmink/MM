package imageconv

import (
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// makePNG는 PNG 픽스처를 만든다. withAlpha가 true면 좌상 사분면은 완전
// 투명, 우하 사분면은 50% 불투명 빨강이 된다 — 두 위치는 알파 합성
// 테스트에서 샘플링된다.
func makePNG(t *testing.T, path string, w, h int, withAlpha bool) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			switch {
			case withAlpha && x < w/2 && y < h/2:
				img.Set(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 0}) // 완전 투명
			case withAlpha && x >= w/2 && y >= h/2:
				img.Set(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 128}) // 반투명
			default:
				img.Set(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 255}) // 불투명 빨강
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
	// 좌상 사분면은 완전 투명이었으므로 거의 흰색에 합성되어야 한다.
	r, g, b, _ := out.At(5, 5).RGBA()
	r8, g8, b8 := r>>8, g>>8, b>>8
	if r8 < 240 || g8 < 240 || b8 < 240 {
		t.Errorf("alpha=0 px sample = (%d,%d,%d), want ~white (≥240 each)", r8, g8, b8)
	}
	// 불투명 빨강 모서리는 빨강을 유지해야 한다.
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
	// 파일이 없는 상태 — quality 검사가 어떤 I/O보다 먼저 트리거되어야 한다.
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

func TestConvertPNGToJPG_RejectsOversizedImage(t *testing.T) {
	// 테스트 동안만 픽셀 상한을 오버라이드해 실제 8K×8K 픽스처(~256 MiB)
	// 없이도 게이트 동작을 검증한다.
	orig := MaxPixels
	MaxPixels = 100
	defer func() { MaxPixels = orig }()

	dir := t.TempDir()
	src := filepath.Join(dir, "big.png")
	dst := filepath.Join(dir, "big.jpg")
	makePNG(t, src, 20, 20, false) // 400 픽셀 > 100 상한

	err := ConvertPNGToJPG(src, dst, 90)
	if err == nil {
		t.Fatal("expected ErrImageTooLarge, got nil")
	}
	if !errors.Is(err, ErrImageTooLarge) {
		t.Errorf("err = %v, want errors.Is(err, ErrImageTooLarge)", err)
	}
	// 상한이 트리거되면 dest 디렉터리에 어떤 파일도 쓰여서는 안 된다 — temp나 jpg 모두.
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Error("dst jpg should not exist after cap rejection")
	}
	leftover, _ := filepath.Glob(filepath.Join(dir, ".imageconv-*"))
	if len(leftover) > 0 {
		t.Errorf("temp files remain: %v", leftover)
	}
}

func TestConvertPNGToJPG_AllowsAtCapBoundary(t *testing.T) {
	// 경계 검사: width*height == MaxPixels는 통과해야 한다.
	orig := MaxPixels
	MaxPixels = 100
	defer func() { MaxPixels = orig }()

	dir := t.TempDir()
	src := filepath.Join(dir, "exact.png")
	dst := filepath.Join(dir, "exact.jpg")
	makePNG(t, src, 10, 10, false) // 정확히 100 픽셀

	if err := ConvertPNGToJPG(src, dst, 90); err != nil {
		t.Fatalf("at-cap boundary should succeed: %v", err)
	}
}

func TestConvertPNGToJPG_RespectsDestExt(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.png")
	makePNG(t, src, 8, 8, false)
	// 확장자는 호출자가 정한다 — 패키지는 destPath가 가리키는 곳에 쓸 뿐이다.
	for _, ext := range []string{".jpg", ".jpeg"} {
		dst := filepath.Join(dir, "out"+ext)
		if err := ConvertPNGToJPG(src, dst, 90); err != nil {
			t.Fatalf("ext=%s: %v", ext, err)
		}
		_ = decodeJPEG(t, dst) // 확장자와 무관하게 실제 JPEG인지 확인
	}
}
