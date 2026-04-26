package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"file_server/internal/importjob"
)

// listJobsResponse is the wire shape of GET /api/import-url/jobs. Splitting
// active and finished up front saves the client a filter pass and matches
// the two distinct UI affordances (live progress rows vs. dismissible
// history rows).
type listJobsResponse struct {
	Active   []importjob.JobSnapshot `json:"active"`
	Finished []importjob.JobSnapshot `json:"finished"`
}

// snapshotEnvelope is the first SSE frame on /jobs/{id}/events. Wrapping the
// snapshot in a phase-tagged envelope keeps the wire format symmetric with
// the live `start/progress/done/error/summary` events; clients route by
// `phase` and never have to special-case the first frame.
type snapshotEnvelope struct {
	Phase string                 `json:"phase"` // always "snapshot"
	Job   importjob.JobSnapshot  `json:"job"`
}

// handleJobsRoot routes /api/import-url/jobs (no trailing slash). GET
// returns the active+finished snapshot; DELETE with `?status=finished`
// clears every terminal job in one call. Other methods/queries 405/400.
// The path-with-trailing-slash variant goes through handleJobsByID below —
// the two registrations together cover the full /jobs and /jobs/{id}/...
// space without ambiguity.
func (h *Handler) handleJobsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListJobs(w, r)
	case http.MethodDelete:
		h.handleDeleteFinishedJobs(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
	}
}

// handleListJobs returns a snapshot of every job the registry currently
// holds, partitioned by terminal state. Encode failures past the header
// boundary are logged and dropped — there is nothing useful to send the
// client at that point and writeError would just produce a malformed
// response on top of the already-flushed body.
func (h *Handler) handleListJobs(w http.ResponseWriter, _ *http.Request) {
	active, finished := h.registry.List()
	body := listJobsResponse{
		Active:   snapshotSlice(active),
		Finished: snapshotSlice(finished),
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("list jobs encode", "err", err)
	}
}

// snapshotSlice fans Snapshot() across a slice of jobs. Pulled out so list
// and (later) cancel-broadcast helpers share the conversion.
func snapshotSlice(jobs []*importjob.Job) []importjob.JobSnapshot {
	out := make([]importjob.JobSnapshot, len(jobs))
	for i, j := range jobs {
		out[i] = j.Snapshot()
	}
	return out
}

// handleJobsByID dispatches /api/import-url/jobs/{id}/{action}. Three
// shapes are wired:
//   GET  /jobs/{id}/events         → SSE snapshot + live stream (J4)
//   POST /jobs/{id}/cancel         → batch or per-URL cancel (J5)
//   DEL  /jobs/{id}                → remove a terminal job (J5)
// Anything else (typo'd action, extra path segments, wrong method) returns
// 404 / 405 so nothing falls through to a default handler.
func (h *Handler) handleJobsByID(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/api/import-url/jobs/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "missing job id", nil)
		return
	}
	jobID := parts[0]
	if len(parts) == 1 {
		// /jobs/{id} bare — only DELETE is valid here.
		switch r.Method {
		case http.MethodDelete:
			h.handleDeleteJob(w, r, jobID)
		default:
			writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		}
		return
	}
	switch parts[1] {
	case "events":
		if len(parts) != 2 {
			writeError(w, r, http.StatusNotFound, "unknown route", nil)
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
			return
		}
		h.handleSubscribeJob(w, r, jobID)
	case "cancel":
		if len(parts) != 2 {
			writeError(w, r, http.StatusNotFound, "unknown route", nil)
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
			return
		}
		h.handleCancelJob(w, r, jobID)
	default:
		writeError(w, r, http.StatusNotFound, "unknown route", nil)
	}
}

// handleCancelJob fires either a per-URL cancel (?index=N) or a whole-batch
// cancel. Per-URL cancellation has two paths:
//
//   - Currently in flight: CancelURL fires the registered ctx and the
//     worker translates that into the standard error("cancelled") event +
//     summary counter increment.
//   - Pending (not yet started): we set URLState to cancelled and emit the
//     error event ourselves; the worker's loop checks URLStatus before
//     each fetch and skips already-terminal entries.
//
// Already-terminal URLs return 409. The job itself must still be active
// (queued or running); a cancel against a finished job is 409.
func (h *Handler) handleCancelJob(w http.ResponseWriter, r *http.Request, jobID string) {
	job, ok := h.registry.Get(jobID)
	if !ok {
		writeError(w, r, http.StatusNotFound, "job not found", nil)
		return
	}
	if !job.IsActive() {
		writeError(w, r, http.StatusConflict, "job already finished", nil)
		return
	}

	indexStr := r.URL.Query().Get("index")
	if indexStr == "" {
		// Whole-batch cancel — worker observes job.Ctx().Done() and emits
		// the cancelled events / summary itself.
		job.Cancel()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	idx, err := strconv.Atoi(indexStr)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid index", err)
		return
	}
	if idx < 0 || idx >= job.URLCount() {
		writeError(w, r, http.StatusBadRequest, "index out of range", nil)
		return
	}

	// Atomic decision: registered cancel takes precedence (worker emits the
	// error frame); pending → mark + handler emits the error frame; else
	// terminal → 409. Doing both checks under one lock closes the previous
	// race window where the worker could RegisterURLCancel between the two
	// separate handler-side checks.
	url, kind := job.CancelOne(idx)
	switch kind {
	case importjob.CancelKindRunning:
		// Worker observes ctx.Done() and emits error("cancelled") via
		// fetchOneJob's existing cancellation path — handler stays silent.
		w.WriteHeader(http.StatusNoContent)
	case importjob.CancelKindPending:
		// Worker bypasses this index on its next URLStatus check; we own
		// the lifecycle event for the row.
		job.Publish(mustEvent("error", sseError{
			Phase: "error", Index: idx, URL: url, Error: "cancelled",
		}))
		w.WriteHeader(http.StatusNoContent)
	default:
		// Terminal or unknown — nothing to cancel.
		writeError(w, r, http.StatusConflict, "url already finished", nil)
	}
}

// handleDeleteJob removes a terminal job from the registry. Active jobs
// (queued/running) require an explicit cancel first → 409. Unknown ids 404.
// Subscribers, if any, were already detached at SetStatus(terminal) time so
// no broadcast is needed here.
func (h *Handler) handleDeleteJob(w http.ResponseWriter, r *http.Request, jobID string) {
	if err := h.registry.Remove(jobID); err != nil {
		switch {
		case errors.Is(err, importjob.ErrJobNotFound):
			writeError(w, r, http.StatusNotFound, "job not found", nil)
		case errors.Is(err, importjob.ErrJobActive):
			writeError(w, r, http.StatusConflict, "job_active", nil)
		default:
			writeError(w, r, http.StatusInternalServerError, "remove failed", err)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteFinishedJobs removes every terminal job at once. Validates
// the `status=finished` query so a stray DELETE without filter cannot be
// silently interpreted as "wipe everything".
func (h *Handler) handleDeleteFinishedJobs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("status") != "finished" {
		writeError(w, r, http.StatusBadRequest, "missing status=finished filter", nil)
		return
	}
	n := h.registry.RemoveFinished()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]int{"removed": n}); err != nil {
		slog.Error("remove finished encode", "err", err)
	}
}

// handleSubscribeJob streams the named job's lifecycle as SSE: a single
// snapshot frame followed by every subsequent live event until the job
// terminates. A subscriber that arrives after the job is already finished
// receives only the snapshot — SubscribeWithSnapshot pre-closes its
// channel in that case so the loop falls out immediately on the first read.
func (h *Handler) handleSubscribeJob(w http.ResponseWriter, r *http.Request, jobID string) {
	job, ok := h.registry.Get(jobID)
	if !ok {
		writeError(w, r, http.StatusNotFound, "job not found", nil)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "streaming unsupported", nil)
		return
	}

	// Atomic snapshot+subscribe under a single j.mu hold — closes the race
	// window where an event published between Snapshot() and Subscribe()
	// would land in both the snapshot AND the channel and double-count on
	// the client.
	snapshot, events, unsubscribe := job.SubscribeWithSnapshot()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeSSEEvent(w, flusher, snapshotEnvelope{Phase: "snapshot", Job: snapshot})

	for {
		select {
		case ev, open := <-events:
			if !open {
				// Job reached terminal state — SetStatus closed our channel.
				// Snapshot already reflects the terminal status (the handler
				// always returns after delivering it).
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", ev.Data)
			flusher.Flush()
			if ev.Phase == "summary" {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}

