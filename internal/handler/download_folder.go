package handler

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"file_server/internal/media"
)

// handleDownloadFolder는 GET/POST /api/download-folder 라우트를 처리한다.
// GET: path 폴더 전체 재귀 ZIP. POST: items에 명시된 자손 파일/폴더만 ZIP.
// 응답은 archive/zip.Writer가 ResponseWriter로 직접 스트리밍 — ZIP 전체를
// 메모리에 적재하지 않는다. 미디어는 이미 압축되어 있어 Store 모드만 사용.
func (h *Handler) handleDownloadFolder(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.downloadFolderAll(w, r)
	case http.MethodPost:
		h.downloadFolderItems(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
	}
}

func (h *Handler) downloadFolderAll(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	abs, ok := h.resolveDownloadDir(w, r, rel)
	if !ok {
		return
	}

	rootName := zipRootName(abs, h.dataDir)
	setZipDownloadHeaders(w, rootName+".zip")

	zw := zip.NewWriter(w)
	if err := walkAndAddDir(r.Context(), zw, abs, rootName); err != nil {
		// 헤더를 이미 보냈으므로 5xx로 응답을 바꿀 수 없다. 로그만 남긴다.
		slog.Warn("download-folder walk failed",
			"method", r.Method, "path", r.URL.Path, "err", err,
		)
	}
	if err := zw.Close(); err != nil {
		slog.Warn("download-folder zip close failed",
			"method", r.Method, "path", r.URL.Path, "err", err,
		)
	}
}

func (h *Handler) downloadFolderItems(w http.ResponseWriter, r *http.Request) {
	rel := r.URL.Query().Get("path")
	abs, ok := h.resolveDownloadDir(w, r, rel)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
	var body struct {
		Items []string `json:"items"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		if isMaxBytesErr(err) {
			writeError(w, r, http.StatusRequestEntityTooLarge, "too_large", err)
			return
		}
		writeError(w, r, http.StatusBadRequest, "invalid body", err)
		return
	}

	pathAbs := filepath.Clean(abs)

	type resolvedItem struct {
		abs string
		fi  os.FileInfo
	}
	resolved := make([]resolvedItem, 0, len(body.Items))
	for _, raw := range body.Items {
		itemAbs, err := media.SafePath(h.dataDir, raw)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid items", nil)
			return
		}
		// path 자신은 허용 (전체 다운로드와 의미 동일). 그 외엔 path 자손이어야 한다.
		if itemAbs != pathAbs && !strings.HasPrefix(itemAbs, pathAbs+string(filepath.Separator)) {
			writeError(w, r, http.StatusBadRequest, "invalid items", nil)
			return
		}
		// SPEC §2.10 "dot-prefix 일체 제외" 정책을 items 우회 경로에도 강제한다.
		// items=["/dir/.thumb"]이나 items=["/dir/.thumb/foo.jpg"]는 walkAndAddDir의
		// `path == root` 가드를 통해 사이드카를 ZIP에 통째로 동봉시킬 수 있어,
		// 진입 시점에서 모든 세그먼트를 검사해 차단한다. path 자신(rel == ".")은
		// 전체 다운로드와 의미가 같으므로 통과시킨다.
		if itemAbs != pathAbs {
			relInside, err := filepath.Rel(pathAbs, itemAbs)
			if err != nil {
				writeError(w, r, http.StatusBadRequest, "invalid items", nil)
				return
			}
			for _, seg := range strings.Split(filepath.ToSlash(relInside), "/") {
				if strings.HasPrefix(seg, ".") {
					writeError(w, r, http.StatusBadRequest, "invalid items", nil)
					return
				}
			}
		}
		// symlink는 root 외부로 새는 것을 막기 위해 Lstat로 검사 후 skip.
		ifi, err := os.Lstat(itemAbs)
		if err != nil {
			if os.IsNotExist(err) {
				writeError(w, r, http.StatusBadRequest, "invalid items", nil)
				return
			}
			writeError(w, r, http.StatusInternalServerError, "stat failed", err)
			return
		}
		if ifi.Mode()&os.ModeSymlink != 0 {
			continue
		}
		resolved = append(resolved, resolvedItem{abs: itemAbs, fi: ifi})
	}

	rootName := zipRootName(abs, h.dataDir)
	setZipDownloadHeaders(w, fmt.Sprintf("%s-selected-%d.zip", rootName, len(resolved)))

	zw := zip.NewWriter(w)
	for _, item := range resolved {
		if err := r.Context().Err(); err != nil {
			break
		}
		relInside, err := filepath.Rel(pathAbs, item.abs)
		if err != nil {
			slog.Warn("download-folder rel failed", "err", err)
			continue
		}
		zipName := filepath.ToSlash(filepath.Join(rootName, relInside))
		if item.fi.IsDir() {
			if err := walkAndAddDir(r.Context(), zw, item.abs, zipName); err != nil {
				slog.Warn("download-folder item walk failed", "err", err)
			}
			continue
		}
		if err := addFileToZip(zw, item.abs, zipName, item.fi); err != nil {
			slog.Warn("download-folder add file failed", "err", err)
		}
	}
	if err := zw.Close(); err != nil {
		slog.Warn("download-folder zip close failed", "err", err)
	}
}

// resolveDownloadDir는 path 쿼리를 SafePath로 검증하고 디렉터리인지 확인한다.
// 검증 실패 시 적절한 4xx/5xx를 직접 응답하고 ok=false를 반환 — 호출자는 ok가
// false면 그대로 return.
func (h *Handler) resolveDownloadDir(w http.ResponseWriter, r *http.Request, rel string) (string, bool) {
	abs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return "", false
	}
	fi, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "not found", nil)
			return "", false
		}
		writeError(w, r, http.StatusInternalServerError, "stat failed", err)
		return "", false
	}
	if !fi.IsDir() {
		writeError(w, r, http.StatusBadRequest, "not a directory", nil)
		return "", false
	}
	return abs, true
}

// zipRootName은 ZIP 안의 최상위 폴더 이름을 결정한다. 데이터 루트면 "files"
// placeholder, 그 외는 폴더 base name. 추출 시 ZIP 파일명과 동일한 폴더에
// 풀리도록 ZIP 내부의 모든 경로를 이 prefix로 묶는다.
func zipRootName(abs, dataDir string) string {
	if filepath.Clean(abs) == filepath.Clean(dataDir) {
		return "files"
	}
	return filepath.Base(abs)
}

// setZipDownloadHeaders는 application/zip MIME과 RFC 5987 인코딩된
// Content-Disposition을 설정한다. ASCII fallback도 같이 보내 옛 클라이언트
// 호환을 유지하되, 한글·기호는 *=UTF-8'' 형태로 정확히 전달.
func setZipDownloadHeaders(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Type", "application/zip")
	asciiFallback := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7E || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s",
			asciiFallback, encodeRFC5987(name)))
}

// encodeRFC5987은 RFC 5987 attr-char(알파넘 + 일부 기호)가 아닌 모든 바이트를
// percent-encode 한다. 멀티바이트 UTF-8은 자연스럽게 바이트 단위로 인코딩된다.
func encodeRFC5987(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			b.WriteByte(c)
			continue
		}
		switch c {
		case '!', '#', '$', '&', '+', '-', '.', '^', '_', '`', '|', '~':
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

// walkAndAddDir은 root 디렉토리를 재귀 walk하며 dot-prefix 항목·symlink·
// 디렉토리 엔트리를 건너뛰고 일반 파일만 zipPrefix 아래에 추가한다.
// 디렉토리 엔트리를 별도로 추가하지 않는 이유: Store 모드 ZIP 추출기는
// 파일 경로의 부모 디렉토리를 자동으로 만들어주므로, 빈 디렉토리만 손실되며
// 그건 의도한 단순화(빈 폴더는 다운로드 시 보존되지 않아도 무방).
// ctx는 클라이언트 abort/서버 shutdown 전파용 — 콜백 진입부에서 검사해 walk를
// 즉시 중단한다 (POST 루프의 동등 패턴과 일관).
func walkAndAddDir(ctx context.Context, zw *zip.Writer, root, zipPrefix string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			// 권한 거부 등 walk 도중 에러는 해당 항목만 skip — 전체 다운로드를
			// 끊지 않는다.
			slog.Warn("download-folder walk error", "path", path, "err", walkErr)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if path == root {
			return nil
		}
		name := d.Name()
		if strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		zipName := filepath.ToSlash(filepath.Join(zipPrefix, rel))
		return addFileToZip(zw, path, zipName, info)
	})
}

func addFileToZip(zw *zip.Writer, abs, zipName string, info os.FileInfo) error {
	// Open이 CreateHeader보다 먼저: 권한 거부·삭제된 파일 등 open 실패가
	// ZIP에 빈 entry를 남기지 않게 한다 — header를 먼저 만들면 ZIP central
	// directory에 오프셋만 적힌 채로 본문이 비어 추출기가 엉뚱한 데이터로
	// 풀거나 corrupt 경고를 낸다.
	f, err := os.Open(abs)
	if err != nil {
		return err
	}
	defer f.Close()
	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = zipName
	header.Method = zip.Store
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.Copy(writer, f)
	return err
}
