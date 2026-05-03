package convertapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"file_server/internal/convert"
	"file_server/internal/handlerutil"
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

// handleConvertWebP은 클립 → 애니메이션 WebP 변환을 위한 SSE 배치
// 엔드포인트를 구동한다. wire 스키마는 POST /api/convert(TS → MP4)와
// 같은 start/progress/done/error/summary phase를 사용하므로 프론트엔드가
// SSE 소비 패턴을 재사용한다. 파일별 자격 검증(GIF는 무조건, 영상은
// ≤50MiB && ≤30s)이 어떤 인코딩 작업보다 먼저 실행되며, HTTP 오류가 아닌
// terminal error 이벤트로 표면화된다.
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

// convertWebPOneSSE는 클립 → WebP 변환 하나를 구동하고 정확히 한 번의
// terminal 이벤트(done 또는 error), start 0~1회, progress 0회 이상을
// 발행한다. 성공 시 true를 반환한다.
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

	// 자격 게이트. SPEC §2.9 + §2.5.3 서버 측 재검증. GIF는 크기·duration
	// 검사 없이 무조건 클립으로 본다. 영상은 크기 상한과 duration 상한을
	// 모두 통과해야 한다 — duration은 thumb 사이드카(저렴)에서 읽고, 없으면
	// ffprobe one-shot으로 폴백한다.
	var inputType string
	var hasAudio bool
	switch {
	case ext == ".gif":
		inputType = "image"
		// GIF 컨테이너는 오디오 스트림이 없다 — ProbeStreamInfo를 건너뛴다.
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
		// 오디오 감지는 best-effort다. probe 실패 시 변환을 실패시키는 대신
		// audio_dropped 경고를 건너뛴다.
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

	// 출력 파일명: <base>.webp, base는 보존하고 확장자는 항상 소문자. SPEC §2.9.
	base := strings.TrimSuffix(origName, filepath.Ext(origName))
	finalName := base + ".webp"
	finalPath := filepath.Join(srcDir, finalName)

	if _, err := os.Stat(finalPath); err == nil {
		return emitErr("already_exists")
	} else if !os.IsNotExist(err) {
		return emitErr("write_error")
	}

	unlock := handlerutil.LockPath(h.webpLocks, abs)
	defer unlock()

	// 잠금 후 재확인: 우리가 대기하는 동안 다른 동시 요청이 .webp를 만들었을 수 있다.
	if _, err := os.Stat(finalPath); err == nil {
		return emitErr("already_exists")
	}

	// done 이벤트용 최종 WebP의 슬래시 prefix 상대 경로를 만들어, 클라이언트가
	// loadBrowse()를 같은 폴더로 가리킬 수 있게 한다.
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
		// classifyConvertError는 DeadlineExceeded를 "convert_timeout"로
		// 매핑한다 — 그 wire 코드를 그대로 둔다(SPEC §5.1의
		// /api/convert-webp와 일치).
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

// videoDurationForGate는 캐시된 duration 사이드카 값이 있으면 반환하고,
// 없으면 ffprobe로 probe해 (이후 호출을 위해 사이드카를 쓰면서) 반환한다.
// duration을 결정할 수 없으면 nil을 반환한다 — 호출자는 이를
// "duration_unknown"으로 표면화한다. thumb 사이드카는
// <srcDir>/.thumb/<name>.jpg.dur 위치에 있다.
func videoDurationForGate(srcDir, origName, absVideoPath string) *float64 {
	thumbPath := filepath.Join(srcDir, ".thumb", origName+".jpg")
	if dur := thumb.LookupDuration(thumbPath); dur != nil {
		return dur
	}
	// BackfillDuration은 사이드카를 best-effort로 쓴다 — 쓰기 실패(예:
	// 읽기 전용 thumb 디렉터리)는 gate 결정을 막지 않는다.
	if dur := thumb.BackfillDuration(thumbPath, absVideoPath); dur != nil {
		return dur
	}
	return nil
}

// deleteOriginalAndSidecars는 소스 클립과 그 .thumb 사이드카들을
// (best-effort로) 제거한다. 파일 부재가 아닌 어떤 실패에서든
// "delete_original_failed"를 반환하고, 성공 시 ""를 반환한다. inputType은
// 어떤 사이드카를 정리할지 결정한다 — 영상은 .jpg와 .jpg.dur 둘 다, GIF는
// .jpg만이다.
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

