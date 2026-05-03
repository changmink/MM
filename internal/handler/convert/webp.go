package convertapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"file_server/internal/convert"
	"file_server/internal/media"
	"file_server/internal/thumb"
)

const (
	maxConvertWebPPaths    = 500
	webpConvertFileTimeout = 5 * time.Minute
	clipMaxBytes           = 50 * 1024 * 1024 // SPEC §2.5.3 / §2.9 ??50 MiB
	clipMaxDurationSec     = 30.0             // SPEC §2.5.3 / §2.9 ??30s
)

type webpConvertRequest struct {
	Paths          []string `json:"paths"`
	DeleteOriginal bool     `json:"delete_original"`
}

// handleConvertWebP drives the SSE batch endpoint for clip ??animated WebP
// conversion. Wire schema mirrors POST /api/convert (TS ??MP4) ??same
// start/progress/done/error/summary phases ??so the frontend can reuse the
// SSE consumer pattern. Per-file gate validation (GIF unconditional /
// video ??0MiB && ??0s) runs before any encoding work and surfaces as a
// terminal error event, not an HTTP error.
func (h *Handler) HandleConvertWebP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	var body webpConvertRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid request", err)
		return
	}
	if len(body.Paths) == 0 {
		writeError(w, r, http.StatusBadRequest, "no paths", nil)
		return
	}
	if len(body.Paths) > maxConvertWebPPaths {
		writeError(w, r, http.StatusBadRequest, "too many paths", nil)
		return
	}

	flusher := assertFlusher(w, r)
	if flusher == nil {
		return
	}
	writeSSEHeaders(w)
	emit := sseEmitter(w, flusher)

	succeeded, failed := 0, 0
	for i, p := range body.Paths {
		if r.Context().Err() != nil {
			return
		}
		if h.convertWebPOneSSE(r.Context(), emit, i, p, body.DeleteOriginal) {
			succeeded++
		} else {
			failed++
		}
	}
	emit(convSummary{Phase: "summary", Succeeded: succeeded, Failed: failed})
}

// convertWebPOneSSE drives one clip ??WebP conversion and emits exactly one
// terminal event (done or error) plus zero-or-one start and zero-or-more
// progress events. Returns true on success.
func (h *Handler) convertWebPOneSSE(parentCtx context.Context, emit func(any),
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
	ext := strings.ToLower(filepath.Ext(origName))
	srcDir := filepath.Dir(abs)

	// Eligibility gate. SPEC §2.9 + §2.5.3 server-side re-validation. GIFs
	// are unconditionally clips (no size or duration check). Videos must
	// pass both the size cap and duration cap; we read duration from the
	// thumb sidecar (cheap) and fall back to ffprobe one-shot when missing.
	var inputType string
	var hasAudio bool
	switch {
	case ext == ".gif":
		inputType = "image"
		// GIF containers carry no audio stream ??skip ProbeStreamInfo.
	case media.IsVideo(origName):
		if fi.Size() > clipMaxBytes {
			return emitErr("not_clip")
		}
		dur := videoDurationForGate(srcDir, origName, abs)
		if dur == nil {
			return emitErr("duration_unknown")
		}
		if *dur > clipMaxDurationSec {
			return emitErr("not_clip")
		}
		// Audio detection is best-effort; on probe failure we skip the
		// audio_dropped warning rather than failing the conversion.
		_, ha, perr := convert.ProbeStreamInfo(abs)
		if perr != nil {
			slog.Warn("convert-webp: ProbeStreamInfo failed",
				"path", relPath, "err", perr)
		}
		hasAudio = ha
		inputType = "video"
	default:
		return emitErr("unsupported_input")
	}

	// Output filename: <base>.webp, base name preserved, extension always
	// lowercase. SPEC §2.9.
	base := strings.TrimSuffix(origName, filepath.Ext(origName))
	finalName := base + ".webp"
	finalPath := filepath.Join(srcDir, finalName)

	if _, err := os.Stat(finalPath); err == nil {
		return emitErr("already_exists")
	} else if !os.IsNotExist(err) {
		return emitErr("write_error")
	}

	unlock := h.lockWebPKey(abs)
	defer unlock()

	// Re-check after lock: another concurrent request might have produced
	// the .webp while we were waiting.
	if _, err := os.Stat(finalPath); err == nil {
		return emitErr("already_exists")
	}

	// Build the slash-prefixed relative path of the final WebP for the done
	// event so the client can point loadBrowse() at the same folder.
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
				Name: finalName, Total: total, Type: inputType,
			})
		},
		OnProgress: func(received int64) {
			select {
			case progressCh <- received:
			default:
			}
		},
	}

	fileCtx, cancel := context.WithTimeout(parentCtx, webpConvertFileTimeout)
	res, rerr := convert.EncodeWebP(fileCtx, abs, srcDir, base, cb)
	cancel()

	close(progressCh)
	<-writerDone

	if rerr != nil {
		code := classifyConvertError(rerr, parentCtx)
		// classifyConvertError maps DeadlineExceeded to "convert_timeout";
		// retain that wire code (matches SPEC §5.1 for /api/convert-webp).
		logConvertError(relPath, rerr, code)
		return emitErr(code)
	}

	warnings := []string{}
	if hasAudio {
		warnings = append(warnings, "audio_dropped")
	}
	if deleteOriginal {
		if w := deleteOriginalAndSidecars(abs, inputType); w != "" {
			warnings = append(warnings, w)
		}
	}

	emit(convDone{
		Phase: "done", Index: index, Path: finalRel, Name: finalName,
		Size: res.Size, Type: "image", Warnings: warnings,
	})
	return true
}

// videoDurationForGate returns the cached duration sidecar value if present,
// otherwise probes via ffprobe (writing a sidecar for future calls). Returns
// nil when duration cannot be determined ??caller surfaces as
// "duration_unknown". The thumb sidecar lives at <srcDir>/.thumb/<name>.jpg.dur.
func videoDurationForGate(srcDir, origName, absVideoPath string) *float64 {
	thumbPath := filepath.Join(srcDir, ".thumb", origName+".jpg")
	if dur := thumb.LookupDuration(thumbPath); dur != nil {
		return dur
	}
	// BackfillDuration writes the sidecar best-effort; a write failure
	// (read-only thumb dir) does not block the gate decision.
	if dur := thumb.BackfillDuration(thumbPath, absVideoPath); dur != nil {
		return dur
	}
	return nil
}

// deleteOriginalAndSidecars removes the source clip and its .thumb sidecars
// (best-effort). Returns "delete_original_failed" on any failure that isn't
// a missing file, "" on success. inputType selects which sidecars to clean:
// videos own both .jpg and .jpg.dur, GIFs only .jpg.
func deleteOriginalAndSidecars(abs, inputType string) string {
	srcDir := filepath.Dir(abs)
	origName := filepath.Base(abs)
	if err := os.Remove(abs); err != nil {
		slog.Warn("convert-webp: delete original failed", "path", abs, "err", err)
		return "delete_original_failed"
	}
	thumbDir := filepath.Join(srcDir, ".thumb")
	suffixes := []string{".jpg"}
	if inputType == "video" {
		suffixes = append(suffixes, ".jpg.dur")
	}
	for _, suf := range suffixes {
		sidecar := filepath.Join(thumbDir, origName+suf)
		if err := os.Remove(sidecar); err != nil && !os.IsNotExist(err) {
			slog.Warn("convert-webp: sidecar delete failed",
				"sidecar", sidecar, "err", err)
			return "delete_original_failed"
		}
	}
	return ""
}

// lockWebPKey serializes producers for the same source path. Mirrors
// convert.go's lockConvertKey ??leaves the map entry in place after unlock,
// bounded by the set of unique source files on disk.
func (h *Handler) lockWebPKey(key string) func() {
	v, _ := h.webpLocks.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
