package urlfetch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	hlsfetch "file_server/internal/urlfetch/hls"
)

// TestFetch_HLS_MediaPlaylist_Success는 variant 없는 미디어 플레이리스트의
// end-to-end happy path를 검증한다 — HLS 감지 발동, ffmpeg가 segment를
// remux, 강제된 .mp4 확장자와 "extension_replaced" 경고와 함께 출력이
// destDir로 원자적 rename 된다.
func TestFetch_HLS_MediaPlaylist_Success(t *testing.T) {
	fixtureDir := t.TempDir()
	playlistName := makeHLSFixture(t, fixtureDir, 1)

	// .m3u8은 정규 HLS Content-Type으로, .ts segment는 기본 mime
	// (FileServer 기본인 application/octet-stream)으로 서빙한다.
	fs := http.FileServer(http.Dir(fixtureDir))
	mux := http.NewServeMux()
	mux.HandleFunc("/"+playlistName, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		http.ServeFile(w, r, filepath.Join(fixtureDir, playlistName))
	})
	mux.Handle("/", fs)
	srv := httptest.NewServer(mux)
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
	if res.Type != "video" {
		t.Errorf("type = %q, want video", res.Type)
	}
	if !slices.Contains(res.Warnings, "extension_replaced") {
		t.Errorf("warnings = %v, want to contain extension_replaced", res.Warnings)
	}
	if res.Size <= 0 {
		t.Errorf("size = %d, want > 0", res.Size)
	}
	data, err := os.ReadFile(filepath.Join(dest, res.Name))
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(data) < 8 || string(data[4:8]) != "ftyp" {
		t.Errorf("output not MP4 (no ftyp): % x", data[:min(16, len(data))])
	}
}

// TestFetch_HLS_MasterPlaylist_PicksHighestBandwidth는 두 variant를 가진
// 마스터 플레이리스트를 서빙하고 Fetch가 더 높은 bandwidth 쪽을 다운로드
// 하는지 검증한다. low variant는 디스크에 존재하지 않는 가짜 미디어
// 플레이리스트라, 거기에 닿으면 remux가 실패한다 — 더 높은 쪽이 선택되면
// ffmpeg가 성공한다.
func TestFetch_HLS_MasterPlaylist_PicksHighestBandwidth(t *testing.T) {
	fixtureDir := t.TempDir()
	playlistName := makeHLSFixture(t, fixtureDir, 1)

	masterBody := "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=100000,RESOLUTION=64x64\n" +
		"nonexistent-low.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=2000000,RESOLUTION=64x64\n" +
		playlistName + "\n"

	fs := http.FileServer(http.Dir(fixtureDir))
	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(masterBody))
	})
	mux.HandleFunc("/"+playlistName, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		http.ServeFile(w, r, filepath.Join(fixtureDir, playlistName))
	})
	mux.Handle("/", fs)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/master.m3u8", dest, "/movies", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Name != "master.mp4" {
		t.Errorf("name = %q, want master.mp4", res.Name)
	}
	// extension_replaced는 미디어 플레이리스트 분기뿐 아니라 마스터
	// 플레이리스트 분기에도 있어야 한다.
	if !slices.Contains(res.Warnings, "extension_replaced") {
		t.Errorf("master branch warnings = %v, want to contain extension_replaced", res.Warnings)
	}
}

// TestFetch_HLS_MislabeledContentType_Fallback은 .m3u8에 대해 text/plain을
// 반환하는 CDN을 다룬다 — 우리의 폴백 감지가 unsupported_content_type으로
// 거부하지 않고 HLS 분기로 라우팅해야 한다.
func TestFetch_HLS_MislabeledContentType_Fallback(t *testing.T) {
	fixtureDir := t.TempDir()
	playlistName := makeHLSFixture(t, fixtureDir, 1)

	fs := http.FileServer(http.Dir(fixtureDir))
	mux := http.NewServeMux()
	mux.HandleFunc("/"+playlistName, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		http.ServeFile(w, r, filepath.Join(fixtureDir, playlistName))
	})
	mux.Handle("/", fs)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/"+playlistName, dest, "/movies", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Type != "video" {
		t.Errorf("type = %q, want video (HLS fallback should still work)", res.Type)
	}
}

// TestFetch_HLS_UppercaseExtension은 URL path suffix 검사가
// case-insensitive 임을 확인한다 — .M3U8도 HLS로 라우팅되어야 한다.
func TestFetch_HLS_UppercaseExtension(t *testing.T) {
	fixtureDir := t.TempDir()
	makeHLSFixture(t, fixtureDir, 1)

	// URL이 .M3U8로 끝나도록 대문자 파일명으로 옮긴다. 디스크상의 실제
	// 파일은 대소문자를 보존하지만, case-insensitive 파일시스템과 다투지
	// 않기 위해 복사한다.
	srcPath := filepath.Join(fixtureDir, "playlist.m3u8")
	body, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	fs := http.FileServer(http.Dir(fixtureDir))
	mux := http.NewServeMux()
	mux.HandleFunc("/STREAM.M3U8", func(w http.ResponseWriter, r *http.Request) {
		// text/plain은 폴백 분기를 트리거하고 URL path가 .M3U8로 끝나므로
		// HLS로 감지되어야 한다.
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write(body)
	})
	mux.Handle("/", fs)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/STREAM.M3U8", dest, "/movies", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if !strings.HasSuffix(res.Name, ".mp4") {
		t.Errorf("name = %q, want .mp4 suffix", res.Name)
	}
}

// TestFetch_HLS_PlaylistTooLarge는 1 MiB 플레이리스트 본문 상한을 검증한다.
// 한도를 약간 넘는 본문은 ffmpeg를 spawn하기 전에 거부되어야 하므로, 이
// 테스트는 ffmpeg가 필요 없다.
func TestFetch_HLS_PlaylistTooLarge(t *testing.T) {
	oversize := make([]byte, hlsfetch.MaxPlaylistBytes+1)
	for i := range oversize {
		oversize[i] = '#'
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write(oversize)
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/big.m3u8", dest, "/movies", testMaxBytes, nil)
	if ferr == nil {
		t.Fatal("expected hls_playlist_too_large error, got nil")
	}
	if ferr.Code != "hls_playlist_too_large" {
		t.Errorf("code = %q, want hls_playlist_too_large", ferr.Code)
	}
}

// TestFetch_HLS_PlaylistExactlyAtCap은 경계 조건을 고정한다 — 정확히
// hlsMaxPlaylistBytes 크기의 본문은 too large로 거부되어선 안 된다. 누군가
// 크기 검사에서 `>`를 `>=`로 바꾸는 회귀를 막는 가드다.
func TestFetch_HLS_PlaylistExactlyAtCap(t *testing.T) {
	// 유효하지만 사소한 플레이리스트에 주석 줄을 padding 해 정확히 상한에
	// 맞춘다. 사용 가능한 segment가 없으니 ffmpeg는 어차피 remux에 실패하므로,
	// 여기서는 크기 검사가 트리거되지 않는다는 점만 단언한다.
	head := []byte("#EXTM3U\n#EXT-X-VERSION:3\n")
	body := make([]byte, hlsfetch.MaxPlaylistBytes)
	copy(body, head)
	for i := len(head); i < len(body); i++ {
		body[i] = '#'
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/atcap.m3u8", dest, "/movies", testMaxBytes, nil)
	if ferr == nil {
		// 예기치 않게 성공했다면 ffmpeg가 비어 있는 미디어 플레이리스트를
		// 소비한 것. 어느 쪽이든 핵심 단언은 크기 검사가 트리거되지
		// 않는다는 것이다.
		return
	}
	if ferr.Code == "hls_playlist_too_large" {
		t.Errorf("exact-cap body rejected as too large (off-by-one)")
	}
}

// TestFetch_HLS_AudioMpegurl_Fallthrough는 Mux 대상 E2E에서 발견된 레거시
// MIME 단축 경로(commit 37c3024)를 보호한다 — audio/mpegurl은
// unsupported_content_type이 아닌 HLS 분기로 라우팅되어야 한다.
func TestFetch_HLS_AudioMpegurl_Fallthrough(t *testing.T) {
	fixtureDir := t.TempDir()
	playlistName := makeHLSFixture(t, fixtureDir, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/"+playlistName, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpegurl")
		http.ServeFile(w, r, filepath.Join(fixtureDir, playlistName))
	})
	mux.Handle("/", http.FileServer(http.Dir(fixtureDir)))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	res, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/"+playlistName, dest, "/movies", testMaxBytes, nil)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if res.Type != "video" {
		t.Errorf("type = %q, want video", res.Type)
	}
}

// TestDeriveHLSFilename_Fallbacks는 ".mp4"나 "..mp4"가 아니라
// "video.mp4"로 해석되어야 하는 세 종류의 퇴화된 stem 케이스를 검증한다.
func TestDeriveHLSFilename_Fallbacks(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"https://x.com/", "video.mp4"},
		{"https://x.com/.", "video.mp4"},
		{"https://x.com/..", "video.mp4"},
		{"https://x.com/path/.m3u8", "video.mp4"},
		{"https://x.com/foo.m3u8", "foo.mp4"},
		{"https://x.com/path/bar.M3U8", "bar.mp4"},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			u, err := url.Parse(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			if got := hlsfetch.DeriveFilename(u, hlsfetch.Deps{SanitizeFilename: sanitizeFilename}); got != tc.want {
				t.Errorf("deriveHLSFilename(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// TestFetch_HLS_VariantFileScheme은 defense-in-depth scheme 가드를 검증한다
// — 선택된 variant가 file://을 가리키는 마스터 플레이리스트는 ffmpeg가
// 돌기 전에 파싱 시점에 invalid_scheme으로 거부되어야 한다.
func TestFetch_HLS_VariantFileScheme(t *testing.T) {
	masterBody := "#EXTM3U\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=1000000\n" +
		"file:///etc/passwd\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte(masterBody))
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/master.m3u8", dest, "/movies", testMaxBytes, nil)
	if ferr == nil {
		t.Fatal("expected invalid_scheme error, got nil")
	}
	if ferr.Code != "invalid_scheme" {
		t.Errorf("code = %q, want invalid_scheme", ferr.Code)
	}
	entries, _ := os.ReadDir(dest)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".urlimport-") {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

// TestFetch_HLS_EmptyPlaylist_FFmpegError는 ffmpeg_error 매핑을 검증한다 —
// 비어 있지만 HLS 타입인 응답은 STREAM-INF가 없으므로 미디어 플레이리스트로
// 취급되지만, 가져올 segment가 없어 ffmpeg가 실패해야 한다.
func TestFetch_HLS_EmptyPlaylist_FFmpegError(t *testing.T) {
	requireFFmpeg(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte("not a real playlist"))
	}))
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/empty.m3u8", dest, "/movies", testMaxBytes, nil)
	if ferr == nil {
		t.Fatal("expected ffmpeg_error, got nil")
	}
	if ferr.Code != "ffmpeg_error" {
		t.Errorf("code = %q, want ffmpeg_error", ferr.Code)
	}
}

// TestFetch_HLS_Start_Callback_TotalZero는 HLS에서 Start 콜백이 total=0을
// 받는지 검증한다(H4가 wire에서 이를 JSON omitempty로 번역한다).
func TestFetch_HLS_Start_Callback_TotalZero(t *testing.T) {
	fixtureDir := t.TempDir()
	playlistName := makeHLSFixture(t, fixtureDir, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/"+playlistName, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		http.ServeFile(w, r, filepath.Join(fixtureDir, playlistName))
	})
	mux.Handle("/", http.FileServer(http.Dir(fixtureDir)))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	var gotTotal int64 = -1
	var gotName, gotType string
	cb := &Callbacks{
		Start: func(name string, total int64, fileType string) {
			gotName = name
			gotTotal = total
			gotType = fileType
		},
	}

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/"+playlistName, dest, "/movies", testMaxBytes, cb)
	if ferr != nil {
		t.Fatalf("fetch failed: %v", ferr)
	}
	if gotTotal != 0 {
		t.Errorf("Start total = %d, want 0", gotTotal)
	}
	if gotType != "video" {
		t.Errorf("Start fileType = %q, want video", gotType)
	}
	if gotName != "playlist.mp4" {
		t.Errorf("Start name = %q, want playlist.mp4", gotName)
	}
}
