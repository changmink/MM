package handler

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/chang/file_server/internal/media"
	"github.com/chang/file_server/internal/thumb"
)

func (h *Handler) handleThumb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rel := r.URL.Query().Get("path")
	abs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "stat failed")
		return
	}

	switch {
	case media.IsImage(fi.Name()):
		h.serveImageThumb(w, r, abs, fi.Name())
	case media.IsVideo(fi.Name()):
		h.serveVideoThumb(w, r, abs, fi.Name())
	default:
		writeError(w, http.StatusBadRequest, "unsupported file type")
	}
}

func (h *Handler) serveImageThumb(w http.ResponseWriter, r *http.Request, abs, name string) {
	thumbPath := filepath.Join(filepath.Dir(abs), ".thumb", name+".jpg")
	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		if err := thumb.Generate(abs, thumbPath); err != nil {
			writeError(w, http.StatusInternalServerError, "thumb generation failed")
			return
		}
	}
	serveThumbFile(w, r, thumbPath)
}

func (h *Handler) serveVideoThumb(w http.ResponseWriter, r *http.Request, abs, name string) {
	thumbPath := filepath.Join(filepath.Dir(abs), ".thumb", name+".jpg")
	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		if err := thumb.GenerateFromVideo(abs, thumbPath); err != nil {
			// ffmpeg unavailable or all frames blank — serve placeholder
			w.Header().Set("Content-Type", "image/jpeg")
			w.WriteHeader(http.StatusOK)
			w.Write(thumb.Placeholder)
			return
		}
	}
	serveThumbFile(w, r, thumbPath)
}

func serveThumbFile(w http.ResponseWriter, r *http.Request, thumbPath string) {
	f, err := os.Open(thumbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open thumb failed")
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stat thumb failed")
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}
