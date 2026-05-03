package convertapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"file_server/internal/imageconv"
	"file_server/internal/media"
)

const (
	maxConvertImagePaths    = 500
	imageConvertFileTimeout = 30 * time.Second
	imageConvertJPEGQuality = 90
)

// convertImageRequest는 POST /api/convert-image의 JSON 본문이다 (SPEC §2.8.2).
type convertImageRequest struct {
	Paths          []string `json:"paths"`
	DeleteOriginal bool     `json:"delete_original"`
}

// convertImageResult는 응답 배열의 한 엔트리를 그대로 반영한다.
// Output/Name/Size/Warnings는 성공 시 채워지고, Error는 실패 시 채워진다
// (배타적이라 동시에 채워질 수 없다).
type convertImageResult struct {
	Index    int      `json:"index"`
	Path     string   `json:"path"`
	Output   string   `json:"output,omitempty"`
	Name     string   `json:"name,omitempty"`
	Size     int64    `json:"size,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type convertImageResponse struct {
	Succeeded int                  `json:"succeeded"`
	Failed    int                  `json:"failed"`
	Results   []convertImageResult `json:"results"`
}

// handleConvertImage는 동기식 PNG → JPG 배치 엔드포인트를 구동한다. SPEC
// §2.8.2에 따라 SSE는 의도적으로 회피한다 — 이미지 변환은 파일당 1초도
// 한참 못 미쳐 끝나므로 스트리밍 프로토콜은 UX 이득 없이 복잡도만 더한다.
// 개별 실패는 HTTP 에러 코드 대신 results[i].Error로 반환되며, 요청 envelope
// 자체가 잘못되지 않은 이상 요청 전체는 200을 반환한다.
func (h *Handler) HandleConvertImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	var body convertImageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid request", err)
		return
	}
	if len(body.Paths) == 0 {
		writeError(w, r, http.StatusBadRequest, "no paths", nil)
		return
	}
	if len(body.Paths) > maxConvertImagePaths {
		writeError(w, r, http.StatusBadRequest, "too many paths", nil)
		return
	}

	resp := convertImageResponse{Results: make([]convertImageResult, len(body.Paths))}
	for i, p := range body.Paths {
		if r.Context().Err() != nil {
			// 요청 취소를 존중한다 — 남은 슬롯을 cancellation 마커로 채워,
			// 클라이언트가 요청한 모든 path마다 결과를 보게 한다(짧은 배열이 아닌).
			for j := i; j < len(body.Paths); j++ {
				resp.Results[j] = convertImageResult{Index: j, Path: body.Paths[j], Error: "canceled"}
				resp.Failed++
			}
			break
		}
		resp.Results[i] = h.convertImageOne(r.Context(), i, p, body.DeleteOriginal)
		if resp.Results[i].Error == "" {
			resp.Succeeded++
		} else {
			resp.Failed++
		}
	}

	writeJSON(w, r, http.StatusOK, resp)
}

// convertImageOne은 path 하나를 처리하고 한 개의 result 엔트리를 반환한다.
// 검증 순서는 spec과 일치한다: SafePath → 존재 → 파일(디렉터리 아님) →
// .png 확장자 → 대상 충돌 → 변환 → 선택적 원본 삭제.
func (h *Handler) convertImageOne(parentCtx context.Context, index int, relPath string, deleteOriginal bool) convertImageResult {
	res := convertImageResult{Index: index, Path: relPath}

	abs, err := media.SafePath(h.dataDir, relPath)
	if err != nil {
		res.Error = "invalid_path"
		return res
	}
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			res.Error = "not_found"
		} else {
			res.Error = "write_failed"
		}
		return res
	}
	if fi.IsDir() {
		res.Error = "not_a_file"
		return res
	}
	ext := filepath.Ext(fi.Name())
	if !strings.EqualFold(ext, ".png") {
		res.Error = "not_png"
		return res
	}
	base := strings.TrimSuffix(fi.Name(), ext)
	srcDir := filepath.Dir(abs)
	dstAbs := filepath.Join(srcDir, base+".jpg")

	// 어떤 작업보다 먼저 충돌 검사를 한다 — 실패를 저렴하게 만들고 대상
	// 슬롯이 차 있을 때 디스크에 손대지 않는다.
	if _, err := os.Stat(dstAbs); err == nil {
		res.Error = "already_exists"
		return res
	} else if !os.IsNotExist(err) {
		res.Error = "write_failed"
		return res
	}

	// 파일별 타임아웃. 변환 자체는 컨텍스트를 받지 않으므로 goroutine에서
	// 실행하고 타이머와 select 한다. 취소 시 워커가 자체 temp 파일을 정리할
	// 짧은 창을 준다.
	fctx, cancel := context.WithTimeout(parentCtx, imageConvertFileTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- imageconv.ConvertPNGToJPG(abs, dstAbs, imageConvertJPEGQuality)
	}()

	select {
	case err := <-done:
		if err != nil {
			res.Error = classifyConvertImageErr(err)
			return res
		}
	case <-fctx.Done():
		if errors.Is(fctx.Err(), context.DeadlineExceeded) {
			res.Error = "convert_timeout"
		} else {
			res.Error = "canceled"
		}
		// best-effort 정리. imageconv는 컨텍스트를 받지 않으므로, 요청이
		// 타임아웃되거나 취소된 뒤에도 워커가 dstAbs를 커밋할 수 있다.
		// 변환이 결국 성공하면 그 늦은 출력을 제거해, 이후 요청이
		// already_exists에 걸리지 않게 한다.
		go func() {
			if werr := <-done; werr == nil {
				_ = os.Remove(dstAbs)
			}
		}()
		return res
	}

	st, statErr := os.Stat(dstAbs)
	if statErr != nil {
		res.Error = "write_failed"
		return res
	}
	res.Output = filepath.ToSlash(filepath.Join(filepath.Dir(relPath), base+".jpg"))
	res.Name = base + ".jpg"
	res.Size = st.Size()

	if deleteOriginal {
		if warns := deleteOriginalPNGAndSidecar(abs); len(warns) > 0 {
			res.Warnings = warns
		}
	}
	return res
}

// classifyConvertImageErr은 imageconv 에러 체인을 공개 wire 코드로 매핑한다.
// Sentinel 검사(errors.Is)를 먼저 두어, 향후 wrap prefix 문구가 바뀌더라도
// too-large 거부가 write_failed로 조용히 다운그레이드되지 않게 한다. 나머지
// 현재 imageconv가 내보내는 wrap은 substring 매칭으로 처리한다.
func classifyConvertImageErr(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, imageconv.ErrImageTooLarge) {
		return "image_too_large"
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "imageconv: decode"):
		return "decode_failed"
	case strings.Contains(msg, "imageconv: encode"):
		return "encode_failed"
	default:
		return "write_failed"
	}
}

// deleteOriginalPNGAndSidecar은 원본 PNG와 그 .thumb 사이드카를 제거한다.
// 두 삭제 모두 best-effort다 — 둘 중 하나라도 실패하면 단일
// delete_original_failed 경고를 표면화한다. 사이드카 부재는 에러가 아니다 —
// 원본 PNG가 한 번도 browse되지 않았을 수 있다.
func deleteOriginalPNGAndSidecar(abs string) []string {
	sidecar := filepath.Join(filepath.Dir(abs), ".thumb", filepath.Base(abs)+".jpg")
	pngErr := os.Remove(abs)
	sidecarErr := os.Remove(sidecar)
	if pngErr != nil {
		return []string{"delete_original_failed"}
	}
	if sidecarErr != nil && !os.IsNotExist(sidecarErr) {
		return []string{"delete_original_failed"}
	}
	return nil
}
