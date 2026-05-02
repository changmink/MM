package handler

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
)

// assertFlusher returns the writer as Flusher or nil after responding with
// 500. Callers must early-return on nil. Use BEFORE any header write so a
// failure path can still send an HTTP error — once writeSSEHeaders runs, the
// status line is committed and a 4xx/5xx response is no longer possible.
func assertFlusher(w http.ResponseWriter, r *http.Request) http.Flusher {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "streaming unsupported", nil)
		return nil
	}
	return flusher
}

// writeSSEHeaders writes the standard text/event-stream response headers and
// the 200 status line. Call after every pre-flight check that might still
// 4xx/5xx — once this returns, the response body is open for SSE frames.
func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// sseEmitter returns an emit closure that serializes SSE writes via an
// internal mutex. Required when the writer is shared between the handler
// goroutine (start/done/error/summary) and per-task progress writers — the
// raw http.ResponseWriter is not safe for concurrent Write/Flush.
func sseEmitter(w http.ResponseWriter, flusher http.Flusher) func(any) {
	var mu sync.Mutex
	return func(payload any) {
		mu.Lock()
		defer mu.Unlock()
		writeSSEEvent(w, flusher, payload)
	}
}

// writeSSEEvent marshals payload as JSON and emits a single `data: ...\n\n`
// SSE frame. Marshal failures are logged and the frame is dropped — past
// the header boundary the caller has nothing useful to do with the error.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("sse marshal", "err", err)
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}
