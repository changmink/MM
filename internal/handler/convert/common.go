package convertapi

import (
	"net/http"
	"sync"

	"file_server/internal/handlerutil"
)

type Handler struct {
	dataDir      string
	convertLocks *sync.Map
	webpLocks    *sync.Map
}

func New(dataDir string, convertLocks, webpLocks *sync.Map) *Handler {
	if convertLocks == nil {
		convertLocks = &sync.Map{}
	}
	if webpLocks == nil {
		webpLocks = &sync.Map{}
	}
	return &Handler{
		dataDir:      dataDir,
		convertLocks: convertLocks,
		webpLocks:    webpLocks,
	}
}

// writeJSON / writeError / assertFlusher / writeSSEHeaders / writeSSEEvent /
// sseEmitter are thin forwards to handlerutil — 패키지 내 호출 사이트가
// 짧은 이름을 그대로 쓰게 유지하되 로직은 handlerutil 단일 출처로 모은다.

func writeJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
	handlerutil.WriteJSON(w, r, code, body)
}

func writeError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) {
	handlerutil.WriteError(w, r, code, msg, err)
}

func assertFlusher(w http.ResponseWriter, r *http.Request) http.Flusher {
	return handlerutil.AssertFlusher(w, r)
}

func writeSSEHeaders(w http.ResponseWriter) {
	handlerutil.WriteSSEHeaders(w)
}

// sseEmitter — convert 경로의 OnStart 콜백은 ffmpeg.Run 안쪽 호출자
// goroutine에서, progress writer는 별도 goroutine에서 emit을 호출하므로
// mutex 보호가 필수. 자세한 의도는 handlerutil.NewSSEEmitter 주석 참조.
func sseEmitter(w http.ResponseWriter, flusher http.Flusher) func(any) {
	return handlerutil.NewSSEEmitter(w, flusher)
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, payload any) {
	handlerutil.WriteSSEEvent(w, flusher, payload)
}
