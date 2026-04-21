package handler

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/chang/file_server/internal/thumb"
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

	t.Run("duration_sec from sidecar for video", func(t *testing.T) {
		os.MkdirAll(filepath.Join(root, ".thumb"), 0755)
		thumbJPG := filepath.Join(root, ".thumb", "film.mp4.jpg")
		os.WriteFile(thumbJPG, []byte("fake-thumb"), 0644)
		if err := thumb.WriteDurationSidecar(thumbJPG, 142.5); err != nil {
			t.Fatal(err)
		}

		req := httptest.NewRequest("GET", "/api/browse?path=/", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		var resp browseResponse
		json.NewDecoder(w.Body).Decode(&resp)
		var got *float64
		for _, e := range resp.Entries {
			if e.Name == "film.mp4" {
				got = e.DurationSec
			}
		}
		if got == nil {
			t.Fatal("expected duration_sec for film.mp4, got nil")
		}
		if math.Abs(*got-142.5) > 0.001 {
			t.Errorf("duration_sec = %v, want 142.5", *got)
		}
	})

	t.Run("duration_sec null for image", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/browse?path=/", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		var resp browseResponse
		json.NewDecoder(w.Body).Decode(&resp)
		for _, e := range resp.Entries {
			if e.Name == "photo.jpg" && e.DurationSec != nil {
				t.Errorf("photo.jpg duration_sec = %v, want nil", *e.DurationSec)
			}
		}
	})

	t.Run("duration_sec null for video without thumb", func(t *testing.T) {
		// separate root to avoid cached thumbs from earlier subtests
		r2 := t.TempDir()
		os.WriteFile(filepath.Join(r2, "orphan.mp4"), []byte("vid"), 0644)

		mux2 := http.NewServeMux()
		Register(mux2, r2, r2)

		req := httptest.NewRequest("GET", "/api/browse?path=/", nil)
		w := httptest.NewRecorder()
		mux2.ServeHTTP(w, req)

		var resp browseResponse
		json.NewDecoder(w.Body).Decode(&resp)
		for _, e := range resp.Entries {
			if e.Name == "orphan.mp4" && e.DurationSec != nil {
				t.Errorf("orphan.mp4 duration_sec = %v, want nil", *e.DurationSec)
			}
		}
	})

	t.Run("duration_sec field always present in json", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/browse?path=/", nil)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)

		// Verify the field appears in the raw JSON (even when null) so the
		// frontend can rely on its presence.
		var raw struct {
			Entries []map[string]any `json:"entries"`
		}
		json.NewDecoder(w.Body).Decode(&raw)
		for _, e := range raw.Entries {
			if _, ok := e["duration_sec"]; !ok {
				t.Errorf("entry %v missing duration_sec key", e["name"])
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

func TestLookupVideoDurationBudget(t *testing.T) {
	dir := t.TempDir()
	thumbDir := filepath.Join(dir, ".thumb")
	os.MkdirAll(thumbDir, 0755)

	cachedThumb := filepath.Join(thumbDir, "cached.mp4.jpg")
	os.WriteFile(cachedThumb, []byte("fake-thumb"), 0644)
	if err := thumb.WriteDurationSidecar(cachedThumb, 60.0); err != nil {
		t.Fatal(err)
	}

	uncachedThumb := filepath.Join(thumbDir, "uncached.mp4.jpg")
	os.WriteFile(uncachedThumb, []byte("fake-thumb"), 0644)
	uncachedSrc := filepath.Join(dir, "uncached.mp4")
	os.WriteFile(uncachedSrc, []byte("not a real video"), 0644)

	t.Run("cache hit does not consume budget", func(t *testing.T) {
		budget := 0
		got := lookupVideoDuration(cachedThumb, filepath.Join(dir, "cached.mp4"), &budget)
		if got == nil || *got != 60.0 {
			t.Errorf("expected cached duration 60.0, got %v", got)
		}
		if budget != 0 {
			t.Errorf("budget = %d, want 0 (cache hit must not decrement)", budget)
		}
	})

	t.Run("zero budget skips backfill", func(t *testing.T) {
		budget := 0
		got := lookupVideoDuration(uncachedThumb, uncachedSrc, &budget)
		if got != nil {
			t.Errorf("expected nil with zero budget, got %v", *got)
		}
		if _, err := os.Stat(thumb.DurationSidecarPath(uncachedThumb)); err == nil {
			t.Error("sidecar should not be written when budget is zero")
		}
	})

	t.Run("backfill attempt decrements budget even on failure", func(t *testing.T) {
		budget := 1
		_ = lookupVideoDuration(uncachedThumb, uncachedSrc, &budget)
		if budget != 0 {
			t.Errorf("budget = %d, want 0 (probe attempt must decrement)", budget)
		}
	})
}
