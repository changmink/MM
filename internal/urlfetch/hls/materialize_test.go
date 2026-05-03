package hls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// servePaths는 path → bytes 매핑에 대해 정해진 응답을 돌려주고, 그 외
// 경로는 404로 응답하는 httptest 서버를 띄운다. materializeHLS 테스트가
// CDN 응답을 모의하는 데 쓴다.
func servePaths(t *testing.T, paths map[string][]byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range paths {
		path, body := path, body
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write(body)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestMaterializeHLS_SimpleMediaPlaylist(t *testing.T) {
	seg0 := []byte("segment-zero")
	seg1 := []byte("segment-one!!")
	seg2 := []byte("segment-two---")
	srv := servePaths(t, map[string][]byte{
		"/seg0.ts": seg0, "/seg1.ts": seg1, "/seg2.ts": seg2,
	})

	body := []byte(fmt.Sprintf(`#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:6
#EXTINF:6.0,
%[1]s/seg0.ts
#EXTINF:6.0,
%[1]s/seg1.ts
#EXTINF:6.0,
%[1]s/seg2.ts
#EXT-X-ENDLIST
`, srv.URL))

	base, _ := url.Parse(srv.URL + "/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	remaining := newCounter(testMaxBytes)
	localPath, total, err := materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, remaining, nil)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}

	wantTotal := int64(len(seg0) + len(seg1) + len(seg2))
	if total != wantTotal {
		t.Errorf("total = %d, want %d", total, wantTotal)
	}
	if !strings.HasPrefix(localPath, tempDir) {
		t.Errorf("localPath %q not under tempDir %q", localPath, tempDir)
	}

	// 각 segment 파일이 올바른 내용으로 존재하는지 확인한다.
	for i, want := range [][]byte{seg0, seg1, seg2} {
		got, err := os.ReadFile(filepath.Join(tempDir, fmt.Sprintf("seg_%04d.ts", i)))
		if err != nil {
			t.Fatalf("read seg %d: %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("seg %d content mismatch: got %q want %q", i, got, want)
		}
	}

	// 재작성된 플레이리스트가 origin URL이 아닌 로컬 이름을 가리키는지 확인한다.
	pgot, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatal(err)
	}
	pstr := string(pgot)
	for i := 0; i < 3; i++ {
		if !strings.Contains(pstr, fmt.Sprintf("seg_%04d.ts", i)) {
			t.Errorf("rewritten playlist missing seg_%04d.ts; got:\n%s", i, pstr)
		}
	}
	if strings.Contains(pstr, srv.URL) {
		t.Errorf("rewritten playlist must not contain origin URL %q; got:\n%s", srv.URL, pstr)
	}
}

func TestMaterializeHLS_WithKeyAndSegments(t *testing.T) {
	keyBody := []byte("0123456789abcdef") // 16바이트 — AES-128 raw key
	seg := []byte("encrypted-segment-data")
	srv := servePaths(t, map[string][]byte{
		"/k.bin": keyBody, "/seg.ts": seg,
	})

	body := []byte(fmt.Sprintf(`#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="%[1]s/k.bin",IV=0x1234
#EXTINF:4.0,
%[1]s/seg.ts
#EXT-X-ENDLIST
`, srv.URL))

	base, _ := url.Parse(srv.URL + "/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	localPath, _, err := materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(testMaxBytes), nil)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(tempDir, "key_0.bin"))
	if !bytes.Equal(got, keyBody) {
		t.Errorf("key file mismatch")
	}
	got, _ = os.ReadFile(filepath.Join(tempDir, "seg_0000.ts"))
	if !bytes.Equal(got, seg) {
		t.Errorf("seg file mismatch")
	}

	pstr, _ := os.ReadFile(localPath)
	// Key URI는 로컬 이름으로 재작성되어야 하고 IV는 보존되어야 한다(IV가
	// 누락되면 복호화가 깨지는 회귀가 발생한다).
	if !strings.Contains(string(pstr), `URI="key_0.bin"`) {
		t.Errorf("playlist key URI not rewritten; got:\n%s", pstr)
	}
	if !strings.Contains(string(pstr), "IV=0x1234") {
		t.Errorf("IV attribute lost during rewrite; got:\n%s", pstr)
	}
}

func TestMaterializeHLS_WithInitSegment(t *testing.T) {
	initBody := []byte("init-segment-bytes")
	seg := []byte("media-segment-bytes")
	srv := servePaths(t, map[string][]byte{
		"/init.mp4": initBody, "/s.m4s": seg,
	})

	body := []byte(fmt.Sprintf(`#EXTM3U
#EXT-X-VERSION:7
#EXT-X-MAP:URI="%[1]s/init.mp4",BYTERANGE="1024@0"
#EXTINF:6.0,
%[1]s/s.m4s
#EXT-X-ENDLIST
`, srv.URL))

	base, _ := url.Parse(srv.URL + "/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	localPath, _, err := materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(testMaxBytes), nil)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(tempDir, "init_0.mp4"))
	if !bytes.Equal(got, initBody) {
		t.Errorf("init file mismatch")
	}
	got, _ = os.ReadFile(filepath.Join(tempDir, "seg_0000.m4s"))
	if !bytes.Equal(got, seg) {
		t.Errorf("seg file mismatch (extension preservation .m4s)")
	}

	pstr, _ := os.ReadFile(localPath)
	if !strings.Contains(string(pstr), `URI="init_0.mp4"`) {
		t.Errorf("init URI not rewritten; got:\n%s", pstr)
	}
	if !strings.Contains(string(pstr), `BYTERANGE="1024@0"`) {
		t.Errorf("BYTERANGE attribute on EXT-X-MAP lost; got:\n%s", pstr)
	}
}

func TestMaterializeHLS_KeyRotation(t *testing.T) {
	srv := servePaths(t, map[string][]byte{
		"/k0.bin": []byte("aaaaaaaaaaaaaaaa"), // 16바이트 키
		"/k1.bin": []byte("bbbbbbbbbbbbbbbb"),
		"/s0.ts":  []byte("seg0"),
		"/s1.ts":  []byte("seg1"),
		"/s2.ts":  []byte("seg2"),
	})

	body := []byte(fmt.Sprintf(`#EXTM3U
#EXT-X-KEY:METHOD=AES-128,URI="%[1]s/k0.bin"
#EXTINF:4.0,
%[1]s/s0.ts
#EXTINF:4.0,
%[1]s/s1.ts
#EXT-X-KEY:METHOD=AES-128,URI="%[1]s/k1.bin",IV=0xabcd
#EXTINF:4.0,
%[1]s/s2.ts
#EXT-X-ENDLIST
`, srv.URL))

	base, _ := url.Parse(srv.URL + "/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	localPath, _, err := materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(testMaxBytes), nil)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}

	for _, name := range []string{"key_0.bin", "key_1.bin"} {
		if _, err := os.Stat(filepath.Join(tempDir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	pstr, _ := os.ReadFile(localPath)
	for i, want := range []string{`URI="key_0.bin"`, `URI="key_1.bin"`} {
		if !strings.Contains(string(pstr), want) {
			t.Errorf("rotation key %d not rewritten to %q; got:\n%s", i, want, pstr)
		}
	}
}

func TestMaterializeHLS_RelativePathsResolved(t *testing.T) {
	seg := []byte("seg-content")
	srv := servePaths(t, map[string][]byte{"/streams/a/sub/seg.ts": seg})

	// 플레이리스트가 상대 URL을 참조한다 — parseMediaPlaylist가 base 기준으로
	// 해석하고, materializeHLS가 해석된 URL에서 다운로드한다.
	body := []byte(`#EXTM3U
#EXTINF:4.0,
sub/seg.ts
#EXT-X-ENDLIST
`)
	base, _ := url.Parse(srv.URL + "/streams/a/playlist.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	_, _, err = materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(testMaxBytes), nil)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(tempDir, "seg_0000.ts"))
	if err != nil || !bytes.Equal(got, seg) {
		t.Errorf("seg file: %v, %q", err, got)
	}
}

func TestMaterializeHLS_ExtensionPreserved(t *testing.T) {
	// segment 확장자가 섞인 플레이리스트 하나 — 로컬 파일명은 URL path의
	// 원본 확장자를 보존해야 한다.
	srv := servePaths(t, map[string][]byte{
		"/a.ts":  []byte("ts-data"),
		"/b.m4s": []byte("m4s-data"),
		"/c.aac": []byte("aac-data"),
		"/d.vtt": []byte("vtt-data"),
	})

	body := []byte(fmt.Sprintf(`#EXTM3U
#EXTINF:4.0,
%[1]s/a.ts
#EXTINF:4.0,
%[1]s/b.m4s
#EXTINF:4.0,
%[1]s/c.aac
#EXTINF:4.0,
%[1]s/d.vtt
#EXT-X-ENDLIST
`, srv.URL))
	base, _ := url.Parse(srv.URL + "/p.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	_, _, err = materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(testMaxBytes), nil)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}

	for i, ext := range []string{".ts", ".m4s", ".aac", ".vtt"} {
		name := fmt.Sprintf("seg_%04d%s", i, ext)
		if _, err := os.Stat(filepath.Join(tempDir, name)); err != nil {
			t.Errorf("expected %s, got %v", name, err)
		}
	}
}

func TestMaterializeHLS_UnknownExtensionFallsToBin(t *testing.T) {
	// .xyz는 whitelist에 없으므로 로컬 이름이 .bin으로 끝나야 한다.
	srv := servePaths(t, map[string][]byte{"/seg.xyz": []byte("data")})
	body := []byte(fmt.Sprintf(`#EXTM3U
#EXTINF:4.0,
%s/seg.xyz
#EXT-X-ENDLIST
`, srv.URL))
	base, _ := url.Parse(srv.URL + "/p.m3u8")
	pl, _ := parseMediaPlaylist(body, base)

	tempDir := t.TempDir()
	_, _, err := materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(testMaxBytes), nil)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}
	if _, err := os.Stat(filepath.Join(tempDir, "seg_0000.bin")); err != nil {
		t.Errorf("expected seg_0000.bin, got %v", err)
	}
}

func TestMaterializeHLS_DownloadFailureReturnsEarly(t *testing.T) {
	// segment 하나가 404라서 materializeHLS는 재작성된 플레이리스트를 쓰지
	// 않고 에러를 반환한다. 병렬 다운로드를 쓰므로, 이미 시작된 성공
	// segment가 취소보다 먼저 끝날 수도, 끝나지 못할 수도 있다.
	mux := http.NewServeMux()
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/seg1.ts", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	body := []byte(fmt.Sprintf(`#EXTM3U
#EXTINF:4.0,
%[1]s/seg0.ts
#EXTINF:4.0,
%[1]s/seg1.ts
#EXTINF:4.0,
%[1]s/seg2-never-reached.ts
#EXT-X-ENDLIST
`, srv.URL))
	base, _ := url.Parse(srv.URL + "/p.m3u8")
	pl, _ := parseMediaPlaylist(body, base)

	tempDir := t.TempDir()
	_, total, err := materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(testMaxBytes), nil)
	if err == nil {
		t.Fatal("expected error from second segment 404")
	}
	if total != 0 && total != 2 {
		t.Errorf("total = %d, want only bytes from completed successful downloads", total)
	}
	if _, statErr := os.Stat(filepath.Join(tempDir, "playlist.m3u8")); !os.IsNotExist(statErr) {
		t.Errorf("playlist should not be written after download failure: %v", statErr)
	}
}

func TestMaterializeHLS_RawLinesPreserved(t *testing.T) {
	// URI가 아닌 라인은 그대로 통과해야 한다 — segment/key/init URI
	// 부분만 재작성된다. 특히 #EXT-X-VERSION, #EXT-X-TARGETDURATION,
	// #EXT-X-BYTERANGE, #EXT-X-ENDLIST.
	srv := servePaths(t, map[string][]byte{"/seg.ts": []byte("d")})
	body := []byte(fmt.Sprintf(`#EXTM3U
#EXT-X-VERSION:4
#EXT-X-TARGETDURATION:4
#EXTINF:4.0,
#EXT-X-BYTERANGE:1024@0
%[1]s/seg.ts
#EXT-X-ENDLIST
`, srv.URL))
	base, _ := url.Parse(srv.URL + "/p.m3u8")
	pl, _ := parseMediaPlaylist(body, base)

	tempDir := t.TempDir()
	localPath, _, err := materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(testMaxBytes), nil)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}
	pstr, _ := os.ReadFile(localPath)
	for _, want := range []string{"#EXTM3U", "#EXT-X-VERSION:4", "#EXT-X-TARGETDURATION:4",
		"#EXT-X-BYTERANGE:1024@0", "#EXT-X-ENDLIST", "seg_0000.ts"} {
		if !strings.Contains(string(pstr), want) {
			t.Errorf("rewritten playlist missing %q; got:\n%s", want, pstr)
		}
	}
}

func TestMaterializeHLS_UnrecognizedTagURINeutered(t *testing.T) {
	// 원격 URI 속성이 있는 LL-HLS / 알 수 없는 태그를 포함한 미디어
	// 플레이리스트 — parseMediaPlaylist가 이 태그를 인식하지 못해 entry를
	// 만들지 않고 URI는 rawLines에 그대로 통과한다. 재작성 패스는
	// URI="..."를 URI=""로 정규화해야 — 향후 ffmpeg whitelist 완화가
	// 일어나도 인식되지 않은 태그를 통해 원격 URL이 바이너리에 닿지 못한다.
	srv := servePaths(t, map[string][]byte{"/seg.ts": []byte("d")})
	body := []byte(fmt.Sprintf(`#EXTM3U
#EXT-X-VERSION:6
#EXT-X-TARGETDURATION:4
#EXT-X-PRELOAD-HINT:TYPE=PART,URI="https://attacker.example/secret.bin"
#EXTINF:4.0,
%s/seg.ts
#EXT-X-ENDLIST
`, srv.URL))
	base, _ := url.Parse(srv.URL + "/p.m3u8")
	pl, err := parseMediaPlaylist(body, base)
	if err != nil {
		t.Fatal(err)
	}

	tempDir := t.TempDir()
	localPath, _, err := materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(testMaxBytes), nil)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}
	pstr, _ := os.ReadFile(localPath)
	if strings.Contains(string(pstr), "attacker.example") {
		t.Errorf("rewritten playlist still contains remote URL on unrecognized tag; got:\n%s", pstr)
	}
	if !strings.Contains(string(pstr), `#EXT-X-PRELOAD-HINT`) {
		t.Errorf("unrecognized tag itself was dropped; got:\n%s", pstr)
	}
	if !strings.Contains(string(pstr), `URI=""`) {
		t.Errorf("URI attribute on unrecognized tag was not normalized to empty; got:\n%s", pstr)
	}
}

func TestMaterializeHLS_CumulativeCapEnforced(t *testing.T) {
	// 첫 segment는 들어가지만 두 번째가 remaining을 초과 — 에러가 표면화 되어야 한다.
	srv := servePaths(t, map[string][]byte{
		"/a.ts": make([]byte, 800),
		"/b.ts": make([]byte, 800),
	})
	body := []byte(fmt.Sprintf(`#EXTM3U
#EXTINF:4.0,
%[1]s/a.ts
#EXTINF:4.0,
%[1]s/b.ts
#EXT-X-ENDLIST
`, srv.URL))
	base, _ := url.Parse(srv.URL + "/p.m3u8")
	pl, _ := parseMediaPlaylist(body, base)

	tempDir := t.TempDir()
	_, _, err := materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(1024), nil)
	if !errors.Is(err, errHLSTooLarge) {
		t.Errorf("err = %v, want errHLSTooLarge", err)
	}
}

func TestMaterializeHLS_ProgressCallbackFires(t *testing.T) {
	// byte threshold(1 MiB)를 넘길 만큼 큰 페이로드를 만든다.
	bigSeg := make([]byte, progressByteThreshold+1024)
	srv := servePaths(t, map[string][]byte{"/big.ts": bigSeg})
	body := []byte(fmt.Sprintf(`#EXTM3U
#EXTINF:4.0,
%s/big.ts
#EXT-X-ENDLIST
`, srv.URL))
	base, _ := url.Parse(srv.URL + "/p.m3u8")
	pl, _ := parseMediaPlaylist(body, base)

	var emitted []int64
	cb := &Callbacks{
		Progress: func(received int64) {
			emitted = append(emitted, received)
		},
	}

	tempDir := t.TempDir()
	_, _, err := materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, tempDir, newCounter(testMaxBytes), cb)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}
	if len(emitted) == 0 {
		t.Errorf("expected at least one Progress callback for >1 MiB segment, got 0")
	}
	for i := 1; i < len(emitted); i++ {
		if emitted[i] <= emitted[i-1] {
			t.Errorf("progress not monotonic: %v", emitted)
			break
		}
	}
}

func TestMaterializeHLS_DownloadsEntriesInParallel(t *testing.T) {
	var active atomic.Int64
	var maxActive atomic.Int64

	mux := http.NewServeMux()
	for i := 0; i < 8; i++ {
		p := fmt.Sprintf("/seg%d.ts", i)
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			cur := active.Add(1)
			for {
				max := maxActive.Load()
				if cur <= max || maxActive.CompareAndSwap(max, cur) {
					break
				}
			}
			defer active.Add(-1)

			time.Sleep(50 * time.Millisecond)
			_, _ = w.Write([]byte("segment"))
		})
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i := 0; i < 8; i++ {
		fmt.Fprintf(&b, "#EXTINF:4.0,\n%s/seg%d.ts\n", srv.URL, i)
	}
	b.WriteString("#EXT-X-ENDLIST\n")

	base, _ := url.Parse(srv.URL + "/p.m3u8")
	pl, err := parseMediaPlaylist([]byte(b.String()), base)
	if err != nil {
		t.Fatalf("parseMediaPlaylist: %v", err)
	}

	_, _, err = materializeHLS(context.Background(),
		NewClient(AllowPrivateNetworks()),
		pl, t.TempDir(), newCounter(testMaxBytes), nil)
	if err != nil {
		t.Fatalf("materializeHLS: %v", err)
	}

	if got := maxActive.Load(); got <= 1 {
		t.Fatalf("max concurrent downloads = %d, want > 1", got)
	}
	if got := maxActive.Load(); got > hlsMaterializeParallelism {
		t.Fatalf("max concurrent downloads = %d, want <= %d", got, hlsMaterializeParallelism)
	}
}
