package handlerutil

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
)

// AssertFlusher asserts the response writer supports streaming. SSE 핸들러는
// http.Flusher가 필수 — 없으면 500을 즉시 응답하고 nil을 반환해 호출자가
// 조기 종료하도록 한다.
func AssertFlusher(w http.ResponseWriter, r *http.Request) http.Flusher {
	flusher, ok := w.(http.Flusher)
	if !ok {
		WriteError(w, r, http.StatusInternalServerError, "streaming unsupported", nil)
		return nil
	}
	return flusher
}

// WriteSSEHeaders sets the SSE response headers and flushes the status line.
// X-Accel-Buffering: no는 nginx 같은 reverse proxy가 응답을 버퍼링해서
// 진행 이벤트를 묶지 않게 하는 신호다.
func WriteSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// WriteSSEEvent encodes payload as JSON and writes a single SSE data frame
// followed by a flush. Marshal 실패는 운영 로그에만 남기고 frame을 skip해
// 워커가 계속 진행하게 한다 (안정 구조체에서는 사실상 발생하지 않음).
func WriteSSEEvent(w http.ResponseWriter, flusher http.Flusher, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("sse marshal", "err", err)
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// NewSSEEmitter returns a closure that wraps WriteSSEEvent under a mutex so
// callers from different goroutines (e.g. ffmpeg OnStart on the handler
// goroutine plus a progress writer on a separate goroutine) cannot interleave
// a partial frame. import_url.go uses a single writer goroutine instead and
// skips this wrapper; the convert path needs it because OnStart fires inside
// ffmpeg.Run on the caller's goroutine.
func NewSSEEmitter(w http.ResponseWriter, flusher http.Flusher) func(any) {
	var mu sync.Mutex
	return func(payload any) {
		mu.Lock()
		defer mu.Unlock()
		WriteSSEEvent(w, flusher, payload)
	}
}
