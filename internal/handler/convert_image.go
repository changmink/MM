package handler

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
	maxConvertImagePaths    = 50
	imageConvertFileTimeout = 30 * time.Second
	imageConvertJPEGQuality = 90
)

// convertImageRequest is the JSON body for POST /api/convert-image (SPEC §2.8.2).
type convertImageRequest struct {
	Paths          []string `json:"paths"`
	DeleteOriginal bool     `json:"delete_original"`
}

// convertImageResult mirrors one entry of the response array. Output/Name/
// Size/Warnings populate on success; Error populates on failure (mutually
// exclusive — never both).
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

// handleConvertImage drives the synchronous PNG → JPG batch endpoint. SSE is
// intentionally avoided (SPEC §2.8.2) — image conversions complete in well
// under a second per file, so a streaming protocol would add complexity
// without UX benefit. Per-item failures are returned via results[i].Error
// rather than HTTP error codes; the request as a whole returns 200 unless
// the request envelope itself is malformed.
func (h *Handler) handleConvertImage(w http.ResponseWriter, r *http.Request) {
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
			// Honor request cancellation — populate remaining slots with
			// a cancellation marker so the client sees a result for every
			// requested path instead of a short array.
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// convertImageOne handles a single path and returns one result entry.
// Validation order matches the spec: SafePath → exists → file (not dir) →
// .png extension → target collision → conversion → optional original delete.
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

	// Conflict check before any work — keeps the failure cheap and avoids
	// touching disk when the target slot is taken.
	if _, err := os.Stat(dstAbs); err == nil {
		res.Error = "already_exists"
		return res
	} else if !os.IsNotExist(err) {
		res.Error = "write_failed"
		return res
	}

	// Per-file timeout. The conversion itself doesn't accept a context, so
	// we run it in a goroutine and select against the timer; on cancel we
	// give the worker a brief window to clean up its own temp file.
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
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		if errors.Is(fctx.Err(), context.DeadlineExceeded) {
			res.Error = "convert_timeout"
		} else {
			res.Error = "canceled"
		}
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

// classifyConvertImageErr maps the imageconv error chain to public wire codes.
// Sentinel checks (errors.Is) come first so a future rewording of the wrap
// prefix doesn't silently downgrade a too-large rejection to write_failed;
// substring matching covers the remaining wraps that imageconv emits today.
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

// deleteOriginalPNGAndSidecar removes the source PNG and its .thumb sidecar.
// Both deletions are best-effort: if either fails we surface a single
// delete_original_failed warning. Missing sidecar is not an error since the
// source PNG may never have been browsed.
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
