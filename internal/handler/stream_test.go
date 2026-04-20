package handler

import (
	"net/http"
	"net/http/httptest"
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

func TestStream(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "video.mp4"), []byte("fake mp4 data"), 0644)

	mux := http.NewServeMux()
	Register(mux, root, root)

	t.Run("stream existing file", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stream?path=/video.mp4", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rw.Code)
		}
		if ct := rw.Header().Get("Content-Type"); ct != "video/mp4" {
			t.Errorf("Content-Type = %q, want video/mp4", ct)
		}
	})

	t.Run("range request returns 206", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stream?path=/video.mp4", nil)
		req.Header.Set("Range", "bytes=0-3")
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusPartialContent {
			t.Errorf("expected 206, got %d", rw.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stream?path=/ghost.mp4", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rw.Code)
		}
	})

	t.Run("traversal blocked", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/stream?path=../../etc/passwd", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("ts returns 200 with video/mp4", func(t *testing.T) {
		tsPath := makeTestTS(t, root)
		name := filepath.Base(tsPath)

		req := httptest.NewRequest("GET", "/api/stream?path=/"+name, nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rw.Code)
		}
		if ct := rw.Header().Get("Content-Type"); ct != "video/mp4" {
			t.Errorf("Content-Type = %q, want video/mp4", ct)
		}
		if rw.Body.Len() == 0 {
			t.Error("expected non-empty body")
		}
	})

	t.Run("ts sets Accept-Ranges none", func(t *testing.T) {
		tsPath := makeTestTS(t, root)
		name := filepath.Base(tsPath)

		req := httptest.NewRequest("GET", "/api/stream?path=/"+name, nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if ar := rw.Header().Get("Accept-Ranges"); ar != "none" {
			t.Errorf("Accept-Ranges = %q, want none", ar)
		}
	})

	t.Run("ts range request returns 200 not 206", func(t *testing.T) {
		tsPath := makeTestTS(t, root)
		name := filepath.Base(tsPath)

		req := httptest.NewRequest("GET", "/api/stream?path=/"+name, nil)
		req.Header.Set("Range", "bytes=0-99")
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusOK {
			t.Errorf("expected 200 (range ignored), got %d", rw.Code)
		}
	})
}
