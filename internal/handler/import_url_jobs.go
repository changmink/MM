package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/chang/file_server/internal/importjob"
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

// handleJobsRoot routes GET /api/import-url/jobs (no trailing slash). The
// path-with-trailing-slash variant goes through handleJobsByID below — the
// two registrations together cover the full /jobs and /jobs/{id}/... space
// without ambiguity.
func (h *Handler) handleJobsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.handleListJobs(w, r)
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

// handleJobsByID dispatches /api/import-url/jobs/{id}/{action}. Only the
// `events` action is wired in J4; J5 adds /cancel and DELETE on the bare id.
// Unknown actions return 404 so a typo doesn't get silently routed somewhere
// nonsensical.
func (h *Handler) handleJobsByID(w http.ResponseWriter, r *http.Request) {
	suffix := strings.TrimPrefix(r.URL.Path, "/api/import-url/jobs/")
	parts := strings.Split(suffix, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "missing job id", nil)
		return
	}
	jobID := parts[0]
	if len(parts) == 1 {
		// /jobs/{id} with no action — J5 will add DELETE here. For J4 the
		// only legal endpoint is /events.
		writeError(w, r, http.StatusNotFound, "unknown route", nil)
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
	default:
		writeError(w, r, http.StatusNotFound, "unknown route", nil)
	}
}

// handleSubscribeJob streams the named job's lifecycle as SSE: a single
// snapshot frame followed by every subsequent live event until the job
// terminates. A subscriber that arrives after the job is already finished
// receives only the snapshot — Subscribe pre-closes its channel in that
// case so the loop falls out immediately on the first read.
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

	// Subscribe BEFORE writing the snapshot so any event published in the
	// gap between snapshot capture and stream start lands in the channel
	// buffer instead of being lost.
	events, unsubscribe := job.Subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	writeSSEEvent(w, flusher, snapshotEnvelope{Phase: "snapshot", Job: job.Snapshot()})

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

