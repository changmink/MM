package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chang/file_server/internal/media"
	"github.com/chang/file_server/internal/urlfetch"
)

const (
	maxImportURLs = 50
	// progressChanBuffer lets the Fetch goroutine drop samples instead of
	// blocking when SSE writes fall behind. A slow client must never stall
	// the download.
	progressChanBuffer = 16
)

type importRequest struct {
	URLs []string `json:"urls"`
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
}

// sseQueued is emitted once per batch right after the response headers are
// flushed, before the handler tries to acquire the process-wide import
// semaphore. When no other batch is in flight the semaphore acquire returns
// immediately and `start` follows without the UI ever rendering the queued
// state; when another batch holds the semaphore the client has an explicit
// signal to display "waiting" instead of a stalled progress bar.
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

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// emit serializes SSE writes between the handler goroutine (start / done /
	// error / summary) and the per-URL progress writer goroutine. Flush blocks
	// on a slow client, so this mutex also bounds concurrency to one in-flight
	// flush. Progress events tolerate back-pressure via the drop-on-full
	// progressCh; every other event type intentionally waits its turn so
	// clients never miss a lifecycle transition.
	var writeMu sync.Mutex
	emit := func(payload any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		writeSSEEvent(w, flusher, payload)
	}

	// Snapshot settings at request arrival time (before queueing) so a PATCH
	// made while this batch is waiting for the semaphore does not change the
	// cap/timeout it eventually runs with.
	snap := h.settingsSnapshot()
	maxBytes := snap.URLImportMaxBytes
	perURLTimeout := time.Duration(snap.URLImportTimeoutSeconds) * time.Second

	// Announce to the client that this batch is queued. When the semaphore
	// is free the subsequent acquire returns instantly and `start` follows
	// immediately; clients treat the gap between `queued` and `start` as
	// the "waiting for other batches" window.
	emit(sseQueued{Phase: "queued"})

	// Serialize batches process-wide. sync.Mutex would deadlock on client
	// disconnect because Lock() cannot be cancelled, so a size-1 channel
	// semaphore is paired with ctx.Done() in a select. Acquire only, release
	// only — defer sits inside the winning case so we never release what we
	// did not acquire.
	select {
	case h.importSem <- struct{}{}:
		defer func() { <-h.importSem }()
	case <-r.Context().Done():
		return
	}

	succeeded, failed := 0, 0
	for i, u := range urls {
		// Stop early when the client disconnects so we don't keep firing
		// HTTP requests at origins for events nobody will read.
		if r.Context().Err() != nil {
			return
		}
		if h.fetchOneSSE(r.Context(), emit, i, u, destAbs, rel, maxBytes, perURLTimeout) {
			succeeded++
		} else {
			failed++
		}
	}
	emit(sseSummary{Phase: "summary", Succeeded: succeeded, Failed: failed})
}

// fetchOneSSE downloads a single URL and emits every SSE event for it: at most
// one start, zero or more progress, and exactly one terminal (done or error).
// Progress samples flow through a buffered channel drained by a writer
// goroutine — if the channel is full the sample is dropped so a slow SSE
// consumer can never block the download goroutine. Returns true on success.
func (h *Handler) fetchOneSSE(ctx context.Context, emit func(any),
	index int, u, destAbs, relDir string,
	maxBytes int64, perURLTimeout time.Duration) bool {

	progressCh := make(chan int64, progressChanBuffer)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for received := range progressCh {
			emit(sseProgress{Phase: "progress", Index: index, Received: received})
		}
	}()

	cb := &urlfetch.Callbacks{
		Start: func(name string, total int64, fileType string) {
			emit(sseStart{
				Phase: "start", Index: index, URL: u,
				Name: name, Total: total, Type: fileType,
			})
		},
		Progress: func(received int64) {
			select {
			case progressCh <- received:
			default:
				// drop — slow SSE consumer must not stall io.Copy
			}
		},
	}

	fctx, cancel := context.WithTimeout(ctx, perURLTimeout)
	res, ferr := urlfetch.Fetch(fctx, h.urlClient, u, destAbs, relDir, maxBytes, cb)
	cancel()

	close(progressCh)
	<-writerDone

	if ferr != nil {
		logFetchError(u, ferr)
		emit(sseError{Phase: "error", Index: index, URL: u, Error: ferr.Code})
		return false
	}
	emit(sseDone{
		Phase: "done", Index: index, URL: u,
		Path: res.Path, Name: res.Name, Size: res.Size,
		Type: res.Type, Warnings: res.Warnings,
	})

	if res.Type != string(media.TypeAudio) {
		thumbDir := filepath.Join(destAbs, ".thumb")
		thumbPath := filepath.Join(thumbDir, res.Name+".jpg")
		finalSrc := filepath.Join(destAbs, res.Name)
		if !h.thumbPool.Submit(finalSrc, thumbPath) {
			slog.Warn("thumb pool full, deferring to lazy generation", "src", finalSrc)
		}
	}
	return true
}

// logFetchError writes a structured server-side log for failed URL imports.
// The client only ever receives the opaque error code; this is where operators
// see what actually broke — especially useful for ffmpeg_missing (operator
// must install ffmpeg) and ffmpeg_error (stream-specific stderr can be
// inspected to tell DRM/format issues apart).
func logFetchError(u string, ferr *urlfetch.FetchError) {
	attrs := []any{"code", ferr.Code, "url", u}
	if unwrapped := ferr.Unwrap(); unwrapped != nil {
		attrs = append(attrs, "err", unwrapped.Error())
	}
	slog.Warn("url import failed", attrs...)
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

// normalizeURLs trims whitespace and drops empty entries while preserving
// order and intentional duplicates (collisions get _N suffixes downstream).
func normalizeURLs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, u := range in {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		out = append(out, u)
	}
	return out
}
