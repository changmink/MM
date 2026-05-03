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

// sseEmitter — 전체 계약은 handlerutil.NewSSEEmitter 참조. 핸들러 안에서
// 두 개 이상의 고루틴이 emit을 동시에 호출할 때만 사용한다. 단일 고루틴
// 이벤트 펌프(예: Job.Publish를 단일 for-range로 드레인하는
// handleImportURL / handleSubscribeJob)는 프레임마다 불필요한 잠금을
// 피하기 위해 writeSSEEvent를 직접 호출하는 게 좋다.
func sseEmitter(w http.ResponseWriter, flusher http.Flusher) func(any) {
	return handlerutil.NewSSEEmitter(w, flusher)
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, payload any) {
	handlerutil.WriteSSEEvent(w, flusher, payload)
}
