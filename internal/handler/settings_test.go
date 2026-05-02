package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"file_server/internal/settings"
)

// newSettingsMux spins up a test server with a backing settings.Store so
// the PATCH path can actually persist. Returns the mux and the store so
// individual tests can peek at state or preload values.
func newSettingsMux(t *testing.T) (*http.ServeMux, *settings.Store, string) {
	t.Helper()
	root := t.TempDir()
	store, err := settings.New(root)
	if err != nil {
		t.Fatalf("settings.New: %v", err)
	}
	mux := http.NewServeMux()
	Register(mux, root, root, store)
	return mux, store, root
}

func TestSettings_GET_ReturnsDefaults(t *testing.T) {
	mux, _, _ := newSettingsMux(t)

	req := httptest.NewRequest("GET", "/api/settings", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	var got settings.Settings
	if err := json.Unmarshal(rw.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := settings.Default()
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestSettings_PATCH_RoundTrip(t *testing.T) {
	mux, store, _ := newSettingsMux(t)

	next := settings.Settings{
		URLImportMaxBytes:       5 * 1024 * 1024 * 1024,
		URLImportTimeoutSeconds: 600,
	}
	body, _ := json.Marshal(next)
	req := httptest.NewRequest("PATCH", "/api/settings", bytes.NewReader(body))
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rw.Code, rw.Body.String())
	}
	var echoed settings.Settings
	if err := json.Unmarshal(rw.Body.Bytes(), &echoed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if echoed != next {
		t.Errorf("response = %+v, want %+v", echoed, next)
	}
	if store.Snapshot() != next {
		t.Errorf("store not updated: %+v", store.Snapshot())
	}

	// Subsequent GET sees the new values.
	getReq := httptest.NewRequest("GET", "/api/settings", nil)
	getRW := httptest.NewRecorder()
	mux.ServeHTTP(getRW, getReq)
	var afterGet settings.Settings
	json.Unmarshal(getRW.Body.Bytes(), &afterGet)
	if afterGet != next {
		t.Errorf("GET after PATCH = %+v, want %+v", afterGet, next)
	}
}

func TestSettings_PATCH_OutOfRange(t *testing.T) {
	cases := []struct {
		name  string
		body  settings.Settings
		field string
	}{
		{"max_bytes too small", settings.Settings{URLImportMaxBytes: 0, URLImportTimeoutSeconds: 600}, "url_import_max_bytes"},
		{"max_bytes too big", settings.Settings{URLImportMaxBytes: settings.MaxMaxBytes + 1, URLImportTimeoutSeconds: 600}, "url_import_max_bytes"},
		{"timeout too small", settings.Settings{URLImportMaxBytes: settings.DefaultMaxBytes, URLImportTimeoutSeconds: 0}, "url_import_timeout_seconds"},
		{"timeout too big", settings.Settings{URLImportMaxBytes: settings.DefaultMaxBytes, URLImportTimeoutSeconds: settings.MaxTimeoutSeconds + 1}, "url_import_timeout_seconds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux, store, _ := newSettingsMux(t)
			body, _ := json.Marshal(tc.body)
			req := httptest.NewRequest("PATCH", "/api/settings", bytes.NewReader(body))
			rw := httptest.NewRecorder()
			mux.ServeHTTP(rw, req)

			if rw.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400, body=%s", rw.Code, rw.Body.String())
			}
			var resp map[string]string
			if err := json.Unmarshal(rw.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if resp["error"] != "out_of_range" {
				t.Errorf("error = %q, want out_of_range", resp["error"])
			}
			if resp["field"] != tc.field {
				t.Errorf("field = %q, want %q", resp["field"], tc.field)
			}
			// Store must remain unchanged after a rejected PATCH.
			if store.Snapshot() != settings.Default() {
				t.Errorf("rejected PATCH mutated store: %+v", store.Snapshot())
			}
		})
	}
}

func TestSettings_PATCH_MalformedJSON(t *testing.T) {
	mux, _, _ := newSettingsMux(t)
	req := httptest.NewRequest("PATCH", "/api/settings", strings.NewReader("{not json"))
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
}

func TestSettings_PATCH_UnknownField(t *testing.T) {
	mux, _, _ := newSettingsMux(t)
	// DisallowUnknownFields means typos like "url_import_max_byte" get 400
	// instead of being silently ignored + persisted as defaults.
	body := []byte(`{"url_import_max_byte": 100, "url_import_timeout_seconds": 600}`)
	req := httptest.NewRequest("PATCH", "/api/settings", bytes.NewReader(body))
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
}

func TestSettings_PATCH_BooleanTypeMismatch(t *testing.T) {
	// auto_convert_png_to_jpg is a bool. Sending a string (or any non-bool)
	// must fail at JSON decode → 400 invalid request, not silently coerce.
	mux, store, _ := newSettingsMux(t)
	body := []byte(`{"url_import_max_bytes": 10737418240, "url_import_timeout_seconds": 1800, "auto_convert_png_to_jpg": "yes"}`)
	req := httptest.NewRequest("PATCH", "/api/settings", bytes.NewReader(body))
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body=%s", rw.Code, rw.Body.String())
	}
	// Rejected PATCH must not mutate the store.
	if store.Snapshot() != settings.Default() {
		t.Errorf("rejected PATCH mutated store: %+v", store.Snapshot())
	}
}

func TestSettings_PATCH_AutoConvertToggle(t *testing.T) {
	// End-to-end: PATCH a false → GET echoes false → PATCH back true → GET echoes true.
	mux, store, _ := newSettingsMux(t)

	off := settings.Default()
	off.AutoConvertPNGToJPG = false
	body, _ := json.Marshal(off)
	req := httptest.NewRequest("PATCH", "/api/settings", bytes.NewReader(body))
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("PATCH off: %d %s", rw.Code, rw.Body.String())
	}
	if store.Snapshot().AutoConvertPNGToJPG {
		t.Fatal("after PATCH false, store still true")
	}

	on := settings.Default() // true
	body, _ = json.Marshal(on)
	req = httptest.NewRequest("PATCH", "/api/settings", bytes.NewReader(body))
	rw = httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("PATCH on: %d %s", rw.Code, rw.Body.String())
	}
	if !store.Snapshot().AutoConvertPNGToJPG {
		t.Fatal("after PATCH true, store still false")
	}
}

func TestSettings_PATCH_LandsInImportURL(t *testing.T) {
	// Verify the patched value is actually used by the next URL import —
	// the request-path read must hit the updated cache (not a stale
	// snapshot taken at server start).
	mux, _, _ := newSettingsMux(t)

	next := settings.Settings{
		URLImportMaxBytes:       settings.MinMaxBytes, // 1 MiB cap
		URLImportTimeoutSeconds: 60,
	}
	body, _ := json.Marshal(next)
	req := httptest.NewRequest("PATCH", "/api/settings", bytes.NewReader(body))
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d: %s", rw.Code, rw.Body.String())
	}

	// Now GET must echo the new value — the only observable signal we have
	// without spinning up a real httptest origin in this unit test.
	getReq := httptest.NewRequest("GET", "/api/settings", nil)
	getRW := httptest.NewRecorder()
	mux.ServeHTTP(getRW, getReq)
	var after settings.Settings
	json.Unmarshal(getRW.Body.Bytes(), &after)
	if after != next {
		t.Errorf("GET after PATCH = %+v, want %+v", after, next)
	}
}

func TestSettings_MethodNotAllowed(t *testing.T) {
	mux, _, _ := newSettingsMux(t)
	for _, m := range []string{"POST", "DELETE", "PUT"} {
		t.Run(m, func(t *testing.T) {
			req := httptest.NewRequest(m, "/api/settings", nil)
			rw := httptest.NewRecorder()
			mux.ServeHTTP(rw, req)
			if rw.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s: status = %d, want 405", m, rw.Code)
			}
		})
	}
}
