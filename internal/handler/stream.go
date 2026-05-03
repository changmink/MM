package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"file_server/internal/ffmpeg"
	"file_server/internal/media"
)

// streamCacheDir is the on-disk subdir (under dataDir) where remuxed mp4s live.
// Hidden from browse via the dotfile filter in handleBrowse.
const streamCacheDir = ".cache/streams"

func (h *Handler) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "not found", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "open failed", err)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "stat failed", err)
		return
	}
	if fi.IsDir() {
		writeError(w, r, http.StatusBadRequest, "path is a directory", nil)
		return
	}

	if media.IsTS(fi.Name()) {
		h.streamTS(w, r, abs, fi)
		return
	}

	w.Header().Set("Content-Type", media.MIMEType(fi.Name()))
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

// streamTS serves a remuxed mp4 from disk cache. On cache miss, runs ffmpeg
// once (other concurrent requests for the same source wait on a per-key mutex)
// and atomically renames the result into place. Cache is keyed by absolute
// path + mtime + size, so any edit to the source invalidates automatically.
func (h *Handler) streamTS(w http.ResponseWriter, r *http.Request, absPath string, fi os.FileInfo) {
	cachePath := h.streamCachePath(absPath, fi)

	if cached, err := os.Open(cachePath); err == nil {
		defer cached.Close()
		ci, err := cached.Stat()
		if err == nil {
			w.Header().Set("Content-Type", "video/mp4")
			http.ServeContent(w, r, "video.mp4", ci.ModTime(), cached)
			return
		}
		// Stat 실패 시 fall-through — defer가 닫는다. 명시적 close를 더하면
		// fd 재할당이 일어난 환경에서 다른 파일을 닫는 패턴이라 제거.
	}

	unlock := h.lockStreamKey(cachePath)
	defer unlock()

	// Re-check after acquiring the lock — another goroutine may have produced it.
	if cached, err := os.Open(cachePath); err == nil {
		defer cached.Close()
		ci, err := cached.Stat()
		if err == nil {
			w.Header().Set("Content-Type", "video/mp4")
			http.ServeContent(w, r, "video.mp4", ci.ModTime(), cached)
			return
		}
	}

	if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
		writeError(w, r, http.StatusInternalServerError, "cache dir failed", err)
		return
	}

	// Pattern "remux_*.mp4" → "remux_<rand>.mp4". ffmpeg picks its muxer from the
	// output extension, so the filename must end in .mp4 (not .mp4.tmp) or it
	// exits with EINVAL.
	tmp, err := os.CreateTemp(filepath.Dir(cachePath), "remux_*.mp4")
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "tmp create failed", err)
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()

	var stderr bytes.Buffer
	args := []string{
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
	}
	if err := ffmpeg.RunWithStderr(r.Context(), &stderr, args...); err != nil {
		os.Remove(tmpPath)
		writeError(w, r, http.StatusInternalServerError, "transcode failed",
			fmt.Errorf("%w: %s", err, bytes.TrimSpace(stderr.Bytes())))
		return
	}

	if err := os.Rename(tmpPath, cachePath); err != nil {
		os.Remove(tmpPath)
		writeError(w, r, http.StatusInternalServerError, "cache write failed", err)
		return
	}

	cached, err := os.Open(cachePath)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "open cache failed", err)
		return
	}
	defer cached.Close()
	ci, err := cached.Stat()
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "stat cache failed", err)
		return
	}

	w.Header().Set("Content-Type", "video/mp4")
	http.ServeContent(w, r, "video.mp4", ci.ModTime(), cached)
}

func (h *Handler) streamCachePath(absPath string, fi os.FileInfo) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", absPath, fi.ModTime().UnixNano(), fi.Size())))
	name := hex.EncodeToString(sum[:]) + ".mp4"
	return filepath.Join(h.dataDir, streamCacheDir, name)
}

// lockStreamKey serializes producers for the same cache key. The map entry
// is left in place after unlock — bounded by the set of unique TS files, so
// growth is acceptable for this single-tenant server.
func (h *Handler) lockStreamKey(key string) func() {
	v, _ := h.streamLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
