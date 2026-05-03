package hls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"file_server/internal/ffmpeg"
)

// Result describes a successful HLS import.
type Result struct {
	URL      string
	Path     string
	Name     string
	Size     int64
	Type     string
	Warnings []string
}

// FetchError is the typed failure returned by Fetch.
type FetchError struct {
	Code string
	Err  error
}

func (e *FetchError) Error() string { return e.Code }
func (e *FetchError) Unwrap() error { return e.Err }

// Callbacks lets a caller observe an HLS fetch in flight.
type Callbacks struct {
	Start    func(name string, total int64, fileType string)
	Progress func(received int64)
}

// Deps carries parent-package helpers into the HLS subpackage without creating
// an import cycle.
type Deps struct {
	ClassifyHTTPError func(error) *FetchError
	RenameUnique      func(tmpPath, destDir, name string) (string, bool, error)
	SanitizeFilename  func(string) string
}

const (
	progressByteThreshold = 1 << 20
	progressTimeThreshold = 250 * time.Millisecond
)

// Sentinel errors for HLS handling. classifyHLSRemuxError maps these plus
// context.Canceled / context.DeadlineExceeded to stable FetchError.Code values
// documented in SPEC §5.1.
var (
	errHLSVariantScheme    = errors.New("invalid_scheme")
	errFFmpegMissing       = ffmpeg.ErrMissing
	errHLSTooLarge         = errors.New("hls_too_large")
	errHLSTooManySegments  = errors.New("hls_too_many_segments")
	errHLSTooManyKeys      = errors.New("hls_too_many_keys")
	errHLSTooManyInits     = errors.New("hls_too_many_inits")
	errHLSDuplicateURIAttr = errors.New("hls_duplicate_uri_attr")
	errHLSMissingMapURI    = errors.New("hls_map_missing_uri")
)

// hlsMaxSegments caps how many #EXTINF segments a single media playlist may
// declare. 10,000 ??16 hours of 6-second VOD ??comfortably above any normal
// movie or lecture, but below an attacker's "1 byte × millions" request-rate
// flood that the cumulative byte cap (url_import_max_bytes) cannot stop on
// its own. See spec §3.2 D-8.
const MaxSegments = 10000

const hlsMaxSegments = MaxSegments

// hlsMaxKeyEntries caps the number of #EXT-X-KEY rotations a single media
// playlist may declare. Real-world HLS rarely rotates keys more than a few
// times per stream; 256 is generous (~25 minutes of 6-second segments under
// 1-segment-per-key rotation). The cap closes the budget-exhaustion vector
// where a hostile playlist declares thousands of keys, each up to
// hlsMaxKeyBytes (64 KiB), to drain url_import_max_bytes before any real
// segment fires.
const hlsMaxKeyEntries = 256

// hlsMaxInitEntries caps the number of #EXT-X-MAP init segments. Standard
// HLS uses at most one (per discontinuity, rarely). 4 leaves room for
// pathological-but-possible playlists with multiple discontinuities while
// blocking the same byte-budget exhaustion class as hlsMaxKeyEntries.
const hlsMaxInitEntries = 4

// ffmpegExitError wraps a non-zero ffmpeg termination with captured stderr so
// the caller can surface diagnostic context in logs.
type ffmpegExitError struct {
	exitCode int
	stderr   string
}

func (e *ffmpegExitError) Error() string {
	return fmt.Sprintf("ffmpeg exited %d: %s", e.exitCode, e.stderr)
}

// hlsWatchInterval is how often the runner checks the tmp output file while
// ffmpeg is running. 500 ms keeps progress samples timely for humans watching
// the SSE feed without wasting syscalls on an idle remux.
const hlsWatchInterval = 500 * time.Millisecond

// hlsMaxPlaylistBytes bounds how much of the initial response body we read to
// parse the master playlist. Real-world master playlists are a few KiB; 1 MiB
// is a generous defense-in-depth ceiling that still lets us fit in memory.
const MaxPlaylistBytes = 1 << 20

const hlsMaxPlaylistBytes = MaxPlaylistBytes

// isHLSResponse decides whether to take the HLS branch. The primary signal is
// a canonical HLS Content-Type. "audio/mpegurl" is the pre-RFC-8216 legacy
// form still emitted by several real-world CDNs (Mux test streams on GCS,
// some Akamai configs) ??treating it as HLS avoids a false
// unsupported_content_type for valid public streams. The fallback covers
// CDNs that mislabel .m3u8 as text/plain or application/octet-stream; it
// only applies when the URL path clearly names a playlist so a generic text
// response from an unrelated URL does not get miscategorized.
func IsResponse(contentType, urlPath string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	mt = strings.ToLower(mt)
	switch mt {
	case "application/vnd.apple.mpegurl",
		"application/x-mpegurl",
		"audio/mpegurl",
		"audio/x-mpegurl":
		return true
	}
	if !strings.HasSuffix(strings.ToLower(urlPath), ".m3u8") {
		return false
	}
	switch mt {
	case "", "text/plain", "application/octet-stream":
		return true
	}
	return false
}

func isHLSResponse(contentType, urlPath string) bool {
	return IsResponse(contentType, urlPath)
}

var bandwidthRE = regexp.MustCompile(`BANDWIDTH=(\d+)`)

// parseMasterPlaylist inspects an HLS playlist body and returns the URL to
// hand to ffmpeg. If the body is a master playlist (contains one or more
// #EXT-X-STREAM-INF entries), it selects the variant with the highest
// BANDWIDTH attribute, ties broken by declaration order. If no variants are
// found, the body is treated as a media playlist and base is returned
// unchanged. Relative variant URLs are resolved against base; variants whose
// resolved scheme is not http/https are rejected up front so ffmpeg's
// protocol_whitelist is backed by an application-level check too.
func parseMasterPlaylist(body []byte, base *url.URL) (*url.URL, error) {
	lines := strings.Split(string(body), "\n")

	var bestURL string
	var bestBW int64 = -1 // -1 so the first variant (even BANDWIDTH=0) is chosen

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			continue
		}
		bw := extractBandwidth(line)
		variantLine := ""
		for j := i + 1; j < len(lines); j++ {
			cand := strings.TrimSpace(lines[j])
			if cand == "" || strings.HasPrefix(cand, "#") {
				continue
			}
			variantLine = cand
			i = j
			break
		}
		if variantLine == "" {
			continue
		}
		if bw > bestBW {
			bestBW = bw
			bestURL = variantLine
		}
	}

	if bestURL == "" {
		return base, nil
	}

	parsed, err := url.Parse(bestURL)
	if err != nil {
		return nil, err
	}
	resolved := base.ResolveReference(parsed)
	scheme := strings.ToLower(resolved.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errHLSVariantScheme
	}
	// Guard against a (hostile or broken) master playlist whose chosen variant
	// resolves back to itself: handing the same URL to ffmpeg would loop
	// through the same master again. Fall back to treating it as a media
	// playlist ??ffmpeg will then either succeed if it is one, or fail with
	// ffmpeg_error, which is the right outcome either way.
	if sameURL(resolved, base) {
		return base, nil
	}
	return resolved, nil
}

// entryKind tags playlistEntry by its source tag ??needed by materializeHLS to
// decide naming convention (seg_NNNN.ext vs key_N.bin vs init.ext) and what
// kind of URI rewrite to perform.
type entryKind int

const (
	entrySegment entryKind = iota
	entryKey
	entryInit
)

// playlistEntry represents one remote resource referenced by a media playlist.
// lineIdx points at the rawLines element materializeHLS should rewrite ??for
// segments that's the URI line, for #EXT-X-KEY / #EXT-X-MAP it's the tag line
// itself (the URI is an attribute embedded in the tag).
type playlistEntry struct {
	lineIdx int
	uri     *url.URL
	kind    entryKind
}

// mediaPlaylist is the parsed view of a media playlist. rawLines preserves
// the input verbatim so materializeHLS can output a near-identical playlist
// with only URI substrings replaced; entries enumerates every external
// resource that needs to be downloaded and rewritten before ffmpeg consumes
// the rewritten playlist.
type mediaPlaylist struct {
	rawLines []string
	entries  []playlistEntry
}

// uriAttrRE extracts the value from a URI="..." attribute used by
// #EXT-X-KEY and #EXT-X-MAP. Real HLS attribute lists are CSV with quoted
// strings and unquoted enumerations; we only need URI which is always
// quoted per RFC 8216 §4.2.
var uriAttrRE = regexp.MustCompile(`URI="([^"]*)"`)

// parseMediaPlaylist walks the playlist body and collects every external
// resource (#EXTINF segments, #EXT-X-KEY URIs except METHOD=NONE, and
// #EXT-X-MAP init segments) with its URL resolved against base. Returns
//   - errHLSVariantScheme for any URI whose resolved scheme is not http/https
//   - errHLSTooManySegments / errHLSTooManyKeys / errHLSTooManyInits past caps
//   - errHLSDuplicateURIAttr if a single #EXT-X-KEY/#EXT-X-MAP line declares
//     more than one URI="..." attribute (parser took the first; rewriter
//     would touch all ??refuse the playlist to keep the two in lockstep)
//   - errHLSMissingMapURI for an #EXT-X-MAP without URI
//
// Per RFC 8216 §4.1.1, between #EXTINF and the segment URI line a media
// playlist may insert helper tags such as #EXT-X-DISCONTINUITY,
// #EXT-X-BYTERANGE, or #EXT-X-PROGRAM-DATE-TIME. The pendingSeg latch keeps
// state across those (any line starting with `#` is preserved verbatim and
// does not consume the latch).
//
// Empty / comment-only bodies return a playlist with no entries (no error)
// ??fetchHLS will treat that as a degenerate stream and let ffmpeg fail
// naturally.
func parseMediaPlaylist(body []byte, base *url.URL) (*mediaPlaylist, error) {
	rawLines := splitPlaylistLines(body)
	pl := &mediaPlaylist{rawLines: rawLines}

	// State: have we just seen #EXTINF? Then the next non-comment, non-blank
	// line is the segment URI for that segment.
	pendingSeg := false
	segCount := 0
	keyCount := 0
	initCount := 0

	for i, line := range rawLines {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "#EXTINF"):
			pendingSeg = true
		case strings.HasPrefix(trim, "#EXT-X-KEY"):
			if strings.Count(trim, `URI="`) > 1 {
				return nil, errHLSDuplicateURIAttr
			}
			uriStr := uriAttrValue(trim)
			if uriStr == "" {
				// METHOD=NONE has no URI ??nothing to download. Other tags
				// without URI also fall through (defensive).
				continue
			}
			entry, err := makePlaylistEntry(uriStr, base, i, entryKey)
			if err != nil {
				return nil, err
			}
			pl.entries = append(pl.entries, entry)
			keyCount++
			if keyCount > hlsMaxKeyEntries {
				return nil, errHLSTooManyKeys
			}
		case strings.HasPrefix(trim, "#EXT-X-MAP"):
			if strings.Count(trim, `URI="`) > 1 {
				return nil, errHLSDuplicateURIAttr
			}
			uriStr := uriAttrValue(trim)
			if uriStr == "" {
				return nil, errHLSMissingMapURI
			}
			entry, err := makePlaylistEntry(uriStr, base, i, entryInit)
			if err != nil {
				return nil, err
			}
			pl.entries = append(pl.entries, entry)
			initCount++
			if initCount > hlsMaxInitEntries {
				return nil, errHLSTooManyInits
			}
		case strings.HasPrefix(trim, "#"):
			// Other tag (#EXTM3U, #EXT-X-VERSION, #EXT-X-BYTERANGE, etc.) ??			// preserved in rawLines, no entry created. materializeHLS's
			// rewrite pass normalizes any URI="..." attribute here to "" so
			// unrecognized tags can never carry a remote URL into ffmpeg's
			// input even if a future ffmpeg whitelist relaxation occurred.
		case trim == "":
			// Blank line ??preserved in rawLines, no entry.
		default:
			// Non-comment, non-blank line. If a segment is pending, this is
			// the segment URI. Otherwise treat as orphan and ignore ??could
			// be a continuation of an unknown tag.
			if !pendingSeg {
				continue
			}
			entry, err := makePlaylistEntry(trim, base, i, entrySegment)
			if err != nil {
				return nil, err
			}
			pl.entries = append(pl.entries, entry)
			pendingSeg = false
			segCount++
			if segCount > hlsMaxSegments {
				return nil, errHLSTooManySegments
			}
		}
	}

	return pl, nil
}

func makePlaylistEntry(uriStr string, base *url.URL, lineIdx int, kind entryKind) (playlistEntry, error) {
	parsed, err := url.Parse(uriStr)
	if err != nil {
		return playlistEntry{}, err
	}
	resolved := base.ResolveReference(parsed)
	scheme := strings.ToLower(resolved.Scheme)
	if scheme != "http" && scheme != "https" {
		return playlistEntry{}, errHLSVariantScheme
	}
	return playlistEntry{lineIdx: lineIdx, uri: resolved, kind: kind}, nil
}

// uriAttrValue extracts the URI attribute value from an #EXT-X-KEY or
// #EXT-X-MAP tag line. Returns "" if URI is absent.
func uriAttrValue(tagLine string) string {
	m := uriAttrRE.FindStringSubmatch(tagLine)
	if m == nil {
		return ""
	}
	return m[1]
}

// splitPlaylistLines normalizes CRLF ??LF and splits on LF, preserving every
// line (including the trailing empty one when the body ends with a newline).
// Used by parseMediaPlaylist so rawLines indices match the original byte
// layout for materializeHLS rewrite.
func splitPlaylistLines(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	return strings.Split(normalized, "\n")
}

// sameURL compares two URLs by scheme/host/path ??query/fragment ignored ??so
// a variant link with a differing token still counts as the same endpoint for
// loop detection.
func sameURL(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Host, b.Host) &&
		a.Path == b.Path
}

func extractBandwidth(line string) int64 {
	m := bandwidthRE.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	bw, _ := strconv.ParseInt(m[1], 10, 64)
	return bw
}

// runFfmpeg is the swappable entry point that runHLSRemux invokes to spawn
// ffmpeg. Tests replace this with a capture-only stub to verify argv
// invariants (AC-10 / AC-11 in spec §4) without launching a real binary.
// Production uses defaultRunFfmpeg. Replacement contract: the implementation
// must honor ctx (kill the child on cancel) and write any process stderr
// into the supplied io.Writer for log surfacing.
//
// Concurrency note: runFfmpeg is a package-level var; tests that swap it
// MUST NOT use t.Parallel() ??code review enforces this rather than a hard
// runtime guard.
var runFfmpeg = defaultRunFfmpeg

func SetRunFfmpegForTest(fn func(context.Context, []string, io.Writer) error) func() {
	orig := runFfmpeg
	runFfmpeg = fn
	return func() { runFfmpeg = orig }
}

// defaultRunFfmpeg surfaces errFFmpegMissing when the binary is absent so that
// runHLSRemux can short-circuit at the same place ??this also lets test swaps
// bypass the LookPath check entirely (no ffmpeg needed for argv invariant
// tests).
func defaultRunFfmpeg(ctx context.Context, args []string, stderr io.Writer) error {
	return ffmpeg.RunWithStderr(ctx, stderr, args...)
}

// runHLSRemux spawns ffmpeg to remux a local HLS playlist (with all segment
// and key files already materialized into the same directory by
// materializeHLS) into a single MP4 at outputPath. Output is capped at
// maxOutputBytes: a watcher polls the output file size every hlsWatchInterval
// and cancels the ffmpeg ctx if the cap is exceeded. Context cancellation
// also kills ffmpeg via the child ctx that runFfmpeg honors. If cb.Progress
// is non-nil, the watcher reports the output file's current size using the
// same throttling rules as progressReader (byte OR time threshold).
//
// Security: ffmpeg is launched with -protocol_whitelist file,crypto and
// -allowed_extensions ALL ??local file reads only, no network access. This
// is the core invariant that closes the HLS DNS rebinding window: ffmpeg
// can't perform its own hostname resolution because the input is a fully
// local playlist and its referenced segments / keys are local files. argv
// invariant tests (AC-10 / AC-11) lock this contract.
//
// Returns one of: nil on exit 0; errHLSTooLarge if the cap was breached;
// ctx.Err() on external cancel or deadline; *ffmpegExitError on non-zero exit
// with stderr captured; errFFmpegMissing if the ffmpeg binary is not on PATH.
// classifyHLSRemuxError translates these to public FetchError.Code values.
//
// Note on observability in practice: ffmpeg's MP4 muxer buffers packets until
// it can finalize headers, so for small remuxes (under a few hundred KiB of
// mdat) the output file may only appear near end-of-input and the watcher
// will not sample any intermediate sizes. For real HLS VOD (minutes of
// video) the buffer does flush periodically and the watcher behaves as
// documented.
func runHLSRemux(ctx context.Context, localPlaylistPath, outputPath string, cb *Callbacks, maxOutputBytes int64) error {
	// -protocol_whitelist file,crypto: ffmpeg may only open local files
	// (segments / keys / init segments materializeHLS staged) and use its
	// AES decryption layer for #EXT-X-KEY. All network protocols are
	// removed ??there is no way for ffmpeg to perform a DNS lookup or
	// network fetch from inside this invocation.
	// -allowed_extensions ALL: segments and init files keep their original
	// extension (.m4s, .vtt, .aac, ?? under materializeHLS's whitelist
	// scheme. ffmpeg's default extension allowlist is too narrow for some
	// containers, so we widen it ??safe because every input path is a
	// local file we just wrote.
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file,crypto",
		"-allowed_extensions", "ALL",
		"-i", localPlaylistPath,
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-f", "mp4",
		"-movflags", "+faststart",
		"-y", outputPath,
	}

	// ffmpegCtx is a child of ctx so external cancel/timeout still propagates
	// to the process. The watcher cancels via cancelFfmpeg() on size-cap
	// breach ??that path also routes through ctx, so runFfmpeg only ever
	// terminates ffmpeg through its supplied context (no out-of-band Kill).
	ffmpegCtx, cancelFfmpeg := context.WithCancel(ctx)
	defer cancelFfmpeg()

	var stderr bytes.Buffer

	// watchCtx is decoupled from parent ctx: we want the watcher to keep
	// polling until we explicitly cancel it after runFfmpeg returns, so a
	// client-initiated ctx cancel does not stop the final size sample from
	// landing.
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	var sizeExceeded atomic.Bool
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watchOutputFile(watchCtx, outputPath, hlsWatchInterval, maxOutputBytes, cb, func() {
			sizeExceeded.Store(true)
			cancelFfmpeg()
		})
	}()

	waitErr := runFfmpeg(ffmpegCtx, args, &stderr)
	cancelWatch()
	<-watchDone

	// errFFmpegMissing is a configuration error and must surface ahead of
	// the watcher / ctx checks (those only matter once the process is up).
	if errors.Is(waitErr, errFFmpegMissing) {
		return errFFmpegMissing
	}
	if sizeExceeded.Load() {
		return errHLSTooLarge
	}
	if ctx.Err() != nil {
		// External cancel or deadline beat the size watcher.
		return ctx.Err()
	}
	if waitErr != nil {
		exitCode := -1
		stderrText := strings.TrimSpace(stderr.String())
		var ffErr *ffmpeg.ExitError
		if errors.As(waitErr, &ffErr) {
			exitCode = ffErr.ExitCode
			stderrText = ffErr.Stderr
		}
		return &ffmpegExitError{exitCode: exitCode, stderr: stderrText}
	}
	return nil
}

// fetchHLS turns an HLS response into a remuxed MP4 entirely without exposing
// origin URLs to ffmpeg. The flow (spec §3.1):
//
//  1. Read master playlist body from the already-issued response (1 MiB cap).
//  2. parseMasterPlaylist ??variantURL.
//  3. If variantURL ??master URL, fetch the variant playlist body via the
//     protected client (IP-pin + DNS validation per request).
//  4. parseMediaPlaylist on the variant body ??segment / key / init entries.
//  5. Create destDir/.urlimport-hls-<random>/ as a self-contained workspace.
//  6. materializeHLS downloads every segment / key / init through the same
//     protected client and writes a rewritten playlist with local-only URIs.
//  7. runHLSRemux invokes ffmpeg on that local playlist (-protocol_whitelist
//     file,crypto). ffmpeg never performs DNS ??DNS rebinding is closed.
//  8. Atomic rename the output MP4 into destDir; defer cleanup wipes the
//     working directory.
//
// All errors map to public FetchError.Code values via classifyHTTPError /
// classifyHLSRemuxError / classifyMaterializeError. Cumulative byte cap
// (maxBytes) is shared between segment downloads and ffmpeg output via a
// single atomic.Int64 counter.
func Fetch(
	ctx context.Context,
	client *http.Client,
	resp *http.Response,
	parsed *url.URL,
	rawURL, destDir, relDir string,
	warnings []string,
	maxBytes int64,
	cb *Callbacks,
	deps Deps,
) (*Result, *FetchError) {
	if deps.ClassifyHTTPError == nil || deps.RenameUnique == nil || deps.SanitizeFilename == nil {
		return nil, &FetchError{Code: "ffmpeg_error", Err: errors.New("missing HLS dependencies")}
	}
	masterBody, err := io.ReadAll(io.LimitReader(resp.Body, hlsMaxPlaylistBytes+1))
	if err != nil {
		return nil, &FetchError{Code: "network_error", Err: err}
	}
	if int64(len(masterBody)) > hlsMaxPlaylistBytes {
		return nil, &FetchError{Code: "hls_playlist_too_large"}
	}
	// Close eagerly so we do not hold the TCP connection open during the
	// variant playlist + segments fetches.
	_ = resp.Body.Close()

	variantURL, err := parseMasterPlaylist(masterBody, parsed)
	if err != nil {
		if errors.Is(err, errHLSVariantScheme) {
			return nil, &FetchError{Code: "invalid_scheme", Err: err}
		}
		return nil, &FetchError{Code: "ffmpeg_error", Err: err}
	}

	// Variant body source: the master itself if parseMasterPlaylist returned
	// the original (no #EXT-X-STREAM-INF), or a fresh fetch via the protected
	// client otherwise. The fetch path is what makes DNS rebinding-safe ??the
	// client's publicOnlyDialContext re-resolves and IP-pins per request.
	var variantBody []byte
	var variantBase *url.URL
	if sameURL(variantURL, parsed) {
		variantBody = masterBody
		variantBase = parsed
	} else {
		body, ferr := fetchPlaylistBody(ctx, client, variantURL.String(), deps)
		if ferr != nil {
			return nil, ferr
		}
		variantBody = body
		variantBase = variantURL
	}

	pl, err := parseMediaPlaylist(variantBody, variantBase)
	if err != nil {
		return nil, classifyMediaPlaylistError(err)
	}

	// Workspace lives inside destDir so atomic rename of the final MP4 stays
	// on the same filesystem (no EXDEV) and so browse's dot-prefix filter
	// hides the directory automatically. RemoveAll runs unconditionally ??	// success / failure / panic all converge on cleanup.
	hlsTempDir, err := os.MkdirTemp(destDir, ".urlimport-hls-*")
	if err != nil {
		return nil, &FetchError{Code: "write_error", Err: err}
	}
	defer os.RemoveAll(hlsTempDir)

	// Single cumulative counter shared by segment downloads and ffmpeg
	// output ??spec D-9. atomic.Int64 keeps it safe under any future
	// parallelization of segment fetches.
	remaining := atomic.Int64{}
	remaining.Store(maxBytes)

	// Wrap progress callbacks so the materialize phase (Phase 1: segment
	// bytes) and the remux phase (Phase 2: output MP4 bytes) emit a single
	// monotonically increasing counter ??spec D-4. Phase 2 emits are
	// offset by Phase 1's total.
	//
	// Concurrency contract: phase1Total is written exactly once after
	// materializeHLS returns (line below) and read by wrappedCb.Progress
	// thereafter. This is safe ONLY because materializeHLS calls
	// cb.Progress synchronously from its own goroutine and runHLSRemux's
	// watcher does not start firing Progress until materializeHLS has
	// returned. If a future change introduces concurrent Progress emit
	// from materializeHLS (e.g. parallel segment downloads), this closure
	// must be replaced with an atomic.Int64 read of the running total ??	// the captured-by-reference `phase1Total` would otherwise produce a
	// torn read.
	var phase1Total int64
	wrappedCb := cb
	if cb != nil {
		original := cb
		wrappedCb = &Callbacks{
			Start: original.Start,
		}
		if original.Progress != nil {
			wrappedCb.Progress = func(n int64) {
				original.Progress(phase1Total + n)
			}
		}
	}

	localPlaylistPath, totalDownloaded, mErr := materializeHLS(ctx, client, pl, hlsTempDir, &remaining, wrappedCb)
	if mErr != nil {
		return nil, classifyMaterializeError(mErr, deps)
	}
	phase1Total = totalDownloaded

	name := deriveHLSFilename(parsed, deps)
	// Extension is always forced to .mp4 (we remux away from .m3u8).
	warnings = append(warnings, "extension_replaced")

	if cb != nil && cb.Start != nil {
		cb.Start(name, 0, "video")
	}

	outputPath := filepath.Join(hlsTempDir, "output.mp4")
	if err := runHLSRemux(ctx, localPlaylistPath, outputPath, wrappedCb, remaining.Load()); err != nil {
		return nil, classifyHLSRemuxError(err)
	}

	stat, err := os.Stat(outputPath)
	if err != nil {
		return nil, &FetchError{Code: "write_error", Err: err}
	}

	finalName, didRename, err := deps.RenameUnique(outputPath, destDir, name)
	if err != nil {
		return nil, &FetchError{Code: "write_error", Err: err}
	}
	if didRename {
		warnings = append(warnings, "renamed")
	}

	return &Result{
		URL:      rawURL,
		Path:     path.Join(relDir, finalName),
		Name:     finalName,
		Size:     stat.Size(),
		Type:     "video",
		Warnings: warnings,
	}, nil
}

// fetchPlaylistBody GETs a playlist URL through the protected client and
// returns its body capped at hlsMaxPlaylistBytes. Errors map onto stable
// FetchError codes so the caller can surface them in the SSE error frame
// without wrapping again.
func fetchPlaylistBody(ctx context.Context, client *http.Client, urlStr string, deps Deps) ([]byte, *FetchError) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, &FetchError{Code: "invalid_url", Err: err}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, deps.ClassifyHTTPError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &FetchError{Code: "http_error", Err: fmt.Errorf("http %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, hlsMaxPlaylistBytes+1))
	if err != nil {
		return nil, &FetchError{Code: "network_error", Err: err}
	}
	if int64(len(body)) > hlsMaxPlaylistBytes {
		return nil, &FetchError{Code: "hls_playlist_too_large"}
	}
	return body, nil
}

// classifyMediaPlaylistError maps parseMediaPlaylist sentinels to public
// FetchError codes. The three "too many" caps share a single wire code
// (hls_too_many_segments) ??operators can grep server logs for the
// underlying sentinel name to distinguish segment / key / init flooding.
// Defaults to ffmpeg_error for unrecognized parser issues (defensive ??// keeps the wire contract narrow).
func classifyMediaPlaylistError(err error) *FetchError {
	switch {
	case errors.Is(err, errHLSVariantScheme):
		return &FetchError{Code: "invalid_scheme", Err: err}
	case errors.Is(err, errHLSTooManySegments),
		errors.Is(err, errHLSTooManyKeys),
		errors.Is(err, errHLSTooManyInits):
		return &FetchError{Code: "hls_too_many_segments", Err: err}
	default:
		return &FetchError{Code: "ffmpeg_error", Err: err}
	}
}

// classifyMaterializeError maps materializeHLS / downloadOne errors to public
// FetchError codes. errHLSTooLarge surfaces as "too_large"; ctx errors map
// to download_timeout / network_error; anything else flows through
// classifyHTTPError so dial / TLS / private_network / http_error stay stable.
func classifyMaterializeError(err error, deps Deps) *FetchError {
	switch {
	case errors.Is(err, errHLSTooLarge):
		return &FetchError{Code: "too_large", Err: err}
	case errors.Is(err, context.DeadlineExceeded):
		return &FetchError{Code: "download_timeout", Err: err}
	case errors.Is(err, context.Canceled):
		return &FetchError{Code: "network_error", Err: err}
	default:
		return deps.ClassifyHTTPError(err)
	}
}

// deriveHLSFilename strips the URL's last path segment of its extension and
// appends .mp4. Empty / "." / ".." basenames fall back to "video.mp4" so the
// remuxed output always has a sensible filename.
func DeriveFilename(parsed *url.URL, deps Deps) string {
	base := path.Base(parsed.Path)
	if decoded, err := url.PathUnescape(base); err == nil {
		base = decoded
	}
	base = deps.SanitizeFilename(base)
	stem := strings.TrimSuffix(base, path.Ext(base))
	if stem == "" || stem == "." || stem == ".." {
		return "video.mp4"
	}
	return stem + ".mp4"
}

func deriveHLSFilename(parsed *url.URL, deps Deps) string {
	return DeriveFilename(parsed, deps)
}

// classifyHLSRemuxError maps runHLSRemux sentinels to public FetchError codes.
// ctx.Err() is checked first so cancels/timeouts surface correctly even when
// ffmpeg returns a non-zero exit alongside a cancellation. ffmpeg_missing is
// a distinct code from ffmpeg_error: the former is a server-side
// misconfiguration (operator should install ffmpeg), the latter is a stream
// or input failure the user can do nothing about on their side.
func classifyHLSRemuxError(err error) *FetchError {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &FetchError{Code: "download_timeout", Err: err}
	case errors.Is(err, context.Canceled):
		return &FetchError{Code: "network_error", Err: err}
	case errors.Is(err, errHLSTooLarge):
		return &FetchError{Code: "too_large", Err: err}
	case errors.Is(err, errFFmpegMissing):
		return &FetchError{Code: "ffmpeg_missing", Err: err}
	default:
		// Includes *ffmpegExitError and any other ffmpeg-layer failure.
		return &FetchError{Code: "ffmpeg_error", Err: err}
	}
}

// watchOutputFile polls tmpPath for growth until ctx cancels. When the file
// exceeds maxBytes, calls onOversize once and returns. Otherwise forwards
// every observed size change through cb.Progress, throttled by the same
// byte/time thresholds as progressReader.
//
// Extracted from runHLSRemux so the polling contract can be tested against a
// controlled growing file without needing ffmpeg's buffered output behavior.
func watchOutputFile(
	ctx context.Context,
	tmpPath string,
	interval time.Duration,
	maxBytes int64,
	cb *Callbacks,
	onOversize func(),
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastReported int64
	lastEmit := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fi, err := os.Stat(tmpPath)
			if err != nil {
				continue
			}
			size := fi.Size()
			if size > maxBytes {
				onOversize()
				return
			}
			if size == lastReported {
				continue
			}
			if cb != nil && cb.Progress != nil {
				now := time.Now()
				delta := size - lastReported
				if delta >= progressByteThreshold || now.Sub(lastEmit) >= progressTimeThreshold {
					cb.Progress(size)
					lastReported = size
					lastEmit = now
				}
			}
		}
	}
}
