// Package urlfetch downloads remote images into a destination directory while
// enforcing size, content-type, and redirect policies. Designed for the
// /api/import-url endpoint: each Fetch call streams a single URL to disk via a
// temporary file and atomically renames it on success, choosing a unique name
// if one already exists.
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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const (
	// MaxBytes caps downloaded payloads at 50 MiB.
	MaxBytes = 50 << 20
	// MaxRedirects bounds redirect chains.
	MaxRedirects = 5
	// DialTimeout limits TCP connection establishment.
	DialTimeout = 10 * time.Second
	// TotalTimeout limits the entire request, including body read.
	TotalTimeout = 60 * time.Second
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

var (
	errTooManyRedirects = errors.New("too_many_redirects")
	errInvalidScheme    = errors.New("invalid_scheme")

	contentTypeToExt = map[string]string{
		"image/jpeg": ".jpg",
		"image/png":  ".png",
		"image/webp": ".webp",
		"image/gif":  ".gif",
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
	}
)

// NewClient returns the http.Client used by Fetch. It enforces a 10s dial
// timeout, a 60s overall timeout, a 5-redirect cap, and refuses any redirect
// hop whose scheme is not http/https. No cookie jar — auth headers and cookies
// are never carried over by default.
func NewClient() *http.Client {
	dialer := &net.Dialer{Timeout: DialTimeout}
	return &http.Client{
		Transport: &http.Transport{
			DialContext: dialer.DialContext,
		},
		Timeout: TotalTimeout,
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

// Fetch downloads rawURL into destDir (absolute path, caller-validated) and
// returns the saved file's metadata. relDir is the slash-form prefix used to
// build Result.Path. On any error no file remains under destDir.
func Fetch(ctx context.Context, client *http.Client, rawURL, destDir, relDir string) (*Result, *FetchError) {
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

	// Reject responses without a known length so we cannot be tricked into
	// streaming an unbounded body. The in-stream cap below is defense in depth.
	if resp.ContentLength < 0 {
		return nil, &FetchError{Code: "missing_content_length"}
	}
	if resp.ContentLength > MaxBytes {
		return nil, &FetchError{Code: "too_large"}
	}

	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
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

	n, err := io.Copy(tmpFile, io.LimitReader(resp.Body, MaxBytes+1))
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &FetchError{Code: "download_timeout", Err: err}
		}
		return nil, &FetchError{Code: "network_error", Err: err}
	}
	if n > MaxBytes {
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
		Type:     "image",
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

// deriveFilename returns the basename to save plus a flag indicating whether
// the extension was forced by the response Content-Type instead of preserved
// from the URL.
func deriveFilename(parsedURL *url.URL, mappedExt string) (string, bool) {
	base := path.Base(parsedURL.Path)
	if decoded, err := url.PathUnescape(base); err == nil {
		base = decoded
	}
	base = sanitizeFilename(base)

	stem := strings.TrimSuffix(base, path.Ext(base))
	if stem == "" || stem == "." || stem == ".." {
		return "image" + mappedExt, false
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
