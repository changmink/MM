package handlerutil_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"file_server/internal/handlerutil"
)

func TestWriteJSON(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	handlerutil.WriteJSON(rr, req, http.StatusCreated, map[string]string{"hello": "world"})

	if rr.Code != http.StatusCreated {
		t.Fatalf("code = %d, want 201", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var body map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body decode: %v", err)
	}
	if body["hello"] != "world" {
		t.Errorf("body = %v, want hello=world", body)
	}
}

func TestWriteError(t *testing.T) {
	cases := []struct {
		name string
		code int
		msg  string
	}{
		{"4xx", http.StatusBadRequest, "invalid_input"},
		{"5xx", http.StatusInternalServerError, "write_failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/x", nil)
			handlerutil.WriteError(rr, req, tc.code, tc.msg, nil)

			if rr.Code != tc.code {
				t.Fatalf("code = %d, want %d", rr.Code, tc.code)
			}
			if got := rr.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			var body map[string]string
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("body decode: %v", err)
			}
			if body["error"] != tc.msg {
				t.Errorf("error = %q, want %q", body["error"], tc.msg)
			}
		})
	}
}

func TestAssertFlusher_OK(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if f := handlerutil.AssertFlusher(rr, req); f == nil {
		t.Error("AssertFlusher returned nil for httptest.ResponseRecorder")
	}
}

type nonFlushingWriter struct {
	http.ResponseWriter
}

func TestAssertFlusher_NotFlusher(t *testing.T) {
	rr := httptest.NewRecorder()
	wrapped := &nonFlushingWriter{ResponseWriter: rr}
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if f := handlerutil.AssertFlusher(wrapped, req); f != nil {
		t.Error("AssertFlusher returned non-nil for non-Flusher writer")
	}
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want 500", rr.Code)
	}
}

func TestWriteSSEHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	handlerutil.WriteSSEHeaders(rr)
	if got := rr.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}
	if got := rr.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if got := rr.Header().Get("X-Accel-Buffering"); got != "no" {
		t.Errorf("X-Accel-Buffering = %q, want no", got)
	}
}

func TestWriteSSEEvent(t *testing.T) {
	rr := httptest.NewRecorder()
	handlerutil.WriteSSEEvent(rr, rr, map[string]int{"n": 7})
	body := rr.Body.String()
	if !strings.HasPrefix(body, "data: ") || !strings.HasSuffix(body, "\n\n") {
		t.Errorf("body = %q, want SSE data frame", body)
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(body, "data: "), "\n\n")
	var got map[string]int
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("payload decode: %v", err)
	}
	if got["n"] != 7 {
		t.Errorf("payload = %v, want n=7", got)
	}
}

// TestNewSSEEmitter_Serializes 잠금 보호된 emitter — concurrent emit
// 호출이 partial frame을 interleave하지 않는다. race detector + 다수 동시 호출.
func TestNewSSEEmitter_Serializes(t *testing.T) {
	rr := httptest.NewRecorder()
	emit := handlerutil.NewSSEEmitter(rr, rr)
	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			emit(map[string]int{"i": i})
		}(i)
	}
	wg.Wait()

	frames := strings.Split(strings.TrimRight(rr.Body.String(), "\n"), "\n\n")
	gotFrames := 0
	for _, frame := range frames {
		frame = strings.TrimSpace(frame)
		if frame == "" {
			continue
		}
		gotFrames++
		if !strings.HasPrefix(frame, "data: ") {
			t.Errorf("frame missing prefix: %q", frame)
		}
	}
	if gotFrames != n {
		t.Errorf("got %d frames, want %d (interleave?)", gotFrames, n)
	}
}
