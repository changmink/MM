package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSameOrigin_Decision tables out the helper directly so the routing
// integration tests below can stay focused on the wiring rather than the
// header-parsing minutiae.
func TestSameOrigin_Decision(t *testing.T) {
	cases := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{"missing origin", "", "localhost:8080", true},
		{"matching origin", "http://localhost:8080", "localhost:8080", true},
		{"matching https", "https://example.com", "example.com", true},
		{"different host", "http://evil.example", "localhost:8080", false},
		{"different port", "http://localhost:9000", "localhost:8080", false},
		{"unparseable origin", "://nope", "localhost:8080", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/import-url", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if got := sameOrigin(r); got != tc.want {
				t.Errorf("sameOrigin(origin=%q, host=%q) = %v, want %v",
					tc.origin, tc.host, got, tc.want)
			}
		})
	}
}

// TestRequireSameOrigin_RejectsCrossOriginMutations verifies the wiring on
// representative endpoints from each method class. We don't need to hit
// every route — the wrapper is the same — but we do want to catch a future
// "forgot to wrap a new mutating route" regression by including one
// example per method.
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

// TestRequireSameOrigin_AllowsSameOrigin and AllowsMissingOrigin: the
// wrapper must not break legitimate same-origin requests or curl/test
// invocations that omit the Origin header entirely.
func TestRequireSameOrigin_AllowsSameOrigin(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	body, _ := bytesBufferFromString(`{"urls":["https://example.com/x.jpg"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/import-url?path=/", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://"+req.Host) // matches r.Host
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	// Origin matched → request flows into handleImportURL. We don't care
	// about the eventual outcome here (the URL is bogus); only that the
	// wrapper let it through. 403 specifically means rejection.
	if rw.Code == http.StatusForbidden {
		t.Errorf("same-origin request rejected: %s", rw.Body.String())
	}
}

// TestRequireSameOrigin_AllowsCrossOriginGET — read-only methods bypass the
// check so EventSource (GET) and curl (no Origin) still work.
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

// bytesBufferFromString is a tiny helper to keep the test body construction
// terse without pulling bytes/strings into the main test signature.
func bytesBufferFromString(s string) (*bytes.Buffer, int) {
	b := bytes.NewBufferString(s)
	return b, b.Len()
}
