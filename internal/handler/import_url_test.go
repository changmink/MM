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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chang/file_server/internal/importjob"
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

	// register (per-request job id), queued (batch acquires semaphore immediately
	// since nothing is in flight), start, done (no progress for tiny payload), summary.
	if !equalSlices(phases, []string{"register", "queued", "start", "done", "summary"}) {
		t.Fatalf("phases = %v, want [register queued start done summary]", phases)
	}
	if events[2]["name"] != "cat.jpg" || events[2]["type"] != "image" {
		t.Errorf("start event = %v", events[2])
	}
	if events[3]["name"] != "cat.jpg" || events[3]["path"] != "/cat.jpg" {
		t.Errorf("done event = %v", events[3])
	}
	if events[4]["succeeded"].(float64) != 1 || events[4]["failed"].(float64) != 0 {
		t.Errorf("summary = %v, want {1, 0}", events[4])
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

	if !equalSlices(phases, []string{"register", "queued", "error", "summary"}) {
		t.Fatalf("phases = %v, want [register queued error summary]", phases)
	}
	if events[2]["error"] != "unsupported_content_type" {
		t.Errorf("error code = %v, want unsupported_content_type", events[2]["error"])
	}
	if events[3]["succeeded"].(float64) != 0 || events[3]["failed"].(float64) != 1 {
		t.Errorf("summary = %v, want {0, 1}", events[3])
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
	// The leading `register` (per-request job id) and `queued` (batch-scoped)
	// events have no index so we skip them here.
	byIndex := map[int][]string{}
	var summary map[string]any
	var sawQueued, sawRegister bool
	for _, e := range events {
		switch e["phase"] {
		case "register":
			sawRegister = true
			continue
		case "queued":
			sawQueued = true
			continue
		case "summary":
			summary = e
			continue
		}
		idx := int(e["index"].(float64))
		byIndex[idx] = append(byIndex[idx], e["phase"].(string))
	}
	if !sawRegister {
		t.Error("expected a register event, got none")
	}
	if !sawQueued {
		t.Error("expected a queued event, got none")
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
	if !equalSlices(phases, []string{"register", "queued", "start", "done", "summary"}) {
		t.Fatalf("phases = %v, want [register queued start done summary]", phases)
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

// TestImportURL_HandlerDisconnect_JobContinues asserts the Phase 20 contract:
// the client closing the SSE stream (or refreshing the tab) does NOT cancel
// the underlying import job. The handler returns promptly, but the worker
// keeps draining URLs until summary, and the registry retains the finished
// job for snapshot/list queries.
func TestImportURL_HandlerDisconnect_JobContinues(t *testing.T) {
	// Origin counts hits so we can confirm every URL was attempted even
	// after the handler returned.
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
	h := Register(mux, root, root, nil)
	defer h.Close() // drains thumbPool + registry.WaitAll for clean t.TempDir cleanup

	// Cancel the request context after the first frame so we observe a real
	// disconnect mid-stream rather than a pre-cancelled never-started one.
	ctx, cancel := context.WithCancel(context.Background())
	rec, wait := postImportStreaming(ctx, t, mux, "/", []string{
		srv.URL + "/a.jpg", srv.URL + "/b.jpg", srv.URL + "/c.jpg",
	})
	registerEv := waitForPhase(t, rec, "register")
	jobID, _ := registerEv["jobId"].(string)
	if jobID == "" {
		t.Fatal("register event missing jobId")
	}
	cancel()
	wait() // handler returns when its ctx is cancelled

	// Wait for the background worker to finish via the registry.
	job, ok := h.registry.Get(jobID)
	if !ok {
		t.Fatalf("job %q not found in registry", jobID)
	}
	select {
	case <-job.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("background job did not finish within 5s after handler disconnect")
	}

	if got := hits.Load(); got != 3 {
		t.Errorf("origin received %d requests, want 3 (every URL should be attempted)", got)
	}
	if got := job.Status(); got != importjob.StatusCompleted {
		t.Errorf("job status = %q, want %q", got, importjob.StatusCompleted)
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

// streamingRecorder implements http.ResponseWriter + http.Flusher so tests
// can observe SSE events as the handler flushes them. Each Flush delimits a
// frame; frames arrive on `frames` so a test can wait for specific phases
// while the handler is still executing. `all` keeps the full response body
// for post-run inspection.
type streamingRecorder struct {
	mu       sync.Mutex
	hdr      http.Header
	status   int
	pending  bytes.Buffer
	all      bytes.Buffer
	frames   chan string
	closedCh chan struct{}
}

func newStreamingRecorder() *streamingRecorder {
	return &streamingRecorder{
		hdr:      http.Header{},
		frames:   make(chan string, 64),
		closedCh: make(chan struct{}),
	}
}

func (r *streamingRecorder) Header() http.Header  { return r.hdr }
func (r *streamingRecorder) WriteHeader(code int) { r.status = code }
func (r *streamingRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending.Write(p)
	r.all.Write(p)
	return len(p), nil
}
func (r *streamingRecorder) Flush() {
	r.mu.Lock()
	frame := r.pending.String()
	r.pending.Reset()
	r.mu.Unlock()
	if frame == "" {
		return
	}
	// Non-blocking send: tests must drain `frames` promptly. The full body
	// is still recoverable from `all` even if frames overflow.
	select {
	case r.frames <- frame:
	default:
	}
}

// waitForPhase blocks until a frame carrying the given phase arrives (or the
// deadline expires). Any earlier phases are discarded by the caller's
// intent — tests pass whatever they haven't yet explicitly consumed.
func waitForPhase(t *testing.T, rec *streamingRecorder, want string) map[string]any {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case frame := <-rec.frames:
			for _, ev := range parseSSEEvents(t, frame) {
				if ev["phase"] == want {
					return ev
				}
			}
		case <-deadline:
			t.Fatalf("timed out waiting for phase %q; body so far: %s",
				want, rec.all.String())
		}
	}
}

// postImportStreaming fires a POST /api/import-url in a goroutine so the
// caller can interleave assertions against the live SSE stream. The returned
// wait() blocks until the handler returns.
func postImportStreaming(ctx context.Context, t *testing.T, mux *http.ServeMux,
	path string, urls []string) (*streamingRecorder, func()) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"urls": urls})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodPost,
		"/api/import-url?path="+path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := newStreamingRecorder()
	go func() {
		defer close(rec.closedCh)
		mux.ServeHTTP(rec, req)
	}()
	wait := func() {
		t.Helper()
		select {
		case <-rec.closedCh:
		case <-time.After(10 * time.Second):
			t.Fatalf("handler did not return within 10s")
		}
	}
	return rec, wait
}

// TestImportURL_Queued_EventEmittedOnce asserts the batch-level `queued`
// event is the first frame and appears exactly once per batch.
func TestImportURL_Queued_EventEmittedOnce(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	// thumbPool spawns goroutines that write `.thumb/*.jpg` sidecars after
	// the JPEG import succeeds; without Shutdown they can race with
	// t.TempDir cleanup and produce "directory not empty" failures.
	defer h.Close()

	rw := postImport(t, mux, "/", []string{srv.URL + "/cat.jpg"})
	events := parseSSEEvents(t, rw.Body.String())
	phases := phasesOf(events)

	// register precedes queued; queued is the second frame.
	if len(phases) < 2 || phases[0] != "register" || phases[1] != "queued" {
		t.Fatalf("phases prefix = %v, want [register queued ...]; phases = %v", phases[:min(2, len(phases))], phases)
	}
	var queuedCount int
	for _, p := range phases {
		if p == "queued" {
			queuedCount++
		}
	}
	if queuedCount != 1 {
		t.Fatalf("queued events = %d, want exactly 1", queuedCount)
	}
	// The queued payload has no index (batch-scoped).
	if _, ok := events[1]["index"]; ok {
		t.Errorf("queued event carries index field, want none: %v", events[1])
	}
}

// TestImportURL_Serialization_TwoBatches proves that a second batch submitted
// while a first is still in flight emits `queued` immediately and then
// blocks until the first releases the batch semaphore.
func TestImportURL_Serialization_TwoBatches(t *testing.T) {
	release := make(chan struct{})
	var released atomic.Bool
	releaseOnce := func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Send headers immediately so the client-side Fetch fires its Start
		// callback — otherwise waitForPhase("start") would hang here waiting
		// for a response that never arrives.
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(jpegBody)))
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if strings.Contains(r.URL.Path, "hold") {
			<-release
		}
		w.Write(jpegBody)
	}))
	defer srv.Close()
	// Declared AFTER srv.Close so deferred LIFO unblocks the origin first,
	// letting httptest drain its active connection instead of deadlocking.
	defer releaseOnce()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close() // drains thumbPool; see TestImportURL_Queued_EventEmittedOnce.

	// Batch A acquires the semaphore and then blocks inside the origin.
	recA, waitA := postImportStreaming(context.Background(), t, mux,
		"/", []string{srv.URL + "/hold.jpg"})
	waitForPhase(t, recA, "queued")
	waitForPhase(t, recA, "start") // confirms A holds the semaphore

	// Batch B starts while A is still blocked. It must emit queued
	// immediately and then wait at the semaphore.
	recB, waitB := postImportStreaming(context.Background(), t, mux,
		"/", []string{srv.URL + "/b.jpg"})
	waitForPhase(t, recB, "queued")

	// Observation window: B should produce no further frames while A
	// still holds the semaphore. 150 ms is generous for a local mux call.
	select {
	case extra := <-recB.frames:
		t.Fatalf("batch B leaked a frame before batch A released: %s", extra)
	case <-time.After(150 * time.Millisecond):
	}

	releaseOnce()
	waitA()
	waitB()

	phasesB := phasesOf(parseSSEEvents(t, recB.all.String()))
	if !equalSlices(phasesB, []string{"register", "queued", "start", "done", "summary"}) {
		t.Errorf("batch B phases = %v, want [register queued start done summary]", phasesB)
	}
}

// TestImportURL_Queued_CanceledWhileWaiting asserts that a client disconnect
// while a batch is waiting at the semaphore returns early without ever
// acquiring it — so no origin request fires for that batch's URLs.
func TestImportURL_Queued_CanceledWhileWaiting(t *testing.T) {
	release := make(chan struct{})
	var released atomic.Bool
	releaseOnce := func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		// Flush headers first so the client sees Start before we block —
		// mirrors the Serialization test's origin contract.
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", strconv.Itoa(len(jpegBody)))
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if strings.Contains(r.URL.Path, "hold") {
			<-release
		}
		w.Write(jpegBody)
	}))
	defer srv.Close()
	defer releaseOnce()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close() // drains thumbPool; see TestImportURL_Queued_EventEmittedOnce.

	// Batch A holds the semaphore.
	recA, waitA := postImportStreaming(context.Background(), t, mux,
		"/", []string{srv.URL + "/hold.jpg"})
	waitForPhase(t, recA, "start")

	// Batch B queues. Phase 20 contract: handler disconnect alone no longer
	// aborts the job, so to verify the wait-at-semaphore cancellation path we
	// reach into the registry and cancel the job directly. (J5 will add an
	// HTTP endpoint that does this.)
	recB, waitB := postImportStreaming(context.Background(), t, mux,
		"/", []string{srv.URL + "/b.jpg"})
	regEv := waitForPhase(t, recB, "register")
	bJobID, _ := regEv["jobId"].(string)
	if bJobID == "" {
		t.Fatal("batch B register event missing jobId")
	}
	waitForPhase(t, recB, "queued")

	bJob, ok := h.registry.Get(bJobID)
	if !ok {
		t.Fatalf("batch B job %q not in registry", bJobID)
	}
	bJob.Cancel()
	waitB()

	// B never acquired the semaphore so origin must not see its URL. The
	// worker exits via the queued-cancel path: error(cancelled) + summary.
	phasesB := phasesOf(parseSSEEvents(t, recB.all.String()))
	if len(phasesB) == 0 || phasesB[len(phasesB)-1] != "summary" {
		t.Errorf("batch B phases = %v, want trailing summary", phasesB)
	}
	if bJob.Status() != importjob.StatusCancelled {
		t.Errorf("batch B status = %q, want %q", bJob.Status(), importjob.StatusCancelled)
	}

	releaseOnce()
	waitA()

	if got := hits.Load(); got != 1 {
		t.Errorf("origin hits = %d, want 1 (batch B must cancel before its URL is fetched)", got)
	}
}

// TestImportURL_Register_FirstEvent verifies the POST response leads with a
// register frame carrying a base32 jobId — the contract that lets a refresh
// rebind to the same job via GET /api/import-url/jobs/{id}/events.
func TestImportURL_Register_FirstEvent(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	rw := postImport(t, mux, "/", []string{srv.URL + "/cat.jpg"})
	events := parseSSEEvents(t, rw.Body.String())
	if len(events) == 0 {
		t.Fatal("no SSE events emitted")
	}
	first := events[0]
	if first["phase"] != "register" {
		t.Fatalf("first phase = %v, want register; events = %v", first["phase"], phasesOf(events))
	}
	jobID, _ := first["jobId"].(string)
	if jobID == "" {
		t.Fatalf("register event missing jobId: %v", first)
	}
	if !regexp.MustCompile(`^imp_[a-z2-7]{8}$`).MatchString(jobID) {
		t.Errorf("jobId %q does not match imp_[a-z2-7]{8}", jobID)
	}
	if _, ok := h.registry.Get(jobID); !ok {
		t.Errorf("jobId %q not registered after POST response", jobID)
	}
}

// TestImportURL_TooManyJobs verifies the 100-active-job cap surfaces as a
// 429 response without consuming the semaphore. Tests poke the registry's
// internal cap so we don't have to spin up 100 fake batches.
func TestImportURL_TooManyJobs(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	// Pre-filled placeholder jobs have no worker so they would never reach a
	// terminal state on their own — mark them done before Close so its 5s
	// WaitAll(grace) returns immediately.
	defer func() {
		active, _ := h.registry.List()
		for _, j := range active {
			j.SetStatus(importjob.StatusCancelled)
		}
		h.Close()
	}()

	// Pre-fill the registry up to a small cap so the next POST is rejected
	// without having to actually run anything to completion.
	const cap = 3
	for i := 0; i < cap; i++ {
		_, err := h.registry.Create("/", []string{"https://placeholder/" + strconv.Itoa(i)})
		if err != nil {
			t.Fatalf("pre-fill #%d: %v", i, err)
		}
	}
	// Tighten the cap so the next Create returns ErrTooManyJobs without us
	// having to fill 100 slots. SetMaxQueuedForTesting is a documented test
	// seam in importjob; production code never calls it.
	importjob.SetMaxQueuedForTesting(h.registry, cap)

	rw := postImport(t, mux, "/", []string{srv.URL + "/cat.jpg"})
	if rw.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body = %s", rw.Code, rw.Body.String())
	}
	if !strings.Contains(rw.Body.String(), "too_many_jobs") {
		t.Errorf("body = %s, want 'too_many_jobs'", rw.Body.String())
	}
}
