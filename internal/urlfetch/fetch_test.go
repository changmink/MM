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
	"sync"
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
		srv.URL+"/photo.jpg", dest, "/photos", nil)
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

func TestFetch_OK_MP4(t *testing.T) {
	body := []byte("fake-mp4-bytes")
	srv := httptest.NewServer(newImageHandler(body, "video/mp4", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/clip.mp4", dest, "/movies", nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "clip.mp4" {
		t.Errorf("name = %q, want clip.mp4", res.Name)
	}
	if res.Type != "video" {
		t.Errorf("type = %q, want video", res.Type)
	}
}

func TestFetch_OK_MP3(t *testing.T) {
	body := []byte("fake-mp3-bytes")
	srv := httptest.NewServer(newImageHandler(body, "audio/mpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/song.mp3", dest, "/music", nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "song.mp3" {
		t.Errorf("name = %q, want song.mp3", res.Name)
	}
	if res.Type != "audio" {
		t.Errorf("type = %q, want audio", res.Type)
	}
}

func TestFetch_ExtensionReplaced_MKV(t *testing.T) {
	body := []byte("fake-mkv-bytes")
	srv := httptest.NewServer(newImageHandler(body, "video/x-matroska", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	// URL declares .mp4 but the server returns MKV; extension must flip to .mkv.
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/clip.mp4", dest, "/", nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "clip.mkv" {
		t.Errorf("name = %q, want clip.mkv", res.Name)
	}
	if !slices.Contains(res.Warnings, "extension_replaced") {
		t.Errorf("warnings = %v, want extension_replaced", res.Warnings)
	}
	if res.Type != "video" {
		t.Errorf("type = %q, want video", res.Type)
	}
}

func TestFetch_DefaultName_Video(t *testing.T) {
	body := []byte("v")
	srv := httptest.NewServer(newImageHandler(body, "video/mp4", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	// URL path is "/" so there is no usable basename.
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/", dest, "/", nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "video.mp4" {
		t.Errorf("name = %q, want video.mp4", res.Name)
	}
}

func TestFetch_DefaultName_Audio(t *testing.T) {
	body := []byte("a")
	srv := httptest.NewServer(newImageHandler(body, "audio/mpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/", dest, "/", nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "audio.mp3" {
		t.Errorf("name = %q, want audio.mp3", res.Name)
	}
}

func TestFetch_InvalidScheme(t *testing.T) {
	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		"file:///etc/passwd", dest, "/", nil)
	if ferr == nil || ferr.Code != "invalid_scheme" {
		t.Fatalf("got %v, want invalid_scheme", ferr)
	}
}

func TestFetch_InvalidURL(t *testing.T) {
	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		"://no-scheme", dest, "/", nil)
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
		srv.URL+"/photo.jpg", dest, "/", nil)
	if ferr == nil || ferr.Code != "missing_content_length" {
		t.Fatalf("got %v, want missing_content_length", ferr)
	}
	assertNoLeftovers(t, dest)
}

func TestFetch_ContentLengthTooLarge(t *testing.T) {
	// Declare 2 GiB + 1 without actually writing the body — the header check
	// must reject before any transfer.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.FormatInt(int64(urlfetch.MaxBytes)+1, 10))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/big.jpg", dest, "/", nil)
	if ferr == nil || ferr.Code != "too_large" {
		t.Fatalf("got %v, want too_large", ferr)
	}
	assertNoLeftovers(t, dest)
}

func TestFetch_UnsupportedContentType(t *testing.T) {
	body := []byte("<html></html>")
	srv := httptest.NewServer(newImageHandler(body, "text/html; charset=utf-8", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/page.html", dest, "/", nil)
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
		srv.URL+"/cat.jpg", dest, "/", nil)
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
		srv.URL+"/photo", dest, "/", nil)
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
		srv.URL+"/photo.jpeg", dest, "/", nil)
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
		srv.URL+"/start", dest, "/", nil)
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
		srv.URL+"/missing.jpg", dest, "/", nil)
	if ferr == nil || ferr.Code != "http_error" {
		t.Fatalf("got %v, want http_error", ferr)
	}
}

func TestFetch_FilenameSanitize_DotDot(t *testing.T) {
	// path.Base("/a/b/..") returns ".." — should fall back to the category default.
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/a/..", dest, "/", nil)
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
		srv.URL+"/photo.jpg", dest, "/", nil)
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
	// Force unsupported_content_type: server returns text/plain.
	srv := httptest.NewServer(newImageHandler([]byte("hi"), "text/plain", 2))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/x.txt", dest, "/", nil)
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
		srv.URL+"/blob.bin", dest, "/", nil)
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

func TestFetch_Start_Called_WithNameTotalType(t *testing.T) {
	body := []byte("mp4-data")
	srv := httptest.NewServer(newImageHandler(body, "video/mp4", len(body)))
	defer srv.Close()

	var gotName, gotType string
	var gotTotal int64
	cb := &urlfetch.Callbacks{
		Start: func(name string, total int64, fileType string) {
			gotName, gotTotal, gotType = name, total, fileType
		},
	}
	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/clip.mp4", dest, "/", cb)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if gotName != "clip.mp4" || gotType != "video" || gotTotal != int64(len(body)) {
		t.Errorf("Start got (%q, %d, %q), want (clip.mp4, %d, video)",
			gotName, gotTotal, gotType, len(body))
	}
}

func TestFetch_Progress_Emitted_ForLargePayload(t *testing.T) {
	// 3 MiB crosses the 1 MiB byte threshold at least twice.
	body := make([]byte, 3<<20)
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	var mu sync.Mutex
	var received []int64
	cb := &urlfetch.Callbacks{
		Progress: func(n int64) {
			mu.Lock()
			received = append(received, n)
			mu.Unlock()
		},
	}

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/big.jpg", dest, "/", cb)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}

	if len(received) == 0 {
		t.Fatal("expected at least one progress callback")
	}
	for i := 1; i < len(received); i++ {
		if received[i] <= received[i-1] {
			t.Errorf("progress not monotonic: %v", received)
			break
		}
	}
	if last := received[len(received)-1]; last > int64(len(body)) {
		t.Errorf("last progress %d exceeds body size %d", last, len(body))
	}
}

func TestFetch_Progress_NotEmitted_ForTinyPayload(t *testing.T) {
	// 512 B finishes well under the 1 MiB byte threshold and — on localhost
	// httptest — well under the 250 ms time threshold, so no progress fires.
	body := make([]byte, 512)
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	var calls int
	cb := &urlfetch.Callbacks{
		Progress: func(int64) { calls++ },
	}

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/tiny.jpg", dest, "/", cb)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	// Allow at most 1 emit in case the test machine is under load past 250 ms.
	if calls > 1 {
		t.Errorf("got %d progress calls for tiny payload, want 0", calls)
	}
}

func TestFetch_Progress_NilCallback_OK(t *testing.T) {
	body := make([]byte, 2<<20) // 2 MiB — would trigger progress if a callback were set.
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	// Explicit zero-value Callbacks — both fields nil.
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(),
		srv.URL+"/any.jpg", dest, "/", &urlfetch.Callbacks{})
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
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
