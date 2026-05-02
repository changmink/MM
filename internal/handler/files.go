package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"file_server/internal/media"
	"file_server/internal/thumb"
)

func (h *Handler) handleFile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodDelete:
		h.deleteFile(w, r)
	case http.MethodPatch:
		h.patchFile(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
	}
}

// patchFile dispatches PATCH /api/file by inspecting the body shape:
//
//	{"name": "..."}  → rename in place (extension preserved)
//	{"to":   "..."}  → move to a different directory
//
// Both fields are mutually exclusive. The body is read once into memory and
// re-attached so each downstream handler can decode it normally — neither
// renameFile nor moveFile knows it was inspected first.
func (h *Handler) patchFile(w http.ResponseWriter, r *http.Request) {
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "read body failed", err)
		return
	}
	var probe struct {
		Name string `json:"name"`
		To   string `json:"to"`
	}
	if err := json.Unmarshal(bodyBytes, &probe); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid body", err)
		return
	}
	if probe.Name != "" && probe.To != "" {
		writeError(w, r, http.StatusBadRequest, "specify either name or to, not both", nil)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	switch {
	case probe.To != "":
		h.moveFile(w, r)
	case probe.Name != "":
		h.renameFile(w, r)
	default:
		writeError(w, r, http.StatusBadRequest, "missing name or to", nil)
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

