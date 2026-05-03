package handlerutil

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
)

// AssertFlusher는 응답 writer가 스트리밍을 지원하는지 확인한다. SSE 핸들러는
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

// WriteSSEHeaders는 SSE 응답 헤더를 설정하고 상태 줄을 flush한다.
// X-Accel-Buffering: no는 nginx 같은 reverse proxy가 응답을 버퍼링해서
// 진행 이벤트를 묶지 않게 하는 신호다.
func WriteSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

// WriteSSEEvent는 payload를 JSON으로 인코딩해 SSE 데이터 프레임 한 개를
// 쓰고 flush한다. Marshal 실패는 운영 로그에만 남기고 frame을 skip해
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

// NewSSEEmitter는 WriteSSEEvent를 뮤텍스로 감싼 클로저를 반환한다. 다른
// 고루틴에서 들어오는 호출(예: 핸들러 고루틴의 ffmpeg OnStart와 별도
// 고루틴의 progress writer)이 부분 프레임을 섞어 쓰지 못하게 하기 위함이다.
// import_url.go는 single writer 고루틴 패턴을 써서 이 래퍼가 필요 없지만,
// convert 경로는 OnStart가 ffmpeg.Run 안에서 호출자 고루틴으로 발생하므로
// 이 래퍼가 필요하다.
func NewSSEEmitter(w http.ResponseWriter, flusher http.Flusher) func(any) {
	var mu sync.Mutex
	return func(payload any) {
		mu.Lock()
		defer mu.Unlock()
		WriteSSEEvent(w, flusher, payload)
	}
}
