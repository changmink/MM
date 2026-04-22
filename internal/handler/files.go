package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/chang/file_server/internal/media"
)

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

	// filepath.Base strips any directory component from the client-supplied filename
	destPath := filepath.Join(destDir, filepath.Base(part.FileName()))
	dst, err := createUniqueFile(destPath)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "create file failed", err)
		return
	}
	destPath = dst.Name()
	defer dst.Close()

	size, err := io.Copy(dst, part)
	if err != nil {
		os.Remove(destPath)
		writeError(w, r, http.StatusInternalServerError, "write file failed", err)
		return
	}

	fileType := media.DetectType(part.FileName())

	// generate thumbnail asynchronously for images via bounded worker pool.
	// If the pool queue is full we log and skip — handleThumb will generate
	// it lazily on first view, so the user still gets a thumbnail.
	if fileType == media.TypeImage {
		thumbDir := filepath.Join(destDir, ".thumb")
		thumbPath := filepath.Join(thumbDir, filepath.Base(destPath)+".jpg")
		if !h.thumbPool.Submit(destPath, thumbPath) {
			slog.Warn("thumb pool full, deferring to lazy generation",
				"src", destPath,
			)
		}
	}

	relResult := filepath.ToSlash(filepath.Join(rel, filepath.Base(destPath)))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"path": relResult,
		"name": filepath.Base(destPath),
		"size": size,
		"type": string(fileType),
	})
}

func (h *Handler) handleFile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodDelete:
		h.deleteFile(w, r)
	case http.MethodPatch:
		h.moveFile(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
	}
}

func (h *Handler) deleteFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	abs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "not found", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "stat failed", err)
		return
	}

	if fi.IsDir() {
		writeError(w, r, http.StatusBadRequest, "cannot delete a directory", nil)
		return
	}

	if err := os.Remove(abs); err != nil {
		writeError(w, r, http.StatusInternalServerError, "delete failed", err)
		return
	}

	// remove thumbnail if image
	if media.IsImage(fi.Name()) {
		thumbPath := filepath.Join(filepath.Dir(abs), ".thumb", fi.Name()+".jpg")
		os.Remove(thumbPath)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) moveFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	srcAbs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	var body struct {
		To string `json:"to"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid body", err)
		return
	}

	destAbs, err := media.SafePath(h.dataDir, body.To)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid destination", err)
		return
	}

	// Reject moving into the same directory: it's almost always a UI mistake,
	// and silently succeeding would still apply a _N suffix (cosmetic noise).
	if filepath.Clean(filepath.Dir(srcAbs)) == filepath.Clean(destAbs) {
		writeError(w, r, http.StatusBadRequest, "same directory", nil)
		return
	}

	finalAbs, err := media.MoveFile(srcAbs, destAbs)
	if err != nil {
		switch {
		case errors.Is(err, media.ErrSrcNotFound):
			writeError(w, r, http.StatusNotFound, "not found", nil)
		case errors.Is(err, media.ErrSrcIsDir):
			writeError(w, r, http.StatusBadRequest, "cannot move directory", nil)
		case errors.Is(err, media.ErrDestNotFound), errors.Is(err, media.ErrDestNotDir):
			writeError(w, r, http.StatusBadRequest, "invalid destination", nil)
		default:
			writeError(w, r, http.StatusInternalServerError, "move failed", err)
		}
		return
	}

	finalName := filepath.Base(finalAbs)
	finalRel := filepath.ToSlash(filepath.Join(body.To, finalName))
	if !strings.HasPrefix(finalRel, "/") {
		finalRel = "/" + finalRel
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"path": finalRel,
		"name": finalName,
	})
}

func (h *Handler) handleFolder(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createFolder(w, r)
	case http.MethodDelete:
		h.deleteFolder(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
	}
}

func (h *Handler) createFolder(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	parentAbs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid body", err)
		return
	}

	if err := validateFolderName(body.Name); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error(), nil)
		return
	}

	targetAbs := filepath.Join(parentAbs, body.Name)

	if _, err := os.Stat(targetAbs); err == nil {
		writeError(w, r, http.StatusConflict, "already exists", nil)
		return
	}

	if err := os.Mkdir(targetAbs, 0755); err != nil {
		writeError(w, r, http.StatusInternalServerError, "mkdir failed", err)
		return
	}

	relResult := filepath.ToSlash(filepath.Join(rel, body.Name))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"path": relResult})
}

func (h *Handler) deleteFolder(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	abs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	// prevent deleting the root data directory itself
	if abs == filepath.Clean(h.dataDir) {
		writeError(w, r, http.StatusBadRequest, "cannot delete root", nil)
		return
	}

	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "not found", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "stat failed", err)
		return
	}

	if !fi.IsDir() {
		writeError(w, r, http.StatusBadRequest, "not a directory", nil)
		return
	}

	if err := os.RemoveAll(abs); err != nil {
		writeError(w, r, http.StatusInternalServerError, "delete failed", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func validateFolderName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid name")
	}
	if len(name) > 255 {
		return fmt.Errorf("invalid name")
	}
	for _, c := range name {
		if c == '/' || c == '\\' {
			return fmt.Errorf("invalid name")
		}
	}
	return nil
}

// createUniqueFile atomically creates path (or path with _N suffix if taken)
// using O_CREATE|O_EXCL so concurrent uploads of the same filename cannot
// observe the same free slot and clobber each other.
func createUniqueFile(path string) (*os.File, error) {
	const maxAttempts = 10000
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err == nil {
		return f, nil
	}
	if !os.IsExist(err) {
		return nil, err
	}
	ext := filepath.Ext(path)
	base := path[:len(path)-len(ext)]
	for i := 1; i < maxAttempts; i++ {
		candidate := fmt.Sprintf("%s_%d%s", base, i, ext)
		f, err := os.OpenFile(candidate, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
		if err == nil {
			return f, nil
		}
		if !os.IsExist(err) {
			return nil, err
		}
	}
	return nil, fmt.Errorf("could not find unique name for %s after %d attempts", path, maxAttempts)
}
