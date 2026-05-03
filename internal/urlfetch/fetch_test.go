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

	"file_server/internal/urlfetch"
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
	// 테스트에 충분한 최소 JFIF SOI/APP0/EOI 시퀀스 — 디코딩하지 않는다.
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
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/photo.jpg", dest, "/photos", testMaxBytes, nil)
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
	// httptest 서버는 http(https 아님) → insecure_http 경고가 기대된다.
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
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/clip.mp4", dest, "/movies", testMaxBytes, nil)
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
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/song.mp3", dest, "/music", testMaxBytes, nil)
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
	// URL은 .mp4를 선언하지만 서버는 MKV를 반환한다 — 확장자가 .mkv로 바뀌어야 한다.
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/clip.mp4", dest, "/", testMaxBytes, nil)
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
	// URL path가 "/"라 쓸만한 basename이 없다.
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/", dest, "/", testMaxBytes, nil)
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
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/", dest, "/", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "audio.mp3" {
		t.Errorf("name = %q, want audio.mp3", res.Name)
	}
}

func TestFetch_InvalidScheme(t *testing.T) {
	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		"file:///etc/passwd", dest, "/", testMaxBytes, nil)
	if ferr == nil || ferr.Code != "invalid_scheme" {
		t.Fatalf("got %v, want invalid_scheme", ferr)
	}
}

func TestFetch_InvalidURL(t *testing.T) {
	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		"://no-scheme", dest, "/", testMaxBytes, nil)
	if ferr == nil || ferr.Code != "invalid_url" {
		t.Fatalf("got %v, want invalid_url", ferr)
	}
}

// Chunked-transfer 응답(Content-Length 없음)은 예전에 "missing_content_length"
// 로 거부됐다. 이제는 바이트 단위로 상한을 강제하므로 헤더 없는 작은 본문은
// 성공해야 한다.
func TestFetch_NoContentLength_Succeeds(t *testing.T) {
	body := dummyJPEG()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		// body 쓰기 전에 flush 해서 chunked encoding을 강제한다.
		w.(http.Flusher).Flush()
		w.Write(body)
	}))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/photo.jpg", dest, "/", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Size != int64(len(body)) {
		t.Errorf("size = %d, want %d", res.Size, len(body))
	}
}

// 상한을 초과하는 Content-Length는 body를 읽기 전에 거부되어야 한다.
func TestFetch_ContentLengthTooLarge(t *testing.T) {
	const cap = int64(1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.FormatInt(cap+1, 10))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/big.jpg", dest, "/", cap, nil)
	if ferr == nil || ferr.Code != "too_large" {
		t.Fatalf("got %v, want too_large", ferr)
	}
	assertNoLeftovers(t, dest)
}

// Content-Length가 선언되지 않으면 헤더 검사가 거부할 수 없으니, 런타임
// 바이트 카운터가 too_large를 트리거하고 부분 tmp 파일을 정리해야 한다.
func TestFetch_NoContentLength_RuntimeCap(t *testing.T) {
	const cap = int64(64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		// LimitReader가 정확히 1 바이트 overshoot 하도록 cap+1 바이트를 쓴다.
		payload := make([]byte, cap+1)
		for i := range payload {
			payload[i] = 0xFF
		}
		w.Write(payload)
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/over.jpg", dest, "/", cap, nil)
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
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/page.html", dest, "/", testMaxBytes, nil)
	if ferr == nil || ferr.Code != "unsupported_content_type" {
		t.Fatalf("got %v, want unsupported_content_type", ferr)
	}
	assertNoLeftovers(t, dest)
}

func TestFetch_ExtensionMismatch_Replaced(t *testing.T) {
	body := dummyJPEG() // PNG인 척하지만 디코딩하지 않으므로 무방하다
	srv := httptest.NewServer(newImageHandler(body, "image/png", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/cat.jpg", dest, "/", testMaxBytes, nil)
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
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/photo", dest, "/", testMaxBytes, nil)
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
	// .jpeg + image/jpeg → .jpeg를 유지하며 경고 없음.
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/photo.jpeg", dest, "/", testMaxBytes, nil)
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
		// 매번 자기 자신으로 redirect 해 체인이 무한해지도록 한다.
		http.Redirect(w, r, srv.URL+"/next", http.StatusFound)
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/start", dest, "/", testMaxBytes, nil)
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
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/missing.jpg", dest, "/", testMaxBytes, nil)
	if ferr == nil || ferr.Code != "http_error" {
		t.Fatalf("got %v, want http_error", ferr)
	}
}

func TestFetch_FilenameSanitize_DotDot(t *testing.T) {
	// path.Base("/a/b/..")는 ".."를 반환한다 — 카테고리 기본값으로 폴백되어야 한다.
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/a/..", dest, "/", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "image.jpg" {
		t.Errorf("name = %q, want image.jpg", res.Name)
	}
	// 파일은 dest 내부에 있어야 하며 빠져나가서는 안 된다.
	if _, err := os.Stat(filepath.Join(dest, res.Name)); err != nil {
		t.Errorf("expected file inside dest: %v", err)
	}
}

func TestFetch_Collision_RenamesUnique(t *testing.T) {
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	// 충돌이 발생하도록 photo.jpg를 미리 만들어둔다.
	if err := os.WriteFile(filepath.Join(dest, "photo.jpg"), []byte("existing"), 0644); err != nil {
		t.Fatal(err)
	}

	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/photo.jpg", dest, "/", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "photo_1.jpg" {
		t.Errorf("name = %q, want photo_1.jpg", res.Name)
	}
	if !slices.Contains(res.Warnings, "renamed") {
		t.Errorf("warnings = %v, want to contain renamed", res.Warnings)
	}
	// 원본 파일은 그대로 유지되어야 한다.
	got, err := os.ReadFile(filepath.Join(dest, "photo.jpg"))
	if err != nil || string(got) != "existing" {
		t.Errorf("original photo.jpg modified: %q, err=%v", got, err)
	}
}

func TestFetch_TempFileCleaned_OnRejection(t *testing.T) {
	// 서버가 text/plain을 반환하게 해 unsupported_content_type을 강제한다.
	srv := httptest.NewServer(newImageHandler([]byte("hi"), "text/plain", 2))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/x.txt", dest, "/", testMaxBytes, nil)
	if ferr == nil {
		t.Fatal("expected failure")
	}
	assertNoLeftovers(t, dest)
}

func TestFetch_ExtensionReplaced_FromNonImageExt(t *testing.T) {
	// .bin은 알 수 없는 URL 확장자이므로 Content-Type 매핑 확장자로 교체되고
	// extension_replaced 경고가 동반되어야 한다.
	body := dummyJPEG()
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/blob.bin", dest, "/", testMaxBytes, nil)
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
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/clip.mp4", dest, "/", testMaxBytes, cb)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if gotName != "clip.mp4" || gotType != "video" || gotTotal != int64(len(body)) {
		t.Errorf("Start got (%q, %d, %q), want (clip.mp4, %d, video)",
			gotName, gotTotal, gotType, len(body))
	}
}

func TestFetch_Progress_Emitted_ForLargePayload(t *testing.T) {
	// 3 MiB는 1 MiB byte threshold를 적어도 두 번 넘는다.
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
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/big.jpg", dest, "/", testMaxBytes, cb)
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
	// 512 B는 1 MiB byte threshold를 한참 밑돌고, localhost httptest 환경에서는
	// 250 ms time threshold도 넘지 못하므로 progress가 발사되지 않는다.
	body := make([]byte, 512)
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	var calls int
	cb := &urlfetch.Callbacks{
		Progress: func(int64) { calls++ },
	}

	dest := t.TempDir()
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/tiny.jpg", dest, "/", testMaxBytes, cb)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	// 테스트 머신 부하로 250 ms를 넘길 수 있으므로 최대 1번 emit 까지는 허용한다.
	if calls > 1 {
		t.Errorf("got %d progress calls for tiny payload, want 0", calls)
	}
}

func TestFetch_Progress_NilCallback_OK(t *testing.T) {
	body := make([]byte, 2<<20) // 2 MiB — 콜백이 설정돼 있으면 progress가 트리거될 크기.
	srv := httptest.NewServer(newImageHandler(body, "image/jpeg", len(body)))
	defer srv.Close()

	dest := t.TempDir()
	// 명시적 zero-value Callbacks — 두 필드 모두 nil.
	_, ferr := urlfetch.Fetch(context.Background(), urlfetch.NewClient(urlfetch.AllowPrivateNetworks()),
		srv.URL+"/any.jpg", dest, "/", testMaxBytes, &urlfetch.Callbacks{})
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
}

// testMaxBytes는 테스트당 사용하는 넉넉한 상한이다 — 4 GiB는 단위 테스트
// 픽스처보다 크므로, 상한 강제 경로를 직접 검증하지 않는 호출자가 우발적
// 거부에 걸리는 일이 없다.
const testMaxBytes = int64(4) << 30

// assertNoLeftovers는 dir에 파일이나 .urlimport-*.tmp가 남아 있으면 테스트를 실패시킨다.
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

// FetchError가 error 인터페이스를 만족하는지 컴파일 타임에 확인한다.
var _ error = (*urlfetch.FetchError)(nil)
