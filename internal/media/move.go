package media

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	ErrSrcNotFound  = errors.New("source not found")
	ErrSrcIsDir     = errors.New("cannot move directory")
	ErrSrcNotDir    = errors.New("source is not a directory")
	ErrDestNotDir   = errors.New("destination is not a directory")
	ErrDestNotFound = errors.New("destination not found")
	ErrDestExists   = errors.New("destination already exists")
	ErrCircular     = errors.New("destination is inside source")
	ErrCrossDevice  = errors.New("cross-device folder move not supported")
)

// MoveFile moves srcAbs into destDir and returns the resulting absolute path.
//
// If destDir already contains a file with the same base name, the destination
// gets a _1, _2, ... suffix to match upload semantics. Sidecar files
// (.thumb/<name>.jpg and .thumb/<name>.jpg.dur) are moved alongside on a
// best-effort basis — sidecar failures are logged but never fail the move,
// since handleThumb can lazily regenerate.
//
// Same-volume moves use os.Rename (atomic). Cross-device moves (EXDEV) fall
// back to copy+fsync+remove. The unique-name probe is stat-then-rename, which
// has a small TOCTOU window; acceptable for the single-user deployment model.
func MoveFile(srcAbs, destDir string) (string, error) {
	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrSrcNotFound
		}
		return "", err
	}
	if srcInfo.IsDir() {
		return "", ErrSrcIsDir
	}

	destInfo, err := os.Stat(destDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrDestNotFound
		}
		return "", err
	}
	if !destInfo.IsDir() {
		return "", ErrDestNotDir
	}

	srcName := filepath.Base(srcAbs)
	destPath, err := uniqueDestPath(destDir, srcName)
	if err != nil {
		return "", err
	}

	if err := moveOne(srcAbs, destPath); err != nil {
		return "", err
	}

	moveSidecars(srcAbs, destPath)
	return destPath, nil
}

// uniqueDestPath probes destDir for the first free name in the
// "name", "name_1", "name_2", ... sequence. The bound matches createUniqueFile.
func uniqueDestPath(destDir, name string) (string, error) {
	const maxAttempts = 10000
	candidate := filepath.Join(destDir, name)
	if _, err := os.Stat(candidate); os.IsNotExist(err) {
		return candidate, nil
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i < maxAttempts; i++ {
		candidate = filepath.Join(destDir, fmt.Sprintf("%s_%d%s", base, i, ext))
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("could not find unique name for %s after %d attempts", name, maxAttempts)
}

func moveOne(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EXDEV) {
		return copyAndRemove(src, dst)
	}
	return err
}

func copyAndRemove(src, dst string) error {
	srcF, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcF.Close()

	dstF, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dstF, srcF); err != nil {
		dstF.Close()
		os.Remove(dst)
		return err
	}
	if err := dstF.Sync(); err != nil {
		dstF.Close()
		os.Remove(dst)
		return err
	}
	if err := dstF.Close(); err != nil {
		os.Remove(dst)
		return err
	}
	if err := os.Remove(src); err != nil {
		// Both copies now exist; drop the new one to keep src as truth.
		os.Remove(dst)
		return err
	}
	return nil
}

// MoveDir moves srcAbs (a directory) into destDir, returning the new absolute
// path destDir/<basename(srcAbs)>.
//
// Unlike MoveFile, name conflicts return ErrDestExists rather than auto-
// suffixing — folders are renamed only by explicit user action and a silent
// _N suffix is more confusing than helpful here.
//
// destDir == srcAbs or any descendant of srcAbs returns ErrCircular. The
// descendant check uses a path-separator boundary so /a/b is not treated as
// inside /a/bc.
//
// Cross-volume moves (EXDEV) return ErrCrossDevice — recursive copy fallback
// is intentionally out of scope for the single-volume deployment model
// (SPEC §10). The folder's contents (including .thumb/) follow the os.Rename
// in a single atomic step, so no sidecar bookkeeping is needed.
func MoveDir(srcAbs, destDir string) (string, error) {
	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrSrcNotFound
		}
		return "", err
	}
	if !srcInfo.IsDir() {
		return "", ErrSrcNotDir
	}

	destInfo, err := os.Stat(destDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrDestNotFound
		}
		return "", err
	}
	if !destInfo.IsDir() {
		return "", ErrDestNotDir
	}

	srcClean := filepath.Clean(srcAbs)
	destClean := filepath.Clean(destDir)
	if destClean == srcClean {
		return "", ErrCircular
	}
	// Separator boundary prevents /tmp/ab being read as a descendant of /tmp/a.
	if strings.HasPrefix(destClean, srcClean+string(filepath.Separator)) {
		return "", ErrCircular
	}

	dstPath := filepath.Join(destClean, filepath.Base(srcClean))
	if _, err := os.Stat(dstPath); err == nil {
		return "", ErrDestExists
	} else if !os.IsNotExist(err) {
		return "", err
	}

	if err := os.Rename(srcAbs, dstPath); err != nil {
		if errors.Is(err, syscall.EXDEV) {
			return "", ErrCrossDevice
		}
		return "", err
	}
	return dstPath, nil
}

// moveSidecars relocates .thumb/<name>.jpg and .thumb/<name>.jpg.dur to match
// the new file location. Failures are logged but never propagated, so a
// missing/locked sidecar can't block the user-visible move.
func moveSidecars(srcFile, dstFile string) {
	srcDir, srcName := filepath.Split(srcFile)
	dstDir, dstName := filepath.Split(dstFile)
	srcThumb := filepath.Join(srcDir, ".thumb", srcName+".jpg")
	dstThumb := filepath.Join(dstDir, ".thumb", dstName+".jpg")

	pairs := []struct{ src, dst string }{
		{srcThumb, dstThumb},
		{srcThumb + ".dur", dstThumb + ".dur"},
	}

	for _, p := range pairs {
		if _, err := os.Stat(p.src); err != nil {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p.dst), 0755); err != nil {
			slog.Warn("sidecar mkdir failed", "dst", p.dst, "err", err)
			continue
		}
		if err := moveOne(p.src, p.dst); err != nil {
			slog.Warn("sidecar move failed", "src", p.src, "dst", p.dst, "err", err)
		}
	}
}
