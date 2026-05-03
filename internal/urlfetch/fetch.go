// Package urlfetch는 원격 미디어(image/video/audio)를 목적지 디렉터리로
// 다운로드하면서 크기·content-type·redirect 정책을 강제한다. /api/import-url
// 엔드포인트를 위해 설계됐다 — Fetch 한 호출이 URL 하나를 임시 파일로
// 스트리밍해 디스크에 쓰고 성공 시 원자적으로 rename 한다. 같은 이름이 이미
// 있으면 고유 이름을 선택한다. 호출자는 Callbacks 훅으로 진행 상황을
// 관찰해 핸들러가 SSE 이벤트를 실시간으로 클라이언트에 흘려보낼 수 있다.
package urlfetch

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"file_server/internal/media"
	hlsfetch "file_server/internal/urlfetch/hls"
)

const (
	// MaxRedirects는 redirect 체인의 상한이다.
	MaxRedirects = 5
	// DialTimeout은 TCP 연결 확립 시간을 제한한다.
	DialTimeout = 10 * time.Second
	// DNSLookupTimeout은 보호된 dial 직전의 호스트명 해석 시간을 제한한다.
	DNSLookupTimeout = 5 * time.Second

	// progressByteThreshold와 progressTimeThreshold는 Progress 콜백 호출
	// 빈도의 상한이다. 매우 빠른 다운로드가 초당 수천 이벤트를 발행하지
	// 않도록 막는다.
	progressByteThreshold = 1 << 20 // 1 MiB
	progressTimeThreshold = 250 * time.Millisecond
)

// Result는 성공한 import의 결과를 담는다.
type Result struct {
	URL      string   `json:"url"`
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	Size     int64    `json:"size"`
	Type     string   `json:"type"`
	Warnings []string `json:"warnings"`
}

// FetchError는 Fetch가 반환하는 타입화된 실패 정보다. Code는 SPEC.md §5.1에
// 문서화된 안정적 식별자 중 하나다.
type FetchError struct {
	Code string
	Err  error
}

func (e *FetchError) Error() string { return e.Code }
func (e *FetchError) Unwrap() error { return e.Err }

// Callbacks는 진행 중인 Fetch를 관찰할 수 있게 해준다. Start는 모든 헤더
// 검증을 통과한 뒤, 바디를 읽기 전에 한 번 호출된다 — 호출자는 페이로드의
// 의도된 이름, 크기(선언된 경우), 파일 타입을 알 수 있다. Progress는
// 바디 스트리밍 중에 0회 이상, 폭주를 막기 위해 throttle된 채 호출된다.
// 두 필드 모두 nil이 가능하다.
type Callbacks struct {
	Start    func(name string, total int64, fileType string)
	Progress func(received int64)
}

var (
	errTooManyRedirects = errors.New("too_many_redirects")
	errInvalidScheme    = errors.New("invalid_scheme")
	errPrivateNetwork   = errors.New("private_network")

	contentTypeToExt = map[string]string{
		"image/jpeg":       ".jpg",
		"image/png":        ".png",
		"image/webp":       ".webp",
		"image/gif":        ".gif",
		"video/mp4":        ".mp4",
		"video/x-matroska": ".mkv",
		"video/x-msvideo":  ".avi",
		"video/mp2t":       ".ts",
		"audio/mpeg":       ".mp3",
		"audio/flac":       ".flac",
		"audio/aac":        ".aac",
		"audio/ogg":        ".ogg",
		"audio/wav":        ".wav",
		"audio/mp4":        ".m4a",
	}

	// urlExtToCanonical은 확장자 별칭을 정규화한다. 매핑된 Content-Type
	// 확장자와 비교할 때 .jpeg와 .jpg를 동일한 정규 타입으로 취급한다.
	urlExtToCanonical = map[string]string{
		".jpg":  ".jpg",
		".jpeg": ".jpg",
		".png":  ".png",
		".webp": ".webp",
		".gif":  ".gif",
		".mp4":  ".mp4",
		".mkv":  ".mkv",
		".avi":  ".avi",
		".ts":   ".ts",
		".mp3":  ".mp3",
		".flac": ".flac",
		".aac":  ".aac",
		".ogg":  ".ogg",
		".wav":  ".wav",
		".m4a":  ".m4a",
	}
)

type clientConfig struct {
	allowPrivateNetworks bool
	resolver             Resolver
}

// ClientOption은 URL import용 HTTP 클라이언트를 구성한다.
type ClientOption func(*clientConfig)

type secureTransport struct {
	*http.Transport
	allowPrivateNetworks bool
	resolver             Resolver
}

// AllowPrivateNetworks는 loopback, private, link-local, multicast, unspecified
// 목적지 IP를 허용한다. 프로덕션 코드는 사용해선 안 된다 — 로컬 전용 테스트와
// 명시적으로 신뢰된 배포만을 위한 옵션이다.
func AllowPrivateNetworks() ClientOption {
	return func(cfg *clientConfig) {
		cfg.allowPrivateNetworks = true
	}
}

// WithResolver는 URL import 클라이언트의 DNS 해석을 오버라이드한다. 프로덕션
// 호출자는 보통 net.DefaultResolver를 쓰며, 테스트에서는 실제 DNS에 의존하지
// 않고 해석 결과를 고정하기 위해 사용한다.
func WithResolver(resolver Resolver) ClientOption {
	return func(cfg *clientConfig) {
		cfg.resolver = resolver
	}
}

// NewClient는 Fetch가 사용할 http.Client를 반환한다. 10초 dial 타임아웃,
// redirect 5회 상한을 강제하고, scheme이 http/https가 아닌 redirect hop은
// 거부하며, 기본적으로 private network 주소로의 요청을 차단한다. 쿠키 jar는
// 없다 — 인증 헤더와 쿠키는 기본적으로 전혀 운반되지 않는다. URL당 전체
// 타임아웃은 호출자가 context로 강제한다(handler/import_url.go 참조) —
// 클라이언트를 재생성하지 않고 /api/settings를 통해 런타임에 조정 가능.
func NewClient(opts ...ClientOption) *http.Client {
	var cfg clientConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.resolver == nil {
		cfg.resolver = net.DefaultResolver
	}
	dialer := &net.Dialer{Timeout: DialTimeout}
	dialContext := dialer.DialContext
	if !cfg.allowPrivateNetworks {
		dialContext = publicOnlyDialContext(dialer, cfg.resolver)
	}
	transport := &secureTransport{
		Transport: &http.Transport{
			DialContext: dialContext,
		},
		allowPrivateNetworks: cfg.allowPrivateNetworks,
		resolver:             cfg.resolver,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= MaxRedirects {
				return errTooManyRedirects
			}
			scheme := strings.ToLower(req.URL.Scheme)
			if scheme != "http" && scheme != "https" {
				return errInvalidScheme
			}
			return nil
		},
	}
}

func publicOnlyDialContext(dialer *net.Dialer, resolver Resolver) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := lookupPublicIPs(ctx, resolver, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				return nil, err
			}
		}
		return nil, fmt.Errorf("dial %s: no reachable addresses", address)
	}
}

// Resolver는 보호된 URL import dial을 위한 호스트명 해석을 담당한다.
type Resolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

func lookupPublicIPs(ctx context.Context, resolver Resolver, host string) ([]netip.Addr, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, DNSLookupTimeout)
	defer cancel()
	ips, err := resolveHost(lookupCtx, resolver, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if isBlockedDestination(ip) {
			return nil, errPrivateNetwork
		}
	}
	return ips, nil
}

func resolveHost(ctx context.Context, resolver Resolver, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip.Unmap()}, nil
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		// fail-closed: 에러와 함께 도착한 부분적 DNS 결과는 SSRF 판단에
		// 신뢰하지 않는다.
		return nil, err
	}
	ips := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		ip, ok := netip.AddrFromSlice(addr.IP)
		if !ok {
			continue
		}
		ips = append(ips, ip.Unmap())
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("resolve %s: no addresses", host)
	}
	return ips, nil
}

func isBlockedDestination(ip netip.Addr) bool {
	ip = ip.Unmap()
	// CGNAT(100.64.0.0/10)는 의도적으로 IsPrivate가 다루지 않으며, 이
	// 단일 사용자 import 정책에서는 공개적으로 라우팅 가능한 주소로 취급한다.
	return !ip.IsValid() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// Fetch는 rawURL을 destDir(절대경로, 호출자가 검증)로 다운로드해 저장된
// 파일의 메타데이터를 반환한다. relDir은 Result.Path를 구성할 때 쓰는 슬래시
// 형식 prefix다. maxBytes는 다운로드 전 Content-Length 검사와 바디 스트리밍
// 중 누적 바이트 검사 둘 다에 적용된다 — Content-Length가 없는 응답도
// 허용하며, 그 경우엔 런타임 상한만이 보호한다. cb가 non-nil이면 헤더 검증
// 후 Start, 바디 스트리밍 중 Progress(throttled)가 호출된다. 어떤 에러가
// 발생하든 destDir에는 파일이 남지 않는다.
func Fetch(ctx context.Context, client *http.Client, rawURL, destDir, relDir string, maxBytes int64, cb *Callbacks) (*Result, *FetchError) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		return nil, &FetchError{Code: "invalid_url", Err: err}
	}

	var warnings []string
	// scheme을 host보다 먼저 검사한다. 그래야 file:///path가 invalid_url이
	// 아닌 invalid_scheme으로 정확히 매핑된다.
	switch strings.ToLower(parsed.Scheme) {
	case "https":
	case "http":
		warnings = append(warnings, "insecure_http")
	default:
		return nil, &FetchError{Code: "invalid_scheme"}
	}

	if parsed.Host == "" {
		return nil, &FetchError{Code: "invalid_url"}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &FetchError{Code: "invalid_url", Err: err}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, classifyHTTPError(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, &FetchError{Code: "http_error", Err: fmt.Errorf("status %d", resp.StatusCode)}
	}

	rawContentType := resp.Header.Get("Content-Type")
	if hlsfetch.IsResponse(rawContentType, parsed.Path) {
		result, ferr := hlsfetch.Fetch(ctx, client, resp, parsed, rawURL, destDir, relDir, warnings, maxBytes, hlsCallbacks(cb), hlsfetch.Deps{
			ClassifyHTTPError: classifyHLSError,
			RenameUnique:      renameUnique,
			SanitizeFilename:  sanitizeFilename,
		})
		if ferr != nil {
			return nil, &FetchError{Code: ferr.Code, Err: ferr.Err}
		}
		return &Result{
			URL:      result.URL,
			Path:     result.Path,
			Name:     result.Name,
			Size:     result.Size,
			Type:     result.Type,
			Warnings: result.Warnings,
		}, nil
	}

	// Content-Length가 상한을 넘어 선언된 경우 다운로드를 시작하지 않고
	// 곧장 거부한다. Content-Length가 없는 경우(chunked나 legacy HTTP/1.0)
	// 는 허용한다 — 아래 in-stream 상한이 도착하는 바이트를 세면서 동일한
	// 한도를 강제한다.
	if resp.ContentLength > maxBytes {
		return nil, &FetchError{Code: "too_large"}
	}

	mediaType, _, err := mime.ParseMediaType(rawContentType)
	if err != nil {
		return nil, &FetchError{Code: "unsupported_content_type"}
	}
	mappedExt, ok := contentTypeToExt[strings.ToLower(mediaType)]
	if !ok {
		return nil, &FetchError{Code: "unsupported_content_type"}
	}

	name, replaced := deriveFilename(parsed, mappedExt)
	if replaced {
		warnings = append(warnings, "extension_replaced")
	}
	fileType := string(media.DetectType(name))

	if cb != nil && cb.Start != nil {
		cb.Start(name, resp.ContentLength, fileType)
	}

	tmpFile, err := os.CreateTemp(destDir, ".urlimport-*.tmp")
	if err != nil {
		return nil, &FetchError{Code: "write_error", Err: err}
	}
	tmpPath := tmpFile.Name()
	renamed := false
	defer func() {
		_ = tmpFile.Close()
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	var src io.Reader = io.LimitReader(resp.Body, maxBytes+1)
	if cb != nil && cb.Progress != nil {
		src = newProgressReader(src, cb.Progress)
	}
	n, err := io.Copy(tmpFile, src)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &FetchError{Code: "download_timeout", Err: err}
		}
		return nil, &FetchError{Code: "network_error", Err: err}
	}
	if n > maxBytes {
		return nil, &FetchError{Code: "too_large"}
	}
	if err := tmpFile.Close(); err != nil {
		return nil, &FetchError{Code: "write_error", Err: err}
	}

	finalName, didRename, err := renameUnique(tmpPath, destDir, name)
	if err != nil {
		return nil, &FetchError{Code: "write_error", Err: err}
	}
	renamed = true
	if didRename {
		warnings = append(warnings, "renamed")
	}

	return &Result{
		URL:      rawURL,
		Path:     path.Join(relDir, finalName),
		Name:     finalName,
		Size:     n,
		Type:     string(media.DetectType(finalName)),
		Warnings: warnings,
	}, nil
}

func classifyHTTPError(err error) *FetchError {
	if errors.Is(err, errTooManyRedirects) {
		return &FetchError{Code: "too_many_redirects", Err: err}
	}
	if errors.Is(err, errInvalidScheme) {
		return &FetchError{Code: "invalid_scheme", Err: err}
	}
	if errors.Is(err, errPrivateNetwork) {
		return &FetchError{Code: "private_network", Err: err}
	}
	var ce *x509.CertificateInvalidError
	var hve x509.HostnameError
	var uae x509.UnknownAuthorityError
	if errors.As(err, &ce) || errors.As(err, &hve) || errors.As(err, &uae) {
		return &FetchError{Code: "tls_error", Err: err}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &FetchError{Code: "download_timeout", Err: err}
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Timeout() {
		return &FetchError{Code: "connect_timeout", Err: err}
	}
	return &FetchError{Code: "network_error", Err: err}
}

func hlsCallbacks(cb *Callbacks) *hlsfetch.Callbacks {
	if cb == nil {
		return nil
	}
	return &hlsfetch.Callbacks{
		Start:    cb.Start,
		Progress: cb.Progress,
	}
}

func classifyHLSError(err error) *hlsfetch.FetchError {
	ferr := classifyHTTPError(err)
	return &hlsfetch.FetchError{Code: ferr.Code, Err: ferr.Err}
}

// deriveFilename은 저장에 쓸 basename과, 확장자가 URL에서 보존되지 않고
// 응답의 Content-Type으로 강제됐는지 여부의 플래그를 반환한다. 비어 있거나
// 안전하지 않은 basename은 카테고리에 어울리는 기본값(image/video/audio)
// 으로 폴백해, 저장된 파일이 합리적인 이름을 갖게 한다.
func deriveFilename(parsedURL *url.URL, mappedExt string) (string, bool) {
	base := path.Base(parsedURL.Path)
	if decoded, err := url.PathUnescape(base); err == nil {
		base = decoded
	}
	base = sanitizeFilename(base)

	stem := strings.TrimSuffix(base, path.Ext(base))
	if stem == "" || stem == "." || stem == ".." {
		return defaultBaseForExt(mappedExt) + mappedExt, false
	}

	urlExt := strings.ToLower(path.Ext(base))
	if urlExt == "" {
		return stem + mappedExt, false
	}
	if canonical, known := urlExtToCanonical[urlExt]; known && canonical == mappedExt {
		return stem + urlExt, false
	}
	return stem + mappedExt, true
}

// defaultBaseForExt는 path가 쓸 만한 파일명을 제공하지 않는 URL을 위해
// 일반화된 stem("image"/"video"/"audio")을 고른다.
func defaultBaseForExt(ext string) string {
	switch media.DetectType("x" + ext) {
	case media.TypeVideo:
		return "video"
	case media.TypeAudio:
		return "audio"
	default:
		return "image"
	}
}

// sanitizeFilename은 path separator, 제어 문자, NUL을 제거해 적대적 URL이
// destDir을 탈출하거나 터미널 escape 코드를 슬쩍 끼워 넣을 수 없게 한다.
func sanitizeFilename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r < 0x20 || r == 0x7F || r == '/' || r == '\\' || r == 0 {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// renameUnique는 tmpPath를 destDir/name으로 옮기되, 대상이 존재하면 name_1,
// name_2, ... 로 폴백한다. 존재 검사 + rename 쌍에는 경미한 race 창이
// 있지만 단일 사용자 서버에서는 허용된다 — 동시 rename이 EEXIST를 반환하면
// 다음 반복으로 넘어가 새 후보를 시도한다. suffix 규칙은 media.NameWithSuffix와
// 같아서 사용자 입장의 "_N" 이름은 업로드·폴더 이동 경로와 동일하다.
func renameUnique(tmpPath, destDir, name string) (string, bool, error) {
	const maxAttempts = 10000
	for i := 0; i < maxAttempts; i++ {
		candidate := media.NameWithSuffix(name, i)
		candidatePath := filepath.Join(destDir, candidate)
		if _, err := os.Lstat(candidatePath); err == nil {
			continue
		}
		if err := os.Rename(tmpPath, candidatePath); err != nil {
			if os.IsExist(err) {
				continue
			}
			return "", false, err
		}
		return candidate, i > 0, nil
	}
	return "", false, fmt.Errorf("could not find unique name for %s after %d attempts", name, maxAttempts)
}

// progressReader는 io.Reader를 감싸 throttle된 progress 알림을 콜백으로
// 발행한다. 마지막 emit 이후 1 MiB가 도착했거나 250 ms가 흘렀을 때 중
// 먼저 도달한 쪽에서 호출이 발생한다. 시계는 생성 시점에 시작하므로,
// 첫 임계값 이전에 끝나는 작은 다운로드는 progress 이벤트를 한 번도
// 발행하지 않을 수 있다 — 호출자는 마지막 progress 값이 아닌 Result의
// 최종 크기를 신뢰해야 한다.
type progressReader struct {
	inner        io.Reader
	progress     func(int64)
	received     int64
	lastReceived int64
	lastAt       time.Time
}

func newProgressReader(r io.Reader, progress func(int64)) *progressReader {
	return &progressReader{
		inner:    r,
		progress: progress,
		lastAt:   time.Now(),
	}
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.inner.Read(buf)
	if n > 0 {
		p.received += int64(n)
		delta := p.received - p.lastReceived
		if delta > 0 {
			now := time.Now()
			if delta >= progressByteThreshold || now.Sub(p.lastAt) >= progressTimeThreshold {
				p.progress(p.received)
				p.lastReceived = p.received
				p.lastAt = now
			}
		}
	}
	return n, err
}
