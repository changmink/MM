package urlfetch

import (
	"errors"
	"net/url"
	"testing"
)

func TestIsHLSResponse(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		urlPath     string
		want        bool
	}{
		{"vnd.apple.mpegurl", "application/vnd.apple.mpegurl", "/stream", true},
		{"x-mpegurl", "application/x-mpegurl", "/stream", true},
		{"audio/mpegurl legacy", "audio/mpegurl", "/stream", true},
		{"audio/x-mpegurl legacy", "audio/x-mpegurl", "/stream", true},
		{"case insensitive CT", "APPLICATION/VND.APPLE.MPEGURL", "/stream", true},
		{"CT with parameters", "application/vnd.apple.mpegurl; charset=utf-8", "/stream", true},
		{"m3u8 ext + text/plain", "text/plain", "/path/stream.m3u8", true},
		{"m3u8 ext + octet-stream", "application/octet-stream", "/path/stream.m3u8", true},
		{"m3u8 ext + empty CT", "", "/path/stream.m3u8", true},
		{"uppercase .M3U8 ext + text/plain", "text/plain", "/path/stream.M3U8", true},
		{"m3u8 ext + unrelated CT is NOT fallback", "video/mp4", "/path/stream.m3u8", false},
		{"non-m3u8 + text/plain", "text/plain", "/page.html", false},
		{"non-m3u8 + unknown CT", "application/foo", "/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isHLSResponse(tc.contentType, tc.urlPath); got != tc.want {
				t.Errorf("isHLSResponse(%q, %q) = %v, want %v",
					tc.contentType, tc.urlPath, got, tc.want)
			}
		})
	}
}

func TestParseMasterPlaylist_SelectsHighestBandwidth(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=500000,RESOLUTION=640x360
low.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=1280x720
high.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1000000,RESOLUTION=854x480
mid.m3u8
`)
	base, _ := url.Parse("https://cdn.example.com/a/master.m3u8")
	got, err := parseMasterPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMasterPlaylist: %v", err)
	}
	want := "https://cdn.example.com/a/high.m3u8"
	if got.String() != want {
		t.Errorf("got %q, want %q", got.String(), want)
	}
}

func TestParseMasterPlaylist_TieBreakingFirstWins(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000000
first.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=1000000
second.m3u8
`)
	base, _ := url.Parse("https://cdn.example.com/master.m3u8")
	got, err := parseMasterPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMasterPlaylist: %v", err)
	}
	if got.String() != "https://cdn.example.com/first.m3u8" {
		t.Errorf("tie-breaking failed: got %q", got.String())
	}
}

func TestParseMasterPlaylist_MissingBandwidthLowestPriority(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-STREAM-INF:RESOLUTION=640x360
nobandwidth.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=100
tiny.m3u8
`)
	base, _ := url.Parse("https://cdn.example.com/master.m3u8")
	got, err := parseMasterPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMasterPlaylist: %v", err)
	}
	// tiny.m3u8 (BANDWIDTH=100) should beat nobandwidth.m3u8 (treated as 0)
	if got.String() != "https://cdn.example.com/tiny.m3u8" {
		t.Errorf("missing-bandwidth should lose: got %q", got.String())
	}
}

func TestParseMasterPlaylist_ResolvesRelativeVariant(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000000
720p/index.m3u8
`)
	base, _ := url.Parse("https://cdn.example.com/a/b/master.m3u8")
	got, err := parseMasterPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMasterPlaylist: %v", err)
	}
	want := "https://cdn.example.com/a/b/720p/index.m3u8"
	if got.String() != want {
		t.Errorf("got %q, want %q", got.String(), want)
	}
}

func TestParseMasterPlaylist_KeepsAbsoluteVariant(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000000
https://other.cdn.net/abs/variant.m3u8
`)
	base, _ := url.Parse("https://cdn.example.com/master.m3u8")
	got, err := parseMasterPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMasterPlaylist: %v", err)
	}
	if got.String() != "https://other.cdn.net/abs/variant.m3u8" {
		t.Errorf("absolute variant not preserved: %q", got.String())
	}
}

func TestParseMasterPlaylist_MediaPlaylistReturnsBase(t *testing.T) {
	// No #EXT-X-STREAM-INF = this is already a media playlist.
	body := []byte(`#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:6
#EXTINF:6.0,
seg0.ts
#EXTINF:6.0,
seg1.ts
#EXT-X-ENDLIST
`)
	base, _ := url.Parse("https://cdn.example.com/media.m3u8")
	got, err := parseMasterPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMasterPlaylist: %v", err)
	}
	if got != base {
		t.Errorf("media playlist should return base; got %q", got.String())
	}
}

func TestParseMasterPlaylist_RejectsFileScheme(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000000
file:///etc/passwd
`)
	base, _ := url.Parse("https://cdn.example.com/master.m3u8")
	_, err := parseMasterPlaylist(body, base)
	if !errors.Is(err, errHLSVariantScheme) {
		t.Errorf("got %v, want errHLSVariantScheme", err)
	}
}

func TestParseMasterPlaylist_EmptyBodyReturnsBase(t *testing.T) {
	base, _ := url.Parse("https://cdn.example.com/playlist.m3u8")
	got, err := parseMasterPlaylist(nil, base)
	if err != nil {
		t.Fatalf("parseMasterPlaylist: %v", err)
	}
	if got != base {
		t.Errorf("empty body should return base")
	}
}

func TestParseMasterPlaylist_SkipsCommentsBeforeVariant(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=500000
# a stray comment line
low.m3u8
`)
	base, _ := url.Parse("https://cdn.example.com/master.m3u8")
	got, err := parseMasterPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMasterPlaylist: %v", err)
	}
	if got.String() != "https://cdn.example.com/low.m3u8" {
		t.Errorf("got %q", got.String())
	}
}
