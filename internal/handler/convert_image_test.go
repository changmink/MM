package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func postConvertImage(t *testing.T, mux *http.ServeMux, paths []string, deleteOriginal bool) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"paths":           paths,
		"delete_original": deleteOriginal,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/convert-image", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	return rw
}

// writePNGFile writes a real PNG to disk for the convert-image tests.
func writePNGFile(t *testing.T, path string, w, h int) {
	t.Helper()
	if err := os.WriteFile(path, pngBytes(t, w, h, false), 0644); err != nil {
		t.Fatal(err)
	}
}

func decodeConvertImageResponse(t *testing.T, rw *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, rw.Body.String())
	}
	return resp
}

func TestConvertImage_Success(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	writePNGFile(t, filepath.Join(root, "photo.png"), 16, 16)

	rw := postConvertImage(t, mux, []string{"photo.png"}, false)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	resp := decodeConvertImageResponse(t, rw)
	if resp["succeeded"].(float64) != 1 || resp["failed"].(float64) != 0 {
		t.Errorf("succeeded/failed = %v/%v, want 1/0", resp["succeeded"], resp["failed"])
	}
	results := resp["results"].([]any)
	r0 := results[0].(map[string]any)
	if r0["name"] != "photo.jpg" || r0["output"] != "photo.jpg" {
		t.Errorf("result[0] name/output = %v/%v", r0["name"], r0["output"])
	}
	if _, err := os.Stat(filepath.Join(root, "photo.jpg")); err != nil {
		t.Errorf("photo.jpg missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "photo.png")); err != nil {
		t.Errorf("photo.png should still exist (delete_original=false): %v", err)
	}
}

func TestConvertImage_DeleteOriginal(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	writePNGFile(t, filepath.Join(root, "photo.png"), 16, 16)
	// Pre-place the sidecar so the deletion path is exercised end-to-end.
	thumbDir := filepath.Join(root, ".thumb")
	os.MkdirAll(thumbDir, 0755)
	sidecar := filepath.Join(thumbDir, "photo.png.jpg")
	os.WriteFile(sidecar, []byte("thumb"), 0644)

	rw := postConvertImage(t, mux, []string{"photo.png"}, true)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	resp := decodeConvertImageResponse(t, rw)
	r0 := resp["results"].([]any)[0].(map[string]any)
	if w, ok := r0["warnings"]; ok && w != nil {
		ws := w.([]any)
		if len(ws) > 0 {
			t.Errorf("unexpected warnings: %v", ws)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "photo.png")); !os.IsNotExist(err) {
		t.Error("photo.png should be deleted (delete_original=true)")
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Error(".thumb/photo.png.jpg should be deleted")
	}
	if _, err := os.Stat(filepath.Join(root, "photo.jpg")); err != nil {
		t.Errorf("converted photo.jpg missing: %v", err)
	}
}

func TestConvertImage_BatchSequential(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	writePNGFile(t, filepath.Join(root, "a.png"), 8, 8)
	writePNGFile(t, filepath.Join(root, "b.png"), 12, 12)

	rw := postConvertImage(t, mux, []string{"a.png", "b.png"}, false)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	resp := decodeConvertImageResponse(t, rw)
	if resp["succeeded"].(float64) != 2 {
		t.Errorf("succeeded = %v, want 2", resp["succeeded"])
	}
	if len(resp["results"].([]any)) != 2 {
		t.Fatalf("results length = %d, want 2", len(resp["results"].([]any)))
	}
	for _, fn := range []string{"a.jpg", "b.jpg"} {
		if _, err := os.Stat(filepath.Join(root, fn)); err != nil {
			t.Errorf("%s missing: %v", fn, err)
		}
	}
}

func TestConvertImage_AlreadyExists(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	writePNGFile(t, filepath.Join(root, "foo.png"), 8, 8)
	os.WriteFile(filepath.Join(root, "foo.jpg"), []byte("pre-existing"), 0644)
	originalJPG, _ := os.ReadFile(filepath.Join(root, "foo.jpg"))

	rw := postConvertImage(t, mux, []string{"foo.png"}, false)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	resp := decodeConvertImageResponse(t, rw)
	r0 := resp["results"].([]any)[0].(map[string]any)
	if r0["error"] != "already_exists" {
		t.Errorf("error = %v, want already_exists", r0["error"])
	}
	// Existing foo.jpg must not be touched.
	current, _ := os.ReadFile(filepath.Join(root, "foo.jpg"))
	if !bytes.Equal(originalJPG, current) {
		t.Error("existing foo.jpg was overwritten")
	}
	leftover, _ := filepath.Glob(filepath.Join(root, ".imageconv-*"))
	if len(leftover) > 0 {
		t.Errorf("temp files remain: %v", leftover)
	}
}

func TestConvertImage_PartialFailure(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	writePNGFile(t, filepath.Join(root, "good.png"), 8, 8)
	os.WriteFile(filepath.Join(root, "bad.txt"), []byte("not png"), 0644)

	rw := postConvertImage(t, mux, []string{"good.png", "bad.txt"}, false)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	resp := decodeConvertImageResponse(t, rw)
	if resp["succeeded"].(float64) != 1 || resp["failed"].(float64) != 1 {
		t.Errorf("succeeded/failed = %v/%v, want 1/1", resp["succeeded"], resp["failed"])
	}
	results := resp["results"].([]any)
	if results[0].(map[string]any)["name"] != "good.jpg" {
		t.Errorf("result[0] name = %v, want good.jpg", results[0].(map[string]any)["name"])
	}
	if results[1].(map[string]any)["error"] != "not_png" {
		t.Errorf("result[1] error = %v, want not_png", results[1].(map[string]any)["error"])
	}
}

func TestConvertImage_DeleteOriginalSidecarWarn(t *testing.T) {
	// We block the sidecar deletion by placing a non-empty directory at the
	// sidecar slot — os.Remove fails on non-empty dirs on both POSIX and
	// Windows, so this exercises the warning branch portably.
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	writePNGFile(t, filepath.Join(root, "foo.png"), 8, 8)
	// Sidecar slot is a non-empty directory — os.Remove fails, triggering
	// the delete_original_failed warning while the conversion itself succeeds.
	thumbDir := filepath.Join(root, ".thumb")
	sidecarDir := filepath.Join(thumbDir, "foo.png.jpg")
	os.MkdirAll(filepath.Join(sidecarDir, "child"), 0755)

	rw := postConvertImage(t, mux, []string{"foo.png"}, true)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	resp := decodeConvertImageResponse(t, rw)
	r0 := resp["results"].([]any)[0].(map[string]any)
	if r0["error"] != nil {
		t.Errorf("expected success despite sidecar delete failure, error = %v", r0["error"])
	}
	warns, _ := r0["warnings"].([]any)
	if len(warns) == 0 || warns[0] != "delete_original_failed" {
		t.Errorf("warnings = %v, want [delete_original_failed]", warns)
	}
	if _, err := os.Stat(filepath.Join(root, "foo.jpg")); err != nil {
		t.Errorf("foo.jpg should exist despite sidecar issue: %v", err)
	}
}

func TestConvertImage_NoPaths(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	rw := postConvertImage(t, mux, []string{}, false)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
}

func TestConvertImage_TooManyPaths(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	paths := make([]string, 51)
	for i := range paths {
		paths[i] = "x.png"
	}
	rw := postConvertImage(t, mux, paths, false)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
}

func TestConvertImage_InvalidJSON(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/convert-image", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
}

func TestConvertImage_MethodNotAllowed(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/convert-image", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rw.Code)
	}
}

func TestConvertImage_PathTraversal(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvertImage(t, mux, []string{"../../etc/passwd"}, false)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	resp := decodeConvertImageResponse(t, rw)
	r0 := resp["results"].([]any)[0].(map[string]any)
	if r0["error"] != "invalid_path" {
		t.Errorf("error = %v, want invalid_path", r0["error"])
	}
}

func TestConvertImage_CorruptPNG(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	os.WriteFile(filepath.Join(root, "broken.png"), []byte("\x89PNG\r\n\x1a\nGARBAGE"), 0644)
	rw := postConvertImage(t, mux, []string{"broken.png"}, false)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d", rw.Code)
	}
	resp := decodeConvertImageResponse(t, rw)
	r0 := resp["results"].([]any)[0].(map[string]any)
	if r0["error"] != "decode_failed" {
		t.Errorf("error = %v, want decode_failed", r0["error"])
	}
	leftover, _ := filepath.Glob(filepath.Join(root, ".imageconv-*"))
	if len(leftover) > 0 {
		t.Errorf("temp files remain: %v", leftover)
	}
	if _, err := os.Stat(filepath.Join(root, "broken.jpg")); !os.IsNotExist(err) {
		t.Error("broken.jpg should not exist after decode failure")
	}
}
