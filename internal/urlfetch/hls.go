package urlfetch

import (
	"errors"
	"mime"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Sentinel errors for HLS handling. Fetch maps these to user-facing FetchError
// codes (hls_playlist_too_large / invalid_scheme / ffmpeg_error).
var (
	errHLSPlaylistTooLarge = errors.New("hls_playlist_too_large")
	errHLSVariantScheme    = errors.New("invalid_scheme")
)

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
