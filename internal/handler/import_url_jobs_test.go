package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
	h := registerImportTest(mux, root)
	defer h.Close()

	body := getJobs(t, mux)
	if len(body.Active) != 0 || len(body.Finished) != 0 {
		t.Errorf("active=%v finished=%v, want both empty", body.Active, body.Finished)
	}
}

func TestListJobs_ActiveAndFinished(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
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
	h := registerImportTest(mux, root)
	defer h.Close()

	// DELETE is also a valid method on /jobs (J5 ?status=finished) so it
	// stays out of the 405 list — its bad-request behaviour is covered by
	// TestDeleteFinishedJobs.
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch} {
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
	h := registerImportTest(mux, root)
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
	h := registerImportTest(mux, root)
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
	h := registerImportTest(mux, root)
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
	h := registerImportTest(mux, root)
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
		{"/api/import-url/jobs/" + job.ID, http.StatusMethodNotAllowed},           // bare id only allows DELETE
		{"/api/import-url/jobs/" + job.ID + "/events/extra", http.StatusNotFound}, // suffix junk
		{"/api/import-url/jobs/" + job.ID + "/unknown", http.StatusNotFound},      // typo'd action
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
	h := registerImportTest(mux, root)
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

// ── Cancel / Dismiss (Phase 20 J5) ───────────────────────────────────────────

// TestCancelJob_Batch: a whole-batch cancel during semaphore wait fires the
// job's context, the worker emits the cancelled events + summary, and the
// status lands as Cancelled.
func TestCancelJob_Batch(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer h.Close()

	// Hold the import semaphore so the batch we cancel never reaches the
	// fetch loop — it's purely the queued-then-cancelled path.
	h.importSem <- struct{}{}
	defer func() { <-h.importSem }()

	rec, wait := postImportStreaming(context.Background(), t, mux,
		"/", []string{srv.URL + "/a.jpg", srv.URL + "/b.jpg"})
	regEv := waitForPhase(t, rec, "register")
	jobID, _ := regEv["jobId"].(string)
	waitForPhase(t, rec, "queued")

	cancelReq := httptest.NewRequest(http.MethodPost,
		"/api/import-url/jobs/"+jobID+"/cancel", nil)
	cancelRW := httptest.NewRecorder()
	mux.ServeHTTP(cancelRW, cancelReq)
	if cancelRW.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204; body = %s", cancelRW.Code, cancelRW.Body.String())
	}

	// Wait for the worker to finalize. Hold the sem so it never acquires —
	// cancel-while-queued is the path we're exercising.
	wait()
	job, ok := h.registry.Get(jobID)
	if !ok {
		t.Fatalf("job %q gone from registry", jobID)
	}
	select {
	case <-job.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("job never finalized after cancel")
	}
	if got := job.Status(); got != importjob.StatusCancelled {
		t.Errorf("status = %q, want %q", got, importjob.StatusCancelled)
	}
}

// TestCancelJob_PerURL_Running: cancel a URL that is currently in flight.
// The worker observes the per-URL ctx fire, urlfetch returns a cancellation
// error, and fetchOneJob's existing path emits exactly one error("cancelled")
// frame for that index. The handler responds 204 without publishing its
// own frame — duplication would inflate the cancelled counter.
func TestCancelJob_PerURL_Running(t *testing.T) {
	// Origin holds index 0 indefinitely; index 1 finishes promptly so the
	// batch's terminal state lands as Completed (succeeded≥1) per spec §3.6.
	srv, releaseFn := newHoldReleaseOrigin(t)
	defer srv.Close()
	defer releaseFn()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer h.Close()

	rec, wait := postImportStreaming(context.Background(), t, mux,
		"/", []string{srv.URL + "/hold.jpg", srv.URL + "/b.jpg"})
	regEv := waitForPhase(t, rec, "register")
	jobID, _ := regEv["jobId"].(string)
	waitForPhase(t, rec, "start") // hold.jpg is in flight

	cancelReq := httptest.NewRequest(http.MethodPost,
		"/api/import-url/jobs/"+jobID+"/cancel?index=0", nil)
	cancelRW := httptest.NewRecorder()
	mux.ServeHTTP(cancelRW, cancelReq)
	if cancelRW.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204; body = %s",
			cancelRW.Code, cancelRW.Body.String())
	}

	releaseFn()
	wait()

	job, _ := h.registry.Get(jobID)
	select {
	case <-job.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("job never finished")
	}

	// Exactly one error frame for index 0 — duplicate emission would
	// indicate handler-side double-publishing on the running path.
	body := rec.all.String()
	events := parseSSEEvents(t, body)
	errFrames := 0
	for _, e := range events {
		if e["phase"] != "error" {
			continue
		}
		if int(e["index"].(float64)) == 0 && e["error"] == "cancelled" {
			errFrames++
		}
	}
	if errFrames != 1 {
		t.Errorf("error(cancelled) frames for index 0 = %d, want exactly 1; phases = %v",
			errFrames, phasesOf(events))
	}

	snap := job.Snapshot()
	if snap.URLs[0].Status != "cancelled" {
		t.Errorf("urls[0].status = %q, want cancelled", snap.URLs[0].Status)
	}
	if snap.Summary == nil || snap.Summary.Cancelled != 1 || snap.Summary.Succeeded != 1 {
		t.Errorf("summary = %+v, want {succeeded:1, cancelled:1}", snap.Summary)
	}
	if got := job.Status(); got != importjob.StatusCompleted {
		t.Errorf("status = %q, want %q (1 success → completed)", got, importjob.StatusCompleted)
	}
}

// TestCancelJob_PerURL_Pending: cancel one URL while it is still pending
// (worker hasn't reached it yet). The worker skips it when it gets there;
// the rest of the batch finishes normally and status is Completed.
func TestCancelJob_PerURL_Pending(t *testing.T) {
	// Origin holds the FIRST URL ("hold") so URL 1 is pending while we
	// cancel it; URL 0 finishes after we release.
	srv, releaseFn := newHoldReleaseOrigin(t)
	defer srv.Close()
	defer releaseFn()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer h.Close()

	rec, wait := postImportStreaming(context.Background(), t, mux,
		"/", []string{srv.URL + "/hold.jpg", srv.URL + "/b.jpg"})
	regEv := waitForPhase(t, rec, "register")
	jobID, _ := regEv["jobId"].(string)
	waitForPhase(t, rec, "start") // index 0 (hold.jpg) is now running

	// Cancel index 1 (still pending — worker is stuck on index 0).
	cancelReq := httptest.NewRequest(http.MethodPost,
		"/api/import-url/jobs/"+jobID+"/cancel?index=1", nil)
	cancelRW := httptest.NewRecorder()
	mux.ServeHTTP(cancelRW, cancelReq)
	if cancelRW.Code != http.StatusNoContent {
		t.Fatalf("cancel status = %d, want 204", cancelRW.Code)
	}

	releaseFn()
	wait()

	job, _ := h.registry.Get(jobID)
	select {
	case <-job.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("job never finished")
	}
	// 1 success + 1 cancelled — succeeded ≥ 1 wins, status = Completed.
	if got := job.Status(); got != importjob.StatusCompleted {
		t.Errorf("status = %q, want %q (succeeded ≥ 1 → completed)", got, importjob.StatusCompleted)
	}
	snap := job.Snapshot()
	if snap.URLs[1].Status != "cancelled" {
		t.Errorf("url[1] status = %q, want cancelled", snap.URLs[1].Status)
	}
	if snap.Summary == nil || snap.Summary.Succeeded != 1 || snap.Summary.Cancelled != 1 {
		t.Errorf("summary = %+v, want {succeeded:1, cancelled:1}", snap.Summary)
	}
}

// TestCancelJob_PerURL_Pending_ThenBatch: a per-URL cancel for a pending
// index publishes its error("cancelled") frame from the handler side
// (CancelKindPending). If the user immediately follows with a batch
// cancel, the worker's mid-flight cancel loop must NOT re-emit the same
// frame for that index — the per-URL handler already owns it. Regression
// guard for the round-5 CQ1 finding.
func TestCancelJob_PerURL_Pending_ThenBatch(t *testing.T) {
	// Origin holds index 0 so URLs 1 and 2 stay pending while we cancel
	// index 1, then cancel the batch. Index 0 is in flight when the batch
	// cancel arrives; index 2 is still pending and rides the mid-flight
	// cancel loop.
	srv, releaseFn := newHoldReleaseOrigin(t)
	defer srv.Close()
	defer releaseFn()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer h.Close()

	rec, wait := postImportStreaming(context.Background(), t, mux,
		"/", []string{srv.URL + "/hold.jpg", srv.URL + "/b.jpg", srv.URL + "/c.jpg"})
	regEv := waitForPhase(t, rec, "register")
	jobID, _ := regEv["jobId"].(string)
	waitForPhase(t, rec, "start") // index 0 is running on hold

	// Per-URL cancel for index 1 (pending). Handler emits its own
	// error("cancelled") frame under CancelKindPending.
	cancel1 := httptest.NewRequest(http.MethodPost,
		"/api/import-url/jobs/"+jobID+"/cancel?index=1", nil)
	cancel1RW := httptest.NewRecorder()
	mux.ServeHTTP(cancel1RW, cancel1)
	if cancel1RW.Code != http.StatusNoContent {
		t.Fatalf("per-URL cancel status = %d, want 204", cancel1RW.Code)
	}

	// Batch cancel — fires job.Cancel() so URL 0's per-URL ctx (child of
	// job.Ctx()) cancels and the worker enters the mid-flight cancel loop.
	cancelAll := httptest.NewRequest(http.MethodPost,
		"/api/import-url/jobs/"+jobID+"/cancel", nil)
	cancelAllRW := httptest.NewRecorder()
	mux.ServeHTTP(cancelAllRW, cancelAll)
	if cancelAllRW.Code != http.StatusNoContent {
		t.Fatalf("batch cancel status = %d, want 204", cancelAllRW.Code)
	}

	releaseFn()
	wait()

	job, _ := h.registry.Get(jobID)
	select {
	case <-job.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("job never finished")
	}

	// Each index must receive exactly one error("cancelled") frame.
	// Without the URLStatus skip in runImportJob's mid-flight cancel loop,
	// index 1 would see 2 frames (CancelKindPending handler + worker loop).
	body := rec.all.String()
	events := parseSSEEvents(t, body)
	perIndex := map[int]int{}
	for _, e := range events {
		if e["phase"] != "error" || e["error"] != "cancelled" {
			continue
		}
		idx := int(e["index"].(float64))
		perIndex[idx]++
	}
	for idx := 0; idx < 3; idx++ {
		if perIndex[idx] != 1 {
			t.Errorf("error(cancelled) frames for index %d = %d, want exactly 1; per-index counts = %v",
				idx, perIndex[idx], perIndex)
		}
	}

	if got := job.Status(); got != importjob.StatusCancelled {
		t.Errorf("status = %q, want %q", got, importjob.StatusCancelled)
	}
	if snap := job.Snapshot(); snap.Summary == nil || snap.Summary.Cancelled != 3 {
		t.Errorf("summary = %+v, want {cancelled:3}", snap.Summary)
	}
}

// TestCancelJob_PerURL_Pending_ThenBatch_PreSemaphore: same regression as
// above but for the pre-semaphore cancel path (batch is queued, never
// reaches the worker fetch loop). Per-URL cancel marks index 1 cancelled
// and emits its frame; batch cancel triggers job.Ctx().Done() before
// runImportJob acquires importSem; the pre-semaphore cancel branch must
// skip the already-cancelled index.
func TestCancelJob_PerURL_Pending_ThenBatch_PreSemaphore(t *testing.T) {
	srv := newOriginServer()
	defer srv.Close()

	root := t.TempDir()
	mux := http.NewServeMux()
	h := Register(mux, root, root, nil)
	defer h.Close()

	// Hold importSem so the batch never enters the fetch loop.
	h.importSem <- struct{}{}
	defer func() { <-h.importSem }()

	rec, wait := postImportStreaming(context.Background(), t, mux,
		"/", []string{srv.URL + "/a.jpg", srv.URL + "/b.jpg", srv.URL + "/c.jpg"})
	regEv := waitForPhase(t, rec, "register")
	jobID, _ := regEv["jobId"].(string)
	waitForPhase(t, rec, "queued")

	cancel1 := httptest.NewRequest(http.MethodPost,
		"/api/import-url/jobs/"+jobID+"/cancel?index=1", nil)
	cancel1RW := httptest.NewRecorder()
	mux.ServeHTTP(cancel1RW, cancel1)
	if cancel1RW.Code != http.StatusNoContent {
		t.Fatalf("per-URL cancel status = %d, want 204", cancel1RW.Code)
	}

	cancelAll := httptest.NewRequest(http.MethodPost,
		"/api/import-url/jobs/"+jobID+"/cancel", nil)
	cancelAllRW := httptest.NewRecorder()
	mux.ServeHTTP(cancelAllRW, cancelAll)
	if cancelAllRW.Code != http.StatusNoContent {
		t.Fatalf("batch cancel status = %d, want 204", cancelAllRW.Code)
	}

	wait()
	job, _ := h.registry.Get(jobID)
	select {
	case <-job.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("job never finished")
	}

	body := rec.all.String()
	events := parseSSEEvents(t, body)
	perIndex := map[int]int{}
	for _, e := range events {
		if e["phase"] != "error" || e["error"] != "cancelled" {
			continue
		}
		idx := int(e["index"].(float64))
		perIndex[idx]++
	}
	for idx := 0; idx < 3; idx++ {
		if perIndex[idx] != 1 {
			t.Errorf("error(cancelled) frames for index %d = %d, want exactly 1; per-index counts = %v",
				idx, perIndex[idx], perIndex)
		}
	}
}

func TestCancelJob_NotFound(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer h.Close()

	req := httptest.NewRequest(http.MethodPost,
		"/api/import-url/jobs/imp_unknown/cancel", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rw.Code)
	}
}

func TestCancelJob_AlreadyFinished(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer h.Close()

	job, err := h.registry.Create("/", []string{"u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	job.SetStatus(importjob.StatusCompleted)

	req := httptest.NewRequest(http.MethodPost,
		"/api/import-url/jobs/"+job.ID+"/cancel", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rw.Code)
	}
}

func TestCancelJob_BadIndex(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer h.Close()

	job, err := h.registry.Create("/", []string{"u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer job.SetStatus(importjob.StatusCancelled)

	cases := []struct {
		query string
		want  int
	}{
		{"?index=abc", http.StatusBadRequest},
		{"?index=-1", http.StatusBadRequest},
		{"?index=99", http.StatusBadRequest},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodPost,
			"/api/import-url/jobs/"+job.ID+"/cancel"+tc.query, nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		if rw.Code != tc.want {
			t.Errorf("query=%s: status = %d, want %d", tc.query, rw.Code, tc.want)
		}
	}
}

func TestDeleteJob_Active(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer h.Close()

	job, err := h.registry.Create("/", []string{"u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer job.SetStatus(importjob.StatusCancelled)

	req := httptest.NewRequest(http.MethodDelete, "/api/import-url/jobs/"+job.ID, nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "job_active") {
		t.Errorf("body = %s, want job_active", rw.Body.String())
	}
}

func TestDeleteJob_Finished(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer h.Close()

	job, err := h.registry.Create("/", []string{"u"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	job.SetStatus(importjob.StatusCompleted)

	req := httptest.NewRequest(http.MethodDelete, "/api/import-url/jobs/"+job.ID, nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", rw.Code, rw.Body.String())
	}
	if _, ok := h.registry.Get(job.ID); ok {
		t.Errorf("job still present after DELETE")
	}
}

func TestDeleteJob_NotFound(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer h.Close()

	req := httptest.NewRequest(http.MethodDelete, "/api/import-url/jobs/imp_unknown", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rw.Code)
	}
}

func TestDeleteFinishedJobs(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	h := registerImportTest(mux, root)
	defer func() {
		// Non-terminated placeholders need explicit cleanup so Close does
		// not consume the WaitAll budget.
		active, _ := h.registry.List()
		for _, j := range active {
			j.SetStatus(importjob.StatusCancelled)
		}
		h.Close()
	}()

	// 2 completed, 1 failed, 1 still queued.
	for _, st := range []importjob.Status{
		importjob.StatusCompleted,
		importjob.StatusCompleted,
		importjob.StatusFailed,
	} {
		j, err := h.registry.Create("/", []string{"u"})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		j.SetStatus(st)
	}
	if _, err := h.registry.Create("/", []string{"u"}); err != nil {
		t.Fatalf("create active: %v", err)
	}

	// Filter required — bare DELETE 400.
	bareReq := httptest.NewRequest(http.MethodDelete, "/api/import-url/jobs", nil)
	bareRW := httptest.NewRecorder()
	mux.ServeHTTP(bareRW, bareReq)
	if bareRW.Code != http.StatusBadRequest {
		t.Errorf("bare DELETE status = %d, want 400", bareRW.Code)
	}

	req := httptest.NewRequest(http.MethodDelete,
		"/api/import-url/jobs?status=finished", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rw.Code, rw.Body.String())
	}
	var body map[string]int
	if err := json.Unmarshal(rw.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["removed"] != 3 {
		t.Errorf("removed = %d, want 3", body["removed"])
	}
	active, finished := h.registry.List()
	if len(finished) != 0 {
		t.Errorf("finished still has %d entries after delete", len(finished))
	}
	if len(active) != 1 {
		t.Errorf("active count = %d, want 1 (one job was queued)", len(active))
	}
}
