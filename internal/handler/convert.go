package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chang/file_server/internal/convert"
	"github.com/chang/file_server/internal/media"
)

const (
	maxConvertPaths    = 50
	convertFileTimeout = 10 * time.Minute
)

type convertRequest struct {
	Paths          []string `json:"paths"`
	DeleteOriginal bool     `json:"delete_original"`
}

// SSE event shapes. The wire schema mirrors import_url.go's sseStart/etc. but
// with `path` instead of `url` since the input is a local file.
type convStart struct {
	Phase string `json:"phase"`
	Index int    `json:"index"`
	Path  string `json:"path"`
	Name  string `json:"name"`
	Total int64  `json:"total,omitempty"`
	Type  string `json:"type"`
}

type convProgress struct {
	Phase    string `json:"phase"`
	Index    int    `json:"index"`
	Received int64  `json:"received"`
}

type convDone struct {
	Phase    string   `json:"phase"`
	Index    int      `json:"index"`
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	Size     int64    `json:"size"`
	Type     string   `json:"type"`
	Warnings []string `json:"warnings"`
}

type convError struct {
	Phase string `json:"phase"`
	Index int    `json:"index"`
	Path  string `json:"path"`
	Error string `json:"error"`
}

type convSummary struct {
	Phase     string `json:"phase"`
	Succeeded int    `json:"succeeded"`
	Failed    int    `json:"failed"`
}

func (h *Handler) handleConvert(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	var body convertRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid request", err)
		return
	}
	if len(body.Paths) == 0 {
		writeError(w, r, http.StatusBadRequest, "no paths", nil)
		return
	}
	if len(body.Paths) > maxConvertPaths {
		writeError(w, r, http.StatusBadRequest, "too many paths", nil)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "streaming unsupported", nil)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	// emit serializes SSE writes between the handler goroutine (start/done/
	// error/summary) and the per-file progress writer. Progress samples use
	// a drop-on-full channel so a slow client can never stall ffmpeg.
	var writeMu sync.Mutex
	emit := func(payload any) {
		writeMu.Lock()
		defer writeMu.Unlock()
		writeSSEEvent(w, flusher, payload)
	}

	succeeded, failed := 0, 0
	for i, p := range body.Paths {
		if r.Context().Err() != nil {
			return
		}
		if h.convertOneSSE(r.Context(), emit, i, p, body.DeleteOriginal) {
			succeeded++
		} else {
			failed++
		}
	}
	emit(convSummary{Phase: "summary", Succeeded: succeeded, Failed: failed})
}

// convertOneSSE drives one TS → MP4 conversion and emits exactly one terminal
// event (done or error) plus zero-or-one start and zero-or-more progress
// events. Returns true on success.
func (h *Handler) convertOneSSE(parentCtx context.Context, emit func(any),
	index int, relPath string, deleteOriginal bool) bool {

	emitErr := func(code string) bool {
		emit(convError{Phase: "error", Index: index, Path: relPath, Error: code})
		return false
	}

	abs, err := media.SafePath(h.dataDir, relPath)
	if err != nil {
		return emitErr("invalid_path")
	}
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return emitErr("not_found")
		}
		return emitErr("write_error")
	}
	if fi.IsDir() {
		return emitErr("not_a_file")
	}
	origName := fi.Name()
	ext := filepath.Ext(origName)
	if !strings.EqualFold(ext, ".ts") {
		return emitErr("not_ts")
	}
	base := strings.TrimSuffix(origName, ext)
	finalName := base + ".mp4"
	srcDir := filepath.Dir(abs)
	finalPath := filepath.Join(srcDir, finalName)

	if _, err := os.Stat(finalPath); err == nil {
		return emitErr("already_exists")
	} else if !os.IsNotExist(err) {
		return emitErr("write_error")
	}

	unlock := h.lockConvertKey(abs)
	defer unlock()

	// Re-check after lock: another request might have produced the .mp4 while
	// we were waiting.
	if _, err := os.Stat(finalPath); err == nil {
		return emitErr("already_exists")
	}

	// Relative path of the final MP4 for the done event. "/"-prefixed like
	// the request input so the client can point loadBrowse at the same folder.
	finalRel := filepath.Join(filepath.Dir(relPath), finalName)
	finalRel = filepath.ToSlash(finalRel)
	if !strings.HasPrefix(finalRel, "/") {
		finalRel = "/" + finalRel
	}

	progressCh := make(chan int64, 16)
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for received := range progressCh {
			emit(convProgress{Phase: "progress", Index: index, Received: received})
		}
	}()

	cb := convert.Callbacks{
		OnStart: func(total int64) {
			emit(convStart{
				Phase: "start", Index: index, Path: relPath,
				Name: finalName, Total: total, Type: "video",
			})
		},
		OnProgress: func(received int64) {
			select {
			case progressCh <- received:
			default:
			}
		},
	}

	fileCtx, cancel := context.WithTimeout(parentCtx, convertFileTimeout)
	res, rerr := convert.RemuxTSToMP4(fileCtx, abs, srcDir, base, cb)
	cancel()

	close(progressCh)
	<-writerDone

	if rerr != nil {
		code := classifyConvertError(rerr, parentCtx)
		logConvertError(relPath, rerr, code)
		return emitErr(code)
	}

	warnings := []string{}
	if deleteOriginal {
		if err := os.Remove(abs); err != nil {
			slog.Warn("convert: delete original failed",
				"path", relPath, "err", err)
			warnings = append(warnings, "delete_original_failed")
		} else {
			// Best-effort sidecar cleanup. The sidecars are regenerated on
			// demand for the new .mp4, so failure here doesn't require a
			// warning — the only observable effect is a stale thumb that the
			// user can clear by touching the folder.
			thumbDir := filepath.Join(srcDir, ".thumb")
			for _, suffix := range []string{".jpg", ".jpg.dur"} {
				sidecar := filepath.Join(thumbDir, origName+suffix)
				if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
					slog.Warn("convert: sidecar delete failed",
						"sidecar", sidecar, "err", err)
				}
			}
		}
	}

	emit(convDone{
		Phase: "done", Index: index, Path: finalRel, Name: finalName,
		Size: res.Size, Type: "video", Warnings: warnings,
	})
	return true
}

// classifyConvertError maps runtime errors to public SSE codes. Parent ctx is
// checked first so client disconnect (canceled) wins over whatever ffmpeg
// happened to report on its way out.
func classifyConvertError(err error, parentCtx context.Context) string {
	if parentCtx.Err() != nil && errors.Is(parentCtx.Err(), context.Canceled) {
		return "canceled"
	}
	switch {
	case errors.Is(err, convert.ErrFFmpegMissing):
		return "ffmpeg_missing"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "convert_timeout"
	}
	var ffErr *convert.FFmpegExitError
	if errors.As(err, &ffErr) {
		return "ffmpeg_error"
	}
	return "write_error"
}

// logConvertError writes a structured server log for an aborted conversion.
// Users only ever see the opaque code; operators can find root cause (ffmpeg
// stderr, I/O error) here.
func logConvertError(relPath string, err error, code string) {
	attrs := []any{"code", code, "path", relPath, "err", err.Error()}
	var ffErr *convert.FFmpegExitError
	if errors.As(err, &ffErr) && ffErr.Stderr != "" {
		attrs = append(attrs, "stderr", ffErr.Stderr)
	}
	slog.Warn("convert failed", attrs...)
}

// lockConvertKey serializes producers for the same source TS path. Matches
// stream.go:lockStreamKey — leaves the map entry in place after unlock,
// bounded by the set of unique TS files on disk.
func (h *Handler) lockConvertKey(key string) func() {
	v, _ := h.convertLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
