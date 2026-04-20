package handler

import (
	"encoding/json"
	"net/http"
	"runtime"

	"github.com/chang/file_server/internal/thumb"
)

type Handler struct {
	dataDir   string
	thumbPool *thumb.Pool
}

func Register(mux *http.ServeMux, dataDir, webDir string) *Handler {
	h := &Handler{
		dataDir:   dataDir,
		thumbPool: thumb.NewPool(runtime.NumCPU()),
	}

	mux.HandleFunc("/api/browse", h.handleBrowse)
	mux.HandleFunc("/api/stream", h.handleStream)
	mux.HandleFunc("/api/thumb", h.handleThumb)
	mux.HandleFunc("/api/upload", h.handleUpload)
	mux.HandleFunc("/api/file", h.handleFile)
	mux.HandleFunc("/api/folder", h.handleFolder)

	mux.Handle("/", http.FileServer(http.Dir(webDir)))
	return h
}

// Close stops the background thumbnail pool. Safe to call once per Handler.
func (h *Handler) Close() {
	if h.thumbPool != nil {
		h.thumbPool.Shutdown()
	}
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
