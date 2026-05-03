package hls

import (
	"errors"
	"net/url"
	"strings"
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

// TestParseMasterPlaylist_VariantSelfLoopReturnsBase covers the guard that
// prevents ffmpeg from looping on a master playlist whose chosen variant URL
// resolves back to the master itself (hostile input or misconfigured CDN).
func TestParseMasterPlaylist_VariantSelfLoopReturnsBase(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000000
master.m3u8
`)
	base, _ := url.Parse("https://cdn.example.com/master.m3u8")
	got, err := parseMasterPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMasterPlaylist: %v", err)
	}
	// Must fall back to base so ffmpeg treats it as a media playlist instead
	// of chasing the same URL again.
	if got.String() != base.String() {
		t.Errorf("self-loop variant: got %q, want base %q", got.String(), base.String())
	}
}

// TestParseMasterPlaylist_PreservesVariantQuery ensures CDN tokens attached to
// the variant URL (common for signed HLS manifests) survive through the
// parser into the ffmpeg input argument.
func TestParseMasterPlaylist_PreservesVariantQuery(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-STREAM-INF:BANDWIDTH=1000000
variant.m3u8?token=abc&sig=xyz
`)
	base, _ := url.Parse("https://cdn.example.com/master.m3u8")
	got, err := parseMasterPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMasterPlaylist: %v", err)
	}
	want := "https://cdn.example.com/variant.m3u8?token=abc&sig=xyz"
	if got.String() != want {
		t.Errorf("query not preserved: got %q, want %q", got.String(), want)
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

// ----------------------------------------------------------------------------
// parseMediaPlaylist ??Phase B / Task B1
// ----------------------------------------------------------------------------

// segmentURIs is a small assertion helper for parseMediaPlaylist tests:
// returns just the resolved URI strings of segment-kind entries in order.
func segmentURIs(pl *mediaPlaylist) []string {
	out := []string{}
	for _, e := range pl.entries {
		if e.kind == entrySegment {
			out = append(out, e.uri.String())
		}
	}
	return out
}

func keyURIs(pl *mediaPlaylist) []string {
	out := []string{}
	for _, e := range pl.entries {
		if e.kind == entryKey {
			out = append(out, e.uri.String())
		}
	}
	return out
}

func initURIs(pl *mediaPlaylist) []string {
	out := []string{}
	for _, e := range pl.entries {
		if e.kind == entryInit {
			out = append(out, e.uri.String())
		}
	}
	return out
}

func TestParseMediaPlaylist_SimpleSegments(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:6
#EXTINF:6.0,
seg0.ts
#EXTINF:6.0,
seg1.ts
#EXTINF:6.0,
seg2.ts
#EXT-X-ENDLIST
`)
	base, _ := url.Parse("https://cdn.example.com/a/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMediaPlaylist: %v", err)
	}
	want := []string{
		"https://cdn.example.com/a/seg0.ts",
		"https://cdn.example.com/a/seg1.ts",
		"https://cdn.example.com/a/seg2.ts",
	}
	got := segmentURIs(pl)
	if len(got) != len(want) {
		t.Fatalf("segments = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("seg[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	// rawLines must preserve the full input verbatim ??materializeHLS will
	// rewrite individual lines by index.
	if len(pl.rawLines) < 10 {
		t.Errorf("rawLines len = %d, want >= 10 (full input preserved)", len(pl.rawLines))
	}
}

func TestParseMediaPlaylist_KeyAndSegments(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-VERSION:5
#EXT-X-TARGETDURATION:4
#EXT-X-KEY:METHOD=AES-128,URI="https://keys.example.com/k1.bin",IV=0x1234
#EXTINF:4.0,
seg0.ts
#EXTINF:4.0,
seg1.ts
#EXT-X-ENDLIST
`)
	base, _ := url.Parse("https://cdn.example.com/a/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMediaPlaylist: %v", err)
	}
	if got := keyURIs(pl); len(got) != 1 || got[0] != "https://keys.example.com/k1.bin" {
		t.Errorf("keys = %v, want [https://keys.example.com/k1.bin]", got)
	}
	if got := segmentURIs(pl); len(got) != 2 {
		t.Errorf("segments len = %d, want 2; got = %v", len(got), got)
	}
}

func TestParseMediaPlaylist_KeyMethodNoneSkipped(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="https://keys.example.com/k1.bin"
#EXTINF:4.0,
enc0.ts
#EXT-X-KEY:METHOD=NONE
#EXTINF:4.0,
plain1.ts
#EXT-X-ENDLIST
`)
	base, _ := url.Parse("https://cdn.example.com/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMediaPlaylist: %v", err)
	}
	keys := keyURIs(pl)
	if len(keys) != 1 || keys[0] != "https://keys.example.com/k1.bin" {
		t.Errorf("keys = %v, want exactly the AES-128 entry; METHOD=NONE must not produce a key entry", keys)
	}
}

func TestParseMediaPlaylist_KeyRotation(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="https://keys.example.com/k0.bin"
#EXTINF:4.0,
seg0.ts
#EXTINF:4.0,
seg1.ts
#EXT-X-KEY:METHOD=AES-128,URI="https://keys.example.com/k1.bin",IV=0xABCD
#EXTINF:4.0,
seg2.ts
#EXT-X-ENDLIST
`)
	base, _ := url.Parse("https://cdn.example.com/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMediaPlaylist: %v", err)
	}
	keys := keyURIs(pl)
	want := []string{
		"https://keys.example.com/k0.bin",
		"https://keys.example.com/k1.bin",
	}
	if len(keys) != 2 || keys[0] != want[0] || keys[1] != want[1] {
		t.Errorf("keys = %v, want %v (rotation order preserved)", keys, want)
	}
}

func TestParseMediaPlaylist_InitSegment(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-VERSION:7
#EXT-X-TARGETDURATION:6
#EXT-X-MAP:URI="https://cdn.example.com/init.mp4"
#EXTINF:6.0,
seg0.m4s
#EXT-X-ENDLIST
`)
	base, _ := url.Parse("https://cdn.example.com/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMediaPlaylist: %v", err)
	}
	inits := initURIs(pl)
	if len(inits) != 1 || inits[0] != "https://cdn.example.com/init.mp4" {
		t.Errorf("inits = %v, want one https init", inits)
	}
}

func TestParseMediaPlaylist_InitMissingURI(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-MAP:BYTERANGE="1024@0"
#EXTINF:6.0,
seg.m4s
`)
	base, _ := url.Parse("https://cdn.example.com/playlist.m3u8")
	if _, err := parseMediaPlaylist(body, base); err == nil {
		t.Errorf("expected error for #EXT-X-MAP missing URI, got nil")
	}
}

func TestParseMediaPlaylist_RelativeURIs(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="../keys/k.bin"
#EXTINF:4.0,
seg0.ts
#EXTINF:4.0,
nested/seg1.ts
#EXT-X-ENDLIST
`)
	base, _ := url.Parse("https://cdn.example.com/streams/a/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMediaPlaylist: %v", err)
	}
	if got := keyURIs(pl); len(got) != 1 || got[0] != "https://cdn.example.com/streams/keys/k.bin" {
		t.Errorf("key = %v, want resolved relative URI to streams/keys/k.bin", got)
	}
	segs := segmentURIs(pl)
	want := []string{
		"https://cdn.example.com/streams/a/seg0.ts",
		"https://cdn.example.com/streams/a/nested/seg1.ts",
	}
	if len(segs) != 2 || segs[0] != want[0] || segs[1] != want[1] {
		t.Errorf("segs = %v, want %v", segs, want)
	}
}

func TestParseMediaPlaylist_BYTERANGEPreserved(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-VERSION:4
#EXT-X-TARGETDURATION:4
#EXTINF:4.0,
#EXT-X-BYTERANGE:1024@0
seg.ts
#EXTINF:4.0,
#EXT-X-BYTERANGE:1024@1024
seg.ts
#EXT-X-ENDLIST
`)
	base, _ := url.Parse("https://cdn.example.com/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMediaPlaylist: %v", err)
	}
	// Both #EXTINF entries point to the same seg.ts URL but with different
	// byte ranges. Parser must produce two segment entries (one per #EXTINF)
	// and rawLines must still contain the #EXT-X-BYTERANGE lines verbatim.
	if got := segmentURIs(pl); len(got) != 2 {
		t.Errorf("segments len = %d, want 2 (one per #EXTINF, BYTERANGE shares URL)", len(got))
	}
	foundByterange := 0
	for _, line := range pl.rawLines {
		if strings.HasPrefix(strings.TrimSpace(line), "#EXT-X-BYTERANGE") {
			foundByterange++
		}
	}
	if foundByterange != 2 {
		t.Errorf("rawLines #EXT-X-BYTERANGE count = %d, want 2 (preserved verbatim)", foundByterange)
	}
}

func TestParseMediaPlaylist_RejectsFileSchemeSegment(t *testing.T) {
	body := []byte(`#EXTM3U
#EXTINF:4.0,
file:///etc/passwd
`)
	base, _ := url.Parse("https://cdn.example.com/playlist.m3u8")
	_, err := parseMediaPlaylist(body, base)
	if !errors.Is(err, errHLSVariantScheme) {
		t.Errorf("err = %v, want errHLSVariantScheme", err)
	}
}

func TestParseMediaPlaylist_EmptyBody(t *testing.T) {
	base, _ := url.Parse("https://cdn.example.com/playlist.m3u8")
	pl, err := parseMediaPlaylist(nil, base)
	if err != nil {
		t.Fatalf("parseMediaPlaylist: %v", err)
	}
	if len(pl.entries) != 0 {
		t.Errorf("entries = %v, want empty", pl.entries)
	}
}

func TestParseMediaPlaylist_TooManyKeys(t *testing.T) {
	// hlsMaxKeyEntries + 1 keys ??errHLSTooManyKeys.
	var buf strings.Builder
	buf.WriteString("#EXTM3U\n")
	for i := 0; i <= hlsMaxKeyEntries; i++ {
		buf.WriteString(`#EXT-X-KEY:METHOD=AES-128,URI="https://k.example/k.bin"` + "\n")
	}
	body := []byte(buf.String())
	base, _ := url.Parse("https://cdn.example.com/p.m3u8")
	_, err := parseMediaPlaylist(body, base)
	if !errors.Is(err, errHLSTooManyKeys) {
		t.Errorf("err = %v, want errHLSTooManyKeys", err)
	}
}

func TestParseMediaPlaylist_TooManyInits(t *testing.T) {
	// hlsMaxInitEntries + 1 init segments ??errHLSTooManyInits.
	var buf strings.Builder
	buf.WriteString("#EXTM3U\n")
	for i := 0; i <= hlsMaxInitEntries; i++ {
		buf.WriteString(`#EXT-X-MAP:URI="https://cdn.example/init.mp4"` + "\n")
	}
	body := []byte(buf.String())
	base, _ := url.Parse("https://cdn.example.com/p.m3u8")
	_, err := parseMediaPlaylist(body, base)
	if !errors.Is(err, errHLSTooManyInits) {
		t.Errorf("err = %v, want errHLSTooManyInits", err)
	}
}

func TestParseMediaPlaylist_DuplicateURIAttribute(t *testing.T) {
	// Hostile playlist with two URI attributes on a single tag ??parser
	// extracts the first, but rewriter would replace all. Refuse the
	// playlist to keep the two stages in lockstep.
	body := []byte(`#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="https://A/k.bin",URI="https://B/secret"
#EXTINF:4.0,
seg.ts
#EXT-X-ENDLIST
`)
	base, _ := url.Parse("https://cdn.example.com/p.m3u8")
	_, err := parseMediaPlaylist(body, base)
	if !errors.Is(err, errHLSDuplicateURIAttr) {
		t.Errorf("err = %v, want errHLSDuplicateURIAttr", err)
	}
}

func TestParseMediaPlaylist_TooManySegments(t *testing.T) {
	// hlsMaxSegments + 1 segments ??errHLSTooManySegments. Build the body
	// programmatically because writing 10001 #EXTINF blocks by hand is silly.
	var buf strings.Builder
	buf.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:1\n")
	for i := 0; i <= hlsMaxSegments; i++ {
		buf.WriteString("#EXTINF:1.0,\nseg.ts\n")
	}
	body := []byte(buf.String())
	base, _ := url.Parse("https://cdn.example.com/playlist.m3u8")
	_, err := parseMediaPlaylist(body, base)
	if !errors.Is(err, errHLSTooManySegments) {
		t.Errorf("err = %v, want errHLSTooManySegments", err)
	}
}

func TestParseMediaPlaylist_LineIdxTracksOriginalPosition(t *testing.T) {
	body := []byte(`#EXTM3U
#EXT-X-VERSION:3
#EXT-X-KEY:METHOD=AES-128,URI="k.bin"
#EXTINF:4.0,
seg0.ts
#EXTINF:4.0,
seg1.ts
`)
	base, _ := url.Parse("https://cdn.example.com/p.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatalf("parseMediaPlaylist: %v", err)
	}
	// lineIdx points at the line that holds the URI to be rewritten:
	// for KEY entries, the EXT-X-KEY line itself (URI="..." is on that line).
	// for SEGMENT entries, the line that holds the URI (e.g. "seg0.ts").
	if len(pl.entries) != 3 {
		t.Fatalf("entries len = %d, want 3 (1 key + 2 segs); entries = %+v", len(pl.entries), pl.entries)
	}
	// Order: entries appear in line order ??key first, then seg0, then seg1.
	if pl.entries[0].kind != entryKey {
		t.Errorf("entries[0].kind = %v, want entryKey", pl.entries[0].kind)
	}
	if pl.entries[1].kind != entrySegment || pl.entries[2].kind != entrySegment {
		t.Errorf("seg kinds wrong: %+v", pl.entries[1:])
	}
	// Validate lineIdx points to a line that actually contains the URI literal.
	cases := []struct {
		entry int
		want  string
	}{
		{0, "k.bin"},   // key URI line
		{1, "seg0.ts"}, // segment URI line
		{2, "seg1.ts"}, // segment URI line
	}
	for _, tc := range cases {
		idx := pl.entries[tc.entry].lineIdx
		if idx < 0 || idx >= len(pl.rawLines) {
			t.Errorf("entry %d lineIdx = %d, out of range", tc.entry, idx)
			continue
		}
		if !strings.Contains(pl.rawLines[idx], tc.want) {
			t.Errorf("entry %d at lineIdx %d: line = %q, want to contain %q",
				tc.entry, idx, pl.rawLines[idx], tc.want)
		}
	}
}
