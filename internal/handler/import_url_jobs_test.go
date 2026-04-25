package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/chang/file_server/internal/importjob"
)

// listJobsBody decodes the GET /api/import-url/jobs response so tests can
// assert on `active` / `finished` independently.
type listJobsBody struct {
	Active   []importjob.JobSnapshot `json:"active"`
	Finished []importjob.JobSnapshot `json:"finished"`
}

func getJobs(t *testing.T, mux *http.ServeMux) listJobsBody {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/import-url/jobs", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rw.Code, rw.Body.String())
	}
	var body listJobsBody
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body = %s", err, rw.Body.String())
	}
	return body
}

func TestListJobs_Empty(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	body := getJobs(t, mux)
	if len(body.Active) != 0 || len(body.Finished) != 0 {
		t.Errorf("active=%v finished=%v, want both empty", body.Active, body.Finished)
	}
}

func TestListJobs_ActiveAndFinished(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	// Pre-filled placeholder jobs need explicit termination so Close does
	// not block the full WaitAll budget.
	defer func() {
		active, _ := h.registry.List()
		for _, j := range active {
			j.SetStatus(importjob.StatusCancelled)
		}
		h.Close()
	}()

	// Two finished, one still active.
	for i := 0; i < 2; i++ {
		j, err := h.registry.Create("/", []string{"https://placeholder/" + string(rune('a'+i))})
		if err != nil {
			t.Fatalf("create finished #%d: %v", i, err)
		}
		j.SetStatus(importjob.StatusCompleted)
	}
	if _, err := h.registry.Create("/", []string{"https://running"}); err != nil {
		t.Fatalf("create active: %v", err)
	}

	body := getJobs(t, mux)
	if len(body.Active) != 1 {
		t.Errorf("active = %d, want 1; body = %+v", len(body.Active), body)
	}
	if len(body.Finished) != 2 {
		t.Errorf("finished = %d, want 2; body = %+v", len(body.Finished), body)
	}
	for _, snap := range body.Finished {
		if snap.Status != importjob.StatusCompleted {
			t.Errorf("finished status = %q, want %q", snap.Status, importjob.StatusCompleted)
		}
	}
}

func TestListJobs_MethodNotAllowed(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		req := httptest.NewRequest(method, "/api/import-url/jobs", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		if rw.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s status = %d, want 405", method, rw.Code)
		}
	}
}

func TestSubscribeJob_NotFound(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	req := httptest.NewRequest(http.MethodGet, "/api/import-url/jobs/imp_unknown/events", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rw.Code)
	}
}

func TestSubscribeJob_FinishedReturnsSnapshotAndCloses(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	job, err := h.registry.Create("/", []string{"https://placeholder/x"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	job.SetSummary(importjob.Summary{Succeeded: 1})
	job.SetStatus(importjob.StatusCompleted) // also closes any subscriber channels

	req := httptest.NewRequest(http.MethodGet,
		"/api/import-url/jobs/"+job.ID+"/events", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rw.Code)
	}
	events := parseSSEEvents(t, rw.Body.String())
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 snapshot only; got phases %v", len(events), phasesOf(events))
	}
	if events[0]["phase"] != "snapshot" {
		t.Errorf("first phase = %v, want snapshot", events[0]["phase"])
	}
	embedded, ok := events[0]["job"].(map[string]any)
	if !ok {
		t.Fatalf("snapshot frame missing job: %v", events[0])
	}
	if embedded["status"] != string(importjob.StatusCompleted) {
		t.Errorf("snapshot status = %v, want completed", embedded["status"])
	}
}

// TestSubscribeJob_ActiveReceivesLiveEvents drives a real import POST, then
// uses a *second* subscriber via /events to confirm the broadcast path works
// end-to-end: snapshot replay is followed by live progress and the closing
// summary frame.
func TestSubscribeJob_ActiveReceivesLiveEvents(t *testing.T) {
	// Origin holds the response until released so we have a deterministic
	// window during which the second subscriber attaches.
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseFn := func() { releaseOnce.Do(func() { close(release) }) }

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Content-Length", "22")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release
		w.Write(jpegBody)
	}))
	defer srv.Close()
	defer releaseFn()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	// First subscriber: the POST itself. We only need it to expose the jobId.
	recA, waitA := postImportStreaming(context.Background(), t, mux,
		"/", []string{srv.URL + "/cat.jpg"})
	regEv := waitForPhase(t, recA, "register")
	jobID, _ := regEv["jobId"].(string)
	if jobID == "" {
		t.Fatal("register event missing jobId")
	}
	waitForPhase(t, recA, "start") // confirms the worker is running mid-fetch

	// Second subscriber via the new endpoint.
	recB := newStreamingRecorder()
	reqB := httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/import-url/jobs/"+jobID+"/events", nil)
	doneB := make(chan struct{})
	go func() {
		defer close(doneB)
		mux.ServeHTTP(recB, reqB)
	}()

	snap := waitForPhase(t, recB, "snapshot")
	if job, ok := snap["job"].(map[string]any); !ok {
		t.Errorf("snapshot frame missing embedded job: %v", snap)
	} else if job["id"] != jobID {
		t.Errorf("snapshot job.id = %v, want %v", job["id"], jobID)
	}

	// Release the origin so the worker emits done + summary into both subs.
	releaseFn()
	waitForPhase(t, recB, "done")
	waitForPhase(t, recB, "summary")
	<-doneB
	waitA()
}

func TestSubscribeJob_BadRoute(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	job, err := h.registry.Create("/", []string{"u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer job.SetStatus(importjob.StatusCancelled)

	cases := []struct {
		path string
		want int
	}{
		{"/api/import-url/jobs/" + job.ID, http.StatusNotFound},                  // missing action
		{"/api/import-url/jobs/" + job.ID + "/events/extra", http.StatusNotFound}, // suffix junk
		{"/api/import-url/jobs/" + job.ID + "/cancel", http.StatusNotFound},       // J5 wires this
		{"/api/import-url/jobs/", http.StatusNotFound},                            // empty id
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		if rw.Code != tc.want {
			t.Errorf("GET %s: status = %d, want %d; body = %s",
				tc.path, rw.Code, tc.want, strings.TrimSpace(rw.Body.String()))
		}
	}
}

// TestSubscribeJob_MethodNotAllowed ensures /events refuses POST/PUT/etc.
func TestSubscribeJob_MethodNotAllowed(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	job, err := h.registry.Create("/", []string{"u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer job.SetStatus(importjob.StatusCancelled)

	req := httptest.NewRequest(http.MethodPost,
		"/api/import-url/jobs/"+job.ID+"/events", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rw.Code)
	}
}

