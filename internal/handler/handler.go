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
	serverCtx    context.Context     // 서버 프로세스 수명. Job들이 여기서 컨텍스트를 파생한다.
	registry     *importjob.Registry // URL import Job 인메모리 상태. 요청 수명을 넘어 살아남는다.
	streamLocks  sync.Map            // cachePath -> *sync.Mutex. cache key별로 ffmpeg를 직렬화한다.
	convertLocks sync.Map            // absSrcPath -> *sync.Mutex. 원본별로 TS → MP4를 직렬화한다.
	webpLocks    sync.Map            // absSrcPath -> *sync.Mutex. 원본별로 clip → WebP를 직렬화한다.
	importSem    chan struct{}       // 크기 1 세마포어. 프로세스 단위로 URL import 배치를 직렬화한다.
	importAPI    *handlerimport.Handler
	convertAPI   *handlerconvert.Handler
}

// Option은 Handler를 구성한다. Register와 함께 써서 테스트가 짧은 4-인자
// 호출을 유지하는 동시에, 프로덕션 wiring(main.go)이 graceful shutdown용
// 서버 수명 컨텍스트 같은 추가 옵션을 켤 수 있게 해준다.
type Option func(*Handler)

// WithServerCtx는 서버 수명 컨텍스트를 부착한다. 이 컨텍스트를 취소하면
// (보통 SIGINT/SIGTERM 핸들러에서) 이후 단계에서 추가된 핸들러 단위 장기
// 상태(J3+: import Job 레지스트리)에 전파된다.
func WithServerCtx(ctx context.Context) Option {
	return func(h *Handler) {
		if ctx == nil {
			return
		}
		h.serverCtx = ctx
	}
}

// WithURLClient는 URL import HTTP 클라이언트를 오버라이드한다. 프로덕션은
// 기본 보호된 urlfetch 클라이언트를 사용한다. 테스트는 localhost를 허용하는
// 클라이언트를 주입할 수 있으며, 프로덕션 경로를 넓히지 않는다.
func WithURLClient(client *http.Client) Option {
	return func(h *Handler) {
		if client == nil {
			return
		}
		h.urlClient = client
	}
}

// Register는 모든 API 라우트를 연결한다. URL import나 /api/settings를
// 검증하지 않는 테스트에서는 settingsStore가 nil이어도 된다 — nil을 넘기면
// settings.Default() 하드코드 값이 사용된다. 프로덕션에서는 graceful
// shutdown이 장기 백그라운드 작업을 취소할 수 있도록 WithServerCtx를 전달한다.
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
	// Registry는 옵션 적용 이후에 만들어야 (필요 시 오버라이드된) serverCtx를
	// 상속하고, graceful shutdown 취소가 모든 active Job에 전파된다.
	h.registry = importjob.New(h.serverCtx)
	h.importAPI = handlerimport.New(h.dataDir, h.thumbPool, h.urlClient, h.settings, h.registry, h.importSem)
	h.convertAPI = handlerconvert.New(h.dataDir, &h.convertLocks, &h.webpLocks)

	// 읽기 전용 라우트는 곧장 통과한다. 변경 라우트는 requireSameOrigin을
	// 거쳐, Origin이 있는 경우 서버 호스트와 일치하지 않으면 상태 변경 전에
	// 거부된다 — 인증이 없는 단일 사용자 LAN 배포를 위한 belt-and-suspenders
	// CSRF 완화책이다. GET/HEAD는 항상 통과한다. Origin이 없는 경우(curl,
	// 서버 측 호출)도 통과한다 — CSRF는 브라우저가 있어야 성립한다.
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

// requireSameOrigin은 핸들러를 감싸서 cross-origin Origin 헤더를 가진
// 요청이 inner 핸들러에 닿기 전에 403으로 거부되도록 한다. safe-method
// 요청(GET/HEAD)은 항상 통과한다 — 상태를 변형할 수 없고 EventSource가
// 필요하다. Origin이 없는 경우는 same-origin으로 간주한다(curl, SSR,
// 내부 호출). CLAUDE.md의 위협 모델은 인증이 없는 단일 사용자 LAN이다 —
// 사용자가 파일 서버를 다른 탭에 열어둔 채 다른 origin의 악성 페이지를
// 방문하더라도, 그 페이지가 파일 서버에 POST/PATCH/DELETE/PUT을 발사할
// 수 있어선 안 된다.
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

// sameOrigin은 요청의 Origin 헤더(있다면)가 요청의 Host와 일치하는지
// 보고한다. Origin이 없으면 Sec-Fetch-Site에 위임하되 allowlist 방식이다 —
// 통과하는 값은 ""(curl, 서버 측, 2020년 이전 브라우저), "same-origin",
// "none"(사용자가 직접 입력한 URL)뿐이다. "same-site"(같은 eTLD+1의 형제
// 서브도메인), "cross-site", "cross-origin"과 알 수 없는 모든 값은
// fail-closed로 거부해, 향후 호환을 위한 새 값이 surface를 조용히 넓히지
// 못하게 한다.
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

// settingsSnapshot은 현재 URL import 설정을 반환한다. store가 nil(테스트
// harness)이면 기본값으로 폴백해, null 검사 없이도 모든 요청 경로가 사용
// 가능한 값을 갖게 한다.
func (h *Handler) settingsSnapshot() settings.Settings {
	if h.settings == nil {
		return settings.Default()
	}
	return h.settings.Snapshot()
}

// Close는 백그라운드 워커(썸네일 풀 + import Job)를 멈춘다. Handler당 한
// 번만 호출해야 한다. CancelAll/WaitAll이 진행 중이던 URL fetch가 풀리는
// 것을 보장한 뒤에야 썸네일 풀이 큐를 비우므로, 반쯤 끝난 import가 풀
// shutdown과 race하지 않는다.
func (h *Handler) Close() {
	if h.registry != nil {
		h.registry.CancelAll()
		h.registry.WaitAll(5 * time.Second)
	}
	if h.thumbPool != nil {
		h.thumbPool.Shutdown()
	}
}

// writeJSON / writeError는 handlerutil로 향하는 얇은 포워더다 — 패키지 내
// 호출 사이트(browse.go·files.go·folders.go 등)가 짧은 이름을 그대로 쓸 수
// 있게 유지하면서, 로직은 handlerutil의 단일 출처로 모은다.
func writeJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
	handlerutil.WriteJSON(w, r, code, body)
}

func writeError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) {
	handlerutil.WriteError(w, r, code, msg, err)
}
