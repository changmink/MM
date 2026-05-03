package hls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// --- helpers ---------------------------------------------------------------

func newCounter(initial int64) *atomic.Int64 {
	c := &atomic.Int64{}
	c.Store(initial)
	return c
}

// --- tests -----------------------------------------------------------------

func TestDownloadOne_Success(t *testing.T) {
	body := []byte("hello segment payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "seg_0000.ts")
	remaining := newCounter(testMaxBytes)

	n, err := downloadOne(context.Background(),
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/seg.ts", dest, 0, remaining)
	if err != nil {
		t.Fatalf("downloadOne: %v", err)
	}
	if n != int64(len(body)) {
		t.Errorf("bytes = %d, want %d", n, len(body))
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("file contents = %q, want %q", got, body)
	}
	if remaining.Load() != testMaxBytes-int64(len(body)) {
		t.Errorf("remaining = %d, want %d", remaining.Load(), testMaxBytes-int64(len(body)))
	}
}

func TestDownloadOne_HTTPError404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "seg.ts")
	_, err := downloadOne(context.Background(),
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/missing.ts", dest, 0, newCounter(testMaxBytes))
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
	// classifyHTTPError should map this to http_error ??we just verify the
	// error string contains the status code so callers get useful diagnostics.
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want to contain status 404", err)
	}
	// dest file must not exist (we never opened it on HTTP failure).
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("dest file leaked on HTTP error: %v", statErr)
	}
}

func TestDownloadOne_HTTPError500(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "seg.ts")
	_, err := downloadOne(context.Background(),
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/x.ts", dest, 0, newCounter(testMaxBytes))
	if err == nil || !strings.Contains(err.Error(), "500") {
		t.Errorf("err = %v, want HTTP 500", err)
	}
}

func TestDownloadOne_PreflightContentLengthCap(t *testing.T) {
	// Server advertises a body bigger than perResourceMax ??downloadOne must
	// reject before reading any bytes.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write(make([]byte, 1000000))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "seg.ts")
	_, err := downloadOne(context.Background(),
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/big.ts", dest, 1024 /* per-resource max */, newCounter(testMaxBytes))
	if !errors.Is(err, errHLSTooLarge) {
		t.Errorf("err = %v, want errHLSTooLarge", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("dest file leaked on preflight cap: %v", statErr)
	}
}

func TestDownloadOne_RuntimeCapPerResource(t *testing.T) {
	// Server omits Content-Length so preflight cannot reject; the runtime cap
	// must catch the body once it grows past perResourceMax.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream out without setting Content-Length ??chunked encoding.
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write(make([]byte, 2048))
		if flusher != nil {
			flusher.Flush()
		}
		_, _ = w.Write(make([]byte, 2048))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "seg.ts")
	_, err := downloadOne(context.Background(),
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/x.ts", dest, 1024 /* per-resource max */, newCounter(testMaxBytes))
	if !errors.Is(err, errHLSTooLarge) {
		t.Errorf("err = %v, want errHLSTooLarge", err)
	}
	// Partial file from first write must be cleaned up.
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("partial file leaked: %v", statErr)
	}
}

func TestDownloadOne_RuntimeCapCumulative(t *testing.T) {
	body := make([]byte, 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "seg.ts")
	// Cumulative remaining is 1 KiB, body is 4 KiB.
	_, err := downloadOne(context.Background(),
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/x.ts", dest, 0, newCounter(1024))
	if !errors.Is(err, errHLSTooLarge) {
		t.Errorf("err = %v, want errHLSTooLarge", err)
	}
}

func TestDownloadOne_NoPerResourceCap(t *testing.T) {
	// perResourceMax=0 means "no per-resource cap" ??only cumulative applies.
	body := make([]byte, 2048)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "seg.ts")
	n, err := downloadOne(context.Background(),
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/x.ts", dest, 0, newCounter(testMaxBytes))
	if err != nil {
		t.Fatalf("downloadOne: %v", err)
	}
	if n != int64(len(body)) {
		t.Errorf("bytes = %d, want %d", n, len(body))
	}
}

func TestDownloadOne_PrivateIPBlocked(t *testing.T) {
	// Production client (no AllowPrivateNetworks) + sequenceResolver pointing
	// "blocked.example" at 127.0.0.1 ??publicOnlyDialContext must reject the
	// dial with errPrivateNetwork before any HTTP attempt.
	resolver := newSequenceResolver(map[string][]net.IPAddr{
		"blocked.example": {{IP: net.ParseIP("127.0.0.1")}},
	})
	client := NewClient(WithResolver(resolver))

	dest := filepath.Join(t.TempDir(), "seg.ts")
	_, err := downloadOne(context.Background(), client,
		"http://blocked.example/seg.ts", dest, 0, newCounter(testMaxBytes))
	if !errors.Is(err, errPrivateNetwork) {
		t.Errorf("err = %v, want errors.Is(err, errPrivateNetwork)", err)
	}
}

func TestDownloadOne_ContextCancelBeforeRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow handler ??but ctx cancel should bail before this runs.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	dest := filepath.Join(t.TempDir(), "seg.ts")
	_, err := downloadOne(ctx,
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/x.ts", dest, 0, newCounter(testMaxBytes))
	if err == nil {
		t.Fatal("expected error for cancelled ctx")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(err, context.Canceled)", err)
	}
}

func TestDownloadOne_DestExists_OEXCL(t *testing.T) {
	// downloadOne uses O_CREATE|O_EXCL so a duplicate destPath is a write
	// error ??guards against materializeHLS bugs that would name two
	// resources to the same file. Use a closed httptest server because the
	// failure happens before any HTTP exchange.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("data"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "seg.ts")
	if err := os.WriteFile(dest, []byte("preexisting"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := downloadOne(context.Background(),
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/x.ts", dest, 0, newCounter(testMaxBytes))
	if err == nil {
		t.Fatal("expected error for existing dest path")
	}
	// Should be an os.PathError or wraps fs.ErrExist; we just check it's not
	// nil and the existing file is untouched.
	got, _ := os.ReadFile(dest)
	if string(got) != "preexisting" {
		t.Errorf("dest overwritten to %q; downloadOne must not clobber", got)
	}
	_ = io.Discard // keep io import for future if needed
}
