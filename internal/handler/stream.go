package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"file_server/internal/ffmpeg"
	"file_server/internal/handlerutil"
	"file_server/internal/media"
)

// streamCacheDir은 remux된 mp4가 저장되는 디스크 상의 서브 디렉터리(dataDir
// 아래)다. handleBrowse의 dotfile 필터로 인해 browse에서는 숨겨진다.
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

// streamTS는 디스크 캐시에서 remux된 mp4를 서빙한다. 캐시가 비어 있으면
// ffmpeg를 한 번만 실행한다(같은 source에 대한 다른 동시 요청은 키별 뮤텍스에서
// 대기). 결과는 원자적으로 rename된다. 캐시 키는 절대 경로 + mtime +
// size 라서, 원본 편집이 자동으로 캐시를 무효화한다.
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

	unlock := handlerutil.LockPath(&h.streamLocks, cachePath)
	defer unlock()

	// 락 획득 후 다시 검사한다 — 다른 고루틴이 그 사이에 캐시를 만들 수 있다.
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

	// 패턴 "remux_*.mp4" → "remux_<rand>.mp4". ffmpeg는 출력 확장자로 muxer를
	// 선택하므로 파일명이 .mp4로 끝나야 한다(.mp4.tmp가 아니라). 그렇지 않으면
	// EINVAL로 종료한다.
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

