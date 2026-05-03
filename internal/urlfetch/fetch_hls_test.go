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

// TestFetch_HLS_MediaPlaylist_Success covers the end-to-end happy path for a
// media playlist (no variants): HLS detection fires, ffmpeg remuxes the
// segments, the output is atomically renamed into destDir with the forced
// .mp4 extension and an "extension_replaced" warning.
func TestFetch_HLS_MediaPlaylist_Success(t *testing.T) {
	fixtureDir := t.TempDir()
	playlistName := makeHLSFixture(t, fixtureDir, 1)

	// Serve .m3u8 with the canonical HLS Content-Type, .ts segments with the
	// default mime (application/octet-stream via FileServer).
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

// TestFetch_HLS_MasterPlaylist_PicksHighestBandwidth serves a master playlist
// with two variants and verifies Fetch downloads the higher-bandwidth one.
// The low variant is a made-up media playlist that does not exist on disk, so
// hitting it would fail the remux — if the higher one is picked ffmpeg
// succeeds.
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
	// extension_replaced must be present on the master-playlist branch too
	// (not only on the media-playlist branch).
	if !slices.Contains(res.Warnings, "extension_replaced") {
		t.Errorf("master branch warnings = %v, want to contain extension_replaced", res.Warnings)
	}
}

// TestFetch_HLS_MislabeledContentType_Fallback covers CDNs that return
// text/plain for .m3u8 — our fallback detection must still route into the
// HLS branch instead of rejecting as unsupported_content_type.
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

// TestFetch_HLS_UppercaseExtension confirms the detection is case-insensitive
// on the URL path suffix so .M3U8 still routes to HLS.
func TestFetch_HLS_UppercaseExtension(t *testing.T) {
	fixtureDir := t.TempDir()
	makeHLSFixture(t, fixtureDir, 1)

	// Move to uppercase filename so the URL ends with .M3U8. The backing file
	// on disk is case-preserving; we copy to avoid fighting case-insensitive
	// filesystems.
	srcPath := filepath.Join(fixtureDir, "playlist.m3u8")
	body, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatal(err)
	}

	fs := http.FileServer(http.Dir(fixtureDir))
	mux := http.NewServeMux()
	mux.HandleFunc("/STREAM.M3U8", func(w http.ResponseWriter, r *http.Request) {
		// text/plain triggers the fallback branch AND the URL path ends .M3U8 →
		// should still detect as HLS.
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

// TestFetch_HLS_PlaylistTooLarge verifies the 1 MiB playlist body cap. A body
// just over the limit must be rejected before any ffmpeg spawn, so this test
// does NOT require ffmpeg to run.
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

// TestFetch_HLS_PlaylistExactlyAtCap pins the boundary condition: a body of
// exactly hlsMaxPlaylistBytes must NOT be rejected as too large. Regression
// guard against someone flipping the `>` to `>=` in the size check.
func TestFetch_HLS_PlaylistExactlyAtCap(t *testing.T) {
	// Valid-but-trivial playlist padded with comment lines to hit the cap
	// exactly. ffmpeg will still fail to remux (no usable segments) so we
	// only assert the size check doesn't trip.
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
		// Unexpected success means ffmpeg happily consumed an empty media
		// playlist. Either way the important assertion is that the size
		// check did not trip.
		return
	}
	if ferr.Code == "hls_playlist_too_large" {
		t.Errorf("exact-cap body rejected as too large (off-by-one)")
	}
}

// TestFetch_HLS_AudioMpegurl_Fallthrough guards the legacy MIME shortcut
// discovered during E2E against Mux (commit 37c3024): audio/mpegurl must
// route through the HLS branch rather than unsupported_content_type.
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

// TestDeriveHLSFilename_Fallbacks covers the three degenerate stem cases that
// must resolve to "video.mp4" rather than ".mp4" or "..mp4".
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

// TestFetch_HLS_VariantFileScheme checks the defense-in-depth scheme guard:
// a master playlist whose winning variant points at file:// must be rejected
// at parse time as invalid_scheme before ffmpeg runs.
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

// TestFetch_HLS_EmptyPlaylist_FFmpegError exercises the ffmpeg_error mapping:
// an empty-but-HLS-typed response is not a master playlist (no STREAM-INF) so
// it's treated as a media playlist, but ffmpeg will fail because it has no
// segments to pull.
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

// TestFetch_HLS_Start_Callback_TotalZero verifies the Start callback receives
// total=0 for HLS (H4 will translate that to JSON omitempty on the wire).
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
