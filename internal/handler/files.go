package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/chang/file_server/internal/media"
	"github.com/chang/file_server/internal/thumb"
)

func (h *Handler) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rel := r.URL.Query().Get("path")
	destDir, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "parse form failed")
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()

	if err := os.MkdirAll(destDir, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "mkdir failed")
		return
	}

	// filepath.Base strips any directory component from the client-supplied filename
	destPath := filepath.Join(destDir, filepath.Base(header.Filename))
	destPath = uniquePath(destPath)

	dst, err := os.Create(destPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create file failed")
		return
	}
	defer dst.Close()

	size, err := io.Copy(dst, file)
	if err != nil {
		dst.Close()
		os.Remove(destPath)
		writeError(w, http.StatusInternalServerError, "write file failed")
		return
	}

	fileType := media.DetectType(header.Filename)

	// generate thumbnail asynchronously for images
	if fileType == media.TypeImage {
		thumbDir := filepath.Join(destDir, ".thumb")
		// use destPath (after uniquePath rename) so thumb name matches the actual file
		thumbPath := filepath.Join(thumbDir, filepath.Base(destPath)+".jpg")
		go func() {
			os.MkdirAll(thumbDir, 0755)
			thumb.Generate(destPath, thumbPath)
		}()
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
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	rel := r.URL.Query().Get("path")
	abs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "stat failed")
		return
	}

	if fi.IsDir() {
		writeError(w, http.StatusBadRequest, "cannot delete a directory")
		return
	}

	if err := os.Remove(abs); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
		return
	}

	// remove thumbnail if image
	if media.IsImage(fi.Name()) {
		thumbPath := filepath.Join(filepath.Dir(abs), ".thumb", fi.Name()+".jpg")
		os.Remove(thumbPath)
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handleFolder(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createFolder(w, r)
	case http.MethodDelete:
		h.deleteFolder(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *Handler) createFolder(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	parentAbs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}

	if err := validateFolderName(body.Name); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	targetAbs := filepath.Join(parentAbs, body.Name)

	if _, err := os.Stat(targetAbs); err == nil {
		writeError(w, http.StatusConflict, "already exists")
		return
	}

	if err := os.Mkdir(targetAbs, 0755); err != nil {
		writeError(w, http.StatusInternalServerError, "mkdir failed")
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
		writeError(w, http.StatusBadRequest, "invalid path")
		return
	}

	// prevent deleting the root data directory itself
	if abs == filepath.Clean(h.dataDir) {
		writeError(w, http.StatusBadRequest, "cannot delete root")
		return
	}

	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "stat failed")
		return
	}

	if !fi.IsDir() {
		writeError(w, http.StatusBadRequest, "not a directory")
		return
	}

	if err := os.RemoveAll(abs); err != nil {
		writeError(w, http.StatusInternalServerError, "delete failed")
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

func uniquePath(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := path[:len(path)-len(ext)]
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s_%d%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
}
