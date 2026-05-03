package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// readZipFromBody는 응답 본문을 ZIP으로 파싱해 (이름 → 바이트) 맵을 돌려준다.
// 부분/손상 ZIP은 에러로 잡아내 테스트가 회귀를 빠르게 드러내게 한다.
func readZipFromBody(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v (body len=%d)", err, len(body))
	}
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %q: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %q: %v", f.Name, err)
		}
		out[f.Name] = data
	}
	return out
}

// seedTree는 download-folder 테스트가 공유하는 디렉터리 구조를 만든다.
// dot-prefix 디렉터리/파일은 모두 제외되어야 한다는 정책 때문에 테스트마다
// 같은 fixture가 반복되어 헬퍼로 빼냈다.
func seedTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	mustWrite("movies/film.mp4", "FILM")
	mustWrite("movies/note.txt", "NOTE")
	mustWrite("movies/sub/clip.mp4", "CLIP")
	mustWrite("movies/한글파일.txt", "KOR")
	// dot-prefix 사이드카·hidden 디렉토리. 모두 제외되어야 한다.
	mustWrite("movies/.thumb/film.mp4.jpg", "thumbdata")
	mustWrite("movies/.hidden/secret.txt", "secret")
	mustWrite("movies/.config", "dotfile")
	return root
}

func TestDownloadFolder_GET_FullRecursive(t *testing.T) {
	root := seedTree(t)
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/download-folder?path=/movies", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rw.Code, rw.Body.String())
	}
	if got := rw.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type = %q, want application/zip", got)
	}
	cd := rw.Header().Get("Content-Disposition")
	if !strings.Contains(cd, `filename="movies.zip"`) {
		t.Errorf("Content-Disposition ASCII fallback missing: %q", cd)
	}
	if !strings.Contains(cd, "filename*=UTF-8''movies.zip") {
		t.Errorf("Content-Disposition RFC 5987 missing: %q", cd)
	}

	files := readZipFromBody(t, rw.Body.Bytes())
	wantPresent := map[string]string{
		"movies/film.mp4":       "FILM",
		"movies/note.txt":       "NOTE",
		"movies/sub/clip.mp4":   "CLIP",
		"movies/한글파일.txt": "KOR",
	}
	for name, content := range wantPresent {
		got, ok := files[name]
		if !ok {
			t.Errorf("ZIP missing entry %q (have: %v)", name, mapKeys(files))
			continue
		}
		if string(got) != content {
			t.Errorf("ZIP entry %q content = %q, want %q", name, got, content)
		}
	}
	for name := range files {
		if strings.Contains(name, "/.thumb/") || strings.Contains(name, "/.hidden/") || strings.HasSuffix(name, "/.config") {
			t.Errorf("ZIP must exclude dot-prefix entries, got %q", name)
		}
	}
}

func TestDownloadFolder_GET_Root(t *testing.T) {
	root := seedTree(t)
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/download-folder?path=/", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	if !strings.Contains(rw.Header().Get("Content-Disposition"), "files.zip") {
		t.Errorf("root download must use files.zip placeholder, got %q", rw.Header().Get("Content-Disposition"))
	}
	files := readZipFromBody(t, rw.Body.Bytes())
	if _, ok := files["files/movies/film.mp4"]; !ok {
		t.Errorf("root ZIP missing films/movies/film.mp4 (have: %v)", mapKeys(files))
	}
}

func TestDownloadFolder_POST_PartialItems(t *testing.T) {
	root := seedTree(t)
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	body, _ := json.Marshal(map[string]any{
		"items": []string{"/movies/film.mp4", "/movies/sub"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/download-folder?path=/movies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Header().Get("Content-Disposition"), "movies-selected-2.zip") {
		t.Errorf("Content-Disposition missing selected suffix: %q", rw.Header().Get("Content-Disposition"))
	}
	files := readZipFromBody(t, rw.Body.Bytes())
	if _, ok := files["movies/film.mp4"]; !ok {
		t.Errorf("missing movies/film.mp4 (have: %v)", mapKeys(files))
	}
	if _, ok := files["movies/sub/clip.mp4"]; !ok {
		t.Errorf("folder item must be walked recursively (have: %v)", mapKeys(files))
	}
	if _, ok := files["movies/note.txt"]; ok {
		t.Errorf("note.txt was not in items but appeared in ZIP")
	}
}

func TestDownloadFolder_POST_RejectsTraversalItems(t *testing.T) {
	root := seedTree(t)
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	cases := []struct {
		name  string
		items []string
	}{
		{"traversal escape", []string{"../etc/passwd"}},
		{"absolute outside", []string{"/etc/passwd"}},
		{"item outside path scope", []string{"/other/file.txt"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(map[string]any{"items": tc.items})
			req := httptest.NewRequest(http.MethodPost, "/api/download-folder?path=/movies", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rw := httptest.NewRecorder()
			mux.ServeHTTP(rw, req)
			if rw.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%q", rw.Code, rw.Body.String())
			}
			if !strings.Contains(rw.Body.String(), "invalid") {
				t.Errorf("body = %q, want 'invalid' marker", rw.Body.String())
			}
		})
	}
}

func TestDownloadFolder_POST_CrossOrigin403(t *testing.T) {
	root := seedTree(t)
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/download-folder?path=/movies",
		strings.NewReader(`{"items":["/movies/film.mp4"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST status = %d, want 403", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "cross_origin") {
		t.Errorf("body = %q, want cross_origin", rw.Body.String())
	}
}

func TestDownloadFolder_GET_CrossOriginPasses(t *testing.T) {
	root := seedTree(t)
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/download-folder?path=/movies", nil)
	req.Header.Set("Origin", "http://evil.example")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code == http.StatusForbidden {
		t.Errorf("cross-origin GET (read-only) was rejected; want pass-through")
	}
}

func TestDownloadFolder_PathErrors(t *testing.T) {
	root := seedTree(t)
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	cases := []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{"GET missing dir", http.MethodGet, "/api/download-folder?path=/no-such", http.StatusNotFound},
		{"GET on file", http.MethodGet, "/api/download-folder?path=/movies/film.mp4", http.StatusBadRequest},
		{"GET traversal", http.MethodGet, "/api/download-folder?path=../etc", http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rw := httptest.NewRecorder()
			mux.ServeHTTP(rw, req)
			if rw.Code != tc.want {
				t.Errorf("status = %d, want %d; body=%q", rw.Code, tc.want, rw.Body.String())
			}
		})
	}
}

func TestDownloadFolder_KoreanFolderName(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "사진첩")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("A"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/download-folder?path=/사진첩", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	cd := rw.Header().Get("Content-Disposition")
	// RFC 5987은 한글을 percent-encoded UTF-8로 표현한다. 디코드 라운드트립 검증.
	if !strings.Contains(cd, "filename*=UTF-8''") {
		t.Fatalf("Content-Disposition missing UTF-8 form: %q", cd)
	}
	files := readZipFromBody(t, rw.Body.Bytes())
	if _, ok := files["사진첩/a.jpg"]; !ok {
		t.Errorf("Korean folder name not preserved in ZIP entries: %v", mapKeys(files))
	}
}

func TestDownloadFolder_POST_BodyTooLarge(t *testing.T) {
	root := seedTree(t)
	prev := maxJSONBodyBytes
	maxJSONBodyBytes = 256
	defer func() { maxJSONBodyBytes = prev }()

	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	// 캡 256B를 명백히 넘기는 페이로드 (긴 path 1개로 충분).
	huge := strings.Repeat("a", 1024)
	body := []byte(`{"items":["/` + huge + `"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/download-folder?path=/movies", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rw.Code)
	}
}

func mapKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
