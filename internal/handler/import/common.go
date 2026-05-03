package importurl

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"file_server/internal/importjob"
	"file_server/internal/settings"
	"file_server/internal/thumb"
)

type Handler struct {
	dataDir   string
	thumbPool *thumb.Pool
	urlClient *http.Client
	settings  *settings.Store
	registry  *importjob.Registry
	importSem chan struct{}
}

func New(dataDir string, thumbPool *thumb.Pool, urlClient *http.Client, settingsStore *settings.Store, registry *importjob.Registry, importSem chan struct{}) *Handler {
	return &Handler{
		dataDir:   dataDir,
		thumbPool: thumbPool,
		urlClient: urlClient,
		settings:  settingsStore,
		registry:  registry,
		importSem: importSem,
	}
}

func (h *Handler) settingsSnapshot() settings.Settings {
	if h.settings == nil {
		return settings.Default()
	}
	return h.settings.Snapshot()
}

func writeJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Debug("response encode failed",
			"method", r.Method, "path", r.URL.Path, "err", err,
		)
	}
}

func writeError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) {
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
		slog.Debug("error response encode failed",
			"method", r.Method, "path", r.URL.Path, "err", encErr,
		)
	}
}

func assertFlusher(w http.ResponseWriter, r *http.Request) http.Flusher {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, r, http.StatusInternalServerError, "streaming unsupported", nil)
		return nil
	}
	return flusher
}

func writeSSEHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
}

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		slog.Error("sse marshal", "err", err)
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

func RecoverImportJob(rec any, job *importjob.Job) {
	recoverImportJob(rec, job)
}

func SummarizeURLs(urls []importjob.URLState) importjob.Summary {
	return summarizeURLs(urls)
}

func SummaryEvent(s importjob.Summary) importjob.Event {
	return summaryEvent(s)
}

func NormalizeURLs(in []string) []string {
	return normalizeURLs(in)
}

func RedactURL(raw string) string {
	return redactURL(raw)
}
