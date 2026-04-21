package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chang/file_server/internal/media"
	"github.com/chang/file_server/internal/urlfetch"
)

const (
	maxImportURLs       = 50
	importPerURLTimeout = 60 * time.Second
)

type importRequest struct {
	URLs []string `json:"urls"`
}

type importFailure struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

type importResponse struct {
	Succeeded []*urlfetch.Result `json:"succeeded"`
	Failed    []importFailure    `json:"failed"`
}

func (h *Handler) handleImportURL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}

	rel := r.URL.Query().Get("path")
	destAbs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	fi, err := os.Stat(destAbs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "path not found", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "stat failed", err)
		return
	}
	if !fi.IsDir() {
		writeError(w, r, http.StatusBadRequest, "not a directory", nil)
		return
	}

	var body importRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid body", err)
		return
	}

	urls := normalizeURLs(body.URLs)
	if len(urls) == 0 {
		writeError(w, r, http.StatusBadRequest, "no urls", nil)
		return
	}
	if len(urls) > maxImportURLs {
		writeError(w, r, http.StatusBadRequest, "too many urls", nil)
		return
	}

	resp := importResponse{
		Succeeded: []*urlfetch.Result{},
		Failed:    []importFailure{},
	}

	for _, u := range urls {
		ctx, cancel := context.WithTimeout(r.Context(), importPerURLTimeout)
		res, ferr := urlfetch.Fetch(ctx, h.urlClient, u, destAbs, rel)
		cancel()
		if ferr != nil {
			resp.Failed = append(resp.Failed, importFailure{URL: u, Error: ferr.Code})
			continue
		}
		resp.Succeeded = append(resp.Succeeded, res)

		// Mirror handleUpload: enqueue async thumbnail; if the queue is full
		// handleThumb will lazily generate on first view.
		thumbDir := filepath.Join(destAbs, ".thumb")
		thumbPath := filepath.Join(thumbDir, res.Name+".jpg")
		finalSrc := filepath.Join(destAbs, res.Name)
		if !h.thumbPool.Submit(finalSrc, thumbPath) {
			slog.Warn("thumb pool full, deferring to lazy generation", "src", finalSrc)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// normalizeURLs trims whitespace and drops empty entries while preserving
// order and intentional duplicates (collisions get _N suffixes downstream).
func normalizeURLs(in []string) []string {
	out := make([]string, 0, len(in))
	for _, u := range in {
		u = strings.TrimSpace(u)
		if u == "" {
			continue
		}
		out = append(out, u)
	}
	return out
}
