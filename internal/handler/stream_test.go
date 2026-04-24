package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}
}

// makeTestTS synthesizes a 1-second H.264 + AAC transport stream at
// <dir>/<name>. The codec pair matches production TS captures and satisfies
// the `-bsf:a aac_adtstoasc` bitstream filter used by both streamTS remux
// (stream.go) and POST /api/convert (convert.go) — mp2 audio would abort
// that filter with "Codec not supported".
func makeTestTS(t *testing.T, dir string, name ...string) string {
	t.Helper()
	requireFFmpeg(t)
	filename := "clip.ts"
	if len(name) > 0 && name[0] != "" {
		filename = name[0]
	}
	out := filepath.Join(dir, filename)
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

func TestStream(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "video.mp4"), []byte("fake mp4 data"), 0644)

	mux := http.NewServeMux()
	Register(mux, root, root, nil)

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

	t.Run("ts supports range requests via cached mp4", func(t *testing.T) {
		tsPath := makeTestTS(t, root)
		name := filepath.Base(tsPath)

		req := httptest.NewRequest("GET", "/api/stream?path=/"+name, nil)
		req.Header.Set("Range", "bytes=0-99")
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusPartialContent {
			t.Errorf("expected 206 from cached mp4, got %d", rw.Code)
		}
	})
}

// Regression: second request for the same TS file must hit the on-disk cache
// rather than re-running ffmpeg. We verify by timing — a cached serve is
// orders of magnitude faster than a remux (~5s vs <50ms).
func TestStreamTSCached(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	tsPath := makeTestTS(t, root)
	name := filepath.Base(tsPath)

	hit := func() time.Duration {
		req := httptest.NewRequest("GET", "/api/stream?path=/"+name, nil)
		rw := httptest.NewRecorder()
		start := time.Now()
		mux.ServeHTTP(rw, req)
		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rw.Code)
		}
		return time.Since(start)
	}

	cold := hit()
	warm := hit()

	if warm > cold/2 {
		t.Errorf("second request should be much faster (cold=%v warm=%v)", cold, warm)
	}

	cacheDir := filepath.Join(root, ".cache", "streams")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatalf("cache dir not created: %v", err)
	}
	mp4Count := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".mp4" {
			mp4Count++
		}
	}
	if mp4Count != 1 {
		t.Errorf("expected exactly 1 cached mp4, got %d", mp4Count)
	}
}
