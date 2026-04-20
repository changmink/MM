package handler

import (
	"encoding/json"
	"net/http"
)

type Handler struct {
	dataDir string
}

func Register(mux *http.ServeMux, dataDir, webDir string) {
	h := &Handler{dataDir: dataDir}

	mux.HandleFunc("/api/browse", h.handleBrowse)
	mux.HandleFunc("/api/stream", h.handleStream)
	mux.HandleFunc("/api/thumb", h.handleThumb)
	mux.HandleFunc("/api/upload", h.handleUpload)
	mux.HandleFunc("/api/file", h.handleFile)
	mux.HandleFunc("/api/folder", h.handleFolder)

	mux.Handle("/", http.FileServer(http.Dir(webDir)))
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
