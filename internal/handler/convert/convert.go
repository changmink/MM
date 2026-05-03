package convertapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"file_server/internal/convert"
	"file_server/internal/handlerutil"
	"file_server/internal/media"
)

const (
	maxConvertPaths    = 500
	convertFileTimeout = 10 * time.Minute
)

type convertRequest struct {
	Paths          []string `json:"paths"`
	DeleteOriginal bool     `json:"delete_original"`
}

// SSE 이벤트 형태. wire 스키마는 import_url.go의 sseStart 등과 같지만,
// 입력이 로컬 파일이라 `url` 대신 `path`를 쓴다.
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

func (h *Handler) HandleConvert(w http.ResponseWriter, r *http.Request) {
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
		if h.convertOneSSE(r.Context(), emit, i, p, body.DeleteOriginal) {
			succeeded++
		} else {
			failed++
		}
	}
	emit(convSummary{Phase: "summary", Succeeded: succeeded, Failed: failed})
}

// convertOneSSE는 TS → MP4 변환 하나를 구동하고 정확히 한 번의 terminal
// 이벤트(done 또는 error), start 0~1회, progress 0회 이상을 발행한다.
// 성공 시 true를 반환한다.
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

	unlock := handlerutil.LockPath(h.convertLocks, abs)
	defer unlock()

	// 잠금 후 재확인: 우리가 대기하는 동안 다른 요청이 .mp4를 만들었을 수 있다.
	if _, err := os.Stat(finalPath); err == nil {
		return emitErr("already_exists")
	}

	// done 이벤트용 최종 MP4의 상대 경로. 요청 입력과 마찬가지로 "/"로 시작해
	// 클라이언트가 loadBrowse를 같은 폴더로 가리킬 수 있게 한다.
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
			// best-effort 사이드카 정리. 사이드카는 새 .mp4에 대해 필요 시
			// 재생성되므로 여기서 실패해도 경고가 필요하지 않다. 관측 가능한
			// 유일한 효과는 잔재 thumb뿐이며, 사용자가 폴더를 건드리면 제거된다.
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

// classifyConvertError는 런타임 에러를 공개 SSE 코드로 매핑한다. 부모 ctx를
// 먼저 검사해, 클라이언트 disconnect(canceled)가 ffmpeg가 종료 경로에서
// 마침 보고한 다른 에러보다 우선하게 한다.
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

// logConvertError는 중단된 변환에 대해 구조화된 서버 로그를 남긴다.
// 사용자는 불투명 코드만 받는다 — 운영자는 여기서 근본 원인(ffmpeg stderr,
// I/O 에러)을 찾을 수 있다.
func logConvertError(relPath string, err error, code string) {
	attrs := []any{"code", code, "path", relPath, "err", err.Error()}
	var ffErr *convert.FFmpegExitError
	if errors.As(err, &ffErr) && ffErr.Stderr != "" {
		attrs = append(attrs, "stderr", ffErr.Stderr)
	}
	slog.Warn("convert failed", attrs...)
}

