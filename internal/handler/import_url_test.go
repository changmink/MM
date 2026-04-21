package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// jpegBody is a minimal JFIF byte sequence — enough for tests; we don't decode it.
var jpegBody = []byte{
	0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00,
	0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00,
	0xFF, 0xD9,
}

// newOriginServer routes test requests by URL path so a single mock origin
// can serve the success/failure mix for partial-success tests.
func newOriginServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".jpg") || strings.HasSuffix(r.URL.Path, ".png"):
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", strconv.Itoa(len(jpegBody)))
			w.WriteHeader(http.StatusOK)
			w.Write(jpegBody)
		case strings.HasSuffix(r.URL.Path, ".html"):
			body := []byte("<html></html>")
			w.Header().Set("Content-Type", "text/html")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			w.Write(body)
		case r.URL.Path == "/missing":
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}

func postImport(t *testing.T, mux *http.ServeMux, path string, urls []string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{"urls": urls})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost,
		"/api/import-url?path="+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	return rw
}

func TestImportURL_Single_OK(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	rw := postImport(t, mux, "/", []string{srv.URL + "/cat.jpg"})
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	var resp struct {
		Succeeded []map[string]any `json:"succeeded"`
		Failed    []map[string]any `json:"failed"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Succeeded) != 1 {
		t.Fatalf("succeeded = %d, want 1: %v", len(resp.Succeeded), resp)
	}
	if len(resp.Failed) != 0 {
		t.Fatalf("failed = %d, want 0: %v", len(resp.Failed), resp)
	}
	if got := resp.Succeeded[0]["name"]; got != "cat.jpg" {
		t.Errorf("name = %v, want cat.jpg", got)
	}
	if _, err := os.Stat(filepath.Join(root, "cat.jpg")); err != nil {
		t.Errorf("file missing on disk: %v", err)
	}
}

func TestImportURL_Multiple_PartialSuccess(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	rw := postImport(t, mux, "/", []string{
		srv.URL + "/ok.jpg",
		srv.URL + "/page.html", // unsupported_content_type
		srv.URL + "/missing",   // http_error
	})
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	var resp struct {
		Succeeded []map[string]any  `json:"succeeded"`
		Failed    []importFailure   `json:"failed"`
	}
	if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Succeeded) != 1 {
		t.Errorf("succeeded = %d, want 1", len(resp.Succeeded))
	}
	if len(resp.Failed) != 2 {
		t.Fatalf("failed = %d, want 2", len(resp.Failed))
	}
	codes := []string{resp.Failed[0].Error, resp.Failed[1].Error}
	if !contains(codes, "unsupported_content_type") {
		t.Errorf("missing unsupported_content_type in %v", codes)
	}
	if !contains(codes, "http_error") {
		t.Errorf("missing http_error in %v", codes)
	}
}

func TestImportURL_EmptyArray(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	rw := postImport(t, mux, "/", []string{})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "no urls") {
		t.Errorf("body = %s, want 'no urls'", rw.Body.String())
	}
}

func TestImportURL_OnlyWhitespace(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	rw := postImport(t, mux, "/", []string{"  ", "\t", ""})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (all whitespace should normalize away)", rw.Code)
	}
}

func TestImportURL_TooMany(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	urls := make([]string, 51)
	for i := range urls {
		urls[i] = fmt.Sprintf("https://example.com/%d.jpg", i)
	}
	rw := postImport(t, mux, "/", urls)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "too many urls") {
		t.Errorf("body = %s, want 'too many urls'", rw.Body.String())
	}
}

func TestImportURL_PathTraversal(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	rw := postImport(t, mux, "../escape", []string{"https://example.com/x.jpg"})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
}

func TestImportURL_PathNotFound(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	rw := postImport(t, mux, "/no-such-dir", []string{"https://example.com/x.jpg"})
	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rw.Code)
	}
}

func TestImportURL_MethodNotAllowed(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	req := httptest.NewRequest(http.MethodGet, "/api/import-url?path=/", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rw.Code)
	}
}

func TestImportURL_InvalidBody(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	req := httptest.NewRequest(http.MethodPost, "/api/import-url?path=/",
		strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
