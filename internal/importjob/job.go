// Package importjob holds the in-memory registry of URL-import jobs.
//
// A Job represents one batch of URLs being downloaded by handleImportURL.
// The job's lifetime is decoupled from the HTTP request that created it: when
// the client closes its tab or refreshes, the request goroutine returns but
// the job continues until it finishes naturally or the user cancels it.
// New SSE subscribers can attach mid-flight (from a different tab or after a
// refresh) and immediately receive a snapshot of current state, then live
// progress for the rest of the run.
package importjob

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// defaultSubBuffer is the per-subscriber channel capacity. Publish drops
// events for any subscriber whose buffer is full so a single slow consumer
// (e.g. a tab whose JS event loop is wedged) cannot stall the worker
// goroutine that produces events for everyone else. Subscribers that miss
// events recover by reconnecting and re-reading the snapshot.
const defaultSubBuffer = 64

// Status is the lifecycle state of a Job.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// IsTerminal reports whether the status represents a finished job that will
// emit no further events.
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// URLState is the snapshot view of a single URL inside a job. New SSE
// subscribers receive this as part of the JobSnapshot so they can reconstruct
// the per-row UI without replaying every progress event.
type URLState struct {
	URL      string   `json:"url"`
	Name     string   `json:"name,omitempty"`
	Type     string   `json:"type,omitempty"`
	Status   string   `json:"status"` // pending | running | done | error | cancelled
	Received int64    `json:"received"`
	Total    int64    `json:"total,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// Summary is the per-batch terminal counter set published as the last SSE
// event when a job ends.
type Summary struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

// Event is a fully marshalled SSE payload ready to be written to the wire as
// `data: <Data>\n\n`. Phase mirrors the JSON `phase` field for cheap routing
// without re-parsing Data.
type Event struct {
	Phase string
	Data  json.RawMessage
}

// JobSnapshot is the JSON-serializable view of a Job sent to new subscribers
// and as the body of GET /api/import-url/jobs.
type JobSnapshot struct {
	ID        string     `json:"id"`
	DestPath  string     `json:"destPath"`
	Status    Status     `json:"status"`
	CreatedAt time.Time  `json:"createdAt"`
	URLs      []URLState `json:"urls"`
	Summary   *Summary   `json:"summary,omitempty"`
}

// Job is one URL-import batch. All exported state-mutating methods are
// goroutine-safe; readers should use Snapshot rather than touching internals.
type Job struct {
	ID        string
	DestPath  string
	CreatedAt time.Time

	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	status     Status
	urls       []URLState
	summary    *Summary
	subs       map[uint64]chan Event
	nextSubID  uint64
	urlCancels map[int]context.CancelFunc
}

// Ctx returns the job's context, which the worker should pass into urlfetch
// instead of the HTTP request context. Cancelling it (via Cancel or via the
// parent server context shutting down) terminates the whole batch.
func (j *Job) Ctx() context.Context {
	return j.ctx
}

// Status returns the current lifecycle state.
func (j *Job) Status() Status {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

// IsActive reports whether the job is still queued or running.
func (j *Job) IsActive() bool {
	return !j.Status().IsTerminal()
}

// SetStatus transitions the job to a new state. Callers are responsible for
// emitting the corresponding SSE event (e.g. summary) separately so that the
// Status field and the broadcast remain in sync.
func (j *Job) SetStatus(s Status) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = s
}

// SetSummary records the terminal counters. Has no effect on Status; the
// caller calls SetStatus separately.
func (j *Job) SetSummary(s Summary) {
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := s
	j.summary = &cp
}

// UpdateURL applies fn to the URLState at idx under the job mutex. fn runs
// with exclusive access; it should be quick and must not call back into Job
// methods that would deadlock.
func (j *Job) UpdateURL(idx int, fn func(*URLState)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if idx < 0 || idx >= len(j.urls) {
		return
	}
	fn(&j.urls[idx])
}

// Snapshot returns a deep copy of the job's externally visible state safe to
// hand to JSON encoders or other goroutines.
func (j *Job) Snapshot() JobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	urlsCopy := make([]URLState, len(j.urls))
	for i, u := range j.urls {
		urlsCopy[i] = u
		if u.Warnings != nil {
			urlsCopy[i].Warnings = append([]string(nil), u.Warnings...)
		}
	}
	var sum *Summary
	if j.summary != nil {
		s := *j.summary
		sum = &s
	}
	return JobSnapshot{
		ID:        j.ID,
		DestPath:  j.DestPath,
		Status:    j.status,
		CreatedAt: j.CreatedAt,
		URLs:      urlsCopy,
		Summary:   sum,
	}
}

// Subscribe registers a new live event listener. The returned channel is
// closed by the unsubscribe function — callers should always defer it to
// avoid leaking the channel. The channel uses a small buffer (defaultSubBuffer)
// and Publish drops events on overflow rather than blocking.
func (j *Job) Subscribe() (<-chan Event, func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.subs == nil {
		j.subs = make(map[uint64]chan Event)
	}
	id := j.nextSubID
	j.nextSubID++
	ch := make(chan Event, defaultSubBuffer)
	j.subs[id] = ch
	return ch, func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if c, ok := j.subs[id]; ok {
			delete(j.subs, id)
			close(c)
		}
	}
}

// Publish broadcasts ev to every active subscriber. Sends are non-blocking;
// any subscriber whose buffer is full simply loses this event and recovers on
// reconnect via the snapshot.
func (j *Job) Publish(ev Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, ch := range j.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Cancel fires the job-wide context, which is what the worker should pass to
// urlfetch. Status is not changed here — the worker observes the cancellation
// and calls SetStatus(StatusCancelled) once it has finished cleaning up.
func (j *Job) Cancel() {
	j.cancel()
}

// RegisterURLCancel records the per-URL cancel function so an external caller
// (the cancel HTTP handler) can stop just one fetch without aborting the
// whole batch. The worker calls UnregisterURLCancel in defer to keep the map
// clean.
func (j *Job) RegisterURLCancel(idx int, cancel context.CancelFunc) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.urlCancels == nil {
		j.urlCancels = make(map[int]context.CancelFunc)
	}
	j.urlCancels[idx] = cancel
}

// UnregisterURLCancel removes the per-URL cancel registration. Safe to call
// even if no entry exists.
func (j *Job) UnregisterURLCancel(idx int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.urlCancels, idx)
}

// CancelURL fires the per-URL cancel function for idx, returning true if one
// was registered. Returns false if the URL is not currently in flight (either
// not yet started or already finished and unregistered).
func (j *Job) CancelURL(idx int) bool {
	j.mu.Lock()
	cancel, ok := j.urlCancels[idx]
	j.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}
