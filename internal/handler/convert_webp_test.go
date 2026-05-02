package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// postConvertWebP issues a POST /api/convert-webp with the given paths +
// delete_original flag and returns the recorder.
func postConvertWebP(t *testing.T, mux *http.ServeMux, paths []string, deleteOriginal bool) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"paths":           paths,
		"delete_original": deleteOriginal,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/convert-webp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	return rw
}

// makeWebPTestMP4 synthesizes a 1-second 64x64 H.264 mp4 fixture. With audio:
// silent AAC stereo track. yuv420p forced for libwebp compatibility.
func makeWebPTestMP4(t *testing.T, dir, name string, withAudio bool) string {
	t.Helper()
	requireFFmpeg(t)
	out := filepath.Join(dir, name)
	args := []string{"-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=black:size=64x64:rate=24"}
	if withAudio {
		args = append(args, "-f", "lavfi", "-i", "anullsrc=r=44100:cl=stereo")
	}
	args = append(args, "-t", "1",
		"-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
		"-pix_fmt", "yuv420p")
	if withAudio {
		args = append(args, "-c:a", "aac", "-b:a", "64k", "-shortest")
	}
	args = append(args, out)
	if err := exec.Command("ffmpeg", args...).Run(); err != nil {
		t.Fatalf("makeWebPTestMP4: %v", err)
	}
	return out
}

// makeWebPTestGIF synthesizes a small 1-second animated GIF.
func makeWebPTestGIF(t *testing.T, dir, name string) string {
	t.Helper()
	requireFFmpeg(t)
	out := filepath.Join(dir, name)
	cmd := exec.Command("ffmpeg", "-y", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=red:size=32x32:rate=10",
		"-t", "1",
		out,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("makeWebPTestGIF: %v", err)
	}
	return out
}

// writeDurationSidecar pre-populates <root>/.thumb/<name>.jpg.dur with sec.
// Used to drive the eligibility gate without depending on ffprobe shape.
// Format matches thumb.WriteDurationSidecar — strconv.FormatFloat with 'f'
// and 3 decimals so thumb.ReadDurationSidecar parses it back.
func writeDurationSidecar(t *testing.T, root, name string, sec float64) {
	t.Helper()
	thumbDir := filepath.Join(root, ".thumb")
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(thumbDir, name+".jpg.dur")
	data := []byte(strconv.FormatFloat(sec, 'f', 3, 64))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestConvertWebP_MethodNotAllowed(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/convert-webp", nil)
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rw.Code)
	}
}

func TestConvertWebP_InvalidJSON(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/convert-webp", strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
}

func TestConvertWebP_NoPaths(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	rw := postConvertWebP(t, mux, []string{}, false)
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "no paths") {
		t.Errorf("body = %q, want 'no paths'", rw.Body.String())
	}
}

func TestConvertWebP_TooManyPaths(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	paths := make([]string, maxConvertWebPPaths+1)
	for i := range paths {
		paths[i] = "/x.mp4"
	}
	rw := postConvertWebP(t, mux, paths, false)
	if rw.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rw.Code)
	}
	if !strings.Contains(rw.Body.String(), "too many paths") {
		t.Errorf("body = %q, want 'too many paths'", rw.Body.String())
	}
}

func TestConvertWebP_CrossOriginRejected(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	body, _ := json.Marshal(map[string]any{"paths": []string{"/x.mp4"}})
	req := httptest.NewRequest(http.MethodPost, "/api/convert-webp", bytes.NewReader(body))
	req.Header.Set("Origin", "http://evil.example")
	req.Host = "localhost:8080"
	req.Header.Set("Content-Type", "application/json")
	rw := httptest.NewRecorder()
	mux.ServeHTTP(rw, req)
	if rw.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rw.Code)
	}
}

func TestConvertWebP_Traversal(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	rw := postConvertWebP(t, mux, []string{"../../etc/passwd"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "invalid_path" {
		t.Errorf("want error invalid_path; got %v", e)
	}
}

func TestConvertWebP_NotFound(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	rw := postConvertWebP(t, mux, []string{"/missing.mp4"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "not_found" {
		t.Errorf("want error not_found; got %v", e)
	}
}

func TestConvertWebP_UnsupportedInput(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	if err := os.WriteFile(filepath.Join(root, "photo.png"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	rw := postConvertWebP(t, mux, []string{"/photo.png"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "unsupported_input" {
		t.Errorf("want error unsupported_input; got %v", e)
	}
}

func TestConvertWebP_NotClipTooBig(t *testing.T) {
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	// Sparse 60 MiB mp4 — size check fails before any encoding.
	big := filepath.Join(root, "big.mp4")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(60 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	f.Close()
	rw := postConvertWebP(t, mux, []string{"/big.mp4"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "not_clip" {
		t.Errorf("want error not_clip; got %v", e)
	}
	// Output webp must not exist.
	if _, err := os.Stat(filepath.Join(root, "big.webp")); err == nil {
		t.Error("big.webp should not exist after not_clip")
	}
}

func TestConvertWebP_NotClipTooLong(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	src := makeWebPTestMP4(t, root, "long.mp4", false)
	// Pre-populate duration sidecar with 35s — exceeds 30s gate.
	writeDurationSidecar(t, root, filepath.Base(src), 35.0)
	rw := postConvertWebP(t, mux, []string{"/long.mp4"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "not_clip" {
		t.Errorf("want error not_clip; got %v", e)
	}
}

func TestConvertWebP_DurationUnknown(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	// Empty .mp4 — stat ok, ffprobe fails to read duration, no sidecar.
	empty := filepath.Join(root, "empty.mp4")
	if err := os.WriteFile(empty, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	rw := postConvertWebP(t, mux, []string{"/empty.mp4"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "duration_unknown" {
		t.Errorf("want error duration_unknown; got %v", e)
	}
}

func TestConvertWebP_HappyPath_MP4(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	src := makeWebPTestMP4(t, root, "clip.mp4", false)

	rw := postConvertWebP(t, mux, []string{"/" + filepath.Base(src)}, false)
	events := parseSSEEvents(t, rw.Body.String())

	if findEventFor(events, "start", 0) == nil {
		t.Errorf("no start event; phases=%v", phasesOf(events))
	}
	done := findEventFor(events, "done", 0)
	if done == nil {
		t.Fatalf("no done event; phases=%v", phasesOf(events))
	}
	if done["name"] != "clip.webp" {
		t.Errorf("done.name = %v, want clip.webp", done["name"])
	}
	if done["type"] != "image" {
		t.Errorf("done.type = %v, want image", done["type"])
	}
	if sz, _ := done["size"].(float64); sz <= 0 {
		t.Errorf("done.size = %v, want > 0", done["size"])
	}
	// Original retained, output exists, no audio_dropped (silent input).
	if _, err := os.Stat(src); err != nil {
		t.Errorf("original mp4 should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "clip.webp")); err != nil {
		t.Errorf("clip.webp missing: %v", err)
	}
	if warns, _ := done["warnings"].([]any); len(warns) != 0 {
		t.Errorf("done.warnings = %v, want []", warns)
	}
}

func TestConvertWebP_HappyPath_GIF(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	src := makeWebPTestGIF(t, root, "clip.gif")

	rw := postConvertWebP(t, mux, []string{"/" + filepath.Base(src)}, false)
	events := parseSSEEvents(t, rw.Body.String())
	done := findEventFor(events, "done", 0)
	if done == nil {
		t.Fatalf("no done event; phases=%v", phasesOf(events))
	}
	if done["name"] != "clip.webp" {
		t.Errorf("done.name = %v, want clip.webp", done["name"])
	}
	if done["type"] != "image" {
		t.Errorf("done.type = %v, want image", done["type"])
	}
	// GIFs never produce audio_dropped warning regardless of source.
	if warns, _ := done["warnings"].([]any); len(warns) != 0 {
		t.Errorf("GIF done.warnings = %v, want []", warns)
	}
}

func TestConvertWebP_AudioDroppedWarning(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	src := makeWebPTestMP4(t, root, "audio.mp4", true /* withAudio */)

	rw := postConvertWebP(t, mux, []string{"/" + filepath.Base(src)}, false)
	events := parseSSEEvents(t, rw.Body.String())
	done := findEventFor(events, "done", 0)
	if done == nil {
		t.Fatalf("no done event; phases=%v", phasesOf(events))
	}
	warns, _ := done["warnings"].([]any)
	found := false
	for _, w := range warns {
		if s, _ := w.(string); s == "audio_dropped" {
			found = true
		}
	}
	if !found {
		t.Errorf("done.warnings = %v, want to contain audio_dropped", warns)
	}
}

func TestConvertWebP_AlreadyExists(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	src := makeWebPTestMP4(t, root, "clip.mp4", false)
	// Pre-existing destination.
	if err := os.WriteFile(filepath.Join(root, "clip.webp"), []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	rw := postConvertWebP(t, mux, []string{"/" + filepath.Base(src)}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "already_exists" {
		t.Errorf("want error already_exists; got %v", e)
	}
	// Existing webp untouched.
	data, err := os.ReadFile(filepath.Join(root, "clip.webp"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing" {
		t.Errorf("existing webp was overwritten")
	}
}

func TestConvertWebP_DeleteOriginal_Video(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	src := makeWebPTestMP4(t, root, "clip.mp4", false)
	name := filepath.Base(src)

	// Pre-populate sidecars to verify they're cleaned alongside the source.
	thumbDir := filepath.Join(root, ".thumb")
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jpg := filepath.Join(thumbDir, name+".jpg")
	dur := filepath.Join(thumbDir, name+".jpg.dur")
	for _, p := range []string{jpg, dur} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	rw := postConvertWebP(t, mux, []string{"/" + name}, true)
	events := parseSSEEvents(t, rw.Body.String())
	if findEventFor(events, "done", 0) == nil {
		t.Fatalf("no done event; phases=%v", phasesOf(events))
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("original mp4 should be deleted, got err=%v", err)
	}
	for _, p := range []string{jpg, dur} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("sidecar %s should be deleted, got err=%v", filepath.Base(p), err)
		}
	}
}

func TestConvertWebP_DeleteOriginal_GIF(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	src := makeWebPTestGIF(t, root, "clip.gif")
	name := filepath.Base(src)

	thumbDir := filepath.Join(root, ".thumb")
	if err := os.MkdirAll(thumbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jpg := filepath.Join(thumbDir, name+".jpg")
	if err := os.WriteFile(jpg, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	rw := postConvertWebP(t, mux, []string{"/" + name}, true)
	events := parseSSEEvents(t, rw.Body.String())
	if findEventFor(events, "done", 0) == nil {
		t.Fatalf("no done event; phases=%v", phasesOf(events))
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("original gif should be deleted")
	}
	if _, err := os.Stat(jpg); !os.IsNotExist(err) {
		t.Errorf("sidecar .jpg should be deleted")
	}
}

func TestConvertWebP_DeleteOriginalFailedWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file traversal semantics differ on Windows")
	}
	requireFFmpeg(t)
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	src := makeWebPTestMP4(t, root, "clip.mp4", false)
	// Plant a regular file at <root>/.thumb so the sidecar Remove call
	// traverses through a non-directory and returns ENOTDIR, which is
	// not IsNotExist — triggering the delete_original_failed warning.
	// Chmod-based read-only tricks don't work under root in containers.
	if err := os.WriteFile(filepath.Join(root, ".thumb"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	rw := postConvertWebP(t, mux, []string{"/" + filepath.Base(src)}, true)
	events := parseSSEEvents(t, rw.Body.String())
	done := findEventFor(events, "done", 0)
	if done == nil {
		t.Fatalf("no done event; phases=%v", phasesOf(events))
	}
	warns, _ := done["warnings"].([]any)
	found := false
	for _, w := range warns {
		if s, _ := w.(string); s == "delete_original_failed" {
			found = true
		}
	}
	if !found {
		t.Errorf("done.warnings = %v, want to contain delete_original_failed", warns)
	}
}

func TestConvertWebP_PartialFailure(t *testing.T) {
	requireFFmpeg(t)
	root := t.TempDir()
	mux := http.NewServeMux()
	Register(mux, root, root, nil)
	good := makeWebPTestMP4(t, root, "good.mp4", false)
	// Sparse 60 MiB mp4 → not_clip on size.
	big := filepath.Join(root, "big.mp4")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(60 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	f.Close()

	rw := postConvertWebP(t, mux,
		[]string{"/" + filepath.Base(good), "/big.mp4"}, false)
	events := parseSSEEvents(t, rw.Body.String())

	if findEventFor(events, "done", 0) == nil {
		t.Errorf("index 0 should have done event; phases=%v", phasesOf(events))
	}
	e := findEventFor(events, "error", 1)
	if e == nil || e["error"] != "not_clip" {
		t.Errorf("index 1 want not_clip; got %v", e)
	}
	summary := findEvent(events, "summary")
	if summary == nil {
		t.Fatal("no summary event")
	}
	if s, _ := summary["succeeded"].(float64); s != 1 {
		t.Errorf("succeeded = %v, want 1", summary["succeeded"])
	}
	if f, _ := summary["failed"].(float64); f != 1 {
		t.Errorf("failed = %v, want 1", summary["failed"])
	}
}

func TestConvertWebP_FFmpegMissing(t *testing.T) {
	root := t.TempDir()
	// Need a real .mp4 that passes stat + IsVideo + size cap. Empty file
	// suffices since the ffmpeg-missing check (in EncodeWebP) trips before
	// any encoder work; we sidecar duration so the gate passes.
	src := filepath.Join(root, "clip.mp4")
	if err := os.WriteFile(src, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	writeDurationSidecar(t, root, "clip.mp4", 5.0)
	mux := http.NewServeMux()
	Register(mux, root, root, nil)

	t.Setenv("PATH", "")
	rw := postConvertWebP(t, mux, []string{"/clip.mp4"}, false)
	events := parseSSEEvents(t, rw.Body.String())
	e := findEventFor(events, "error", 0)
	if e == nil || e["error"] != "ffmpeg_missing" {
		t.Errorf("want error ffmpeg_missing; got %v", e)
	}
}
