package handler

import (
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"file_server/internal/imageconv"
	"file_server/internal/media"
)

// pngAutoConvertQuality는 업로드 시점 PNG → JPG 변환에 사용하는 JPEG
// 품질이다(SPEC §2.8). 90은 일반적인 사진·스크린샷 콘텐츠에서 시각 품질과
// 파일 크기를 균형 있게 만든다. 사용자 설정 대상이 아닌 것은 의도된 결정이다.
const pngAutoConvertQuality = 90

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	rel := r.URL.Query().Get("path")
	destDir, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	// MultipartReader가 본문에 hook하기 전에 multipart 본문 상한을 건다.
	// MaxBytesReader가 한도 초과 시 Copy 도중 *http.MaxBytesError를 발사하므로,
	// 아래 part 루프가 이를 413으로 매핑한다 — 멈춘/악의적 클라이언트가 디스크를
	// 채우는 일을 막는다.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	// ParseMultipartForm이 업로드 전체를 버퍼링하는 대신(32MB는 RAM,
	// 나머지는 temp 파일로 spill), multipart 본문을 디스크로 직접 스트리밍
	// 한다. MultipartReader는 그 버퍼링을 완전히 건너뛴다.
	mr, err := r.MultipartReader()
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "expected multipart body", err)
		return
	}

	var part *multipart.Part
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			writeError(w, r, http.StatusBadRequest, "missing file field", nil)
			return
		}
		if err != nil {
			if isMaxBytesErr(err) {
				writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", nil)
				return
			}
			writeError(w, r, http.StatusBadRequest, "read part failed", err)
			return
		}
		if p.FormName() == "file" {
			part = p
			break
		}
		p.Close()
	}
	defer part.Close()

	if part.FileName() == "" {
		writeError(w, r, http.StatusBadRequest, "missing filename", nil)
		return
	}

	if err := os.MkdirAll(destDir, 0755); err != nil {
		writeError(w, r, http.StatusInternalServerError, "mkdir failed", err)
		return
	}

	// filepath.Base는 클라이언트가 보낸 파일명에서 디렉터리 부분을 제거한다.
	clientName := filepath.Base(part.FileName())
	destPath := filepath.Join(destDir, clientName)

	// 디스크에 손대기 전에 PNG → JPG 분기 여부를 결정한다 — 그래야 업로드를
	// 사용자 노출 대상이 아닌 temp PNG로 스트리밍하고, 변환 결과를 알게 된
	// 뒤에야 최종 이름으로 확정할 수 있다. SPEC §2.8.1: 확장자가 .png(대소문자
	// 무시)이고 스냅샷의 토글이 켜져 있을 때만 분기로 들어간다.
	autoConvert := strings.EqualFold(filepath.Ext(clientName), ".png") &&
		h.settingsSnapshot().AutoConvertPNGToJPG

	var (
		finalPath string
		finalSize int64
		converted bool
		warnings  = []string{}
	)

	if autoConvert {
		var err error
		finalPath, finalSize, converted, warnings, err = h.uploadPNGAutoConvert(destDir, destPath, part)
		if err != nil {
			if isMaxBytesErr(err) {
				writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", nil)
				return
			}
			writeError(w, r, http.StatusInternalServerError, "write file failed", err)
			return
		}
	} else {
		dst, err := createUniqueFile(destPath)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "create file failed", err)
			return
		}
		finalPath = dst.Name()
		size, copyErr := io.Copy(dst, part)
		closeErr := dst.Close()
		if copyErr != nil {
			os.Remove(finalPath)
			if isMaxBytesErr(copyErr) {
				writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", nil)
				return
			}
			writeError(w, r, http.StatusInternalServerError, "write file failed", copyErr)
			return
		}
		// NFS / 일부 네트워크 FS는 deferred write 에러를 close 시점에만 노출.
		// 무시하면 잘림 업로드가 201로 응답될 수 있어 부분 파일을 정리하고 5xx 반환.
		if closeErr != nil {
			os.Remove(finalPath)
			writeError(w, r, http.StatusInternalServerError, "write file failed", closeErr)
			return
		}
		finalSize = size
	}

	fileType := media.DetectType(filepath.Base(finalPath))

	// 이미지·영상은 제한된 워커 풀로 썸네일을 비동기 생성한다. 풀 큐가 가득
	// 차면 로그만 남기고 건너뛴다 — handleThumb가 첫 조회 시 lazy로 생성하므로
	// 사용자는 어쨌든 썸네일을 받는다.
	if fileType == media.TypeImage || fileType == media.TypeVideo {
		thumbDir := filepath.Join(destDir, ".thumb")
		thumbPath := filepath.Join(thumbDir, filepath.Base(finalPath)+".jpg")
		if !h.thumbPool.Submit(finalPath, thumbPath) {
			slog.Warn("thumb pool full, deferring to lazy generation",
				"src", finalPath,
			)
		}
	}

	relResult := filepath.ToSlash(filepath.Join(rel, filepath.Base(finalPath)))
	writeJSON(w, r, http.StatusCreated, map[string]interface{}{
		"path":      relResult,
		"name":      filepath.Base(finalPath),
		"size":      finalSize,
		"type":      string(fileType),
		"converted": converted,
		"warnings":  warnings,
	})
}

// uploadPNGAutoConvert는 multipart part를 숨겨진 temp PNG로 스트리밍하고,
// imageconv로 JPEG로 변환한 뒤 결과를 destDir의 <basename>.jpg로 rename한다.
// 어떤 변환 실패에서든 원본 PNG는 처음 요청된 destPath에 보존된다 — 업로드는
// "convert_failed" 경고와 함께 여전히 성공(201)하므로 클라이언트는 데이터
// 손실로 취급하지 않고 폴백을 표면화할 수 있다(SPEC §2.8.1).
func (h *Handler) uploadPNGAutoConvert(destDir, destPath string, part io.Reader) (finalPath string, size int64, converted bool, warnings []string, err error) {
	warnings = []string{}

	// 두 temp 파일 모두 destDir에 두어 최종 rename이 같은 볼륨에서 일어나도록
	// 한다. dot prefix는 짧은 쓰기 창 동안 /api/browse에서 이 파일들을 숨긴다.
	tmpPNG, err := os.CreateTemp(destDir, ".pngconvert-*.png")
	if err != nil {
		return "", 0, false, warnings, fmt.Errorf("create temp png: %w", err)
	}
	tmpPNGPath := tmpPNG.Name()
	pngConsumed := false
	defer func() {
		if !pngConsumed {
			_ = os.Remove(tmpPNGPath)
		}
	}()

	size, copyErr := io.Copy(tmpPNG, part)
	closeErr := tmpPNG.Close()
	// 비-conversion 경로(handleUpload)와 동일한 wrap 패턴으로 통일 — 운영
	// 로그의 err 필드 형태가 두 경로 사이에 일관되게 유지된다.
	if copyErr != nil {
		return "", 0, false, warnings, fmt.Errorf("copy png temp: %w", copyErr)
	}
	if closeErr != nil {
		return "", 0, false, warnings, fmt.Errorf("close png temp: %w", closeErr)
	}

	// 사용자 노출 jpgPath가 아닌 형제 temp 파일로 변환해, rename 시점까지는
	// 사용자 노출 이름을 차지하지 않게 한다.
	tmpJPGPath := tmpPNGPath + ".jpg"
	convErr := imageconv.ConvertPNGToJPG(tmpPNGPath, tmpJPGPath, pngAutoConvertQuality)

	// jpgPath는 사람이 보기 좋은 대상 — 원래 base 이름에 .jpg(소문자)다.
	// SHOT.PNG → SHOT.jpg가 된다.
	ext := filepath.Ext(destPath)
	jpgPath := destPath[:len(destPath)-len(ext)] + ".jpg"

	if convErr == nil {
		// 변환 성공 — JPG를 자리로 rename하고 PNG temp는 정리한다.
		final, suffixed, renameErr := renameToUniqueDest(tmpJPGPath, jpgPath)
		if renameErr != nil {
			_ = os.Remove(tmpJPGPath)
			return "", 0, false, warnings, fmt.Errorf("rename converted jpg: %w", renameErr)
		}
		_ = os.Remove(tmpPNGPath)
		pngConsumed = true
		if suffixed {
			warnings = append(warnings, "renamed")
		}
		st, statErr := os.Stat(final)
		if statErr != nil {
			return "", 0, false, warnings, fmt.Errorf("stat converted jpg: %w", statErr)
		}
		return final, st.Size(), true, warnings, nil
	}

	// 변환 실패 — 원본 PNG 저장으로 폴백한다.
	slog.Warn("png auto-convert failed, falling back to original",
		"dest", destPath, "err", convErr,
	)
	_ = os.Remove(tmpJPGPath) // imageconv가 자체 temp를 정리한다. 여기서는 best-effort.
	warnings = append(warnings, "convert_failed")
	final, suffixed, renameErr := renameToUniqueDest(tmpPNGPath, destPath)
	if renameErr != nil {
		return "", 0, false, warnings, fmt.Errorf("rename fallback png: %w", renameErr)
	}
	pngConsumed = true
	if suffixed {
		warnings = append(warnings, "renamed")
	}
	return final, size, false, warnings, nil
}
