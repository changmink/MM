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

// --- 헬퍼 -----------------------------------------------------------------

func newCounter(initial int64) *atomic.Int64 {
	c := &atomic.Int64{}
	c.Store(initial)
	return c
}

// --- 테스트 ---------------------------------------------------------------

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
	// classifyHTTPError가 이를 http_error로 매핑해야 한다 — 호출자가 유용한
	// 진단 정보를 얻도록 에러 문자열에 상태 코드가 포함되는지만 확인한다.
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("err = %v, want to contain status 404", err)
	}
	// HTTP 실패 시에는 파일을 열지 않으므로 dest 파일이 존재해선 안 된다.
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
	// 서버가 perResourceMax보다 큰 body를 광고한다 — downloadOne은 바이트를
	// 읽기 전에 거부해야 한다.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		_, _ = w.Write(make([]byte, 1000000))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "seg.ts")
	_, err := downloadOne(context.Background(),
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/big.ts", dest, 1024 /* per-resource 상한 */, newCounter(testMaxBytes))
	if !errors.Is(err, errHLSTooLarge) {
		t.Errorf("err = %v, want errHLSTooLarge", err)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Errorf("dest file leaked on preflight cap: %v", statErr)
	}
}

func TestDownloadOne_RuntimeCapPerResource(t *testing.T) {
	// 서버가 Content-Length를 생략하므로 preflight가 거부할 수 없다. body가
	// perResourceMax를 넘으면 런타임 상한이 잡아내야 한다.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Content-Length 없이 스트리밍 — chunked encoding이 된다.
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
		srv.URL+"/x.ts", dest, 1024 /* per-resource 상한 */, newCounter(testMaxBytes))
	if !errors.Is(err, errHLSTooLarge) {
		t.Errorf("err = %v, want errHLSTooLarge", err)
	}
	// 첫 쓰기로 생긴 부분 파일은 정리되어야 한다.
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
	// 누적 remaining은 1 KiB, body는 4 KiB.
	_, err := downloadOne(context.Background(),
		NewClient(AllowPrivateNetworks()),
		srv.URL+"/x.ts", dest, 0, newCounter(1024))
	if !errors.Is(err, errHLSTooLarge) {
		t.Errorf("err = %v, want errHLSTooLarge", err)
	}
}

func TestDownloadOne_NoPerResourceCap(t *testing.T) {
	// perResourceMax=0은 "per-resource 상한 없음"을 의미한다 — 누적 상한만 적용된다.
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
	// 프로덕션 클라이언트(AllowPrivateNetworks 없음) + "blocked.example"을
	// 127.0.0.1로 가리키는 sequenceResolver — publicOnlyDialContext는 어떠한
	// HTTP 시도보다 먼저 dial을 errPrivateNetwork로 거부해야 한다.
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
		// 느린 핸들러 — 하지만 ctx 취소가 이 핸들러가 돌기 전에 종료시켜야 한다.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 미리 취소

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
	// downloadOne은 O_CREATE|O_EXCL을 사용해 destPath 중복을 쓰기 에러로
	// 만든다 — 두 리소스가 같은 파일을 가리키는 materializeHLS 버그를
	// 방지한다. HTTP 교환 이전에 실패하므로 실제 통신을 만들지 않아도 된다.
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
	// os.PathError이거나 fs.ErrExist를 wrap한 형태일 것 — 여기서는 nil이
	// 아니고 기존 파일이 그대로인지만 확인한다.
	got, _ := os.ReadFile(dest)
	if string(got) != "preexisting" {
		t.Errorf("dest overwritten to %q; downloadOne must not clobber", got)
	}
	_ = io.Discard // 나중을 위해 io import 유지
}
