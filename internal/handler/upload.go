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

// pngAutoConvertQuality is the JPEG quality used for upload-time PNG → JPG
// conversion (SPEC §2.8). 90 balances visible quality against file size for
// typical photo / screenshot content; not user-configurable on purpose.
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

	// Cap the multipart body before MultipartReader hooks into it. MaxBytesReader
	// fires *http.MaxBytesError mid-Copy once the limit is exceeded, so the part
	// loop below maps it to 413 instead of letting a stuck/malicious client fill
	// the disk.
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

	// Stream the multipart body directly to disk instead of letting
	// ParseMultipartForm buffer the whole upload (32MB in RAM, rest spilled
	// to temp files). MultipartReader skips that buffering entirely.
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

	// filepath.Base strips any directory component from the client-supplied filename.
	clientName := filepath.Base(part.FileName())
	destPath := filepath.Join(destDir, clientName)

	// Decide whether to take the PNG → JPG branch BEFORE touching disk so we
	// can stream the upload into a temp PNG (rather than the user-visible
	// destination) and only commit to a final name after the conversion result
	// is known. SPEC §2.8.1: branch fires only for .png (case-insensitive)
	// AND when the snapshot has the toggle on.
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

	// generate thumbnail asynchronously for images and videos via the bounded
	// worker pool. If the pool queue is full we log and skip — handleThumb
	// generates lazily on first view, so the user still gets a thumbnail.
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

// uploadPNGAutoConvert streams the multipart part into a hidden temp PNG,
// converts it to JPEG via imageconv, and renames the result to <basename>.jpg
// in destDir. On any conversion failure the original PNG is preserved at the
// originally-requested destPath instead — the upload still succeeds (201)
// with a "convert_failed" warning so the client can surface the fallback
// without treating it as data loss (SPEC §2.8.1).
func (h *Handler) uploadPNGAutoConvert(destDir, destPath string, part io.Reader) (finalPath string, size int64, converted bool, warnings []string, err error) {
	warnings = []string{}

	// Both temp files go in destDir so the final rename is same-volume. The
	// dot prefix hides them from /api/browse during the brief write window.
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

	// Convert into a sibling temp file rather than the visible jpgPath so we
	// don't claim the user-facing name until rename time.
	tmpJPGPath := tmpPNGPath + ".jpg"
	convErr := imageconv.ConvertPNGToJPG(tmpPNGPath, tmpJPGPath, pngAutoConvertQuality)

	// jpgPath is the human-friendly target — original base name with .jpg
	// (lowercased, so SHOT.PNG → SHOT.jpg).
	ext := filepath.Ext(destPath)
	jpgPath := destPath[:len(destPath)-len(ext)] + ".jpg"

	if convErr == nil {
		// Conversion succeeded — rename the JPG into place; PNG temp can go.
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

	// Conversion failed — fall back to saving the original PNG.
	slog.Warn("png auto-convert failed, falling back to original",
		"dest", destPath, "err", convErr,
	)
	_ = os.Remove(tmpJPGPath) // imageconv cleans its own temp, this is best-effort
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
