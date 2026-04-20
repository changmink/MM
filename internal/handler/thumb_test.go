package handler

import (
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func makePNGFile(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

func TestThumb(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	t.Run("non-image returns 400", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "doc.txt"), []byte("text"), 0644)
		req := httptest.NewRequest("GET", "/api/thumb?path=/doc.txt", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("not found returns 404", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/thumb?path=/ghost.jpg", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rw.Code)
		}
	})

	t.Run("lazy generates and returns thumbnail", func(t *testing.T) {
		makePNGFile(t, filepath.Join(root, "img.png"))

		req := httptest.NewRequest("GET", "/api/thumb?path=/img.png", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
		}
		if ct := rw.Header().Get("Content-Type"); ct != "image/jpeg" {
			t.Errorf("Content-Type = %q, want image/jpeg", ct)
		}
		if rw.Body.Len() == 0 {
			t.Error("expected non-empty thumbnail body")
		}
		// verify thumbnail file was created on disk
		thumbPath := filepath.Join(root, ".thumb", "img.png.jpg")
		if _, err := os.Stat(thumbPath); err != nil {
			t.Errorf("thumbnail not created on disk: %v", err)
		}
	})

	t.Run("traversal blocked", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/thumb?path=../../etc/passwd", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})
}
