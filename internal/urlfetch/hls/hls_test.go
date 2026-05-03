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
	// tiny.m3u8(BANDWIDTH=100)이 nobandwidth.m3u8(0으로 처리)보다 우선해야 한다.
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
	// #EXT-X-STREAM-INF가 없으면 이미 미디어 플레이리스트다.
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

// TestParseMasterPlaylist_VariantSelfLoopReturnsBase는 선택된 variant URL이
// 마스터 자신으로 다시 해석되는 마스터 플레이리스트(악의적 입력이나 잘못된
// CDN 설정)에서 ffmpeg가 루프에 빠지지 않게 막는 가드를 검증한다.
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
	// 같은 URL을 다시 쫓아가지 않도록 base로 폴백해야 ffmpeg가 미디어
	// 플레이리스트로 취급한다.
	if got.String() != base.String() {
		t.Errorf("self-loop variant: got %q, want base %q", got.String(), base.String())
	}
}

// TestParseMasterPlaylist_PreservesVariantQuery는 variant URL에 붙은 CDN
// 토큰(서명된 HLS 매니페스트에서 흔하다)이 파서를 거쳐 ffmpeg 입력 인자까지
// 유지되는지 보장한다.
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

// segmentURIs는 parseMediaPlaylist 테스트용 작은 단언 헬퍼다 — segment
// 종류 엔트리의 해석된 URI 문자열만 순서대로 반환한다.
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
	// rawLines는 입력 전체를 그대로 보존해야 한다 — materializeHLS가 인덱스로
	// 개별 라인을 재작성한다.
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
	// 두 #EXTINF 엔트리가 같은 seg.ts URL을 가리키지만 byte range가 다르다.
	// 파서는 #EXTINF당 하나씩 두 segment 엔트리를 만들어야 하고, rawLines는
	// #EXT-X-BYTERANGE 라인을 그대로 유지해야 한다.
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
	// hlsMaxKeyEntries + 1개의 키 — errHLSTooManyKeys가 나야 한다.
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
	// hlsMaxInitEntries + 1개의 init segment — errHLSTooManyInits가 나야 한다.
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
	// 하나의 태그에 URI 속성이 두 개 있는 적대적 플레이리스트 — 파서는
	// 첫 번째를 뽑지만 rewriter는 전부를 교체한다. 두 단계가 어긋나지
	// 않도록 플레이리스트 자체를 거부한다.
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
	// hlsMaxSegments + 1개의 segment — errHLSTooManySegments가 나야 한다.
	// 10001개의 #EXTINF 블록을 손으로 쓰는 건 비현실적이라 본문을 코드로
	// 생성한다.
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
	// lineIdx는 재작성될 URI를 담고 있는 라인을 가리킨다:
	// KEY 엔트리의 경우 EXT-X-KEY 라인 자체(URI="..."가 거기 있다).
	// SEGMENT 엔트리의 경우 URI를 담은 라인(예: "seg0.ts").
	if len(pl.entries) != 3 {
		t.Fatalf("entries len = %d, want 3 (1 key + 2 segs); entries = %+v", len(pl.entries), pl.entries)
	}
	// 순서: 엔트리는 라인 순서대로 나타난다 — key 먼저, 그다음 seg0, seg1.
	if pl.entries[0].kind != entryKey {
		t.Errorf("entries[0].kind = %v, want entryKey", pl.entries[0].kind)
	}
	if pl.entries[1].kind != entrySegment || pl.entries[2].kind != entrySegment {
		t.Errorf("seg kinds wrong: %+v", pl.entries[1:])
	}
	// lineIdx가 실제로 URI 리터럴을 담은 라인을 가리키는지 확인한다.
	cases := []struct {
		entry int
		want  string
	}{
		{0, "k.bin"},   // key URI 라인
		{1, "seg0.ts"}, // segment URI 라인
		{2, "seg1.ts"}, // segment URI 라인
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
