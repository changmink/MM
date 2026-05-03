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

	"file_server/internal/media"
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

// validateName은 FS root를 탈출하거나 OS 수준 Mkdir/Rename을 실패시키거나
// Windows 호환성을 깨뜨릴 이름을 거부한다. 잡아내는 것:
//   - 빈 문자열 / "." / ".." / 255자 초과 / 경로 구분자 포함
//   - Windows 예약 문자: < > : " | ? *
//   - 제어 문자 (NUL ~ 0x1F + DEL)
//   - Windows 예약 basename: CON / PRN / AUX / NUL / COM1-9 / LPT1-9
//     (대소문자 무시, 확장자 유무 무관)
//
// 파일 rename, 폴더 rename, 폴더 생성에 모두 사용한다 — path traversal
// 1차 방어선이 호출자별로 흔들리지 않도록 같은 규칙을 적용한다. 호출자는
// 여기서 발생한 에러를 모두 `400 {"error": "invalid name"}`(SPEC §5)로
// 매핑하므로 단일 짧은 코드를 유지한다 — 더 풍부한 진단은 클라이언트 UI의
// 몫이다.
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

// fileExtension은 파일 rename 동안 보존할 확장자를 반환한다. filepath.Ext
// 와 달리, 다른 점이 없는 leading-dot 이름(".gitignore", ".env")은 확장자가
// 없는 것으로 취급해, 사용자가 원치 않는 접미사가 다시 붙는 일 없이
// dotfile을 자유롭게 rename할 수 있다. JS 클라이언트의 splitExtension과
// 동일한 규칙이다.
func fileExtension(name string) string {
	if strings.HasPrefix(name, ".") && strings.Count(name, ".") == 1 {
		return ""
	}
	return filepath.Ext(name)
}

// stripTrailingExt는 사용자가 새 이름에 입력했을 수도 있는 확장자를 제거한다.
// fileExtension과 달리 일반 filepath.Ext를 사용하므로, ".mp4"같은
// leading-dot 입력은 ""로 깎인다(validateName이 거부한다) — 사용자는 base
// name을 입력해야 하고, 확장자만 단독으로 들어왔다면 dotfile 의도보다
// 오타일 가능성이 더 높다.
func stripTrailingExt(name string) string {
	if ext := filepath.Ext(name); ext != "" {
		return strings.TrimSuffix(name, ext)
	}
	return name
}

// atomicRenameFile은 srcAbs를 dstAbs로 옮기고, 대상이 이미 있으면
// os.ErrExist를 반환한다. POSIX와 Windows NTFS에서 EEXIST가 원자적인
// os.Link와 os.Remove를 함께 사용해, 일반적인 Stat+Rename이 동시 생성자에
// 대해 남기는 TOCTOU 창을 닫는다. 대소문자만 다른 rename(a.txt → A.txt)은
// 일반 os.Rename으로 폴백한다 — case-insensitive 파일시스템에서 같은
// inode의 두 표기 사이에 hard link를 거는 행위 자체가 EEXIST로 실패하기
// 때문이다.
func atomicRenameFile(srcAbs, dstAbs, oldName, newName string) error {
	if strings.EqualFold(oldName, newName) && oldName != newName {
		return os.Rename(srcAbs, dstAbs)
	}
	if err := os.Link(srcAbs, dstAbs); err != nil {
		return err
	}
	if err := os.Remove(srcAbs); err != nil {
		os.Remove(dstAbs) // link를 되돌려 src가 정본 파일로 남게 한다
		return err
	}
	return nil
}

// renameToUniqueDest는 srcPath를 destPath로 옮기되, 대상이 이미 차 있으면
// <base>_N.<ext> 접미사로 폴백한다. atomicRenameFile과 동일하게
// os.Link + os.Remove를 사용해 EEXIST를 race-free로 감지한다 — 병렬 업로더가
// 우리 존재 검사와 rename 사이에 같은 빈 슬롯을 차지할 수 없다. suffixed
// 반환은 접미사가 적용됐는지를 알려, 호출자가 "renamed" 경고를 표면화할 수
// 있게 한다. 접미사 규칙은 media.NameWithSuffix와 동일하다.
func renameToUniqueDest(srcPath, destPath string) (finalPath string, suffixed bool, err error) {
	const maxAttempts = 10000
	for i := 0; i < maxAttempts; i++ {
		candidate := media.NameWithSuffix(destPath, i)
		if linkErr := os.Link(srcPath, candidate); linkErr == nil {
			_ = os.Remove(srcPath)
			return candidate, i > 0, nil
		} else if !os.IsExist(linkErr) {
			return "", false, linkErr
		}
	}
	return "", false, fmt.Errorf("renameToUniqueDest: exhausted attempts for %s", destPath)
}

// createUniqueFile은 O_CREATE|O_EXCL로 path를(차 있으면 _N 접미사 path를)
// 원자적으로 생성한다. 같은 파일명에 대한 동시 업로드가 같은 빈 슬롯을
// 보고 서로를 덮어쓰는 일이 없다. 접미사 규칙은 media.NameWithSuffix와 동일하다.
func createUniqueFile(path string) (*os.File, error) {
	const maxAttempts = 10000
	for i := 0; i < maxAttempts; i++ {
		candidate := media.NameWithSuffix(path, i)
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
