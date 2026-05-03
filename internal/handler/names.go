package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// 이 파일은 file/folder/upload 핸들러가 함께 쓰는 이름·경로 유틸을 모은다.
// 라우트군별 파일(file.go / folders.go / upload.go)에 흩어져 있을 때 단일
// 파일이 비공개 심볼을 다른 라우트군에 빌려주는 단방향 의존이 생겨 분할
// 기준이 약해지므로, 공유 유틸은 여기로 모으고 라우트 파일은 자기 라우트만
// 다룬다.

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
