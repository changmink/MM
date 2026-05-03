package urlfetch

// 이 파일의 테스트들은 spec §3.1의 HLS 강화를 통합 수준에서 검증한다.
// 보호된 URL 클라이언트와 httptest 서버(언제나 127.0.0.1에 바인딩)와
// AllowPrivateNetworks()를 함께 써서, 흐름 형태(상한·정리·에러 코드 매핑)는
// 검증하지만 "master와 variant가 서로 다른 IP" 시나리오를 그대로 연출하진
// 못한다.
//
// 왜 DNS-rebinding 통합 테스트가 여기 없는가:
// publicOnlyDialContext는 "모든 private 차단" 하나의 거친 스위치다.
// AllowPrivateNetworks()가 켜지면 클라이언트 전체에 private IP가 허용되고,
// 꺼지면 httptest 마스터 서버조차 닿지 못한다. master.example이 우리가
// 서빙도 할 수 있는 진짜 public IP로 해석되는 제3의 상태는 없다. 그래서
// DNS rebinding의 메커니즘은 단위 레벨에서 검증한다:
//   - TestDownloadOne_PrivateIPBlocked (hls_download_test.go):
//     sequenceResolver가 호스트명을 127.0.0.1로 매핑하면 보호된
//     클라이언트가 errPrivateNetwork로 거부한다. materializeHLS가 모든
//     segment / key / init fetch에 사용하는 같은 코드 경로다.
//   - TestSequenceResolver_OrderedAnswers (helpers_test.go):
//     N번째 호출에 대한 정렬된 응답 — "처음은 public, 다음은 private"
//     rebinding 시나리오를 만드는 빌딩 블록.
// 둘을 합치면 다음을 증명한다: 보호된 클라이언트가 매 dial마다 IP를
// 검증하고, sequenceResolver가 후속 테스트에서 호출별로 서로 다른 답을
// 무대화할 수 있다. fetchHLS는 모든 fetch에 같은 클라이언트를 흘려보내므로,
// N번째 해석이 private IP를 반환하는 호스트는 — 실제 public origin 없이도
// — 그 dial에서 차단된다.
//
// 이 파일이 다루는 범위 (Fetch + fetchHLS 통합):
//   - hls_too_many_segments wire 코드 (AC-13)
//   - variant 플레이리스트 본문에 대한 hls_playlist_too_large (AC-12)
//   - 실패 경로의 hlsTempDir 정리 (AC-16)와 성공 경로의 정리 (AC-17)

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	hlsfetch "file_server/internal/urlfetch/hls"
)

func TestFetch_HLS_TooManySegments_WireCode(t *testing.T) {
	// hlsMaxSegments + 1개의 #EXTINF 엔트리를 가진 미디어 플레이리스트를
	// 만든다 — parseMediaPlaylist가 이를 거부하고 Fetch는 새 코드
	// hls_too_many_segments를 표면화해, 운영자가 일반 "too_large"와
	// 구분할 수 있어야 한다.
	var buf strings.Builder
	buf.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:1\n")
	for i := 0; i <= hlsfetch.MaxSegments; i++ {
		buf.WriteString("#EXTINF:1.0,\nseg.ts\n")
	}
	body := buf.String()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/p.m3u8", dest, "/movies", testMaxBytes, nil)
	if ferr == nil {
		t.Fatal("expected hls_too_many_segments error, got nil")
	}
	if ferr.Code != "hls_too_many_segments" {
		t.Errorf("Code = %q, want hls_too_many_segments", ferr.Code)
	}
}

func captureFfmpeg(t *testing.T) *[][]string {
	t.Helper()
	var captured [][]string
	var mu sync.Mutex

	restore := hlsfetch.SetRunFfmpegForTest(func(_ context.Context, args []string, _ io.Writer) error {
		mu.Lock()
		captured = append(captured, append([]string(nil), args...))
		mu.Unlock()

		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-y" {
				_ = os.WriteFile(args[i+1], []byte{
					0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p',
					'm', 'p', '4', '2', 0x00, 0x00, 0x00, 0x00,
					'i', 's', 'o', 'm', 'm', 'p', '4', '2',
				}, 0644)
				break
			}
		}
		return nil
	})
	t.Cleanup(restore)

	return &captured
}

func TestFetch_HLS_VariantPlaylistTooLarge(t *testing.T) {
	// 마스터 플레이리스트가 1 MiB 상한을 넘는 본문의 variant를 가리키게 한다.
	// fetchPlaylistBody는 variant를 파싱하기 전에 거부해야 한다.
	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nvariant.m3u8\n")
	})
	mux.HandleFunc("/variant.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		// 본문 2 MiB — hlsMaxPlaylistBytes(1 MiB) 상한 초과.
		_, _ = w.Write(make([]byte, 2<<20))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/master.m3u8", dest, "/movies", testMaxBytes, nil)
	if ferr == nil {
		t.Fatal("expected hls_playlist_too_large for oversized variant body")
	}
	if ferr.Code != "hls_playlist_too_large" {
		t.Errorf("Code = %q, want hls_playlist_too_large", ferr.Code)
	}
}

func TestFetch_HLS_TempDirCleanedOnFailure(t *testing.T) {
	// 마스터 + 미디어 플레이리스트가 404되는 segment를 참조한다. 실패
	// 경로에서도 fetchHLS는 .urlimport-hls-*를 제거해, 부분 다운로드가
	// 여러 시도에 걸쳐 사용자 미디어 디렉터리로 새지 않게 해야 한다.
	mux := http.NewServeMux()
	mux.HandleFunc("/playlist.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = fmt.Fprintln(w, `#EXTM3U`)
		_, _ = fmt.Fprintln(w, `#EXT-X-VERSION:3`)
		_, _ = fmt.Fprintln(w, `#EXTINF:4.0,`)
		_, _ = fmt.Fprintln(w, `seg.ts`)
		_, _ = fmt.Fprintln(w, `#EXT-X-ENDLIST`)
	})
	mux.HandleFunc("/seg.ts", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r) // 404 → segment 다운로드 실패
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/playlist.m3u8", dest, "/", testMaxBytes, nil)
	if ferr == nil {
		t.Fatal("expected error from segment 404, got nil")
	}

	// Fetch가 반환된 뒤 destDir에는 .urlimport-hls-* 디렉터리가 없어야 한다.
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".urlimport-hls-") {
			t.Errorf("leftover hls temp dir: %s", e.Name())
		}
	}
}

func TestFetch_HLS_FFmpegInvocation_LocalOnly_SimpleMedia(t *testing.T) {
	// captureFfmpeg stub과 함께 Fetch + fetchHLS + materializeHLS +
	// runHLSRemux 전체 파이프라인을 통과시킨다. argv 불변식(AC-10 /
	// AC-11)을 검증한다 — 파이프라인이 끝나면 ffmpeg는 -i에서 로컬 경로만
	// 보고 protocol whitelist는 정확히 file,crypto다. 플레이리스트가 어떻게
	// 구성됐든 원격 URL이 ffmpeg에 닿아서는 안 된다.
	captured := captureFfmpeg(t)

	seg0 := []byte("seg0-data")
	seg1 := []byte("seg1-data")
	mux := http.NewServeMux()
	mux.HandleFunc("/playlist.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		host := "http://" + r.Host
		_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n"+
			"#EXTINF:4.0,\n%s/seg0.ts\n#EXTINF:4.0,\n%s/seg1.ts\n#EXT-X-ENDLIST\n", host, host)
	})
	mux.HandleFunc("/seg0.ts", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(seg0) })
	mux.HandleFunc("/seg1.ts", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(seg1) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/playlist.m3u8", dest, "/", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	if len(*captured) != 1 {
		t.Fatalf("ffmpeg invoked %d times, want 1; argv = %v", len(*captured), *captured)
	}
	verifyLocalOnlyArgv(t, (*captured)[0])
}

func TestFetch_HLS_FFmpegInvocation_LocalOnly_KeyAndInit(t *testing.T) {
	// 위와 동일한 불변식이지만 가장 풍부한 플레이리스트로 검증한다 —
	// 마스터 → variant → segment + #EXT-X-KEY + #EXT-X-MAP. 원격 리소스
	// 종류가 4가지여도 ffmpeg argv는 local-only를 유지해야 한다.
	captured := captureFfmpeg(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = fmt.Fprintln(w, `#EXTM3U`)
		_, _ = fmt.Fprintln(w, `#EXT-X-STREAM-INF:BANDWIDTH=1000000`)
		_, _ = fmt.Fprintln(w, `variant.m3u8`)
	})
	mux.HandleFunc("/variant.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		host := "http://" + r.Host
		_, _ = fmt.Fprintln(w, `#EXTM3U`)
		_, _ = fmt.Fprintln(w, `#EXT-X-VERSION:7`)
		_, _ = fmt.Fprintf(w, "#EXT-X-MAP:URI=\"%s/init.mp4\"\n", host)
		_, _ = fmt.Fprintf(w, "#EXT-X-KEY:METHOD=AES-128,URI=\"%s/k.bin\",IV=0x1234\n", host)
		_, _ = fmt.Fprintln(w, `#EXTINF:4.0,`)
		_, _ = fmt.Fprintf(w, "%s/s0.m4s\n", host)
		_, _ = fmt.Fprintln(w, `#EXT-X-ENDLIST`)
	})
	mux.HandleFunc("/init.mp4", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("init")) })
	mux.HandleFunc("/k.bin", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("0123456789abcdef")) })
	mux.HandleFunc("/s0.m4s", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("seg-data")) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/master.m3u8", dest, "/", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("Fetch: %v", ferr)
	}
	if len(*captured) != 1 {
		t.Fatalf("ffmpeg invoked %d times, want 1; argv = %v", len(*captured), *captured)
	}
	verifyLocalOnlyArgv(t, (*captured)[0])
}

// verifyLocalOnlyArgv는 캡처된 ffmpeg argv에 대해 AC-10 / AC-11 불변식을
// 단언한다 — -i 입력은 로컬 절대 경로이고 -protocol_whitelist는 정확히
// file,crypto로 네트워크 프로토콜이 들어 있지 않다.
func verifyLocalOnlyArgv(t *testing.T, args []string) {
	t.Helper()
	whitelistVal := ""
	inputVal := ""
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "-protocol_whitelist":
			whitelistVal = args[i+1]
		case "-i":
			inputVal = args[i+1]
		}
	}
	if whitelistVal != "file,crypto" {
		t.Errorf("-protocol_whitelist = %q, want %q", whitelistVal, "file,crypto")
	}
	for _, forbidden := range []string{"http", "tcp", "tls", "udp", "rtp", "pipe", "async"} {
		if strings.Contains(whitelistVal, forbidden) {
			t.Errorf("-protocol_whitelist contains forbidden token %q: %q",
				forbidden, whitelistVal)
		}
	}
	if strings.HasPrefix(inputVal, "http://") || strings.HasPrefix(inputVal, "https://") {
		t.Errorf("-i input is a remote URL: %q (must be local file)", inputVal)
	}
	if !filepath.IsAbs(inputVal) {
		t.Errorf("-i input %q is not absolute (materializeHLS must use abs path)", inputVal)
	}
}

func TestFetch_HLS_TempDirCleanedOnSuccess(t *testing.T) {
	// requireFFmpeg가 게이트한다 — 실제 remux에는 진짜 ffmpeg가 필요하다.
	// 목적은 happy-path 종료 후에 hlsTempDir이 남지 않음을 확인하는 것이다.
	requireFFmpeg(t)

	fixtureDir := t.TempDir()
	playlistName := makeHLSFixture(t, fixtureDir, 1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			http.ServeFile(w, r, filepath.Join(fixtureDir, playlistName))
		default:
			http.ServeFile(w, r, filepath.Join(fixtureDir, filepath.Base(r.URL.Path)))
		}
	}))
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/"+playlistName, dest, "/movies", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "playlist.mp4" {
		t.Errorf("name = %q, want playlist.mp4", res.Name)
	}

	// 최종 MP4는 dest에 있어야 하지만 .urlimport-hls-* 작업 디렉터리는 없어야 한다.
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".urlimport-hls-") {
			t.Errorf("leftover hls temp dir after success: %s", e.Name())
		}
	}
}
