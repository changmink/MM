package handler

import (
	"encoding/json"
	"errors"
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

// patchFile dispatches PATCH /api/file:
//
//	{"name": "..."}  → renameFile (extension preserved)
//	{"to":   "..."}  → moveFile
//
// 본문 분기·검증 로직은 patchFolder와 공유되므로 names.go의 patchDispatch
// 단일 출처에 위임한다.
func (h *Handler) patchFile(w http.ResponseWriter, r *http.Request) {
	patchDispatch(w, r, h.renameFile, h.moveFile)
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

	// 사이드카 썸네일을(영상은 duration 사이드카까지 포함해) 제거한다.
	// 둘 다 best-effort다 — 잔재 .jpg는 다음 생성 시 덮어써진다.
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

	// 같은 디렉터리로의 이동은 거부한다 — 거의 항상 UI 실수이며, 조용히
	// 성공시키면 _N 접미사가 붙어 미관상 잡음이 된다.
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

	writeJSON(w, r, http.StatusOK, map[string]string{
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

	// rename은 원본 확장자를 유지한다. 사용자가 입력한 확장자는 떼어내,
	// .mp4 파일에 대한 "new.mkv"는 "new.mp4"가 되도록 한다.
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

	// parentAbs는 srcAbs를 통해 이미 safe-check 되었고, newName은
	// validateName이 path separator를 막아주므로 join이 root를 탈출할 수 없다.
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
	writeJSON(w, r, http.StatusOK, map[string]string{
		"path": relResult,
		"name": newName,
	})
}

// renameThumbSidecars는 rename된 원본 파일에 맞춰 .thumb/{oldName}.jpg와
// (영상의 경우) 그 .dur 사이드카를 옮긴다. oldName과 newName은 basename만
// 와야 한다 — 슬래시가 들어간 경로를 호출자가 넘기면 조용히 잘못된 thumb
// 경로를 만든다. 현재로선 rename 진입부의 validateName이 이를 보장한다.
// 사이드카 실패는 로그만 남기고 성공을 막지 않는다 — 다음 /api/thumb
// 요청이 재생성한다.
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

