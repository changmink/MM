package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	"github.com/chang/file_server/internal/importjob"
	"github.com/chang/file_server/internal/settings"
	"github.com/chang/file_server/internal/thumb"
	"github.com/chang/file_server/internal/urlfetch"
)

type Handler struct {
	dataDir      string
	thumbPool    *thumb.Pool
	urlClient    *http.Client
	settings     *settings.Store
	serverCtx    context.Context     // lifetime of the server process; jobs derive their context from this
	registry     *importjob.Registry // in-memory URL-import job state; survives request lifecycle
	streamLocks  sync.Map            // cachePath -> *sync.Mutex; serializes ffmpeg per cache key
	convertLocks sync.Map            // absSrcPath -> *sync.Mutex; serializes TS → MP4 per source
	importSem    chan struct{}       // size-1 semaphore; serializes URL import batches process-wide
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
	// Registry must be created after options apply so it inherits the
	// (possibly overridden) serverCtx and propagates a graceful shutdown
	// cancel into every active job.
	h.registry = importjob.New(h.serverCtx)

	// Read-only routes pass through directly. Mutating routes go through
	// requireSameOrigin so a request whose Origin (when present) does not
	// match the server's host is rejected before any state changes — a
	// belt-and-suspenders CSRF mitigation for the no-auth single-user
	// LAN deployment. GET/HEAD always pass; missing Origin (curl, server-
	// side calls) also passes since CSRF requires a browser.
	mux.HandleFunc("/api/browse", h.handleBrowse)
	mux.HandleFunc("/api/tree", h.handleTree)
	mux.HandleFunc("/api/stream", h.handleStream)
	mux.HandleFunc("/api/thumb", h.handleThumb)
	mux.HandleFunc("/api/upload", requireSameOrigin(h.handleUpload))
	mux.HandleFunc("/api/file", requireSameOrigin(h.handleFile))
	mux.HandleFunc("/api/folder", requireSameOrigin(h.handleFolder))
	mux.HandleFunc("/api/import-url", requireSameOrigin(h.handleImportURL))
	mux.HandleFunc("/api/import-url/jobs", requireSameOrigin(h.handleJobsRoot))
	mux.HandleFunc("/api/import-url/jobs/", requireSameOrigin(h.handleJobsByID))
	mux.HandleFunc("/api/convert", requireSameOrigin(h.handleConvert))
	mux.HandleFunc("/api/settings", requireSameOrigin(h.handleSettings))

	mux.Handle("/", http.FileServer(http.Dir(webDir)))
	return h
}

// requireSameOrigin wraps a handler so requests with a cross-origin Origin
// header are rejected with 403 before reaching the inner handler. Safe-method
// requests (GET/HEAD) always pass through — they cannot mutate state and
// EventSource needs them. Missing Origin is treated as same-origin (curl,
// SSR, internal calls). The CLAUDE.md threat model is single-user LAN with
// no auth; a malicious page on another origin should not be able to issue
// POST/PATCH/DELETE/PUT against the file server even if the user happens
// to visit it while having the file server open in another tab.
func requireSameOrigin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !sameOrigin(r) {
				writeError(w, r, http.StatusForbidden, "cross_origin", nil)
				return
			}
		}
		next(w, r)
	}
}

// sameOrigin reports whether the request's Origin header (if present)
// matches the request's Host. An absent Origin defers to Sec-Fetch-Site
// — browsers send it on every request, and a cross-site value rejects
// even if Origin was somehow stripped (extension, niche client). With
// both signals absent the request passes: curl, server-side callers, and
// pre-2020 browsers omit both, and the LAN single-user threat model
// accepts that.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		switch r.Header.Get("Sec-Fetch-Site") {
		case "cross-site", "cross-origin":
			return false
		default:
			return true
		}
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	return u.Host == r.Host
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

// Close stops background workers (thumbnail pool + import jobs). Safe to call
// once per Handler. CancelAll/WaitAll ensures in-flight URL fetches unwind
// before the thumbnail pool drains its queue, so half-finished imports do
// not race the pool shutdown.
func (h *Handler) Close() {
	if h.registry != nil {
		h.registry.CancelAll()
		h.registry.WaitAll(5 * time.Second)
	}
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
