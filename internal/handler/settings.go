package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"file_server/internal/settings"
)

// handleSettings serves GET /api/settings (current values) and
// PATCH /api/settings (replace values). Single-tenant deployment, so no
// auth check — same trust boundary as every other /api route. Per SPEC §5,
// the PATCH body replaces the entire document; partial updates are not
// supported on purpose (keeps validation trivial and the wire shape
// identical to GET).
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
	// The test harness constructs a Handler with settings==nil; a PATCH in
	// that mode has nothing to persist against, so reject explicitly instead
	// of silently no-oping.
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
			// Exposing the field name lets the client highlight which input
			// was rejected without a second round-trip to GET.
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
