package convertapi

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
)

type Handler struct {
	dataDir      string
	convertLocks *sync.Map
	webpLocks    *sync.Map
}

func New(dataDir string, convertLocks, webpLocks *sync.Map) *Handler {
	if convertLocks == nil {
		convertLocks = &sync.Map{}
	}
	if webpLocks == nil {
		webpLocks = &sync.Map{}
	}
	return &Handler{
		dataDir:      dataDir,
		convertLocks: convertLocks,
		webpLocks:    webpLocks,
	}
}

func writeJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Debug("response encode failed",
			"method", r.Method, "path", r.URL.Path, "err", err,
		)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) {
	switch {
	case code >= 500:
		slog.Error("request failed",
			"method", r.Method, "path", r.URL.Path,
			"status", code, "msg", msg, "err", err,
		)
	case err != nil:
		slog.Warn("request rejected",
			"method", r.Method, "path", r.URL.Path,
			"status", code, "msg", msg, "err", err,
		)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if encErr := json.NewEncoder(w).Encode(map[string]string{"error": msg}); encErr != nil {
		slog.Debug("error response encode failed",
			"method", r.Method, "path", r.URL.Path, "err", encErr,
		)
	}
}

func assertFlusher(w http.ResponseWriter, r *http.Request) http.Flusher {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "streaming unsupported", nil)
		return nil
	}
	return flusher
}

func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func sseEmitter(w http.ResponseWriter, flusher http.Flusher) func(any) {
	var mu sync.Mutex
	return func(payload any) {
		mu.Lock()
		defer mu.Unlock()
		writeSSEEvent(w, flusher, payload)
	}
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
