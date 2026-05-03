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

// MoveFile은 srcAbs를 destDir 아래로 옮기고 결과 절대경로를 반환한다.
//
// destDir에 같은 base name이 이미 있으면 업로드와 동일한 의미로 _1, _2, ...
// 접미사를 붙인다. 사이드카 파일(.thumb/<name>.jpg 및 .thumb/<name>.jpg.dur)
// 은 best-effort로 함께 이동한다 — handleThumb가 lazy 재생성할 수 있으니
// 사이드카 실패는 로그만 남기고 이동 자체를 막지 않는다.
//
// 동일 볼륨 이동은 os.Rename(atomic)을 쓰고, cross-device(EXDEV)는
// copy+fsync+remove로 폴백한다. 고유 이름 탐색은 stat-then-rename이라 짧은
// TOCTOU 창이 있지만, 단일 사용자 배포 모델에서는 허용된다.
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

// NameWithSuffix는 attempt ≤ 0이면 name을 그대로 반환하고, 그 외엔
// "<stem>_<attempt><ext>" 형태를 만든다. stem/ext는 filepath.Ext로 자르므로
// .tar.gz 같은 합성 확장자는 마지막 세그먼트만 분리된다. 업로드·URL import·
// 파일 rename·폴더 이동이 모두 공유하는 _N 충돌 회피 규칙의 단일 출처 —
// 어느 경로로 만들어졌든 사용자 입장에서 "foo_3.png"의 의미가 동일하게
// 유지되도록 한다.
func NameWithSuffix(name string, attempt int) string {
	if attempt <= 0 {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	return fmt.Sprintf("%s_%d%s", stem, attempt, ext)
}

// uniqueDestPath는 destDir에서 "name", "name_1", "name_2", ... 순서로
// 처음 비어 있는 이름을 찾아 반환한다. 상한은 createUniqueFile과 동일하다.
func uniqueDestPath(destDir, name string) (string, error) {
	const maxAttempts = 10000
	for i := 0; i < maxAttempts; i++ {
		candidate := filepath.Join(destDir, NameWithSuffix(name, i))
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
		// 이 시점엔 양쪽 사본이 모두 존재한다. src를 진실로 유지하기 위해 dst를 지운다.
		os.Remove(dst)
		return err
	}
	return nil
}

// MoveDir은 디렉터리 srcAbs를 destDir 아래로 옮기고 새 절대경로
// destDir/<basename(srcAbs)>을 반환한다.
//
// MoveFile과 달리 이름 충돌 시 자동 접미사 대신 ErrDestExists를 반환한다 —
// 폴더 rename은 사용자 명시 행동이라 조용한 _N 접미사가 도움보다 혼란을
//준다.
//
// destDir이 srcAbs와 같거나 그 하위면 ErrCircular를 반환한다. 하위 판정은
// 경로 구분자 경계를 사용해 /a/b가 /a/bc 안에 있다고 잘못 인식되지 않게 한다.
//
// 다른 볼륨 간 이동(EXDEV)은 ErrCrossDevice로 처리한다 — 재귀 복사 폴백은
// 단일 볼륨 배포 모델(SPEC §10)에서 의도적으로 범위 밖이다. 폴더의 내용물
// (.thumb/ 포함)은 os.Rename에 의해 한 번에 원자적으로 따라가므로 별도
// 사이드카 처리도 필요하지 않다.
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
	// 구분자 경계가 있어 /tmp/ab가 /tmp/a의 하위로 잘못 인식되는 것을 막는다.
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

// moveSidecars는 .thumb/<name>.jpg와 .thumb/<name>.jpg.dur를 새 파일 위치에
// 맞춰 옮긴다. 실패는 로그만 남기고 전파하지 않아, 누락되거나 잠긴 사이드카
// 때문에 사용자 가시 이동이 막히지 않게 한다.
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
