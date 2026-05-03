package importurl

import (
	"net/http"

	"file_server/internal/handlerutil"
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

// writeJSON / writeError / assertFlusher / writeSSEHeaders / writeSSEEvent
// are thin forwards to handlerutil — 패키지 내 호출 사이트가 짧은 이름을
// 그대로 쓰게 유지하되 로직은 handlerutil 단일 출처로 모은다.

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

func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, payload any) {
	handlerutil.WriteSSEEvent(w, flusher, payload)
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
