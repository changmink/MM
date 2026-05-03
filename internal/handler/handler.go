package handler

import (
	"context"
	"net/http"
	"net/url"
	"runtime"
	"sync"
	"time"

	handlerconvert "file_server/internal/handler/convert"
	handlerimport "file_server/internal/handler/import"
	"file_server/internal/handlerutil"
	"file_server/internal/importjob"
	"file_server/internal/settings"
	"file_server/internal/thumb"
	"file_server/internal/urlfetch"
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
	webpLocks    sync.Map            // absSrcPath -> *sync.Mutex; serializes clip → WebP per source
	importSem    chan struct{}       // size-1 semaphore; serializes URL import batches process-wide
	importAPI    *handlerimport.Handler
	convertAPI   *handlerconvert.Handler
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

// WithURLClient overrides the URL import HTTP client. Production uses the
// default protected urlfetch client; tests can inject a localhost-capable
// client without widening the production path.
func WithURLClient(client *http.Client) Option {
	return func(h *Handler) {
		if client == nil {
			return
		}
		h.urlClient = client
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
	h.importAPI = handlerimport.New(h.dataDir, h.thumbPool, h.urlClient, h.settings, h.registry, h.importSem)
	h.convertAPI = handlerconvert.New(h.dataDir, &h.convertLocks, &h.webpLocks)

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
	mux.HandleFunc("/api/import-url", requireSameOrigin(h.importAPI.HandleImportURL))
	mux.HandleFunc("/api/import-url/jobs", requireSameOrigin(h.importAPI.HandleJobsRoot))
	mux.HandleFunc("/api/import-url/jobs/", requireSameOrigin(h.importAPI.HandleJobsByID))
	mux.HandleFunc("/api/convert", requireSameOrigin(h.convertAPI.HandleConvert))
	mux.HandleFunc("/api/convert-image", requireSameOrigin(h.convertAPI.HandleConvertImage))
	mux.HandleFunc("/api/convert-webp", requireSameOrigin(h.convertAPI.HandleConvertWebP))
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
// using an allowlist: only "" (curl, server-side, pre-2020 browsers),
// "same-origin", and "none" (user-typed URL) pass. "same-site" (sibling
// subdomain on the same eTLD+1), "cross-site", "cross-origin", and any
// future/unknown value fail-closed so a forward-compat addition can't
// silently widen the surface.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		switch r.Header.Get("Sec-Fetch-Site") {
		case "", "same-origin", "none":
			return true
		default:
			return false
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

// writeJSON / writeError are thin forwards to handlerutil — 패키지 내 호출
// 사이트(browse.go·files.go·folders.go 등)가 짧은 이름을 그대로 쓸 수 있게
// 유지하되 로직은 handlerutil 단일 출처로 모은다.
func writeJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
	handlerutil.WriteJSON(w, r, code, body)
}

func writeError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) {
	handlerutil.WriteError(w, r, code, msg, err)
}
