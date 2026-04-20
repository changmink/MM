package handler

import (
	"net/http"
	"os"
	"os/exec"

	"github.com/chang/file_server/internal/media"
)

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	rel := r.URL.Query().Get("path")
	abs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "open failed")
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stat failed")
		return
	}
	if fi.IsDir() {
		writeError(w, http.StatusBadRequest, "path is a directory")
		return
	}

	if media.IsTS(fi.Name()) {
		h.streamTS(w, r, abs)
		return
	}

	w.Header().Set("Content-Type", media.MIMEType(fi.Name()))
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// streamTS remuxes a MPEG-2 TS file to MP4 via ffmpeg, writing to a temp file
// first so that http.ServeContent can serve it with proper Range support and
// Content-Length. The temp file is removed after the response is sent.
// Remuxing is fast (stream copy, no re-encoding) — typically 2-5 seconds.
func (h *Handler) streamTS(w http.ResponseWriter, r *http.Request, absPath string) {
	tmp, err := os.CreateTemp("", "ts_remux_*.mp4")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tmp create failed")
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.CommandContext(r.Context(), "ffmpeg",
		"-y",
		"-loglevel", "error",
		"-i", absPath,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c:v", "copy",
		"-c:a", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "faststart",
		tmpPath,
	)
	if err := cmd.Run(); err != nil {
		writeError(w, http.StatusInternalServerError, "transcode failed")
		return
	}

	f, err := os.Open(tmpPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "open tmp failed")
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "stat tmp failed")
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	http.ServeContent(w, r, "video.mp4", fi.ModTime(), f)
}
