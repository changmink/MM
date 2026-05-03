package handler

import (
	"encoding/json"
	"errors"
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

// patchFolder dispatches PATCH /api/folder:
//
//	{"name": "..."}  → renameFolder
//	{"to":   "..."}  → moveFolder (base name preserved)
//
// 본문 분기·검증은 patchFile과 공유 — names.go의 patchDispatch 단일 출처.
func (h *Handler) patchFolder(w http.ResponseWriter, r *http.Request) {
	patchDispatch(w, r, h.renameFolder, h.moveFolder)
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

	// 같은 부모로의 이동은 거부한다 — moveFile과 동일한 규칙.
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

	// parentAbs는 srcAbs를 통해 이미 safe-check 되었고, body.Name은
	// validateName이 separator를 막아주므로 join이 root를 탈출할 수 없다.
	dstAbs := filepath.Join(filepath.Dir(srcAbs), body.Name)

	// 대소문자만 다른 rename(movies → Movies)은 존재 검사를 건너뛰어야 한다
	// — case-insensitive FS에서 Stat이 source 자체로 해석되기 때문이다.
	caseOnly := strings.EqualFold(fi.Name(), body.Name) && fi.Name() != body.Name
	if !caseOnly {
		if _, err := os.Stat(dstAbs); err == nil {
			writeError(w, r, http.StatusConflict, "already exists", nil)
			return
		}
	}

	// 단일 OS rename이 디렉터리를 원자적으로 이동시킨다. .thumb/ 서브디렉터리
	// 포함 내용물이 자동으로 따라가므로 별도 사이드카 처리가 필요 없다.
	// Stat→Rename 사이를 동시 생성자가 차지하는 race는 허용된다 — SPEC §9
	// "known limitations" (단일 사용자 배포 대상) 참조.
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

	// 루트 데이터 디렉터리 자체의 삭제를 막는다
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
