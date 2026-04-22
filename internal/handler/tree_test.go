package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// rawTreeNode mirrors treeNode but uses *json.RawMessage for Children so tests
// can distinguish JSON `null` (not loaded) from `[]` (empty).
type rawTreeNode struct {
	Path        string          `json:"path"`
	Name        string          `json:"name"`
	HasChildren bool            `json:"has_children"`
	Children    json.RawMessage `json:"children"`
}

func getTree(mux *http.ServeMux, query string) (*httptest.ResponseRecorder, rawTreeNode) {
	req := httptest.NewRequest(http.MethodGet, "/api/tree"+query, nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	var node rawTreeNode
	if rw.Code == http.StatusOK {
		json.NewDecoder(rw.Body).Decode(&node)
	}
	return rw, node
}

func decodeChildren(t *testing.T, raw json.RawMessage) []rawTreeNode {
	t.Helper()
	if string(raw) == "null" {
		t.Fatal("children was null, expected []")
	}
	var out []rawTreeNode
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode children: %v", err)
	}
	return out
}

func TestTree_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	rw, node := getTree(mux, "")
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	if node.Path != "/" {
		t.Errorf("path = %q, want /", node.Path)
	}
	if node.Name != "" {
		t.Errorf("name = %q, want empty for root", node.Name)
	}
	if node.HasChildren {
		t.Error("empty root should not report has_children")
	}
	kids := decodeChildren(t, node.Children)
	if len(kids) != 0 {
		t.Errorf("expected 0 children, got %d", len(kids))
	}
}

func TestTree_DefaultsToDepth1(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, "movies", "2024"), 0755)
	os.MkdirAll(filepath.Join(root, "photos"), 0755)

	rw, node := getTree(mux, "")
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	if !node.HasChildren {
		t.Error("root should report has_children")
	}
	kids := decodeChildren(t, node.Children)
	if len(kids) != 2 {
		t.Fatalf("expected 2 top-level folders, got %d", len(kids))
	}
	// alphabetical: movies, photos
	if kids[0].Name != "movies" || kids[1].Name != "photos" {
		t.Errorf("order: %s, %s", kids[0].Name, kids[1].Name)
	}
	// movies has a child but at depth=1 it stays unloaded
	if !kids[0].HasChildren {
		t.Error("movies should report has_children")
	}
	if string(kids[0].Children) != "null" {
		t.Errorf("movies.children = %s, want null at depth=1", kids[0].Children)
	}
	// photos is empty: explicit []
	if kids[1].HasChildren {
		t.Error("photos should not report has_children")
	}
	if string(kids[1].Children) != "[]" {
		t.Errorf("photos.children = %s, want []", kids[1].Children)
	}
}

func TestTree_Depth2LoadsGrandchildren(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, "movies", "2024"), 0755)
	os.MkdirAll(filepath.Join(root, "movies", "2025", "deep"), 0755)

	rw, node := getTree(mux, "?depth=2")
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	kids := decodeChildren(t, node.Children)
	if len(kids) != 1 || kids[0].Name != "movies" {
		t.Fatalf("expected [movies], got %v", kids)
	}
	grand := decodeChildren(t, kids[0].Children)
	if len(grand) != 2 {
		t.Fatalf("expected 2 grandchildren, got %d", len(grand))
	}
	// 2024 (no children) → []
	if grand[0].Name != "2024" || grand[0].HasChildren {
		t.Errorf("grand[0] = %+v", grand[0])
	}
	if string(grand[0].Children) != "[]" {
		t.Errorf("2024.children = %s, want []", grand[0].Children)
	}
	// 2025 has "deep" but depth=2 means we include it but don't load deep
	if grand[1].Name != "2025" || !grand[1].HasChildren {
		t.Errorf("grand[1] = %+v", grand[1])
	}
	if string(grand[1].Children) != "null" {
		t.Errorf("2025.children = %s, want null", grand[1].Children)
	}
}

func TestTree_HiddenAndFilesExcluded(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, ".thumb"), 0755)
	os.MkdirAll(filepath.Join(root, ".hidden"), 0755)
	os.MkdirAll(filepath.Join(root, "real"), 0755)
	os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0644)

	_, node := getTree(mux, "")
	kids := decodeChildren(t, node.Children)
	if len(kids) != 1 || kids[0].Name != "real" {
		t.Errorf("expected [real], got %v", names(kids))
	}
}

func TestTree_CaseInsensitiveSort(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, "Banana"), 0755)
	os.MkdirAll(filepath.Join(root, "apple"), 0755)
	os.MkdirAll(filepath.Join(root, "cherry"), 0755)

	_, node := getTree(mux, "")
	kids := decodeChildren(t, node.Children)
	got := names(kids)
	want := []string{"apple", "Banana", "cherry"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("order: got %v, want %v", got, want)
			break
		}
	}
}

func TestTree_SubpathStartingPoint(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, "movies", "2024"), 0755)
	os.MkdirAll(filepath.Join(root, "movies", "2025"), 0755)

	rw, node := getTree(mux, "?path=/movies")
	if rw.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rw.Code)
	}
	if node.Path != "/movies" || node.Name != "movies" {
		t.Errorf("got path=%q name=%q", node.Path, node.Name)
	}
	kids := decodeChildren(t, node.Children)
	if len(kids) != 2 {
		t.Errorf("expected 2 kids, got %d", len(kids))
	}
	// child paths should be properly nested
	if kids[0].Path != "/movies/2024" {
		t.Errorf("path = %q, want /movies/2024", kids[0].Path)
	}
}

func TestTree_DirHasOnlyFiles(t *testing.T) {
	// has_children should be false when a folder contains only files.
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	os.MkdirAll(filepath.Join(root, "music"), 0755)
	os.WriteFile(filepath.Join(root, "music", "a.mp3"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(root, "music", "b.mp3"), []byte("y"), 0644)

	_, node := getTree(mux, "")
	kids := decodeChildren(t, node.Children)
	if len(kids) != 1 || kids[0].Name != "music" {
		t.Fatalf("got %v", names(kids))
	}
	if kids[0].HasChildren {
		t.Error("music has only files; has_children should be false")
	}
	if string(kids[0].Children) != "[]" {
		t.Errorf("children = %s, want []", kids[0].Children)
	}
}

func TestTree_ErrorCases(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

	t.Run("not found", func(t *testing.T) {
		rw, _ := getTree(mux, "?path=/ghost")
		if rw.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rw.Code)
		}
	})

	t.Run("traversal", func(t *testing.T) {
		rw, _ := getTree(mux, "?path=../../etc")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("path is file", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "afile"), []byte("x"), 0644)
		rw, _ := getTree(mux, "?path=/afile")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("depth zero", func(t *testing.T) {
		rw, _ := getTree(mux, "?depth=0")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("depth too large", func(t *testing.T) {
		rw, _ := getTree(mux, "?depth=6")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("depth non-numeric", func(t *testing.T) {
		rw, _ := getTree(mux, "?depth=abc")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("post not allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/tree", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		if rw.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", rw.Code)
		}
	})
}

func names(nodes []rawTreeNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Name
	}
	return out
}
