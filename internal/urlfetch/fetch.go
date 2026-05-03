// Package urlfetch downloads remote media (image/video/audio) into a
// destination directory while enforcing size, content-type, and redirect
// policies. Designed for the /api/import-url endpoint: each Fetch call streams
// a single URL to disk via a temporary file and atomically renames it on
// success, choosing a unique name if one already exists. Callers can observe
// progress through the Callbacks hook so the handler can stream SSE events to
// the client in real time.
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
	// MaxRedirects bounds redirect chains.
	MaxRedirects = 5
	// DialTimeout limits TCP connection establishment.
	DialTimeout = 10 * time.Second
	// DNSLookupTimeout bounds hostname resolution before each protected dial.
	DNSLookupTimeout = 5 * time.Second

	// progressByteThreshold and progressTimeThreshold bound how often the
	// Progress callback fires so a very fast download does not emit thousands
	// of events per second.
	progressByteThreshold = 1 << 20 // 1 MiB
	progressTimeThreshold = 250 * time.Millisecond
)

// Result describes a successful import.
type Result struct {
	URL      string   `json:"url"`
	Path     string   `json:"path"`
	Name     string   `json:"name"`
	Size     int64    `json:"size"`
	Type     string   `json:"type"`
	Warnings []string `json:"warnings"`
}

// FetchError is the typed failure returned by Fetch. Code is one of the
// stable identifiers documented in SPEC.md §5.1.
type FetchError struct {
	Code string
	Err  error
}

func (e *FetchError) Error() string { return e.Code }
func (e *FetchError) Unwrap() error { return e.Err }

// Callbacks lets a caller observe a Fetch in flight. Start fires once after
// all header validation passes but before the body is read, so the caller
// knows the payload's intended name, size (if declared), and file type.
// Progress fires zero or more times during body streaming, throttled to avoid
// flooding. Either field may be nil.
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

	// urlExtToCanonical normalizes extension aliases so .jpeg and .jpg are
	// treated as the same canonical type when comparing against the mapped
	// Content-Type extension.
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

// ClientOption configures the URL import HTTP client.
type ClientOption func(*clientConfig)

type secureTransport struct {
	*http.Transport
	allowPrivateNetworks bool
	resolver             Resolver
}

// AllowPrivateNetworks permits loopback, private, link-local, multicast, and
// unspecified destination IPs. Production code should not use this; it exists
// for local-only tests and explicitly trusted deployments.
func AllowPrivateNetworks() ClientOption {
	return func(cfg *clientConfig) {
		cfg.allowPrivateNetworks = true
	}
}

// WithResolver overrides DNS resolution for the URL import client. Production
// callers normally use net.DefaultResolver; tests use this to pin resolution
// outcomes without relying on real DNS.
func WithResolver(resolver Resolver) ClientOption {
	return func(cfg *clientConfig) {
		cfg.resolver = resolver
	}
}

// NewClient returns the http.Client used by Fetch. It enforces a 10s dial
// timeout, a 5-redirect cap, refuses any redirect hop whose scheme is not
// http/https, and blocks requests to private network addresses by default.
// No cookie jar — auth headers and cookies are never carried over by default.
// The per-URL total timeout is enforced via context by the caller (see
// handler/import_url.go) so it can be adjusted at runtime via /api/settings
// without reconstructing the client.
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

// Resolver resolves hostnames for protected URL import dials.
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
		// Fail closed: partial DNS results that arrive with an error are not
		// trusted for SSRF decisions.
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
	// CGNAT (100.64.0.0/10) is intentionally not covered by IsPrivate and is
	// treated as publicly routable for this single-user import policy.
	return !ip.IsValid() ||
		ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified()
}

// Fetch downloads rawURL into destDir (absolute path, caller-validated) and
// returns the saved file's metadata. relDir is the slash-form prefix used to
// build Result.Path. maxBytes caps both pre-download Content-Length check
// and cumulative bytes during body streaming — responses lacking a
// Content-Length are permitted and protected solely by the runtime cap. If
// cb is non-nil, Start fires after header validation and Progress fires
// during body streaming (throttled). On any error no file remains under
// destDir.
func Fetch(ctx context.Context, client *http.Client, rawURL, destDir, relDir string, maxBytes int64, cb *Callbacks) (*Result, *FetchError) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" {
		return nil, &FetchError{Code: "invalid_url", Err: err}
	}

	var warnings []string
	// Scheme is checked before host so file:///path correctly maps to
	// invalid_scheme rather than invalid_url.
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

	// A declared Content-Length above the cap is rejected up front so we
	// don't start a doomed download. A missing Content-Length (chunked or
	// legacy HTTP/1.0) is allowed — the in-stream cap below enforces the
	// same limit by counting bytes as they arrive.
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

// deriveFilename returns the basename to save plus a flag indicating whether
// the extension was forced by the response Content-Type instead of preserved
// from the URL. Empty or unsafe basenames fall back to a category-appropriate
// default (image/video/audio) so the saved file has a sensible name.
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

// defaultBaseForExt picks a generic stem ("image"/"video"/"audio") for URLs
// whose path contributes no usable filename.
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

// sanitizeFilename strips path separators, control characters, and NULs so a
// hostile URL cannot escape destDir or smuggle terminal escape codes.
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

// renameUnique moves tmpPath to destDir/name, falling back to name_1, name_2,
// ... if the target exists. The existence check + rename pair has a benign
// race window (acceptable for a single-user server); a concurrent rename
// returning EEXIST simply triggers another iteration.
func renameUnique(tmpPath, destDir, name string) (string, bool, error) {
	const maxAttempts = 10000
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)

	for i := 0; i < maxAttempts; i++ {
		candidate := name
		if i > 0 {
			candidate = fmt.Sprintf("%s_%d%s", stem, i, ext)
		}
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

// progressReader wraps an io.Reader and emits throttled progress notifications
// through the supplied callback. Emission fires when either 1 MiB has arrived
// since the last emit OR 250 ms has elapsed, whichever comes first. The clock
// starts at construction, so small downloads that complete before the first
// threshold may emit zero progress events — the caller should rely on the
// final size from Result, not the last progress value.
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
