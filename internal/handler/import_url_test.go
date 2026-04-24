package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
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
		case r.URL.Path == "/big.jpg":
			body := make([]byte, 3<<20) // 3 MiB → triggers ≥1 progress event
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			w.Write(body)
		case strings.HasSuffix(r.URL.Path, ".jpg") || strings.HasSuffix(r.URL.Path, ".png"):
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", strconv.Itoa(len(jpegBody)))
			w.WriteHeader(http.StatusOK)
			w.Write(jpegBody)
		case strings.HasSuffix(r.URL.Path, ".mp3"):
			body := []byte("fake-mp3-bytes")
			w.Header().Set("Content-Type", "audio/mpeg")
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			w.WriteHeader(http.StatusOK)
			w.Write(body)
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

// parseSSEEvents splits an SSE response body into JSON payloads. It expects
// each event to be a single `data: {json}\n\n` frame (no event names, no IDs)
// and returns the parsed payloads in order. Malformed frames fail the test.
func parseSSEEvents(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, frame := range strings.Split(strings.TrimRight(body, "\n"), "\n\n") {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		if !strings.HasPrefix(frame, "data:") {
			t.Fatalf("frame missing data prefix: %q", frame)
		}
		payload := strings.TrimSpace(strings.TrimPrefix(frame, "data:"))
		var ev map[string]any
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			t.Fatalf("bad json in frame %q: %v", payload, err)
		}
		out = append(out, ev)
	}
	return out
}

// phasesOf returns the phase string of each event in order.
func phasesOf(events []map[string]any) []string {
	out := make([]string, len(events))
	for i, e := range events {
		if p, ok := e["phase"].(string); ok {
			out[i] = p
		}
	}
	return out
}

func TestImportURL_SSE_Headers(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postImport(t, mux, "/", []string{srv.URL + "/cat.jpg"})
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	if got := rw.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := rw.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if got := rw.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
}

func TestImportURL_SSE_SingleImage_StartDoneSummary(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postImport(t, mux, "/", []string{srv.URL + "/cat.jpg"})
	events := parseSSEEvents(t, rw.Body.String())
	phases := phasesOf(events)

	// start, done (no progress for tiny payload), summary.
	if !equalSlices(phases, []string{"start", "done", "summary"}) {
		t.Fatalf("phases = %v, want [start done summary]", phases)
	}
	if events[0]["name"] != "cat.jpg" || events[0]["type"] != "image" {
		t.Errorf("start event = %v", events[0])
	}
	if events[1]["name"] != "cat.jpg" || events[1]["path"] != "/cat.jpg" {
		t.Errorf("done event = %v", events[1])
	}
	if events[2]["succeeded"].(float64) != 1 || events[2]["failed"].(float64) != 0 {
		t.Errorf("summary = %v, want {1, 0}", events[2])
	}
	if _, err := os.Stat(filepath.Join(root, "cat.jpg")); err != nil {
		t.Errorf("file missing on disk: %v", err)
	}
}

func TestImportURL_SSE_HeaderError_NoStart(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	// page.html → unsupported_content_type rejected before Start fires.
	rw := postImport(t, mux, "/", []string{srv.URL + "/page.html"})
	events := parseSSEEvents(t, rw.Body.String())
	phases := phasesOf(events)

	if !equalSlices(phases, []string{"error", "summary"}) {
		t.Fatalf("phases = %v, want [error summary]", phases)
	}
	if events[0]["error"] != "unsupported_content_type" {
		t.Errorf("error code = %v, want unsupported_content_type", events[0]["error"])
	}
	if events[1]["succeeded"].(float64) != 0 || events[1]["failed"].(float64) != 1 {
		t.Errorf("summary = %v, want {0, 1}", events[1])
	}
}

func TestImportURL_SSE_Mixed_PartialSuccess(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postImport(t, mux, "/", []string{
		srv.URL + "/ok.jpg",
		srv.URL + "/page.html",
		srv.URL + "/missing",
	})
	events := parseSSEEvents(t, rw.Body.String())

	// Index-grouped expectations: index 0 → start+done, 1 → error, 2 → error, then summary.
	byIndex := map[int][]string{}
	var summary map[string]any
	for _, e := range events {
		if e["phase"] == "summary" {
			summary = e
			continue
		}
		idx := int(e["index"].(float64))
		byIndex[idx] = append(byIndex[idx], e["phase"].(string))
	}
	if !equalSlices(byIndex[0], []string{"start", "done"}) {
		t.Errorf("index 0 phases = %v, want [start done]", byIndex[0])
	}
	if !equalSlices(byIndex[1], []string{"error"}) {
		t.Errorf("index 1 phases = %v, want [error]", byIndex[1])
	}
	if !equalSlices(byIndex[2], []string{"error"}) {
		t.Errorf("index 2 phases = %v, want [error]", byIndex[2])
	}
	if summary == nil {
		t.Fatal("summary event missing")
	}
	if summary["succeeded"].(float64) != 1 || summary["failed"].(float64) != 2 {
		t.Errorf("summary = %v, want {1, 2}", summary)
	}
}

func TestImportURL_SSE_LargeFile_ProgressEmitted(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postImport(t, mux, "/", []string{srv.URL + "/big.jpg"})
	events := parseSSEEvents(t, rw.Body.String())

	var progressCount int
	var lastReceived float64
	for _, e := range events {
		if e["phase"] != "progress" {
			continue
		}
		progressCount++
		got := e["received"].(float64)
		if got < lastReceived {
			t.Errorf("progress not monotonic: %v after %v", got, lastReceived)
		}
		lastReceived = got
	}
	if progressCount == 0 {
		t.Errorf("expected ≥1 progress event for 3 MiB body, got 0")
	}
}

func TestImportURL_SSE_AudioSkipsThumbPool(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postImport(t, mux, "/", []string{srv.URL + "/song.mp3"})
	events := parseSSEEvents(t, rw.Body.String())
	phases := phasesOf(events)
	if !equalSlices(phases, []string{"start", "done", "summary"}) {
		t.Fatalf("phases = %v, want [start done summary]", phases)
	}
	// Audio file should land on disk.
	if _, err := os.Stat(filepath.Join(root, "song.mp3")); err != nil {
		t.Errorf("song.mp3 missing: %v", err)
	}
	// .thumb/song.mp3.jpg must NOT exist (audio skips thumbnail generation).
	thumbPath := filepath.Join(root, ".thumb", "song.mp3.jpg")
	if _, err := os.Stat(thumbPath); !os.IsNotExist(err) {
		t.Errorf("audio should not generate thumbnail, got err = %v", err)
	}
}

func TestImportURL_SSE_ClientCancelled_StopsBatch(t *testing.T) {
	// Origin counts hits so we can confirm the loop never reaches URLs after
	// the client disconnects.
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(jpegBody)))
		w.WriteHeader(http.StatusOK)
		w.Write(jpegBody)
	}))
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	// Pre-cancelled context simulates a client that gave up before the handler
	// dispatched the first fetch.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	body, _ := json.Marshal(map[string]any{
		"urls": []string{srv.URL + "/a.jpg", srv.URL + "/b.jpg", srv.URL + "/c.jpg"},
	})
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/api/import-url?path=/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if got := hits.Load(); got != 0 {
		t.Errorf("origin received %d requests, want 0 (handler should have aborted on cancelled ctx)", got)
	}
}

func TestImportURL_EmptyArray(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

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
	Register(mux, root, root, nil)

	rw := postImport(t, mux, "/", []string{"  ", "\t", ""})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (all whitespace should normalize away)", rw.Code)
	}
}

func TestImportURL_TooMany(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

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
	Register(mux, root, root, nil)

	rw := postImport(t, mux, "../escape", []string{"https://example.com/x.jpg"})
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
}

func TestImportURL_PathNotFound(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postImport(t, mux, "/no-such-dir", []string{"https://example.com/x.jpg"})
	if rw.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rw.Code)
	}
}

func TestImportURL_MethodNotAllowed(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

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
	Register(mux, root, root, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/import-url?path=/",
		strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rw.Code)
	}
}

// TestSSEStart_TotalOmittedWhenZero guards the JSON tag: HLS imports fire
// Start with total=0 (unknown length) and the client relies on the field's
// absence to switch into an indeterminate progress mode. A regression here
// would leak `"total": 0` to the wire and confuse the client into showing a
// 0% bar for the entire remux.
func TestSSEStart_TotalOmittedWhenZero(t *testing.T) {
	data, err := json.Marshal(sseStart{
		Phase: "start", Index: 0, URL: "https://x/y.m3u8",
		Name: "y.mp4", Total: 0, Type: "video",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"total"`) {
		t.Errorf("total field leaked for Total=0: %s", data)
	}
}

// TestSSEStart_TotalPresentWhenNonZero ensures the omitempty tag does not
// accidentally drop legitimate byte counts for the non-HLS path.
func TestSSEStart_TotalPresentWhenNonZero(t *testing.T) {
	data, err := json.Marshal(sseStart{
		Phase: "start", Index: 0, URL: "https://x/y.jpg",
		Name: "y.jpg", Total: 1024, Type: "image",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"total":1024`) {
		t.Errorf("expected total=1024 in marshaled JSON, got: %s", data)
	}
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
