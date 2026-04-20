package urlfetch_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/chang/file_server/internal/urlfetch"
)

func newImageHandler(body []byte, contentType string, headerLength int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		if headerLength >= 0 {
			w.Header().Set("Content-Length", strconv.Itoa(headerLength))
		}
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}
}

func dummyJPEG() []byte {
	// Minimal JFIF SOI/APP0/EOI sequence — enough for tests; we don't decode it.
	return []byte{
		0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00,
		0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00,
		0xFF, 0xD9,
	}
}

func TestFetch_OK_JPEG(t *testing.T) {
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/photo.jpg", dest, "/photos")
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "photo.jpg" {
		t.Errorf("name = %q, want photo.jpg", res.Name)
	}
	if res.Path != "/photos/photo.jpg" {
		t.Errorf("path = %q, want /photos/photo.jpg", res.Path)
	}
	if res.Size != int64(len(body)) {
		t.Errorf("size = %d, want %d", res.Size, len(body))
	}
	if res.Type != "image" {
		t.Errorf("type = %q, want image", res.Type)
	}
	// httptest server is http (not https) → insecure_http warning expected.
	if !slices.Contains(res.Warnings, "insecure_http") {
		t.Errorf("warnings = %v, want to contain insecure_http", res.Warnings)
	}
	if _, err := os.Stat(filepath.Join(dest, "photo.jpg")); err != nil {
		t.Errorf("file not on disk: %v", err)
	}
}

func TestFetch_InvalidScheme(t *testing.T) {
	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		"file:///etc/passwd", dest, "/")
	if ferr == nil || ferr.Code != "invalid_scheme" {
		t.Fatalf("got %v, want invalid_scheme", ferr)
	}
}

func TestFetch_InvalidURL(t *testing.T) {
	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		"://no-scheme", dest, "/")
	if ferr == nil || ferr.Code != "invalid_url" {
		t.Fatalf("got %v, want invalid_url", ferr)
	}
}

func TestFetch_NoContentLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		// Force chunked encoding by flushing before writing body.
		w.(http.Flusher).Flush()
		w.Write(dummyJPEG())
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/photo.jpg", dest, "/")
	if ferr == nil || ferr.Code != "missing_content_length" {
		t.Fatalf("got %v, want missing_content_length", ferr)
	}
	assertNoLeftovers(t, dest)
}

func TestFetch_ContentLengthTooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(urlfetch.MaxBytes+1))
		w.WriteHeader(http.StatusOK)
		// We deliberately don't write the body — header check must reject first.
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/big.jpg", dest, "/")
	if ferr == nil || ferr.Code != "too_large" {
		t.Fatalf("got %v, want too_large", ferr)
	}
	assertNoLeftovers(t, dest)
}

func TestFetch_NonImageContentType(t *testing.T) {
	body := []byte("<html></html>")
	srv := httptest.NewServer(newImageHandler(body, "text/html; charset=utf-8", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/page.html", dest, "/")
	if ferr == nil || ferr.Code != "unsupported_content_type" {
		t.Fatalf("got %v, want unsupported_content_type", ferr)
	}
	assertNoLeftovers(t, dest)
}

func TestFetch_ExtensionMismatch_Replaced(t *testing.T) {
	body := dummyJPEG() // pretend it's a PNG; we don't decode
	srv := httptest.NewServer(newImageHandler(body, "image/png", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/cat.jpg", dest, "/")
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "cat.png" {
		t.Errorf("name = %q, want cat.png", res.Name)
	}
	if !slices.Contains(res.Warnings, "extension_replaced") {
		t.Errorf("warnings = %v, want to contain extension_replaced", res.Warnings)
	}
}

func TestFetch_NoExtensionInURL(t *testing.T) {
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/photo", dest, "/")
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "photo.jpg" {
		t.Errorf("name = %q, want photo.jpg", res.Name)
	}
	if slices.Contains(res.Warnings, "extension_replaced") {
		t.Errorf("should not warn extension_replaced when URL has no ext: %v", res.Warnings)
	}
}

func TestFetch_ExtensionEquivalent_NoWarning(t *testing.T) {
	// .jpeg + image/jpeg → keep .jpeg, no warning.
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/photo.jpeg", dest, "/")
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "photo.jpeg" {
		t.Errorf("name = %q, want photo.jpeg", res.Name)
	}
	if slices.Contains(res.Warnings, "extension_replaced") {
		t.Errorf("should not warn for equivalent ext: %v", res.Warnings)
	}
}

func TestFetch_RedirectCap(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always redirect to ourselves so the chain is unbounded.
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/start", dest, "/")
	if ferr == nil || ferr.Code != "too_many_redirects" {
		t.Fatalf("got %v, want too_many_redirects", ferr)
	}
}

func TestFetch_HTTP404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/missing.jpg", dest, "/")
	if ferr == nil || ferr.Code != "http_error" {
		t.Fatalf("got %v, want http_error", ferr)
	}
}

func TestFetch_FilenameSanitize_DotDot(t *testing.T) {
	// path.Base("/a/b/..") returns ".." — should be replaced with "image".
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/a/..", dest, "/")
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "image.jpg" {
		t.Errorf("name = %q, want image.jpg", res.Name)
	}
	// File must live inside dest, not escape it.
	if _, err := os.Stat(filepath.Join(dest, res.Name)); err != nil {
		t.Errorf("expected file inside dest: %v", err)
	}
}

func TestFetch_Collision_RenamesUnique(t *testing.T) {
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	// Pre-create photo.jpg so the import collides.
	if err := os.WriteFile(filepath.Join(dest, "photo.jpg"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/photo.jpg", dest, "/")
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "photo_1.jpg" {
		t.Errorf("name = %q, want photo_1.jpg", res.Name)
	}
	if !slices.Contains(res.Warnings, "renamed") {
		t.Errorf("warnings = %v, want to contain renamed", res.Warnings)
	}
	// Original file must be untouched.
	got, err := os.ReadFile(filepath.Join(dest, "photo.jpg"))
	if err != nil || string(got) != "existing" {
		t.Errorf("original photo.jpg modified: %q, err=%v", got, err)
	}
}

func TestFetch_TempFileCleaned_OnRejection(t *testing.T) {
	// Force unsupported_content_type: server returns text/html.
	srv := httptest.NewServer(newImageHandler([]byte("hi"), "text/plain", 2))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/x.txt", dest, "/")
	if ferr == nil {
		t.Fatal("expected failure")
	}
	assertNoLeftovers(t, dest)
}

func TestFetch_ExtensionReplaced_FromNonImageExt(t *testing.T) {
	// .bin URL extension is unknown; must be replaced by Content-Type's mapped
	// extension and warn extension_replaced.
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/blob.bin", dest, "/")
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "blob.jpg" {
		t.Errorf("name = %q, want blob.jpg", res.Name)
	}
	if !slices.Contains(res.Warnings, "extension_replaced") {
		t.Errorf("warnings = %v, want extension_replaced", res.Warnings)
	}
}

// assertNoLeftovers fails the test if any file or .urlimport-*.tmp remains in dir.
func assertNoLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".urlimport-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

// Compile-time guard that FetchError satisfies the error interface.
var _ error = (*urlfetch.FetchError)(nil)
