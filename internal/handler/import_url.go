package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/chang/file_server/internal/importjob"
	"github.com/chang/file_server/internal/media"
	"github.com/chang/file_server/internal/settings"
	"github.com/chang/file_server/internal/urlfetch"
)

const (
	maxImportURLs = 50
	// maxImportURLLength bounds individual URL strings so the registry cannot
	// be force-fed arbitrarily large blobs that later surface verbatim through
	// GET /api/import-url/jobs. 2 KB is well above any legitimate signed-URL
	// length seen in practice (S3, Cloudfront, Mux ~1.5 KB worst case).
	maxImportURLLength = 2048
	// progressChanBuffer lets the Fetch goroutine drop samples instead of
	// blocking when broadcast falls behind. A slow subscriber must never
	// stall the download.
	progressChanBuffer = 16
)

type importRequest struct {
	URLs []string `json:"urls"`
}

// sseRegister is the first frame of the POST response. It hands the client
// the jobId so a refresh can re-subscribe via GET /jobs/{id}/events. It is
// written directly to the request writer (not via Job.Publish) — register is
// per-request metadata, not job state, so other subscribers do not see it.
type sseRegister struct {
	Phase string `json:"phase"` // always "register"
	JobID string `json:"jobId"`
}

type sseStart struct {
	Phase string `json:"phase"`
	Index int    `json:"index"`
	URL   string `json:"url"`
	Name  string `json:"name"`
	// Total is the Content-Length advertised by the origin. HLS has no total
	// byte count (variable-bitrate remux of streaming segments), so it arrives
	// as 0 and is omitted from the wire via omitempty — clients use this
	// absence to render an indeterminate progress bar.
	Total int64  `json:"total,omitempty"`
	Type  string `json:"type"`
}

type sseProgress struct {
	Phase    string `json:"phase"`
	Index    int    `json:"index"`
	Received int64  `json:"received"`
}

type sseDone struct {
	Phase    string   `json:"phase"`
	Index    int      `json:"index"`
	URL      string   `json:"url"`
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	Size     int64    `json:"size"`
	Type     string   `json:"type"`
	Warnings []string `json:"warnings"`
}

type sseError struct {
	Phase string `json:"phase"`
	Index int    `json:"index"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

type sseSummary struct {
	Phase     string `json:"phase"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
	Cancelled int    `json:"cancelled,omitempty"`
}

// sseQueued is the first event published into the job's event stream — it
// fires before the handler tries to acquire the process-wide import
// semaphore. When no other batch is in flight the subsequent semaphore
// acquire returns immediately and `start` follows without the UI ever
// rendering the queued state; when another batch holds the semaphore the
// client has an explicit signal to display "waiting" instead of a stalled
// progress bar.
type sseQueued struct {
	Phase string `json:"phase"` // always "queued"
}

func (h *Handler) handleImportURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	rel := r.URL.Query().Get("path")
	destAbs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	fi, err := os.Stat(destAbs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "path not found", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "stat failed", err)
		return
	}
	if !fi.IsDir() {
		writeError(w, r, http.StatusBadRequest, "not a directory", nil)
		return
	}

	var body importRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid body", err)
		return
	}

	urls := normalizeURLs(body.URLs)
	if len(urls) == 0 {
		writeError(w, r, http.StatusBadRequest, "no urls", nil)
		return
	}
	if len(urls) > maxImportURLs {
		writeError(w, r, http.StatusBadRequest, "too many urls", nil)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "streaming unsupported", nil)
		return
	}

	job, err := h.registry.Create(rel, urls)
	if err != nil {
		if errors.Is(err, importjob.ErrTooManyJobs) {
			writeError(w, r, http.StatusTooManyRequests, "too_many_jobs", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "registry create failed", err)
		return
	}

	// Subscribe before spawning the worker so the handler cannot miss the
	// initial queued event the worker will publish immediately.
	events, unsubscribe := job.Subscribe()
	defer unsubscribe()

	// Snapshot settings at request arrival time (before queueing) so a PATCH
	// while this batch is waiting for the semaphore does not change the
	// cap/timeout it eventually runs with.
	snap := h.settingsSnapshot()

	// Worker drives the actual import in the background. It uses job.Ctx()
	// (server-lifetime, cancellable on shutdown or user-issued Cancel) instead
	// of the request context so the download keeps running after the client
	// closes its tab or refreshes.
	go h.runImportJob(job, snap, destAbs)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// Hand the jobId to the client straight away so a refresh can rebind to
	// this job via GET /api/import-url/jobs/{id}/events (added in J4).
	writeSSEEvent(w, flusher, sseRegister{Phase: "register", JobID: job.ID})

	// Pump events from the job into the SSE stream. The summary frame is the
	// last live event a job emits, so the handler can return as soon as it is
	// flushed without coordinating with the worker. Client disconnect short-
	// circuits via r.Context() but never cancels the job.
	for {
		select {
		case ev, ok := <-events:
			if !ok {
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

// runImportJob is the worker goroutine that drives one import batch. It
// owns the URLState mutations and SSE event publication; the HTTP handler
// is just one of (potentially) many subscribers and never touches Job state
// directly.
//
// The deferred recover guarantees that even on a panic in urlfetch / ffmpeg
// helpers the job lands in a terminal state — without it the goroutine dies
// silently, Done() never closes, the slot stays counted against
// MaxQueuedJobs forever, and Handler.Close stalls the full WaitAll
// deadline on shutdown.
func (h *Handler) runImportJob(job *importjob.Job, snap settings.Settings, destAbs string) {
	defer func() {
		if rec := recover(); rec != nil {
			slog.Error("import worker panic",
				"jobId", job.ID, "panic", rec, "stack", string(debug.Stack()))
			urls := job.Snapshot().URLs
			job.SetSummary(importjob.Summary{Failed: len(urls)})
			job.Publish(mustEvent("summary", sseSummary{
				Phase: "summary", Failed: len(urls),
			}))
			job.SetStatus(importjob.StatusFailed)
		}
	}()

	maxBytes := snap.URLImportMaxBytes
	perURLTimeout := time.Duration(snap.URLImportTimeoutSeconds) * time.Second

	// Tell the world this batch is queued. Even when the semaphore is free
	// the event still fires; the gap before `start` is just shorter.
	job.Publish(mustEvent("queued", sseQueued{Phase: "queued"}))

	// Acquire the process-wide batch semaphore. importSem is paired with
	// job.Ctx() so a graceful-shutdown cancel unblocks the wait — request
	// context is intentionally NOT used so closing the browser tab does not
	// abort the queue position.
	select {
	case h.importSem <- struct{}{}:
		defer func() { <-h.importSem }()
	case <-job.Ctx().Done():
		// Cancelled before acquiring the semaphore — every URL is reported
		// cancelled, no Start/Done was ever emitted.
		urls := job.Snapshot().URLs
		for i, u := range urls {
			job.UpdateURL(i, func(s *importjob.URLState) {
				s.Status = "cancelled"
				s.Error = "cancelled"
			})
			job.Publish(mustEvent("error", sseError{
				Phase: "error", Index: i, URL: u.URL, Error: "cancelled",
			}))
		}
		summary := importjob.Summary{Cancelled: len(urls)}
		job.SetSummary(summary)
		job.Publish(mustEvent("summary", sseSummary{
			Phase: "summary", Succeeded: 0, Failed: 0, Cancelled: len(urls),
		}))
		job.SetStatus(importjob.StatusCancelled)
		return
	}

	job.SetStatus(importjob.StatusRunning)

	rel := job.DestPath
	urls := job.Snapshot().URLs
	succeeded, failed, cancelled := 0, 0, 0
	for i, urlState := range urls {
		if job.Ctx().Err() != nil {
			// Batch cancelled mid-flight: mark every remaining URL cancelled
			// and stop hitting origins.
			for j := i; j < len(urls); j++ {
				u := urls[j].URL
				job.UpdateURL(j, func(s *importjob.URLState) {
					s.Status = "cancelled"
					s.Error = "cancelled"
				})
				job.Publish(mustEvent("error", sseError{
					Phase: "error", Index: j, URL: u, Error: "cancelled",
				}))
				cancelled++
			}
			break
		}
		switch h.fetchOneJob(job, i, urlState.URL, destAbs, rel, maxBytes, perURLTimeout) {
		case fetchSucceeded:
			succeeded++
		case fetchCancelled:
			cancelled++
		default:
			failed++
		}
	}

	summary := importjob.Summary{
		Succeeded: succeeded, Failed: failed, Cancelled: cancelled,
	}
	job.SetSummary(summary)
	job.Publish(mustEvent("summary", sseSummary{
		Phase: "summary", Succeeded: succeeded, Failed: failed, Cancelled: cancelled,
	}))

	switch {
	case succeeded > 0:
		job.SetStatus(importjob.StatusCompleted)
	case cancelled > 0:
		job.SetStatus(importjob.StatusCancelled)
	default:
		job.SetStatus(importjob.StatusFailed)
	}
}

type fetchResult int

const (
	fetchFailed fetchResult = iota
	fetchSucceeded
	fetchCancelled
)

// fetchOneJob downloads a single URL through urlfetch.Fetch and emits every
// SSE event for it: at most one start, zero or more progress, and exactly
// one terminal (done or error). It also updates the corresponding URLState
// on the Job so snapshot replay and live event streams stay in sync.
func (h *Handler) fetchOneJob(job *importjob.Job, index int, u, destAbs, relDir string,
	maxBytes int64, perURLTimeout time.Duration) fetchResult {

	// Per-URL context — registered with the Job so the cancel API added in
	// J5 can target a single URL without aborting the whole batch.
	urlCtx, cancelURL := context.WithCancel(job.Ctx())
	defer cancelURL()
	job.RegisterURLCancel(index, cancelURL)
	defer job.UnregisterURLCancel(index)

	progressCh := make(chan int64, progressChanBuffer)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for received := range progressCh {
			job.UpdateURL(index, func(s *importjob.URLState) { s.Received = received })
			job.Publish(mustEvent("progress", sseProgress{
				Phase: "progress", Index: index, Received: received,
			}))
		}
	}()

	cb := &urlfetch.Callbacks{
		Start: func(name string, total int64, fileType string) {
			job.UpdateURL(index, func(s *importjob.URLState) {
				s.Name = name
				s.Type = fileType
				s.Total = total
				s.Status = "running"
			})
			job.Publish(mustEvent("start", sseStart{
				Phase: "start", Index: index, URL: u,
				Name: name, Total: total, Type: fileType,
			}))
		},
		Progress: func(received int64) {
			select {
			case progressCh <- received:
			default:
				// drop — slow subscribers must not stall io.Copy
			}
		},
	}

	fctx, cancelTimeout := context.WithTimeout(urlCtx, perURLTimeout)
	res, ferr := urlfetch.Fetch(fctx, h.urlClient, u, destAbs, relDir, maxBytes, cb)
	cancelTimeout()

	close(progressCh)
	<-writerDone

	if ferr != nil {
		// Distinguish a per-URL/batch cancellation from a genuine fetch
		// failure so the worker's success/fail/cancelled counters and the
		// terminal status are correct.
		if isCancelled(urlCtx, job.Ctx(), ferr) {
			job.UpdateURL(index, func(s *importjob.URLState) {
				s.Status = "cancelled"
				s.Error = "cancelled"
			})
			job.Publish(mustEvent("error", sseError{
				Phase: "error", Index: index, URL: u, Error: "cancelled",
			}))
			return fetchCancelled
		}
		logFetchError(u, ferr)
		job.UpdateURL(index, func(s *importjob.URLState) {
			s.Status = "error"
			s.Error = ferr.Code
		})
		job.Publish(mustEvent("error", sseError{
			Phase: "error", Index: index, URL: u, Error: ferr.Code,
		}))
		return fetchFailed
	}

	job.UpdateURL(index, func(s *importjob.URLState) {
		s.Status = "done"
		s.Name = res.Name
		s.Type = res.Type
		s.Received = res.Size
		s.Warnings = append([]string(nil), res.Warnings...)
	})
	job.Publish(mustEvent("done", sseDone{
		Phase: "done", Index: index, URL: u,
		Path: res.Path, Name: res.Name, Size: res.Size,
		Type: res.Type, Warnings: res.Warnings,
	}))

	if res.Type != string(media.TypeAudio) {
		thumbDir := filepath.Join(destAbs, ".thumb")
		thumbPath := filepath.Join(thumbDir, res.Name+".jpg")
		finalSrc := filepath.Join(destAbs, res.Name)
		if !h.thumbPool.Submit(finalSrc, thumbPath) {
			slog.Warn("thumb pool full, deferring to lazy generation", "src", finalSrc)
		}
	}
	return fetchSucceeded
}

// isCancelled reports whether the urlfetch error is the result of a context
// cancellation (per-URL or batch-wide) rather than a genuine origin/IO
// failure. Used to decide whether to count a URL as failed or cancelled.
func isCancelled(urlCtx, jobCtx context.Context, ferr *urlfetch.FetchError) bool {
	if jobCtx.Err() != nil || urlCtx.Err() != nil {
		return true
	}
	if ferr == nil {
		return false
	}
	if u := ferr.Unwrap(); u != nil {
		return errors.Is(u, context.Canceled) || errors.Is(u, context.DeadlineExceeded)
	}
	return false
}

// logFetchError writes a structured server-side log for failed URL imports.
// The client only ever receives the opaque error code; this is where operators
// see what actually broke — especially useful for ffmpeg_missing (operator
// must install ffmpeg) and ffmpeg_error (stream-specific stderr can be
// inspected to tell DRM/format issues apart). URLs are redacted before
// logging because user-supplied origins commonly carry signed query
// parameters and credentials (`?token=`, `user:pass@host`) that should not
// land in journald or pasted log snippets.
func logFetchError(u string, ferr *urlfetch.FetchError) {
	attrs := []any{"code", ferr.Code, "url", redactURL(u)}
	if unwrapped := ferr.Unwrap(); unwrapped != nil {
		attrs = append(attrs, "err", redactErr(unwrapped))
	}
	slog.Warn("url import failed", attrs...)
}

// sensitiveQueryKeys lists query parameters whose value the URL redactor
// strips before logging. The match is case-insensitive and is intentionally
// narrow — broad redaction would obscure useful diagnostic info on benign
// origins.
var sensitiveQueryKeys = map[string]struct{}{
	"token":         {},
	"access_token":  {},
	"signature":     {},
	"sig":           {},
	"x-amz-signature": {},
	"key":           {},
	"apikey":        {},
	"api_key":       {},
	"password":      {},
	"secret":        {},
}

// redactURL strips userinfo and masks values for query parameters that look
// like credentials or signatures. Returns "<unparseable>" for inputs that
// fail url.Parse so a malformed string never short-circuits the rest of the
// log line.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<unparseable>"
	}
	if u.User != nil {
		u.User = nil
	}
	q := u.Query()
	changed := false
	for k := range q {
		if _, ok := sensitiveQueryKeys[strings.ToLower(k)]; ok {
			q.Set(k, "REDACTED")
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
	return u.String()
}

// redactErr applies redactURL to *url.Error wrappers so the URL embedded by
// net/http inside the error string is also masked. Other errors fall through
// to err.Error() unchanged.
func redactErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		copy := *ue
		copy.URL = redactURL(ue.URL)
		return copy.Error()
	}
	return err.Error()
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("sse marshal", "err", err)
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// mustEvent marshals payload into a importjob.Event with the given phase.
// JSON marshalling of these stable structs cannot fail in practice; on the
// pathological miss the broadcast is skipped (logged once) so the worker
// keeps progressing.
func mustEvent(phase string, payload any) importjob.Event {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("import job event marshal", "phase", phase, "err", err)
		return importjob.Event{Phase: phase, Data: []byte("{}")}
	}
	return importjob.Event{Phase: phase, Data: data}
}

// normalizeURLs trims whitespace, drops empty entries, and discards any URL
// over maxImportURLLength. Order and intentional duplicates are preserved
// (collisions get _N suffixes downstream). Over-length entries are silently
// dropped; the request still goes through with the remaining URLs and the
// downstream count check (maxImportURLs) catches batches whose every URL was
// rejected.
func normalizeURLs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, u := range in {
		u = strings.TrimSpace(u)
		if u == "" || len(u) > maxImportURLLength {
			continue
		}
		out = append(out, u)
	}
	return out
}
