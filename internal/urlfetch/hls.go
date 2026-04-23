package urlfetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Sentinel errors for HLS handling. Fetch maps these to user-facing FetchError
// codes (hls_playlist_too_large / invalid_scheme / ffmpeg_error / too_large).
var (
	errHLSPlaylistTooLarge = errors.New("hls_playlist_too_large")
	errHLSVariantScheme    = errors.New("invalid_scheme")
	errFFmpegMissing       = errors.New("ffmpeg_missing")
	errHLSTooLarge         = errors.New("hls_too_large")
)

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
const hlsMaxPlaylistBytes = 1 << 20

// isHLSResponse decides whether to take the HLS branch. The primary signal is
// a canonical HLS Content-Type. The fallback covers CDNs that mislabel .m3u8
// as text/plain or application/octet-stream — we only apply the fallback when
// the URL path clearly names a playlist, so a generic text response from an
// unrelated URL does not get miscategorized.
func isHLSResponse(contentType, urlPath string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	mt = strings.ToLower(mt)
	if mt == "application/vnd.apple.mpegurl" || mt == "application/x-mpegurl" {
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
	return resolved, nil
}

func extractBandwidth(line string) int64 {
	m := bandwidthRE.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	bw, _ := strconv.ParseInt(m[1], 10, 64)
	return bw
}

// runHLSRemux spawns ffmpeg to pull variantURL via HLS and remux its segments
// into a single MP4 at tmpPath. Output is capped at maxOutputBytes: a watcher
// polls the tmp file size every hlsWatchInterval and kills ffmpeg if the cap
// is exceeded. Context cancellation also kills ffmpeg via exec.CommandContext.
// If cb.Progress is non-nil, the watcher reports the tmp file's current size
// using the same throttling rules as progressReader (byte OR time threshold).
//
// Returns one of: nil on exit 0; errHLSTooLarge if the cap was breached;
// ctx.Err() on external cancel; *ffmpegExitError on non-zero exit with stderr
// captured; errFFmpegMissing if the binary is not on PATH.
//
// Note on observability in practice: ffmpeg's MP4 muxer buffers packets until
// it can finalize headers, so for small remuxes (under a few hundred KiB of
// mdat) the output file may only appear near end-of-input and the watcher
// will not sample any intermediate sizes. For real HLS VOD (minutes of
// video) the buffer does flush periodically and the watcher behaves as
// documented.
func runHLSRemux(ctx context.Context, variantURL, tmpPath string, cb *Callbacks, maxOutputBytes int64) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return errFFmpegMissing
	}

	// -protocol_whitelist blocks file:, rtp:, udp:, data: and other schemes
	// ffmpeg would otherwise follow from inside the playlist — essential
	// defense against SSRF/LFI via a hostile master or media playlist.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "http,https,tls,tcp,crypto",
		"-i", variantURL,
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-f", "mp4",
		"-movflags", "+faststart",
		"-y", tmpPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	// watchCtx is decoupled from ctx so the watcher survives until cmd.Wait()
	// returns — otherwise we would race with the final Stat and lose the
	// last progress sample.
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	var sizeExceeded atomic.Bool
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watchOutputFile(watchCtx, tmpPath, hlsWatchInterval, maxOutputBytes, cb, func() {
			sizeExceeded.Store(true)
			_ = cmd.Process.Kill()
		})
	}()

	waitErr := cmd.Wait()
	cancelWatch()
	<-watchDone

	if sizeExceeded.Load() {
		return errHLSTooLarge
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		exitCode := -1
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		}
		return &ffmpegExitError{exitCode: exitCode, stderr: strings.TrimSpace(stderr.String())}
	}
	return nil
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
