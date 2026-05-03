package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"file_server/internal/settings"
)

// handleSettings는 GET /api/settings(현재 값)와 PATCH /api/settings(값 교체)를
// 처리한다. 단일 테넌트 배포라 별도 auth 검사가 없다 — 다른 모든 /api 라우트와
// 동일한 신뢰 경계다. SPEC §5에 따라 PATCH 본문은 문서 전체를 교체한다 —
// 부분 업데이트는 의도적으로 지원하지 않는다(검증을 단순하게 유지하고
// wire shape를 GET과 동일하게 만들기 위함).
func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getSettings(w, r)
	case http.MethodPatch:
		h.patchSettings(w, r)
	default:
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
	}
}

func (h *Handler) getSettings(w http.ResponseWriter, r *http.Request) {
	snap := h.settingsSnapshot()
	writeJSON(w, r, http.StatusOK, snap)
}

func (h *Handler) patchSettings(w http.ResponseWriter, r *http.Request) {
	// 테스트 harness는 settings==nil인 Handler를 만든다. 이 모드에서는
	// 영속화할 대상이 없으니 조용히 no-op으로 두는 대신 명시적으로 거부한다.
	if h.settings == nil {
		writeError(w, r, http.StatusInternalServerError, "settings disabled", nil)
		return
	}

	var body settings.Settings
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid request", err)
		return
	}

	if err := h.settings.Update(body); err != nil {
		var re *settings.RangeError
		if errors.As(err, &re) {
			// 필드 이름을 노출하면 클라이언트가 GET을 다시 호출하지 않고도
			// 어떤 입력이 거부됐는지 강조할 수 있다.
			writeJSON(w, r, http.StatusBadRequest, map[string]string{
				"error": "out_of_range",
				"field": re.Field,
			})
			return
		}
		writeError(w, r, http.StatusInternalServerError, "write_failed", err)
		return
	}

	writeJSON(w, r, http.StatusOK, h.settings.Snapshot())
}
