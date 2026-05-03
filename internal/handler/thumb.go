package handler

import (
	"net/http"
	"os"
	"path/filepath"

	"file_server/internal/media"
	"file_server/internal/thumb"
)

func (h *Handler) handleThumb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "not found", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "stat failed", err)
		return
	}

	switch {
	case media.IsImage(fi.Name()):
		h.serveImageThumb(w, r, abs, fi.Name())
	case media.IsVideo(fi.Name()):
		h.serveVideoThumb(w, r, abs, fi.Name())
	default:
		writeError(w, r, http.StatusBadRequest, "unsupported file type", nil)
	}
}

func (h *Handler) serveImageThumb(w http.ResponseWriter, r *http.Request, abs, name string) {
	thumbPath := filepath.Join(filepath.Dir(abs), ".thumb", name+".jpg")
	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		if err := thumb.Generate(abs, thumbPath); err != nil {
			writeError(w, r, http.StatusInternalServerError, "thumb generation failed", err)
			return
		}
	}
	serveThumbFile(w, r, thumbPath)
}

func (h *Handler) serveVideoThumb(w http.ResponseWriter, r *http.Request, abs, name string) {
	thumbPath := filepath.Join(filepath.Dir(abs), ".thumb", name+".jpg")
	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		if err := thumb.GenerateFromVideo(abs, thumbPath); err != nil {
			// ffmpeg가 없거나 모든 프레임이 비어 있다 — 플레이스홀더 제공
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
		writeError(w, r, http.StatusInternalServerError, "open thumb failed", err)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "stat thumb failed", err)
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}
