package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func patchMove(mux *http.ServeMux, srcRel, destRel string) *httptest.ResponseRecorder {
	body := bytes.NewBufferString(`{"to":"` + destRel + `"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/file?path="+srcRel, body)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	return rw
}

func TestMoveFile_HappyPath(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, "movies"), 0755)
	os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("video"), 0644)

	rw := patchMove(mux, "/clip.mp4", "/movies")
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rw.Body).Decode(&resp)
	if resp["path"] != "/movies/clip.mp4" {
		t.Errorf("path = %q, want /movies/clip.mp4", resp["path"])
	}
	if resp["name"] != "clip.mp4" {
		t.Errorf("name = %q, want clip.mp4", resp["name"])
	}
	if _, err := os.Stat(filepath.Join(root, "movies", "clip.mp4")); err != nil {
		t.Error("file not at destination")
	}
	if _, err := os.Stat(filepath.Join(root, "clip.mp4")); !os.IsNotExist(err) {
		t.Error("source still present")
	}
}

func TestMoveFile_ConflictGetsSuffix(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, "movies"), 0755)
	os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("new"), 0644)
	os.WriteFile(filepath.Join(root, "movies", "clip.mp4"), []byte("existing"), 0644)

	rw := patchMove(mux, "/clip.mp4", "/movies")
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	var resp map[string]string
	json.NewDecoder(rw.Body).Decode(&resp)
	if resp["name"] != "clip_1.mp4" {
		t.Errorf("name = %q, want clip_1.mp4", resp["name"])
	}
	if resp["path"] != "/movies/clip_1.mp4" {
		t.Errorf("path = %q, want /movies/clip_1.mp4", resp["path"])
	}
}

func TestMoveFile_SidecarsMoved(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, ".thumb"), 0755)
	os.MkdirAll(filepath.Join(root, "movies"), 0755)
	os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("video"), 0644)
	os.WriteFile(filepath.Join(root, ".thumb", "clip.mp4.jpg"), []byte("thumb"), 0644)
	os.WriteFile(filepath.Join(root, ".thumb", "clip.mp4.jpg.dur"), []byte("12.5"), 0644)

	rw := patchMove(mux, "/clip.mp4", "/movies")
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, "movies", ".thumb", "clip.mp4.jpg")); err != nil {
		t.Error("thumb not at destination")
	}
	if _, err := os.Stat(filepath.Join(root, "movies", ".thumb", "clip.mp4.jpg.dur")); err != nil {
		t.Error("dur sidecar not at destination")
	}
}

func TestMoveFile_SameDirectory(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, "movies"), 0755)
	os.WriteFile(filepath.Join(root, "movies", "clip.mp4"), []byte("video"), 0644)

	rw := patchMove(mux, "/movies/clip.mp4", "/movies")
	if rw.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rw.Code, rw.Body.String())
	}
	var resp map[string]string
	json.NewDecoder(rw.Body).Decode(&resp)
	if resp["error"] != "same directory" {
		t.Errorf("error = %q, want same directory", resp["error"])
	}
	// file still in original location
	if _, err := os.Stat(filepath.Join(root, "movies", "clip.mp4")); err != nil {
		t.Error("file should remain after rejected move")
	}
}

func TestMoveFile_SrcIsDirectory(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, "src"), 0755)
	os.MkdirAll(filepath.Join(root, "dest"), 0755)

	rw := patchMove(mux, "/src", "/dest")
	if rw.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rw.Code)
	}
}

func TestMoveFile_DestMissing(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("video"), 0644)

	rw := patchMove(mux, "/clip.mp4", "/nonexistent")
	if rw.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d: %s", rw.Code, rw.Body.String())
	}
	// source still present after rejected move
	if _, err := os.Stat(filepath.Join(root, "clip.mp4")); err != nil {
		t.Error("source removed despite rejected move")
	}
}

func TestMoveFile_DestIsFile(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("video"), 0644)
	os.WriteFile(filepath.Join(root, "regular.txt"), []byte("not a dir"), 0644)

	rw := patchMove(mux, "/clip.mp4", "/regular.txt")
	if rw.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rw.Code)
	}
}

func TestMoveFile_SrcMissing(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, "movies"), 0755)
	rw := patchMove(mux, "/ghost.mp4", "/movies")
	if rw.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rw.Code)
	}
}

func TestMoveFile_TraversalRejected(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	t.Run("src traversal", func(t *testing.T) {
		rw := patchMove(mux, "../etc/passwd", "/")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("dest traversal", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "x.txt"), []byte("x"), 0644)
		rw := patchMove(mux, "/x.txt", "../../tmp")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})
}

func TestMoveFile_InvalidBody(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)
	os.WriteFile(filepath.Join(root, "x.txt"), []byte("x"), 0644)

	req := httptest.NewRequest(http.MethodPatch, "/api/file?path=/x.txt",
		bytes.NewBufferString("not json"))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rw.Code)
	}
}

func TestFile_MethodGetReturns405(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	req := httptest.NewRequest(http.MethodGet, "/api/file?path=/x", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rw.Code)
	}
}
