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
	Total int64  `json:"total"`
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

	var writeMu sync.Mutex
	emit := func(payload any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		writeSSEEvent(w, flusher, payload)
	}

	succeeded, failed := 0, 0
	for i, u := range urls {
		// Stop early when the client disconnects so we don't keep firing
		// HTTP requests at origins for events nobody will read.
		if r.Context().Err() != nil {
			return
		}
		if h.fetchOneSSE(r.Context(), emit, i, u, destAbs, rel) {
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
	index int, u, destAbs, relDir string) bool {

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

	fctx, cancel := context.WithTimeout(ctx, urlfetch.TotalTimeout)
	res, ferr := urlfetch.Fetch(fctx, h.urlClient, u, destAbs, relDir, cb)
	cancel()

	close(progressCh)
	<-writerDone

	if ferr != nil {
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
