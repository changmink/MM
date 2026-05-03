// Package handlerutil은 HTTP 핸들러 패키지(internal/handler 및 그 서브패키지)
// 가 공유하는 응답 직렬화·로깅 헬퍼의 단일 출처다. 분리 이전엔 동일 로직이
// handler/handler.go, handler/import/common.go, handler/convert/common.go
// 세 곳에 byte-identical 복제되어 한 곳만 고치면 다른 두 곳에서 정책이
// drift할 위험이 있었다 — 이 패키지로 통합한다.
package handlerutil

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// WriteJSON은 주어진 상태 코드와 함께 JSON 응답 본문을 내보낸다. Encode
// 실패는 보통 클라이언트 mid-disconnect로 발생하므로 WriteError와 같은
// 패턴으로 slog.Debug 한 줄만 남긴다 — 정상 흐름이지만 디버깅 단서가
// 필요할 때 추적 가능하게.
func WriteJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Debug("response encode failed",
			"method", r.Method, "path", r.URL.Path, "err", err,
		)
	}
}

// WriteError는 JSON 에러 본문을 내보낸다. 5xx 응답과 err != nil인 클라이언트
// 실수를 분리해서 로그한다 — 5xx는 서버 오작동을 의미하므로 Error로,
// 4xx + err(예: JSON parse 실패)은 운영자 진단용 Warn으로 남긴다. 둘 다
// 아닌 plain 4xx는 기록하지 않는다 (의도적 클라이언트 거부 — 정상 동작).
func WriteError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) {
	switch {
	case code >= 500:
		slog.Error("request failed",
			"method", r.Method, "path", r.URL.Path,
			"status", code, "msg", msg, "err", err,
		)
	case err != nil:
		slog.Warn("request rejected",
			"method", r.Method, "path", r.URL.Path,
			"status", code, "msg", msg, "err", err,
		)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if encErr := json.NewEncoder(w).Encode(map[string]string{"error": msg}); encErr != nil {
		// 클라이언트가 mid-error로 끊기면 진단을 위해 흔적 정도만 남긴다.
		slog.Debug("error response encode failed",
			"method", r.Method, "path", r.URL.Path, "err", encErr,
		)
	}
}
