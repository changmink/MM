package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSameOrigin_Decision은 helper를 직접 테이블로 검증한다. 그래야 아래의
// 라우팅 통합 테스트가 헤더 파싱 세부 대신 wiring에 집중할 수 있다.
func TestSameOrigin_Decision(t *testing.T) {
	cases := []struct {
		name      string
		origin    string
		fetchSite string
		host      string
		want      bool
	}{
		{"missing origin", "", "", "localhost:8080", true},
		{"matching origin", "http://localhost:8080", "", "localhost:8080", true},
		{"matching https", "https://example.com", "", "example.com", true},
		{"different host", "http://evil.example", "", "localhost:8080", false},
		{"different port", "http://localhost:9000", "", "localhost:8080", false},
		{"unparseable origin", "://nope", "", "localhost:8080", false},
		// Origin이 없을 때의 Sec-Fetch-Site 폴백. allowlist 의미:
		// "", "same-origin", "none"만 통과한다.
		{"no origin, sec-fetch-site cross-site", "", "cross-site", "localhost:8080", false},
		{"no origin, sec-fetch-site cross-origin", "", "cross-origin", "localhost:8080", false},
		{"no origin, sec-fetch-site same-origin", "", "same-origin", "localhost:8080", true},
		{"no origin, sec-fetch-site none", "", "none", "localhost:8080", true},
		// allowlist fail-closed 케이스: same-site(형제 서브도메인), 알 수 없는
		// 향후 값, 대소문자 불일치 헤더 값은 거부되어야 한다.
		{"no origin, sec-fetch-site same-site", "", "same-site", "localhost:8080", false},
		{"no origin, sec-fetch-site junk value", "", "garbage", "localhost:8080", false},
		{"no origin, sec-fetch-site uppercase rejected", "", "Same-Origin", "localhost:8080", false},
		// Origin이 있으면 Sec-Fetch-Site보다 우선한다.
		{"matching origin overrides cross-site", "http://localhost:8080", "cross-site", "localhost:8080", true},
		{"cross-origin origin overrides same-origin fetch-site", "http://evil.example", "same-origin", "localhost:8080", false},
		// IPv6 호스트 리터럴 — 대괄호 호스트의 url.Parse 라운드트립을 고정한다.
		{"ipv6 host match", "http://[::1]:8080", "", "[::1]:8080", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/import-url", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				r.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			if got := sameOrigin(r); got != tc.want {
				t.Errorf("sameOrigin(origin=%q, sec-fetch-site=%q, host=%q) = %v, want %v",
					tc.origin, tc.fetchSite, tc.host, got, tc.want)
			}
		})
	}
}

// TestRequireSameOrigin_RejectsCrossOriginMutations은 각 메서드 클래스의
// 대표 엔드포인트에서 wiring을 검증한다. 모든 라우트를 칠 필요는 없다
// — 래퍼가 동일하다 — 하지만 메서드별로 한 예씩 포함해, 향후 "새 변경
// 라우트를 wrap 빼먹은" 회귀를 잡을 수 있게 한다.
func TestRequireSameOrigin_RejectsCrossOriginMutations(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"POST /api/import-url", http.MethodPost, "/api/import-url?path=/", `{"urls":["https://x"]}`},
		{"POST /api/upload", http.MethodPost, "/api/upload?path=/", "irrelevant"},
		{"PATCH /api/file", http.MethodPatch, "/api/file?path=/x.jpg", `{"name":"y"}`},
		{"DELETE /api/file", http.MethodDelete, "/api/file?path=/x.jpg", ""},
		{"POST /api/folder", http.MethodPost, "/api/folder?path=/", `{"name":"new"}`},
		{"POST /api/convert", http.MethodPost, "/api/convert?path=/", `{"paths":["/x.ts"]}`},
		{"PATCH /api/settings", http.MethodPatch, "/api/settings", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Origin", "http://evil.example")
			rw := httptest.NewRecorder()
			mux.ServeHTTP(rw, req)
			if rw.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403; body = %s", rw.Code, rw.Body.String())
			}
			if !strings.Contains(rw.Body.String(), "cross_origin") {
				t.Errorf("body = %s, want 'cross_origin'", rw.Body.String())
			}
		})
	}
}

// TestRequireSameOrigin_AllowsSameOrigin과 AllowsMissingOrigin: 래퍼는
// 정상 same-origin 요청이나, Origin 헤더를 전혀 보내지 않는 curl/test
// 호출을 깨뜨려서는 안 된다.
func TestRequireSameOrigin_AllowsSameOrigin(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	body, _ := bytesBufferFromString(`{"urls":["https://example.com/x.jpg"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/import-url?path=/", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.Host) // r.Host와 일치
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	// Origin 일치 → 요청이 handleImportURL로 흘러간다. 여기서 최종 결과는
	// 신경 쓰지 않는다(URL이 가짜다). 래퍼가 통과시켰는지가 핵심이며, 403이
	// 명확히 거부를 의미한다.
	if rw.Code == http.StatusForbidden {
		t.Errorf("same-origin request rejected: %s", rw.Body.String())
	}
}

// TestRequireSameOrigin_AllowsCrossOriginGET — 읽기 전용 메서드는 검사를
// 우회한다. 그래야 EventSource(GET)와 curl(Origin 없음)이 정상 동작한다.
func TestRequireSameOrigin_AllowsCrossOriginGET(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/import-url/jobs", nil)
	req.Header.Set("Origin", "http://evil.example")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code == http.StatusForbidden {
		t.Errorf("GET with cross-origin header was rejected; want pass-through (read-only)")
	}
}

// bytesBufferFromString은 테스트 시그니처에 bytes/strings를 끌어들이지 않고도
// 본문 생성을 간결하게 유지하기 위한 작은 헬퍼다.
func bytesBufferFromString(s string) (*bytes.Buffer, int) {
	b := bytes.NewBufferString(s)
	return b, b.Len()
}
