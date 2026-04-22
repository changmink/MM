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
	"github.com/chang/file_server/internal/thumb"
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

	// generate thumbnail asynchronously for images and videos via the bounded
	// worker pool. If the pool queue is full we log and skip — handleThumb
	// generates lazily on first view, so the user still gets a thumbnail.
	if fileType == media.TypeImage || fileType == media.TypeVideo {
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
		h.renameFile(w, r)
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

	// remove sidecar thumbnail (and duration sidecar for videos). Both are
	// best-effort: a stale .jpg simply gets overwritten on next generation.
	if media.IsImage(fi.Name()) || media.IsVideo(fi.Name()) {
		thumbPath := filepath.Join(filepath.Dir(abs), ".thumb", fi.Name()+".jpg")
		os.Remove(thumbPath)
		if media.IsVideo(fi.Name()) {
			os.Remove(thumb.DurationSidecarPath(thumbPath))
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) renameFile(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	srcAbs, err := media.SafePath(h.dataDir, rel)
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

	fi, err := os.Stat(srcAbs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "not found", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "stat failed", err)
		return
	}
	if fi.IsDir() {
		writeError(w, r, http.StatusBadRequest, "not a file", nil)
		return
	}

	// Rename keeps the original extension. Strip any extension the user
	// may have included so "new.mkv" on an .mp4 file becomes "new.mp4".
	oldName := fi.Name()
	origExt := fileExtension(oldName)
	newBase := stripTrailingExt(body.Name)
	if err := validateName(newBase); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error(), nil)
		return
	}
	newName := newBase + origExt
	if len(newName) > 255 {
		writeError(w, r, http.StatusBadRequest, "invalid name", nil)
		return
	}

	if newName == oldName {
		writeError(w, r, http.StatusBadRequest, "name unchanged", nil)
		return
	}

	// parentAbs was safe-checked via srcAbs; newName has no path separators
	// per validateName; join cannot escape the root.
	parentAbs := filepath.Dir(srcAbs)
	dstAbs := filepath.Join(parentAbs, newName)

	if err := atomicRenameFile(srcAbs, dstAbs, oldName, newName); err != nil {
		if errors.Is(err, os.ErrExist) {
			writeError(w, r, http.StatusConflict, "already exists", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "rename failed", err)
		return
	}

	renameThumbSidecars(parentAbs, oldName, newName)

	dstRel, err := filepath.Rel(h.dataDir, dstAbs)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "path failed", err)
		return
	}
	relResult := "/" + filepath.ToSlash(dstRel)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"path": relResult,
		"name": newName,
	})
}

// renameThumbSidecars moves .thumb/{oldName}.jpg and (for videos) its .dur
// sidecar to match a renamed source file. oldName and newName must be
// basenames only — a caller passing a path-with-slashes would silently
// produce a wrong thumb path. validateName on the rename entry points
// guarantees this today. Sidecar failures are logged but never block
// success — the next /api/thumb request will regenerate them.
func renameThumbSidecars(parentAbs, oldName, newName string) {
	if !media.IsImage(oldName) && !media.IsVideo(oldName) {
		return
	}
	thumbDir := filepath.Join(parentAbs, ".thumb")
	oldThumb := filepath.Join(thumbDir, oldName+".jpg")
	newThumb := filepath.Join(thumbDir, newName+".jpg")
	if err := os.Rename(oldThumb, newThumb); err != nil && !os.IsNotExist(err) {
		slog.Warn("thumb sidecar rename failed", "old", oldThumb, "new", newThumb, "err", err)
	}
	if media.IsVideo(oldName) {
		oldDur := oldThumb + ".dur"
		newDur := newThumb + ".dur"
		if err := os.Rename(oldDur, newDur); err != nil && !os.IsNotExist(err) {
			slog.Warn("duration sidecar rename failed", "old", oldDur, "new", newDur, "err", err)
		}
	}
}

// fileExtension returns the extension to preserve during file rename.
// Unlike filepath.Ext, a leading-dot name with no other dot (".gitignore",
// ".env") is treated as having no extension so the user can freely rename
// dotfiles without an unwanted suffix being reattached. Matches the JS
// client's splitExtension.
func fileExtension(name string) string {
	if strings.HasPrefix(name, ".") && strings.Count(name, ".") == 1 {
		return ""
	}
	return filepath.Ext(name)
}

// stripTrailingExt removes any extension the user may have typed in the
// new name. Unlike fileExtension, this uses plain filepath.Ext so that a
// leading-dot input like ".mp4" strips to "" (which validateName rejects)
// — users are expected to enter a base name, and a bare extension is more
// likely a typo than a dotfile intent.
func stripTrailingExt(name string) string {
	if ext := filepath.Ext(name); ext != "" {
		return strings.TrimSuffix(name, ext)
	}
	return name
}

// atomicRenameFile moves srcAbs to dstAbs, returning os.ErrExist if the
// destination already exists. Uses os.Link (atomic EEXIST on POSIX and
// Windows NTFS) plus os.Remove to close the TOCTOU window that a plain
// Stat+Rename would leave open against a concurrent creator. Case-only
// renames (a.txt → A.txt) fall back to plain os.Rename because a hard
// link between two spellings of the same inode on case-insensitive
// filesystems would itself fail EEXIST.
func atomicRenameFile(srcAbs, dstAbs, oldName, newName string) error {
	if strings.EqualFold(oldName, newName) && oldName != newName {
		return os.Rename(srcAbs, dstAbs)
	}
	if err := os.Link(srcAbs, dstAbs); err != nil {
		return err
	}
	if err := os.Remove(srcAbs); err != nil {
		os.Remove(dstAbs) // roll back the link so src remains the canonical file
		return err
	}
	return nil
}

func (h *Handler) handleFolder(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createFolder(w, r)
	case http.MethodDelete:
		h.deleteFolder(w, r)
	case http.MethodPatch:
		h.renameFolder(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
	}
}

func (h *Handler) renameFolder(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	srcAbs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	if srcAbs == filepath.Clean(h.dataDir) {
		writeError(w, r, http.StatusBadRequest, "cannot rename root", nil)
		return
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid body", err)
		return
	}

	if err := validateName(body.Name); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error(), nil)
		return
	}

	fi, err := os.Stat(srcAbs)
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

	if body.Name == fi.Name() {
		writeError(w, r, http.StatusBadRequest, "name unchanged", nil)
		return
	}

	// parentAbs was safe-checked via srcAbs; body.Name has no separators
	// per validateName; join cannot escape the root.
	dstAbs := filepath.Join(filepath.Dir(srcAbs), body.Name)

	// Case-only rename (movies → Movies) must skip the existence check
	// because Stat on a case-insensitive FS resolves to the source itself.
	caseOnly := strings.EqualFold(fi.Name(), body.Name) && fi.Name() != body.Name
	if !caseOnly {
		if _, err := os.Stat(dstAbs); err == nil {
			writeError(w, r, http.StatusConflict, "already exists", nil)
			return
		}
	}

	// Single OS rename moves the directory atomically; contents (including
	// .thumb/ subdirectory) follow automatically — no sidecar bookkeeping.
	// A concurrent creator winning the Stat→Rename gap is an accepted race;
	// see SPEC §9 "known limitations" (single-user deployment target).
	if err := os.Rename(srcAbs, dstAbs); err != nil {
		writeError(w, r, http.StatusInternalServerError, "rename failed", err)
		return
	}

	dstRel, err := filepath.Rel(h.dataDir, dstAbs)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "path failed", err)
		return
	}
	relResult := "/" + filepath.ToSlash(dstRel)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"path": relResult,
		"name": body.Name,
	})
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

	if err := validateName(body.Name); err != nil {
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

func validateName(name string) error {
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
