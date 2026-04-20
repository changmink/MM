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

	if !media.IsImage(fi.Name()) {
		writeError(w, http.StatusBadRequest, "not an image")
		return
	}

	thumbPath := filepath.Join(filepath.Dir(abs), ".thumb", fi.Name()+".jpg")

	if _, err := os.Stat(thumbPath); os.IsNotExist(err) {
		if err := thumb.Generate(abs, thumbPath); err != nil {
			writeError(w, http.StatusInternalServerError, "thumb generation failed")
			return
		}
	}

	f, err := os.Open(thumbPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open thumb failed")
		return
	}
	defer f.Close()

	fi2, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stat thumb failed")
		return
	}

	w.Header().Set("Content-Type", "image/jpeg")
	http.ServeContent(w, r, fi2.Name(), fi2.ModTime(), f)
}
