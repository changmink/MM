package handler

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"file_server/internal/media"
)

const maxTreeDepth = 5

// treeNode는 폴더 전용 내비게이션 노드다.
//
//	Children == nil   → 로드되지 않음(depth 한도에 도달했고 더 있음)
//	Children == []    → 로드됨, 자식 없음
//	Children == [...] → 자식 로드됨
//
// HasChildren은 항상 채워지므로 UI가 추가 요청 없이도 expand chevron을
// 렌더링할 수 있다.
type treeNode struct {
	Path        string     `json:"path"`
	Name        string     `json:"name"`
	HasChildren bool       `json:"has_children"`
	Children    []treeNode `json:"children"`
}

func (h *Handler) handleTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	rel := r.URL.Query().Get("path")
	if rel == "" {
		rel = "/"
	}

	depth := 1
	if d := r.URL.Query().Get("depth"); d != "" {
		n, err := strconv.Atoi(d)
		if err != nil || n < 1 || n > maxTreeDepth {
			writeError(w, r, http.StatusBadRequest, "invalid depth", nil)
			return
		}
		depth = n
	}

	abs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "not found", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "stat failed", err)
		return
	}
	if !info.IsDir() {
		writeError(w, r, http.StatusBadRequest, "not a directory", nil)
		return
	}

	children, hasChildren, err := walkTree(abs, normalizeTreeRel(rel), depth)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "read dir failed", err)
		return
	}

	root := treeNode{
		Path:        normalizeTreeRel(rel),
		Name:        nodeName(rel),
		HasChildren: hasChildren,
		Children:    children,
	}

	writeJSON(w, r, http.StatusOK, root)
}

// walkTree는 dirAbs의 직속 자식 폴더를 반환한다. depth > 1이면 각 자식으로
// depth-1로 재귀한다. depth == 1에서는 각 자식의 Children을 HasChildren에
// 따라 nil(더 있음) 또는 []로 둔다.
func walkTree(dirAbs, dirRel string, depth int) ([]treeNode, bool, error) {
	entries, err := os.ReadDir(dirAbs)
	if err != nil {
		return nil, false, err
	}

	dirs := make([]os.DirEntry, 0, len(entries))
	for _, e := range entries {
		// .thumb를 비롯한 모든 dotfile/dotdir은 서버 내부 항목이라 노출하지 않는다.
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if e.IsDir() {
			dirs = append(dirs, e)
		}
	}
	if len(dirs) == 0 {
		return []treeNode{}, false, nil
	}

	sort.Slice(dirs, func(i, j int) bool {
		return strings.ToLower(dirs[i].Name()) < strings.ToLower(dirs[j].Name())
	})

	children := make([]treeNode, 0, len(dirs))
	for _, e := range dirs {
		name := e.Name()
		childRel := joinTreeRel(dirRel, name)
		childAbs := filepath.Join(dirAbs, name)

		node := treeNode{Path: childRel, Name: name}

		if depth > 1 {
			grand, hasGrand, err := walkTree(childAbs, childRel, depth-1)
			if err != nil {
				// 한 개의 읽기 불가 subdir이 트리 전체를 실패시켜선 안 된다.
				// "자식 없음"으로 표면화하되, 운영자가 권한/IO 문제를 추적할 수
				// 있도록 로그를 남긴다 — 조용한 빈 칸이 되지 않게.
				slog.Debug("tree walk: subdir unreadable", "path", childAbs, "err", err)
				node.Children = []treeNode{}
				node.HasChildren = false
			} else {
				node.Children = grand
				node.HasChildren = hasGrand
			}
		} else {
			has, err := dirHasSubdirs(childAbs)
			if err != nil {
				slog.Debug("tree walk: has-children probe failed", "path", childAbs, "err", err)
			}
			node.HasChildren = has
			if has {
				node.Children = nil // 로드되지 않음 — UI가 expand 시 lazy fetch 한다
			} else {
				node.Children = []treeNode{}
			}
		}
		children = append(children, node)
	}
	return children, true, nil
}

// dirHasSubdirs는 dirAbs에 숨겨지지 않은 subdirectory가 하나라도 있는지
// 확인한다. depth 경계에서 grandchild를 로드하지 않고 HasChildren을 설정할
// 때 쓴다. 실제 읽기 오류가 발생하면 (false, err)를 반환해 호출자가 로그를
// 남길 수 있게 한다 — 예전에는 IO 실패까지 포함해 모든 에러를 (false, nil)로
// 매핑해 운영자에게 문제를 가렸다.
func dirHasSubdirs(dirAbs string) (bool, error) {
	f, err := os.Open(dirAbs)
	if err != nil {
		return false, err
	}
	defer f.Close()
	// 작은 배치로 읽고 처음 일치하는 항목에서 빠져나간다 — 그래야 파일이
	// 수천 개인 디렉터리가 단순 bool 하나를 위해 전체 스캔되지 않는다.
	for {
		batch, err := f.ReadDir(64)
		for _, e := range batch {
			if strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if e.IsDir() {
				return true, nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
	}
}

// normalizeTreeRel은 rel을 후행 슬래시 없는 슬래시 prefix POSIX 경로로
// 반환한다. 루트는 예외적으로 "/"다.
func normalizeTreeRel(rel string) string {
	if rel == "" || rel == "/" {
		return "/"
	}
	rel = path.Clean("/" + strings.TrimLeft(rel, "/\\"))
	return rel
}

func joinTreeRel(parent, name string) string {
	if parent == "/" {
		return "/" + name
	}
	return parent + "/" + name
}

// nodeName은 트리 노드의 표시 이름을 반환한다. 루트는 빈 문자열이다.
func nodeName(rel string) string {
	r := normalizeTreeRel(rel)
	if r == "/" {
		return ""
	}
	return path.Base(r)
}
