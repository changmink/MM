package urlfetch

// Tests in this file exercise spec §3.1's HLS hardening at the integration
// level. They pair the protected URL client with a httptest server (which
// always binds 127.0.0.1) and AllowPrivateNetworks(), so they validate the
// flow shape — caps, cleanup, error code mapping — but cannot drive the
// "different IPs for master vs variant" scenarios literally.
//
// Why DNS-rebinding integration tests aren't here:
// publicOnlyDialContext is a single coarse "block all private" knob. Once
// AllowPrivateNetworks() is on, every private IP is allowed for the whole
// client; once it's off, even the httptest master server is unreachable.
// There is no third state where master.example resolves to a real public IP
// we can also serve from. The DNS rebinding MECHANISM is therefore covered
// at the unit level:
//   - TestDownloadOne_PrivateIPBlocked (hls_download_test.go):
//     sequenceResolver maps a hostname to 127.0.0.1; protected client
//     refuses with errPrivateNetwork. Same code path materializeHLS uses
//     for every segment / key / init fetch.
//   - TestSequenceResolver_OrderedAnswers (helpers_test.go):
//     ordered N-th-call answers — the building block for "first public,
//     second private" rebinding scenarios.
// Together they prove: the protected client validates IPs at every dial,
// and the sequenceResolver lets a future test stage different answers per
// call. fetchHLS just plumbs the same client through every fetch, so any
// hostname whose Nth resolution returns a private IP is blocked at that
// dial — without needing a real public origin.
//
// What this file covers (integration through Fetch + fetchHLS):
//   - hls_too_many_segments wire code (AC-13)
//   - hls_playlist_too_large for the variant playlist body (AC-12)
//   - hlsTempDir cleanup on failure (AC-16) and on success (AC-17)

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetch_HLS_TooManySegments_WireCode(t *testing.T) {
	// Build a media playlist with hlsMaxSegments + 1 #EXTINF entries —
	// parseMediaPlaylist must reject it and Fetch must surface the new
	// hls_too_many_segments code so operators can distinguish it from
	// generic "too_large".
	var buf strings.Builder
	buf.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:1\n")
	for i := 0; i <= hlsMaxSegments; i++ {
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

func TestFetch_HLS_VariantPlaylistTooLarge(t *testing.T) {
	// Master playlist points at a variant whose body exceeds the 1 MiB cap.
	// fetchPlaylistBody must reject before trying to parse the variant.
	mux := http.NewServeMux()
	mux.HandleFunc("/master.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = fmt.Fprintf(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\nvariant.m3u8\n")
	})
	mux.HandleFunc("/variant.m3u8", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		// Body 2 MiB — over the hlsMaxPlaylistBytes (1 MiB) cap.
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
	// Master + media playlist references a segment that 404s. fetchHLS must
	// remove .urlimport-hls-* even on failure paths so a partial download
	// doesn't leak into the user's media directory across multiple attempts.
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
		http.NotFound(w, r) // 404 → segment download fails
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dest := t.TempDir()
	_, ferr := Fetch(context.Background(), NewClient(AllowPrivateNetworks()),
		srv.URL+"/playlist.m3u8", dest, "/", testMaxBytes, nil)
	if ferr == nil {
		t.Fatal("expected error from segment 404, got nil")
	}

	// destDir must contain no .urlimport-hls-* directory after Fetch returns.
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
	// Full integration through Fetch + fetchHLS + materializeHLS + runHLSRemux
	// with the captureFfmpeg stub. Verifies the argv invariant (AC-10 / AC-11):
	// after the entire pipeline runs, ffmpeg only ever sees a local path on
	// -i and the protocol whitelist is exactly file,crypto. No remote URL
	// reaches ffmpeg regardless of how the playlist was structured.
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
	// Same invariant as above but with the maximally rich playlist: master →
	// variant → segments + #EXT-X-KEY + #EXT-X-MAP. Even with 4 different
	// remote-resource kinds, ffmpeg argv must remain local-only.
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

// verifyLocalOnlyArgv asserts the AC-10 / AC-11 invariants on a captured
// ffmpeg argv: -i input is a local absolute path and -protocol_whitelist is
// exactly file,crypto with no network protocols.
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
	// requireFFmpeg gates this — needs real ffmpeg to actually remux. The
	// goal is to confirm hlsTempDir doesn't survive a happy-path finish.
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

	// The final MP4 must be in dest, but no .urlimport-hls-* workspace.
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
