package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowse(t *testing.T) {
	root := t.TempDir()

	// 테스트 파일 구조 생성
	os.MkdirAll(filepath.Join(root, "subdir"), 0755)
	os.WriteFile(filepath.Join(root, "photo.jpg"), []byte("img"), 0644)
	os.WriteFile(filepath.Join(root, "film.mp4"), []byte("vid"), 0644)
	os.WriteFile(filepath.Join(root, "song.mp3"), []byte("aud"), 0644)
	os.MkdirAll(filepath.Join(root, ".thumb"), 0755) // 숨겨져야 함

	mux := http.NewServeMux()
	Register(mux, root, root)

	t.Run("root listing", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/browse?path=/", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}

		var resp browseResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatal(err)
		}

		// .thumb 디렉토리는 숨겨져야 함
		for _, e := range resp.Entries {
			if e.Name == ".thumb" {
				t.Error(".thumb should be hidden from listing")
			}
		}

		// 파일 타입 확인
		types := map[string]string{}
		for _, e := range resp.Entries {
			types[e.Name] = string(e.Type)
		}
		if types["photo.jpg"] != "image" {
			t.Errorf("photo.jpg type = %q, want image", types["photo.jpg"])
		}
		if types["film.mp4"] != "video" {
			t.Errorf("film.mp4 type = %q, want video", types["film.mp4"])
		}
		if types["song.mp3"] != "audio" {
			t.Errorf("song.mp3 type = %q, want audio", types["song.mp3"])
		}
		if types["subdir"] != "dir" {
			t.Errorf("subdir type = %q, want dir", types["subdir"])
		}
	})

	t.Run("thumb_available true for image with cached thumb", func(t *testing.T) {
		os.MkdirAll(filepath.Join(root, ".thumb"), 0755)
		os.WriteFile(filepath.Join(root, ".thumb", "photo.jpg.jpg"), []byte("fake-thumb"), 0644)

		req := httptest.NewRequest("GET", "/api/browse?path=/", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		var resp browseResponse
		json.NewDecoder(w.Body).Decode(&resp)
		for _, e := range resp.Entries {
			if e.Name == "photo.jpg" && !e.ThumbAvailable {
				t.Error("photo.jpg should have thumb_available=true")
			}
		}
	})

	t.Run("thumb_available true for video with cached thumb", func(t *testing.T) {
		os.MkdirAll(filepath.Join(root, ".thumb"), 0755)
		os.WriteFile(filepath.Join(root, ".thumb", "film.mp4.jpg"), []byte("fake-thumb"), 0644)

		req := httptest.NewRequest("GET", "/api/browse?path=/", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		var resp browseResponse
		json.NewDecoder(w.Body).Decode(&resp)
		for _, e := range resp.Entries {
			if e.Name == "film.mp4" && !e.ThumbAvailable {
				t.Error("film.mp4 should have thumb_available=true when .thumb cache exists")
			}
		}
	})

	t.Run("path traversal blocked", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/browse?path=../../etc/passwd", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", w.Code)
		}
	})

	t.Run("not found", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/browse?path=/nonexistent", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("expected 404, got %d", w.Code)
		}
	})
}
