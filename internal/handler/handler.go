package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime"
	"sync"

	"github.com/chang/file_server/internal/thumb"
	"github.com/chang/file_server/internal/urlfetch"
)

type Handler struct {
	dataDir     string
	thumbPool   *thumb.Pool
	urlClient   *http.Client
	streamLocks sync.Map // cachePath -> *sync.Mutex; serializes ffmpeg per cache key
}

func Register(mux *http.ServeMux, dataDir, webDir string) *Handler {
	h := &Handler{
		dataDir:   dataDir,
		thumbPool: thumb.NewPool(runtime.NumCPU()),
		urlClient: urlfetch.NewClient(),
	}

	mux.HandleFunc("/api/browse", h.handleBrowse)
	mux.HandleFunc("/api/stream", h.handleStream)
	mux.HandleFunc("/api/thumb", h.handleThumb)
	mux.HandleFunc("/api/upload", h.handleUpload)
	mux.HandleFunc("/api/file", h.handleFile)
	mux.HandleFunc("/api/folder", h.handleFolder)
	mux.HandleFunc("/api/import-url", h.handleImportURL)

	mux.Handle("/", http.FileServer(http.Dir(webDir)))
	return h
}

// Close stops the background thumbnail pool. Safe to call once per Handler.
func (h *Handler) Close() {
	if h.thumbPool != nil {
		h.thumbPool.Shutdown()
	}
}

// writeError emits a JSON error body and (for 5xx, or any non-nil err) logs
// the underlying cause with request context. Pass nil for err on plain 4xx
// validation failures where the message is the whole story.
func writeError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) {
	if code >= 500 || err != nil {
		slog.Error("request failed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", code,
			"msg", msg,
			"err", err,
		)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
