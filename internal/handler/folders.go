package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"file_server/internal/media"
)

func (h *Handler) handleFolder(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.createFolder(w, r)
	case http.MethodDelete:
		h.deleteFolder(w, r)
	case http.MethodPatch:
		h.patchFolder(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
	}
}

// patchFolder dispatches PATCH /api/folder by inspecting the body shape:
//
//	{"name": "..."}  → rename in place
//	{"to":   "..."}  → move into a different directory (base name preserved)
//
// Mirrors patchFile so the API surface for files and folders stays symmetric.
func (h *Handler) patchFolder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		if isMaxBytesErr(err) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", nil)
			return
		}
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
		h.moveFolder(w, r)
	case probe.Name != "":
		h.renameFolder(w, r)
	default:
		writeError(w, r, http.StatusBadRequest, "missing name or to", nil)
	}
}

func (h *Handler) moveFolder(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	srcAbs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	if srcAbs == filepath.Clean(h.dataDir) {
		writeError(w, r, http.StatusBadRequest, "cannot move root", nil)
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
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	// Reject moving into the same parent — same rule as moveFile.
	if filepath.Clean(filepath.Dir(srcAbs)) == filepath.Clean(destAbs) {
		writeError(w, r, http.StatusBadRequest, "same directory", nil)
		return
	}

	finalAbs, err := media.MoveDir(srcAbs, destAbs)
	if err != nil {
		switch {
		case errors.Is(err, media.ErrSrcNotFound):
			writeError(w, r, http.StatusNotFound, "not found", nil)
		case errors.Is(err, media.ErrSrcNotDir):
			writeError(w, r, http.StatusBadRequest, "not a directory", nil)
		case errors.Is(err, media.ErrDestNotFound),
			errors.Is(err, media.ErrDestNotDir),
			errors.Is(err, media.ErrCircular):
			writeError(w, r, http.StatusBadRequest, "invalid destination", nil)
		case errors.Is(err, media.ErrDestExists):
			writeError(w, r, http.StatusConflict, "already exists", nil)
		case errors.Is(err, media.ErrCrossDevice):
			// 다른 볼륨 마운트는 운영 precondition 미충족 — 5xx가 아니라 4xx로
			// 매핑해야 운영 도구가 server malfunction으로 오해하지 않는다.
			writeError(w, r, http.StatusBadRequest, "cross_device", nil)
		default:
			writeError(w, r, http.StatusInternalServerError, "move failed", err)
		}
		return
	}

	dstRel, err := filepath.Rel(h.dataDir, finalAbs)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "path failed", err)
		return
	}
	relResult := "/" + filepath.ToSlash(dstRel)
	writeJSON(w, r, http.StatusOK, map[string]string{
		"path": relResult,
		"name": filepath.Base(finalAbs),
	})
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
	writeJSON(w, r, http.StatusOK, map[string]string{
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

	// Stat→Mkdir 사이의 race 제거: Mkdir 자체가 EEXIST를 반환하므로
	// 별도 Stat은 redundant + 동시 creator 둘 다 통과/실패할 위험이 있다.
	if err := os.Mkdir(targetAbs, 0755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			writeError(w, r, http.StatusConflict, "already exists", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "mkdir failed", err)
		return
	}

	relResult := filepath.ToSlash(filepath.Join(rel, body.Name))
	writeJSON(w, r, http.StatusCreated, map[string]string{"path": relResult})
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
