package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime"
	"sync"

	"github.com/chang/file_server/internal/settings"
	"github.com/chang/file_server/internal/thumb"
	"github.com/chang/file_server/internal/urlfetch"
)

type Handler struct {
	dataDir      string
	thumbPool    *thumb.Pool
	urlClient    *http.Client
	settings     *settings.Store
	serverCtx    context.Context // lifetime of the server process; J3 derives the import-job registry from this
	streamLocks  sync.Map        // cachePath -> *sync.Mutex; serializes ffmpeg per cache key
	convertLocks sync.Map        // absSrcPath -> *sync.Mutex; serializes TS → MP4 per source
	importSem    chan struct{}   // size-1 semaphore; serializes URL import batches process-wide
}

// Option configures a Handler. Use with Register so tests can keep their
// terse 4-arg call sites while production wiring (main.go) opts in to extras
// like a server-lifetime context for graceful shutdown.
type Option func(*Handler)

// WithServerCtx attaches a server-lifetime context. Cancelling it (typically
// from a SIGINT/SIGTERM handler) propagates to long-lived per-handler state
// added in later phases (J3+: import job registry).
func WithServerCtx(ctx context.Context) Option {
	return func(h *Handler) {
		if ctx == nil {
			return
		}
		h.serverCtx = ctx
	}
}

// Register wires all API routes. settingsStore may be nil in tests that do
// not exercise URL import or /api/settings — callers that pass nil get the
// hard-coded Default() values from settings. Pass WithServerCtx in production
// so graceful shutdown can cancel long-lived background work.
func Register(mux *http.ServeMux, dataDir, webDir string, settingsStore *settings.Store, opts ...Option) *Handler {
	h := &Handler{
		dataDir:   dataDir,
		thumbPool: thumb.NewPool(runtime.NumCPU()),
		urlClient: urlfetch.NewClient(),
		settings:  settingsStore,
		serverCtx: context.Background(),
		importSem: make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(h)
	}

	mux.HandleFunc("/api/browse", h.handleBrowse)
	mux.HandleFunc("/api/tree", h.handleTree)
	mux.HandleFunc("/api/stream", h.handleStream)
	mux.HandleFunc("/api/thumb", h.handleThumb)
	mux.HandleFunc("/api/upload", h.handleUpload)
	mux.HandleFunc("/api/file", h.handleFile)
	mux.HandleFunc("/api/folder", h.handleFolder)
	mux.HandleFunc("/api/import-url", h.handleImportURL)
	mux.HandleFunc("/api/convert", h.handleConvert)
	mux.HandleFunc("/api/settings", h.handleSettings)

	mux.Handle("/", http.FileServer(http.Dir(webDir)))
	return h
}

// settingsSnapshot returns the current URL import settings, falling back to
// defaults when the store is nil (test harness) so every request path has a
// usable value without null-checks.
func (h *Handler) settingsSnapshot() settings.Settings {
	if h.settings == nil {
		return settings.Default()
	}
	return h.settings.Snapshot()
}

// Close stops the background thumbnail pool. Safe to call once per Handler.
func (h *Handler) Close() {
	if h.thumbPool != nil {
		h.thumbPool.Shutdown()
	}
}

// writeError emits a JSON error body and (for 5xx, or any non-nil err) logs
// the underlying cause with request context. Pass nil for err on plain 4xx
// validation failures where the message is the whole story.
func writeError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) {
	if code >= 500 || err != nil {
		slog.Error("request failed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", code,
			"msg", msg,
			"err", err,
		)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
