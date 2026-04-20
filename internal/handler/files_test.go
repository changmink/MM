package handler

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUpload(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

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
	Register(mux, root, root)

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
	Register(mux, root, root)

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
	Register(mux, root, root)

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
	Register(mux, root, root)

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
	Register(mux, root, root)

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
	Register(mux, root, root)

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

func TestDelete(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root)

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
