package handler

import (
	"net/http"

	"file_server/internal/handlerutil"
)

// assertFlusher / writeSSEHeaders / sseEmitter / writeSSEEvent are thin
// forwards to handlerutil so SSE 핸들러가 짧은 이름을 그대로 쓰게 두되
// 정책(헤더·mutex·marshal-fail 처리)은 handlerutil 단일 출처로 모은다.

func assertFlusher(w http.ResponseWriter, r *http.Request) http.Flusher {
	return handlerutil.AssertFlusher(w, r)
}

func writeSSEHeaders(w http.ResponseWriter) {
	handlerutil.WriteSSEHeaders(w)
}

// sseEmitter — see handlerutil.NewSSEEmitter for the full contract. Use only
// when more than one goroutine in a handler can call emit concurrently;
// single-goroutine event pumps (e.g. Job.Publish drained in one for-range
// loop — see handleImportURL / handleSubscribeJob) should call
// writeSSEEvent directly to avoid a redundant lock per frame.
func sseEmitter(w http.ResponseWriter, flusher http.Flusher) func(any) {
	return handlerutil.NewSSEEmitter(w, flusher)
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, payload any) {
	handlerutil.WriteSSEEvent(w, flusher, payload)
}
