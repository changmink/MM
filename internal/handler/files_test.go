package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestUpload(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	t.Run("upload text file", func(t *testing.T) {
		body := &bytes.Buffer{}
		w := multipart.NewWriter(body)
		fw, _ := w.CreateFormFile("file", "hello.txt")
		fw.Write([]byte("hello world"))
		w.Close()

		req := httptest.NewRequest("POST", "/api/upload?path=/", body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rw.Code, rw.Body.String())
		}

		var resp map[string]interface{}
		json.NewDecoder(rw.Body).Decode(&resp)
		if resp["name"] != "hello.txt" {
			t.Errorf("name = %v, want hello.txt", resp["name"])
		}

		// file should exist
		if _, err := os.Stat(filepath.Join(root, "hello.txt")); err != nil {
			t.Error("file not found on disk")
		}
	})

	t.Run("traversal blocked", func(t *testing.T) {
		body := &bytes.Buffer{}
		w := multipart.NewWriter(body)
		fw, _ := w.CreateFormFile("file", "evil.txt")
		fw.Write([]byte("evil"))
		w.Close()

		req := httptest.NewRequest("POST", "/api/upload?path=../../etc", body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})
}

func TestUploadResponseFields(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	upload := func(filename string, content []byte) *httptest.ResponseRecorder {
		body := &bytes.Buffer{}
		w := multipart.NewWriter(body)
		fw, _ := w.CreateFormFile("file", filename)
		fw.Write(content)
		w.Close()
		req := httptest.NewRequest("POST", "/api/upload?path=/", body)
		req.Header.Set("Content-Type", w.FormDataContentType())
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		return rw
	}

	t.Run("video upload returns correct type", func(t *testing.T) {
		rw := upload("clip.mp4", []byte("fake video"))
		if rw.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d", rw.Code)
		}
		var resp map[string]interface{}
		json.NewDecoder(rw.Body).Decode(&resp)
		if resp["type"] != "video" {
			t.Errorf("type = %v, want video", resp["type"])
		}
		if resp["size"].(float64) != 10 {
			t.Errorf("size = %v, want 10", resp["size"])
		}
	})

	t.Run("audio upload returns correct type", func(t *testing.T) {
		rw := upload("song.mp3", []byte("fake audio"))
		var resp map[string]interface{}
		json.NewDecoder(rw.Body).Decode(&resp)
		if resp["type"] != "audio" {
			t.Errorf("type = %v, want audio", resp["type"])
		}
	})
}

func TestDeleteImageCleansThumbnail(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	// create image and its thumbnail
	thumbDir := filepath.Join(root, ".thumb")
	os.MkdirAll(thumbDir, 0755)
	os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("img data"), 0644)
	os.WriteFile(filepath.Join(thumbDir, "photo.jpg.jpg"), []byte("thumb data"), 0644)

	req := httptest.NewRequest("DELETE", "/api/file?path=/photo.jpg", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rw.Code)
	}
	if _, err := os.Stat(filepath.Join(thumbDir, "photo.jpg.jpg")); !os.IsNotExist(err) {
		t.Error("thumbnail should be deleted along with the image")
	}
}

func TestFolderMethodNotAllowed(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	req := httptest.NewRequest("GET", "/api/folder?path=/", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rw.Code)
	}
}

func TestCreateFolder(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	jsonBody := func(name string) *bytes.Buffer {
		b := &bytes.Buffer{}
		json.NewEncoder(b).Encode(map[string]string{"name": name})
		return b
	}
	post := func(path, name string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/folder?path="+path, jsonBody(name))
		req.Header.Set("Content-Type", "application/json")
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		return rw
	}

	t.Run("creates folder", func(t *testing.T) {
		rw := post("/", "myfolder")
		if rw.Code != http.StatusCreated {
			t.Fatalf("expected 201, got %d: %s", rw.Code, rw.Body.String())
		}
		if _, err := os.Stat(filepath.Join(root, "myfolder")); err != nil {
			t.Error("folder not found on disk")
		}
		var resp map[string]string
		json.NewDecoder(rw.Body).Decode(&resp)
		if resp["path"] == "" {
			t.Error("response missing path field")
		}
	})

	t.Run("already exists returns 409", func(t *testing.T) {
		os.Mkdir(filepath.Join(root, "existing"), 0755)
		rw := post("/", "existing")
		if rw.Code != http.StatusConflict {
			t.Errorf("expected 409, got %d", rw.Code)
		}
	})

	t.Run("empty name returns 400", func(t *testing.T) {
		rw := post("/", "")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("slash in name returns 400", func(t *testing.T) {
		rw := post("/", "a/b")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("dot name returns 400", func(t *testing.T) {
		rw := post("/", ".")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("dotdot name returns 400", func(t *testing.T) {
		rw := post("/", "..")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("traversal in path returns 400", func(t *testing.T) {
		rw := post("../../etc", "evil")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})
}

func TestDeleteFolder(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	del := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("DELETE", "/api/folder?path="+path, nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		return rw
	}

	t.Run("recursive delete with contents", func(t *testing.T) {
		dir := filepath.Join(root, "todel")
		os.MkdirAll(filepath.Join(dir, ".thumb"), 0755)
		os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hi"), 0644)
		os.WriteFile(filepath.Join(dir, ".thumb", "file.txt.jpg"), []byte("thumb"), 0644)

		rw := del("/todel")
		if rw.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d: %s", rw.Code, rw.Body.String())
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Error("folder should be deleted")
		}
	})

	t.Run("nonexistent returns 404", func(t *testing.T) {
		rw := del("/ghost")
		if rw.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rw.Code)
		}
	})

	t.Run("file path returns 400", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "afile.txt"), []byte("x"), 0644)
		rw := del("/afile.txt")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("traversal returns 400", func(t *testing.T) {
		rw := del("../../etc")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("root path returns 400", func(t *testing.T) {
		rw := del("/")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for root deletion, got %d", rw.Code)
		}
	})
}

func TestCreateFolderResponsePath(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	body := bytes.NewBufferString(`{"name":"alpha"}`)
	req := httptest.NewRequest("POST", "/api/folder?path=/", body)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d", rw.Code)
	}
	var resp map[string]string
	json.NewDecoder(rw.Body).Decode(&resp)
	if resp["path"] != "/alpha" {
		t.Errorf("path = %q, want /alpha", resp["path"])
	}
}

// Regression: prior implementation called os.Stat then os.Create in two steps.
// N goroutines could observe the same free name, and N-1 uploads would clobber
// each other. createUniqueFile now uses O_CREATE|O_EXCL so each concurrent
// upload of the same filename lands in a distinct path.
func TestConcurrentUploadSameNameNoClobber(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	const n = 20
	var wg sync.WaitGroup
	results := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			body := &bytes.Buffer{}
			w := multipart.NewWriter(body)
			fw, _ := w.CreateFormFile("file", "race.txt")
			fmt.Fprintf(fw, "writer-%d", idx)
			w.Close()
			req := httptest.NewRequest("POST", "/api/upload?path=/", body)
			req.Header.Set("Content-Type", w.FormDataContentType())
			rw := httptest.NewRecorder()
			mux.ServeHTTP(rw, req)
			if rw.Code != http.StatusCreated {
				t.Errorf("worker %d: expected 201, got %d", idx, rw.Code)
				return
			}
			var resp map[string]interface{}
			json.NewDecoder(rw.Body).Decode(&resp)
			results[idx] = resp["name"].(string)
		}(i)
	}
	wg.Wait()

	seen := map[string]bool{}
	for _, name := range results {
		if name == "" {
			continue
		}
		if seen[name] {
			t.Fatalf("duplicate filename %q returned to multiple uploaders — clobber occurred", name)
		}
		seen[name] = true
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Errorf("file %q missing on disk: %v", name, err)
		}
	}
	if len(seen) != n {
		t.Errorf("expected %d distinct files, got %d", n, len(seen))
	}
}

func TestRenameFile(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	jsonBody := func(name string) *bytes.Buffer {
		b := &bytes.Buffer{}
		json.NewEncoder(b).Encode(map[string]string{"name": name})
		return b
	}
	patch := func(path, name string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", "/api/file?path="+path, jsonBody(name))
		req.Header.Set("Content-Type", "application/json")
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		return rw
	}

	t.Run("rename success", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "old.txt"), []byte("hi"), 0644)
		rw := patch("/old.txt", "new")
		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
		}
		var resp map[string]string
		json.NewDecoder(rw.Body).Decode(&resp)
		if resp["name"] != "new.txt" {
			t.Errorf("name = %q, want new.txt", resp["name"])
		}
		if resp["path"] != "/new.txt" {
			t.Errorf("path = %q, want /new.txt", resp["path"])
		}
		if _, err := os.Stat(filepath.Join(root, "old.txt")); !os.IsNotExist(err) {
			t.Error("old file should be gone")
		}
		if _, err := os.Stat(filepath.Join(root, "new.txt")); err != nil {
			t.Errorf("new file missing: %v", err)
		}
	})

	t.Run("user-supplied extension is stripped and original kept", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("v"), 0644)
		rw := patch("/clip.mp4", "movie.mkv")
		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
		}
		var resp map[string]string
		json.NewDecoder(rw.Body).Decode(&resp)
		if resp["name"] != "movie.mp4" {
			t.Errorf("name = %q, want movie.mp4 (original ext preserved)", resp["name"])
		}
		if _, err := os.Stat(filepath.Join(root, "movie.mp4")); err != nil {
			t.Errorf("new file missing: %v", err)
		}
	})

	t.Run("image thumbnail follows rename", func(t *testing.T) {
		sub := filepath.Join(root, "imgdir")
		thumbDir := filepath.Join(sub, ".thumb")
		os.MkdirAll(thumbDir, 0755)
		os.WriteFile(filepath.Join(sub, "a.jpg"), []byte("img"), 0644)
		os.WriteFile(filepath.Join(thumbDir, "a.jpg.jpg"), []byte("thumb"), 0644)

		rw := patch("/imgdir/a.jpg", "b")
		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
		}
		if _, err := os.Stat(filepath.Join(thumbDir, "a.jpg.jpg")); !os.IsNotExist(err) {
			t.Error("old thumb should be gone")
		}
		if _, err := os.Stat(filepath.Join(thumbDir, "b.jpg.jpg")); err != nil {
			t.Errorf("new thumb missing: %v", err)
		}
	})

	t.Run("video duration sidecar follows rename", func(t *testing.T) {
		sub := filepath.Join(root, "viddir")
		thumbDir := filepath.Join(sub, ".thumb")
		os.MkdirAll(thumbDir, 0755)
		os.WriteFile(filepath.Join(sub, "x.mp4"), []byte("v"), 0644)
		os.WriteFile(filepath.Join(thumbDir, "x.mp4.jpg"), []byte("thumb"), 0644)
		os.WriteFile(filepath.Join(thumbDir, "x.mp4.jpg.dur"), []byte("273.456"), 0644)

		rw := patch("/viddir/x.mp4", "y")
		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
		}
		if _, err := os.Stat(filepath.Join(thumbDir, "y.mp4.jpg")); err != nil {
			t.Errorf("new thumb missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(thumbDir, "y.mp4.jpg.dur")); err != nil {
			t.Errorf("new duration sidecar missing: %v", err)
		}
		if _, err := os.Stat(filepath.Join(thumbDir, "x.mp4.jpg.dur")); !os.IsNotExist(err) {
			t.Error("old duration sidecar should be gone")
		}
	})

	t.Run("missing sidecar is not an error", func(t *testing.T) {
		sub := filepath.Join(root, "nothumb")
		os.MkdirAll(sub, 0755)
		os.WriteFile(filepath.Join(sub, "orig.jpg"), []byte("img"), 0644)

		rw := patch("/nothumb/orig.jpg", "renamed")
		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
		}
	})

	t.Run("conflict returns 409", func(t *testing.T) {
		sub := filepath.Join(root, "conf")
		os.MkdirAll(sub, 0755)
		os.WriteFile(filepath.Join(sub, "a.txt"), []byte("a"), 0644)
		os.WriteFile(filepath.Join(sub, "b.txt"), []byte("b"), 0644)
		rw := patch("/conf/a.txt", "b")
		if rw.Code != http.StatusConflict {
			t.Errorf("expected 409, got %d", rw.Code)
		}
	})

	t.Run("name unchanged returns 400", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "same.txt"), []byte("x"), 0644)
		rw := patch("/same.txt", "same")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rw.Code, rw.Body.String())
		}
	})

	t.Run("empty name returns 400", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "named.txt"), []byte("x"), 0644)
		rw := patch("/named.txt", "")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("slash in name returns 400", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "hasslash.txt"), []byte("x"), 0644)
		rw := patch("/hasslash.txt", "a/b")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("dotdot in name returns 400", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "hasdotdot.txt"), []byte("x"), 0644)
		rw := patch("/hasdotdot.txt", "..")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	t.Run("nonexistent returns 404", func(t *testing.T) {
		rw := patch("/ghost.txt", "renamed")
		if rw.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rw.Code)
		}
	})

	t.Run("directory returns 400", func(t *testing.T) {
		os.MkdirAll(filepath.Join(root, "adir"), 0755)
		rw := patch("/adir", "newdir")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rw.Code, rw.Body.String())
		}
	})

	t.Run("traversal in path returns 400", func(t *testing.T) {
		rw := patch("../../etc/passwd", "pwned")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})

	// Phase 9.1 hardening — dotfile, case, length, empty-after-strip

	t.Run("dotfile rename preserves intent (no ext auto-reattach)", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, ".gitignore"), []byte("x"), 0644)
		rw := patch("/.gitignore", "config")
		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
		}
		var resp map[string]string
		json.NewDecoder(rw.Body).Decode(&resp)
		if resp["name"] != "config" {
			t.Errorf("name = %q, want config (no .gitignore reattachment)", resp["name"])
		}
		if _, err := os.Stat(filepath.Join(root, "config")); err != nil {
			t.Errorf("new file missing: %v", err)
		}
	})

	t.Run("case-only rename succeeds", func(t *testing.T) {
		sub := filepath.Join(root, "casesub")
		os.MkdirAll(sub, 0755)
		os.WriteFile(filepath.Join(sub, "readme.md"), []byte("x"), 0644)
		rw := patch("/casesub/readme.md", "README")
		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
		}
		// On case-sensitive FS both names coexist; on case-insensitive, casing changes.
		// The important invariant: no 409 was returned for a case-only change.
	})

	t.Run("stripped-to-empty input returns 400", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "keep.mp4"), []byte("x"), 0644)
		rw := patch("/keep.mp4", ".mp4")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rw.Code, rw.Body.String())
		}
	})

	t.Run("length overflow after reattaching extension returns 400", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "lg.webp"), []byte("x"), 0644)
		rw := patch("/lg.webp", strings.Repeat("a", 253)) // 253 + ".webp"(5) = 258 > 255
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rw.Code, rw.Body.String())
		}
	})
}

func TestRenameFolder(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	jsonBody := func(name string) *bytes.Buffer {
		b := &bytes.Buffer{}
		json.NewEncoder(b).Encode(map[string]string{"name": name})
		return b
	}
	patch := func(path, name string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PATCH", "/api/folder?path="+path, jsonBody(name))
		req.Header.Set("Content-Type", "application/json")
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)
		return rw
	}

	t.Run("rename folder with contents", func(t *testing.T) {
		dir := filepath.Join(root, "movies")
		thumbDir := filepath.Join(dir, ".thumb")
		os.MkdirAll(thumbDir, 0755)
		os.WriteFile(filepath.Join(dir, "film.mp4"), []byte("v"), 0644)
		os.WriteFile(filepath.Join(thumbDir, "film.mp4.jpg"), []byte("t"), 0644)

		rw := patch("/movies", "films")
		if rw.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d: %s", rw.Code, rw.Body.String())
		}
		var resp map[string]string
		json.NewDecoder(rw.Body).Decode(&resp)
		if resp["path"] != "/films" {
			t.Errorf("path = %q, want /films", resp["path"])
		}
		if resp["name"] != "films" {
			t.Errorf("name = %q, want films", resp["name"])
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Error("old folder should be gone")
		}
		if _, err := os.Stat(filepath.Join(root, "films", "film.mp4")); err != nil {
			t.Errorf("inner file missing after rename: %v", err)
		}
		if _, err := os.Stat(filepath.Join(root, "films", ".thumb", "film.mp4.jpg")); err != nil {
			t.Errorf("thumb dir not carried with folder: %v", err)
		}
	})

	t.Run("conflict returns 409", func(t *testing.T) {
		os.MkdirAll(filepath.Join(root, "a"), 0755)
		os.MkdirAll(filepath.Join(root, "b"), 0755)
		rw := patch("/a", "b")
		if rw.Code != http.StatusConflict {
			t.Errorf("expected 409, got %d", rw.Code)
		}
	})

	t.Run("nonexistent returns 404", func(t *testing.T) {
		rw := patch("/ghost-folder", "renamed")
		if rw.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rw.Code)
		}
	})

	t.Run("file path returns 400 not a directory", func(t *testing.T) {
		os.WriteFile(filepath.Join(root, "actually-a-file.txt"), []byte("x"), 0644)
		rw := patch("/actually-a-file.txt", "renamed")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rw.Code, rw.Body.String())
		}
	})

	t.Run("root rename is rejected", func(t *testing.T) {
		rw := patch("/", "anything")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400 for root rename, got %d", rw.Code)
		}
	})

	t.Run("invalid name returns 400", func(t *testing.T) {
		os.MkdirAll(filepath.Join(root, "tobe"), 0755)
		for _, name := range []string{"", ".", "..", "a/b"} {
			rw := patch("/tobe", name)
			if rw.Code != http.StatusBadRequest {
				t.Errorf("name %q: expected 400, got %d", name, rw.Code)
			}
		}
	})

	t.Run("name unchanged returns 400", func(t *testing.T) {
		os.MkdirAll(filepath.Join(root, "samename"), 0755)
		rw := patch("/samename", "samename")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d: %s", rw.Code, rw.Body.String())
		}
	})

	t.Run("traversal returns 400", func(t *testing.T) {
		rw := patch("../../etc", "evil")
		if rw.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", rw.Code)
		}
	})
}

func TestDelete(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	// create a file to delete
	os.WriteFile(filepath.Join(root, "todelete.txt"), []byte("bye"), 0644)

	t.Run("delete existing file", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/file?path=/todelete.txt", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusNoContent {
			t.Fatalf("expected 204, got %d", rw.Code)
		}
		if _, err := os.Stat(filepath.Join(root, "todelete.txt")); !os.IsNotExist(err) {
			t.Error("file should be deleted")
		}
	})

	t.Run("delete nonexistent", func(t *testing.T) {
		req := httptest.NewRequest("DELETE", "/api/file?path=/ghost.txt", nil)
		rw := httptest.NewRecorder()
		mux.ServeHTTP(rw, req)

		if rw.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", rw.Code)
		}
	})
}
