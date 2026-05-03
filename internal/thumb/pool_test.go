package thumb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPoolGeneratesThumbnails(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "photo.png")
	dst := filepath.Join(dir, ".thumb", "photo.png.jpg")
	makePNG(t, src, 200, 100)

	p := NewPool(2)
	if !p.Submit(src, dst) {
		t.Fatal("Submit returned false on empty queue")
	}
	p.Shutdown()

	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("thumbnail not generated: %v", err)
	}
}

func TestPoolDispatchesVideoThumbnails(t *testing.T) {
	dir := t.TempDir()
	src := makeTestMP4(t, dir) // ffmpeg가 없으면 skip 한다
	dst := filepath.Join(dir, ".thumb", "clip.mp4.jpg")

	p := NewPool(1)
	if !p.Submit(src, dst) {
		t.Fatal("Submit returned false on empty queue")
	}
	p.Shutdown()

	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("video thumbnail not generated: %v", err)
	}
}

func TestPoolShutdownDrains(t *testing.T) {
	dir := t.TempDir()
	p := NewPool(2)

	const n = 10
	for i := 0; i < n; i++ {
		src := filepath.Join(dir, "in.png")
		makePNG(t, src, 50, 50)
		dst := filepath.Join(dir, ".thumb", "out.jpg")
		p.Submit(src, dst)
	}

	p.Shutdown()
	// Shutdown이 반환됐다는 건 진행 중이던 워커가 모두 정상 종료됐다는 뜻.
}
