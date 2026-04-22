package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/chang/file_server/internal/media"
)

const maxTreeDepth = 5

// treeNode is a folder-only navigation node.
//
//	Children == nil   → not loaded (depth limit reached, more exists)
//	Children == []    → loaded, no children
//	Children == [...] → loaded children
//
// HasChildren is always populated so the UI can render an expand chevron
// without needing to follow up with another request.
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(root)
}

// walkTree returns the immediate child folders of dirAbs. When depth > 1 it
// recurses into each child reducing depth by 1; at depth == 1 each child's
// Children is left as nil (more) or [] (none) per HasChildren.
func walkTree(dirAbs, dirRel string, depth int) ([]treeNode, bool, error) {
	entries, err := os.ReadDir(dirAbs)
	if err != nil {
		return nil, false, err
	}

	dirs := make([]os.DirEntry, 0, len(entries))
	for _, e := range entries {
		// .thumb and any other dotfile/dotdir is server-internal; never expose.
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
				// One unreadable subdir shouldn't fail the whole tree;
				// surface as "no children" with a debug log path.
				node.Children = []treeNode{}
				node.HasChildren = false
			} else {
				node.Children = grand
				node.HasChildren = hasGrand
			}
		} else {
			has, _ := dirHasSubdirs(childAbs)
			node.HasChildren = has
			if has {
				node.Children = nil // not loaded — UI lazy-fetches on expand
			} else {
				node.Children = []treeNode{}
			}
		}
		children = append(children, node)
	}
	return children, true, nil
}

// dirHasSubdirs probes whether dirAbs contains at least one non-hidden
// subdirectory. Used at the depth boundary to set HasChildren without
// loading grandchildren.
func dirHasSubdirs(dirAbs string) (bool, error) {
	f, err := os.Open(dirAbs)
	if err != nil {
		return false, err
	}
	defer f.Close()
	// Read in small batches and bail on the first hit so a directory
	// of thousands of files doesn't trigger a full scan just to set a bool.
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
			return false, nil // io.EOF or end — no subdir found
		}
	}
}

// normalizeTreeRel returns rel as a slash-prefixed POSIX path with no
// trailing slash, except the root which is "/".
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

// nodeName returns the display name for a tree node — empty string for root.
func nodeName(rel string) string {
	r := normalizeTreeRel(rel)
	if r == "/" {
		return ""
	}
	return path.Base(r)
}
