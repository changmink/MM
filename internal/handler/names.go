package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// 이 파일은 file/folder/upload 핸들러가 함께 쓰는 이름·경로·라우팅 유틸을
// 모은다. 라우트군별 파일(file.go / folders.go / upload.go)에 흩어져 있을 때
// 단일 파일이 비공개 심볼을 다른 라우트군에 빌려주는 단방향 의존이 생겨
// 분할 기준이 약해지므로, 공유 유틸은 여기로 모으고 라우트 파일은 자기
// 라우트만 다룬다.

// patchDispatch는 PATCH /api/file과 PATCH /api/folder가 공유하는 본문 분기
// 헬퍼다. 본문을 한 번 읽어 `{"name": ...}` 또는 `{"to": ...}` 둘 중 하나로
// 분류하고 해당 핸들러에 dispatch한다.
//
// 본문은 한 번 읽어 io.NopCloser로 다시 r.Body에 부착하므로 onRename·onMove
// 핸들러는 body가 처음 요청된 것처럼 다시 디코드할 수 있다 — 두 핸들러가
// 본문 inspect 사실을 알 필요 없다.
//
// 동일한 본문 구조 + 동일한 4xx 매트릭스(too_large / read body failed /
// invalid body / specify-both / missing-both)를 두 라우트에서 그대로 쓰므로
// 단일 출처. 정책 변경(maxJSONBodyBytes 조정, 새 4xx 분기 추가)은 여기 한
// 곳만 수정하면 두 라우트가 동시에 따라간다.
func patchDispatch(w http.ResponseWriter, r *http.Request, onRename, onMove http.HandlerFunc) {
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
		onMove(w, r)
	case probe.Name != "":
		onRename(w, r)
	default:
		writeError(w, r, http.StatusBadRequest, "missing name or to", nil)
	}
}

// validateName rejects names that would either escape the FS root, fail an
// OS-level Mkdir/Rename, or break Windows portability. Catches:
//   - empty / "." / ".." / >255 / path-separator-bearing names
//   - Windows-reserved chars: < > : " | ? *
//   - control chars (NUL ~ 0x1F + DEL)
//   - Windows-reserved basenames: CON / PRN / AUX / NUL / COM1-9 / LPT1-9
//     (case-insensitive, with or without extension)
//
// Used by file rename, folder rename, and folder create — same rule everywhere
// so the path-traversal first line of defense doesn't drift between callers.
// Wire: callers map any error here to `400 {"error": "invalid name"}` (SPEC §5)
// so we keep the single short code; richer diagnostics belong in client UI.
func validateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid name")
	}
	if len(name) > 255 {
		return fmt.Errorf("invalid name")
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f {
			return fmt.Errorf("invalid name")
		}
		switch c {
		case '/', '\\', '<', '>', ':', '"', '|', '?', '*':
			return fmt.Errorf("invalid name")
		}
	}
	base := strings.ToUpper(stripTrailingExt(name))
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return fmt.Errorf("invalid name")
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		if base[3] >= '1' && base[3] <= '9' {
			return fmt.Errorf("invalid name")
		}
	}
	return nil
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

// renameToUniqueDest moves srcPath to destPath, falling back to <base>_N.<ext>
// suffixes if the target slot is taken. Uses os.Link + os.Remove so EEXIST is
// detected race-free (matches atomicRenameFile) — a parallel uploader cannot
// claim the same free slot between our existence check and our rename. The
// suffixed return reports whether a suffix was applied so the caller can
// surface a "renamed" warning.
func renameToUniqueDest(srcPath, destPath string) (finalPath string, suffixed bool, err error) {
	const maxAttempts = 10000
	if linkErr := os.Link(srcPath, destPath); linkErr == nil {
		_ = os.Remove(srcPath)
		return destPath, false, nil
	} else if !os.IsExist(linkErr) {
		return "", false, linkErr
	}
	ext := filepath.Ext(destPath)
	base := destPath[:len(destPath)-len(ext)]
	for i := 1; i < maxAttempts; i++ {
		candidate := fmt.Sprintf("%s_%d%s", base, i, ext)
		if linkErr := os.Link(srcPath, candidate); linkErr == nil {
			_ = os.Remove(srcPath)
			return candidate, true, nil
		} else if !os.IsExist(linkErr) {
			return "", false, linkErr
		}
	}
	return "", false, fmt.Errorf("renameToUniqueDest: exhausted attempts for %s", destPath)
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
