package urlfetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// hlsMaxKeyBytes caps the size of any AES-128 key file. Real keys are 16
// bytes (128 bits); 64 KiB is a defensive ceiling that still rejects an
// attacker stuffing arbitrary bytes through a key URI.
const hlsMaxKeyBytes = int64(64) << 10

// hlsMaxInitBytes caps the size of any #EXT-X-MAP init segment. fMP4 init
// segments are typically a few KiB. 16 MiB tolerates oddly fat container
// initialization while still bounding attacker abuse.
const hlsMaxInitBytes = int64(16) << 20

// segmentExtWhitelist gates which URL path extensions become local file
// extensions verbatim. Anything else falls through to ".bin" so ffmpeg's
// fmt sniff handles classification.
var segmentExtWhitelist = map[string]struct{}{
	".ts":  {},
	".m4s": {},
	".mp4": {},
	".aac": {},
	".m4a": {},
	".vtt": {},
}

// downloadOne fetches urlStr through client and writes the body to destPath
// (created with O_CREATE|O_EXCL — caller must guarantee destPath is unique
// within the materializeHLS temp directory). Two caps gate the transfer:
//
//   - perResourceMax > 0: rejects responses whose Content-Length exceeds the
//     cap (preflight) and aborts the read once the body grows past the cap
//     (runtime). 0 disables this check.
//   - remainingBytes: shared cumulative counter for the entire HLS materialize
//     session. Every byte read is debited; once the counter would go negative
//     the download aborts.
//
// Cap breaches return errHLSTooLarge with any partial file removed. HTTP
// errors return fmt.Errorf("http %d", status); dial / TLS / private-network
// errors propagate as-is from client.Do (the protected client surfaces
// errPrivateNetwork via *url.Error, so errors.Is(err, errPrivateNetwork)
// works at the call site).
func downloadOne(
	ctx context.Context,
	client *http.Client,
	urlStr string,
	destPath string,
	perResourceMax int64,
	remainingBytes *atomic.Int64,
) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http %d", resp.StatusCode)
	}

	// Preflight Content-Length when the server provided one. Skipping when
	// ContentLength <= 0 (chunked / unknown) is fine — runtime cap will
	// catch oversize bodies.
	if cl := resp.ContentLength; cl > 0 {
		if perResourceMax > 0 && cl > perResourceMax {
			return 0, errHLSTooLarge
		}
		if cl > remainingBytes.Load() {
			return 0, errHLSTooLarge
		}
	}

	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return 0, err
	}

	written, copyErr := copyWithCaps(f, resp.Body, perResourceMax, remainingBytes)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(destPath)
		return written, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destPath)
		return written, closeErr
	}
	return written, nil
}

// materializeHLS turns a parsed media playlist into a self-contained directory
// tree under hlsTempDir: every segment, key, and init resource is downloaded
// through client (so DNS validation and IP-pin happen for each fetch), and a
// rewritten playlist named "playlist.m3u8" is written with all URIs replaced
// by relative local file names. Returns the rewritten playlist path and total
// bytes downloaded — caller passes total to the SSE progress accounting and
// uses localPlaylistPath as the ffmpeg -i argument.
//
// Cap policy:
//   - segments share the cumulative remainingBytes counter, no per-resource cap
//   - keys: hlsMaxKeyBytes (64 KiB)
//   - init segments: hlsMaxInitBytes (16 MiB)
//
// Failure mode: any download error returns immediately with the partial total.
// Caller is responsible for removing hlsTempDir on any error path.
func materializeHLS(
	ctx context.Context,
	client *http.Client,
	pl *mediaPlaylist,
	hlsTempDir string,
	remainingBytes *atomic.Int64,
	cb *Callbacks,
) (localPlaylistPath string, totalDownloaded int64, err error) {
	if pl == nil {
		return "", 0, fmt.Errorf("nil playlist")
	}

	segIdx, keyIdx, initIdx := 0, 0, 0
	// nameByLineIdx records the local relative file name for each rewritable
	// line so the second pass can emit a faithful local-only playlist.
	nameByLineIdx := make(map[int]string, len(pl.entries))

	var lastReportedBytes int64
	lastEmit := time.Now()

	for _, e := range pl.entries {
		var (
			destName       string
			perResourceMax int64
		)
		switch e.kind {
		case entrySegment:
			destName = fmt.Sprintf("seg_%04d%s", segIdx, segmentExt(e.uri))
			segIdx++
			perResourceMax = 0 // only cumulative bound applies
		case entryKey:
			destName = fmt.Sprintf("key_%d.bin", keyIdx)
			keyIdx++
			perResourceMax = hlsMaxKeyBytes
		case entryInit:
			destName = fmt.Sprintf("init_%d%s", initIdx, segmentExt(e.uri))
			initIdx++
			perResourceMax = hlsMaxInitBytes
		default:
			return "", totalDownloaded, fmt.Errorf("unknown entry kind: %d", e.kind)
		}

		destPath := filepath.Join(hlsTempDir, destName)
		n, err := downloadOne(ctx, client, e.uri.String(), destPath, perResourceMax, remainingBytes)
		if err != nil {
			return "", totalDownloaded, err
		}
		totalDownloaded += n
		nameByLineIdx[e.lineIdx] = destName

		if cb != nil && cb.Progress != nil {
			now := time.Now()
			delta := totalDownloaded - lastReportedBytes
			if delta >= progressByteThreshold || now.Sub(lastEmit) >= progressTimeThreshold {
				cb.Progress(totalDownloaded)
				lastReportedBytes = totalDownloaded
				lastEmit = now
			}
		}
	}

	// Second pass: rewrite rawLines, replacing only lines that materialized
	// a remote resource. All other lines (#EXTM3U, #EXTINF, #EXT-X-VERSION,
	// #EXT-X-BYTERANGE, …) pass through verbatim so ffmpeg sees the same
	// playlist semantics it would have received from the origin.
	out := make([]string, len(pl.rawLines))
	for i, line := range pl.rawLines {
		if newName, ok := nameByLineIdx[i]; ok {
			out[i] = rewritePlaylistLine(line, newName)
		} else {
			out[i] = line
		}
	}

	localPlaylistPath = filepath.Join(hlsTempDir, "playlist.m3u8")
	if err := os.WriteFile(localPlaylistPath, []byte(strings.Join(out, "\n")), 0644); err != nil {
		return "", totalDownloaded, err
	}
	return localPlaylistPath, totalDownloaded, nil
}

// segmentExt picks the local file extension for a segment / init URL: keep
// the original extension if it is in the safe whitelist, otherwise ".bin"
// so ffmpeg's fmt sniff (under -allowed_extensions ALL) handles it.
func segmentExt(u *url.URL) string {
	ext := strings.ToLower(path.Ext(u.Path))
	if _, ok := segmentExtWhitelist[ext]; ok {
		return ext
	}
	return ".bin"
}

// rewritePlaylistLine substitutes the URI within an EXT-X-KEY / EXT-X-MAP
// attribute line, or replaces the entire URI line for a segment, with the
// local relative name. Leading whitespace on segment URI lines is preserved
// so ffmpeg's playlist parser sees identical structure.
func rewritePlaylistLine(line, newName string) string {
	trim := strings.TrimSpace(line)
	if strings.HasPrefix(trim, "#EXT-X-KEY") || strings.HasPrefix(trim, "#EXT-X-MAP") {
		// ReplaceAllLiteralString avoids regex backreference interpretation
		// in the replacement (newName is a local file name with no $ etc.,
		// but treating it as literal makes the contract obvious).
		return uriAttrRE.ReplaceAllLiteralString(line, fmt.Sprintf(`URI=%q`, newName))
	}
	// Segment URI line: preserve leading whitespace, replace the URI body.
	leading := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	return leading + newName
}

// copyWithCaps streams src into dst while enforcing a per-resource cap and a
// shared cumulative counter. Sequential by design — atomic.Int64 keeps it
// safe for any future parallel materializeHLS variant. On cap breach returns
// errHLSTooLarge after writing the bytes that fit; the caller removes the
// partial file.
func copyWithCaps(dst io.Writer, src io.Reader, perResourceMax int64, remaining *atomic.Int64) (int64, error) {
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if perResourceMax > 0 && written+int64(n) > perResourceMax {
				return written, errHLSTooLarge
			}
			// Check before debit so the counter cannot go negative — keeps
			// other in-flight reads' view of the counter accurate.
			if remaining.Load() < int64(n) {
				return written, errHLSTooLarge
			}
			remaining.Add(-int64(n))

			if _, werr := dst.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			return written, rerr
		}
	}
}
