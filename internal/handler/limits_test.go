package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// withMaxUploadBytes / withMaxJSONBodyBytes는 var 한도를 임시로 낮추고
// 테스트가 끝나면 원복하는 헬퍼. 100 GiB / 64 KiB짜리 정상 한도로는
// MaxBytesReader trip 동작을 테스트할 수 없다.
func withMaxUploadBytes(t *testing.T, n int64) {
	t.Helper()
	orig := maxUploadBytes
	maxUploadBytes = n
	t.Cleanup(func() { maxUploadBytes = orig })
}

func withMaxJSONBodyBytes(t *testing.T, n int64) {
	t.Helper()
	orig := maxJSONBodyBytes
	maxJSONBodyBytes = n
	t.Cleanup(func() { maxJSONBodyBytes = orig })
}

func decodeErrorBody(t *testing.T, rw *httptest.ResponseRecorder) string {
	t.Helper()
	var resp map[string]string
	if err := json.NewDecoder(rw.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error body: %v (body=%q)", err, rw.Body.String())
	}
	return resp["error"]
}

func TestUpload_MaxBytesReader_TripsWith413(t *testing.T) {
	withMaxUploadBytes(t, 1024)

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	t.Cleanup(h.Close)

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, _ := w.CreateFormFile("file", "big.bin")
	// 2 KiB > 1 KiB cap. multipart overhead까지 합치면 본문이 더 커서
	// MaxBytesReader가 NextPart 또는 Copy 단계에서 트립한다.
	fw.Write(make([]byte, 2048))
	w.Close()

	req := httptest.NewRequest("POST", "/api/upload?path=/", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 — body=%s", rw.Code, rw.Body.String())
	}
	if got := decodeErrorBody(t, rw); got != "too_large" {
		t.Errorf("error code = %q, want too_large", got)
	}
}

func TestUpload_MaxBytesReader_AllowsUnderCap(t *testing.T) {
	// 캡 회귀 테스트 — 정상 한도 내 업로드는 여전히 201로 통과해야 한다.
	withMaxUploadBytes(t, 1<<20) // 1 MiB

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	t.Cleanup(h.Close)

	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, _ := w.CreateFormFile("file", "small.txt")
	fw.Write([]byte("hello"))
	w.Close()

	req := httptest.NewRequest("POST", "/api/upload?path=/", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 — body=%s", rw.Code, rw.Body.String())
	}
}

func TestPatchFile_MaxBytesReader_TripsWith413(t *testing.T) {
	withMaxJSONBodyBytes(t, 64)

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	t.Cleanup(h.Close)

	// 64 byte cap을 넘는 JSON. name 값 자체를 길게 만들면 io.ReadAll에서 트립.
	bigName := strings.Repeat("a", 200)
	body := bytes.NewBufferString(`{"name":"` + bigName + `"}`)

	req := httptest.NewRequest("PATCH", "/api/file?path=/x.txt", body)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 — body=%s", rw.Code, rw.Body.String())
	}
	if got := decodeErrorBody(t, rw); got != "too_large" {
		t.Errorf("error code = %q, want too_large", got)
	}
}

func TestPatchFolder_MaxBytesReader_TripsWith413(t *testing.T) {
	withMaxJSONBodyBytes(t, 64)

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	t.Cleanup(h.Close)

	bigName := strings.Repeat("a", 200)
	body := bytes.NewBufferString(`{"name":"` + bigName + `"}`)

	req := httptest.NewRequest("PATCH", "/api/folder?path=/x", body)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 — body=%s", rw.Code, rw.Body.String())
	}
	if got := decodeErrorBody(t, rw); got != "too_large" {
		t.Errorf("error code = %q, want too_large", got)
	}
}

func TestPatchFile_MaxBytesReader_AllowsUnderCap(t *testing.T) {
	// 캡 회귀 테스트 — 작은 JSON body는 정상 흐름을 타야 한다.
	withMaxJSONBodyBytes(t, 1024)

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	t.Cleanup(h.Close)

	// 실제 파일이 없으면 not found(404)가 떨어지는데, 그건 본문 cap이 트립되지
	// 않고 핸들러까지 무사히 흘러갔다는 증거. 413 안 나오는 것만 확인하면 된다.
	body := bytes.NewBufferString(`{"name":"new"}`)
	req := httptest.NewRequest("PATCH", "/api/file?path=/missing.txt", body)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("status = 413, body cap should not trip — body=%s", rw.Body.String())
	}
}
