package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// postConvert issues a POST /api/convert with the given paths + delete_original
// flag and returns the recorder. Content-Type is application/json.
func postConvert(t *testing.T, mux *http.ServeMux, paths []string, deleteOriginal bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"paths":           paths,
		"delete_original": deleteOriginal,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/convert", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	return rw
}

// findEvent returns the first SSE event with phase == want, or nil.
func findEvent(events []map[string]any, want string) map[string]any {
	for _, e := range events {
		if p, _ := e["phase"].(string); p == want {
			return e
		}
	}
	return nil
}

// findEventFor returns the first SSE event matching phase + index, or nil.
func findEventFor(events []map[string]any, phase string, index int) map[string]any {
	for _, e := range events {
		if p, _ := e["phase"].(string); p != phase {
			continue
		}
		if idx, ok := e["index"].(float64); ok && int(idx) == index {
			return e
		}
	}
	return nil
}

func TestHandleConvert_MethodNotAllowed(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/convert", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rw.Code)
	}
}

func TestHandleConvert_InvalidJSON(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/convert", strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)

	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
}

func TestHandleConvert_NoPaths(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{}, false)
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "no paths") {
		t.Errorf("body %q missing 'no paths'", rw.Body.String())
	}
}

func TestHandleConvert_TooManyPaths(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	paths := make([]string, 501)
	for i := range paths {
		paths[i] = "/x.ts"
	}
	rw := postConvert(t, mux, paths, false)
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "too many paths") {
		t.Errorf("body %q missing 'too many paths'", rw.Body.String())
	}
}

func TestHandleConvert_PathTraversal(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"../../etc/passwd.ts"}, false)
	if rw.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (SSE); body=%s", rw.Code, rw.Body.String())
	}
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil {
		t.Fatalf("no error event; phases=%v", phasesOf(events))
	}
	if got := e["error"]; got != "invalid_path" {
		t.Errorf("error = %v, want invalid_path", got)
	}
}

func TestHandleConvert_NotFound(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/ghost.ts"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "not_found" {
		t.Errorf("want error not_found; got %v", e)
	}
}

func TestHandleConvert_NotAFile(t *testing.T) {
	root := t.TempDir()
	// Create a directory named like a TS file (still fails IsDir check).
	dir := filepath.Join(root, "clip.ts")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/clip.ts"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "not_a_file" {
		t.Errorf("want error not_a_file; got %v", e)
	}
}

func TestHandleConvert_NotTS(t *testing.T) {
	root := t.TempDir()
	mp4 := filepath.Join(root, "video.mp4")
	if err := os.WriteFile(mp4, []byte("fake mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/video.mp4"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "not_ts" {
		t.Errorf("want error not_ts; got %v", e)
	}
}

func TestHandleConvert_AlreadyExists(t *testing.T) {
	root := t.TempDir()
	// Bypass ffmpeg entirely: both .ts AND .mp4 exist, so the handler must
	// short-circuit with already_exists without ever invoking ffmpeg.
	if err := os.WriteFile(filepath.Join(root, "clip.ts"), []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "clip.mp4"), []byte("mp4"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/clip.ts"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "already_exists" {
		t.Errorf("want error already_exists; got %v", e)
	}
}

func TestHandleConvert_FFmpegMissing(t *testing.T) {
	t.Setenv("PATH", "")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clip.ts"), []byte("ts"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/clip.ts"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "ffmpeg_missing" {
		t.Errorf("want error ffmpeg_missing; got %v", e)
	}
}

func TestHandleConvert_Success(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	ts := makeTestTS(t, root)
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/" + filepath.Base(ts)}, false)
	events := parseSSEEvents(t, rw.Body.String())

	if findEventFor(events, "start", 0) == nil {
		t.Errorf("no start event; phases=%v", phasesOf(events))
	}
	done := findEventFor(events, "done", 0)
	if done == nil {
		t.Fatalf("no done event; phases=%v", phasesOf(events))
	}
	if got := done["name"]; got != "clip.mp4" {
		t.Errorf("done.name = %v, want clip.mp4", got)
	}
	if sz, _ := done["size"].(float64); sz <= 0 {
		t.Errorf("done.size = %v, want > 0", done["size"])
	}
	summary := findEvent(events, "summary")
	if summary == nil {
		t.Fatal("no summary event")
	}
	if s, _ := summary["succeeded"].(float64); s != 1 {
		t.Errorf("succeeded = %v, want 1", summary["succeeded"])
	}
	// Original retained.
	if _, err := os.Stat(ts); err != nil {
		t.Errorf("original TS should remain, got err=%v", err)
	}
	// Converted exists.
	if _, err := os.Stat(filepath.Join(root, "clip.mp4")); err != nil {
		t.Errorf("clip.mp4 missing: %v", err)
	}
}

func TestHandleConvert_CaseInsensitiveTS(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	ts := makeTestTS(t, root)
	// Rename to uppercase extension.
	upper := filepath.Join(root, "CLIP.TS")
	if err := os.Rename(ts, upper); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/CLIP.TS"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	done := findEventFor(events, "done", 0)
	if done == nil {
		t.Fatalf("no done event; phases=%v", phasesOf(events))
	}
	// Output extension is always lowercase .mp4.
	if got := done["name"]; got != "CLIP.mp4" {
		t.Errorf("done.name = %v, want CLIP.mp4", got)
	}
	if _, err := os.Stat(filepath.Join(root, "CLIP.mp4")); err != nil {
		t.Errorf("CLIP.mp4 missing: %v", err)
	}
}

func TestHandleConvert_DeleteOriginal(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	ts := makeTestTS(t, root)
	name := filepath.Base(ts)

	// Pre-populate sidecars so we can verify they're cleaned alongside the
	// original .ts.
	thumbDir := filepath.Join(root, ".thumb")
	if err := os.Mkdir(thumbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jpg := filepath.Join(thumbDir, name+".jpg")
	dur := filepath.Join(thumbDir, name+".jpg.dur")
	for _, p := range []string{jpg, dur} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/" + name}, true)
	events := parseSSEEvents(t, rw.Body.String())
	done := findEventFor(events, "done", 0)
	if done == nil {
		t.Fatalf("no done event; phases=%v", phasesOf(events))
	}
	warns, _ := done["warnings"].([]any)
	for _, w := range warns {
		if w == "delete_original_failed" {
			t.Errorf("unexpected warning delete_original_failed: %v", warns)
		}
	}
	// Original + sidecars gone.
	for _, p := range []string{ts, jpg, dur} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should be deleted, err=%v", p, err)
		}
	}
}

func TestHandleConvert_BatchSequential(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	_ = makeTestTS(t, root)
	_ = makeTestTS(t, root, "clip2.ts")
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/clip.ts", "/clip2.ts"}, false)
	events := parseSSEEvents(t, rw.Body.String())

	// Both done events must exist.
	if findEventFor(events, "done", 0) == nil {
		t.Errorf("no done for index 0; phases=%v", phasesOf(events))
	}
	if findEventFor(events, "done", 1) == nil {
		t.Errorf("no done for index 1; phases=%v", phasesOf(events))
	}
	// Summary succeeded=2.
	s := findEvent(events, "summary")
	if s == nil {
		t.Fatal("no summary")
	}
	if succ, _ := s["succeeded"].(float64); succ != 2 {
		t.Errorf("succeeded=%v, want 2", s["succeeded"])
	}
	// Index 0 must finish before index 1 starts (sequential).
	start0Idx, done0Idx, start1Idx := -1, -1, -1
	for i, e := range events {
		phase, _ := e["phase"].(string)
		idx, _ := e["index"].(float64)
		switch {
		case phase == "start" && int(idx) == 0 && start0Idx < 0:
			start0Idx = i
		case phase == "done" && int(idx) == 0 && done0Idx < 0:
			done0Idx = i
		case phase == "start" && int(idx) == 1 && start1Idx < 0:
			start1Idx = i
		}
	}
	if !(start0Idx < done0Idx && done0Idx < start1Idx) {
		t.Errorf("expected start0 < done0 < start1, got %d,%d,%d", start0Idx, done0Idx, start1Idx)
	}
}

func TestHandleConvert_PartialFailure(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	ts := makeTestTS(t, root)
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/" + filepath.Base(ts), "/missing.ts"}, false)
	events := parseSSEEvents(t, rw.Body.String())

	if findEventFor(events, "done", 0) == nil {
		t.Errorf("index 0 should succeed; phases=%v", phasesOf(events))
	}
	e1 := findEventFor(events, "error", 1)
	if e1 == nil || e1["error"] != "not_found" {
		t.Errorf("index 1 want error not_found; got %v", e1)
	}
	s := findEvent(events, "summary")
	if s == nil {
		t.Fatal("no summary")
	}
	if succ, _ := s["succeeded"].(float64); succ != 1 {
		t.Errorf("succeeded=%v, want 1", s["succeeded"])
	}
	if fail, _ := s["failed"].(float64); fail != 1 {
		t.Errorf("failed=%v, want 1", s["failed"])
	}
}

func TestHandleConvert_ContextCancel(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	ts := makeTestTS(t, root)
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	body, _ := json.Marshal(map[string]any{"paths": []string{"/" + filepath.Base(ts)}})
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/api/convert", bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()

	// Cancel almost immediately; ffmpeg should not complete.
	go func() {
		cancel()
	}()
	mux.ServeHTTP(rw, req)

	// No leftover .convert-*.mp4 in root.
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".convert-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
	// We don't strictly require any particular event shape here — cancel
	// is racy — but the handler must not have left a finalized clip.mp4.
	if _, err := os.Stat(filepath.Join(root, "clip.mp4")); err == nil {
		// If the cancel lost the race and ffmpeg finished, that's acceptable.
		// The key anti-regression is temp-file cleanup above.
		t.Log("cancel lost the race; ffmpeg finished before cancel took effect")
	}
}

// Sanity: the SSE headers match the import-URL convention.
func TestHandleConvert_SSEHeaders(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "clip.ts"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	rw := postConvert(t, mux, []string{"/clip.ts"}, false)
	if rw.Code != http.StatusOK {
		t.Fatalf("status=%d, body=%s", rw.Code, rw.Body.String())
	}
	if got := rw.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type=%q, want text/event-stream", got)
	}
	if got := rw.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control=%q, want no-cache", got)
	}
	if got := rw.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering=%q, want no", got)
	}
}

// Kept so the file compiles even when no test reads the body stream type.
var _ = io.Discard
