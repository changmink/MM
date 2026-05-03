package hls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// captureFfmpeg는 runFfmpeg를 호출마다 argv를 기록하고 출력 경로에 stub
// MP4(ftyp box)를 써주는 stub으로 교체한다. 그래야 호출자의 후속 os.Stat /
// atomic rename이 성공한다. 호출별 argv가 누적되는 슬라이스의 포인터를
// 반환한다. cleanup은 t.Cleanup으로 원래 runFfmpeg를 복원한다.
//
// 중요: captureFfmpeg를 쓰는 테스트는 t.Parallel()을 호출해서는 안 된다 —
// runFfmpeg는 패키지 수준 var이라 동시 swap이 race를 일으킨다. 코드
// 리뷰가 이 규칙을 강제한다.
func captureFfmpeg(t *testing.T) *[][]string {
	t.Helper()
	var captured [][]string
	var mu sync.Mutex

	orig := runFfmpeg
	runFfmpeg = func(ctx context.Context, args []string, stderr io.Writer) error {
		mu.Lock()
		// 사후 arg 변형이 기록을 거꾸로 오염시키지 않도록 복사한다.
		captured = append(captured, append([]string(nil), args...))
		mu.Unlock()

		// 호출자의 Stat/rename이 성공하도록 출력 경로에 stub MP4를 쓴다.
		// argv 패턴: 끝에 ... -y <outPath>.
		for i := 0; i < len(args)-1; i++ {
			if args[i] == "-y" {
				outPath := args[i+1]
				_ = os.WriteFile(outPath, []byte{
					0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p',
					'm', 'p', '4', '2', 0x00, 0x00, 0x00, 0x00,
					'i', 's', 'o', 'm', 'm', 'p', '4', '2',
				}, 0644)
				break
			}
		}
		return nil
	}
	t.Cleanup(func() { runFfmpeg = orig })

	return &captured
}

// requireFFmpeg는 ffmpeg가 없을 때 테스트를 skip한다.
// handler/stream_test.go와 같은 패턴이라 ffmpeg가 없는 CI 머신에서도 단위
// 테스트가 돌아간다.
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}
}

// makeHLSFixture는 dir 안에 durationSec 초 길이의 VOD HLS 플레이리스트와
// .ts segment를 만들고 플레이리스트의 basename을 반환한다. 1초 segment를
// 사용하므로 durationSec개의 segment가 쓰인다. 백그라운드 watcher
// (progress/size cap)를 검증하려면 remuxer가 출력을 쓰는 시간이 충분히
// 길어야 하므로 이 점이 중요하다.
func makeHLSFixture(t *testing.T, dir string, durationSec int) string {
	t.Helper()
	requireFFmpeg(t)
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "color=black:size=64x64:rate=1",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", fmt.Sprintf("%d", durationSec),
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "1",
		"-c:a", "aac",
		"-hls_time", "1",
		"-hls_segment_type", "mpegts",
		"-hls_list_size", "0",
		"-hls_playlist_type", "vod",
		"-f", "hls",
		filepath.Join(dir, "playlist.m3u8"),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("makeHLSFixture: %v\nstderr: %s", err, stderr.String())
	}
	return "playlist.m3u8"
}

// slowHLSServer는 디렉터리 FileServer를 감싸서 모든 .ts fetch가 응답 전에
// 최소 perSegment만큼 대기하게 만든다. 500 ms 주기의 watcher가 출력 증가를
// 관측하고(progress 테스트), ffmpeg가 끝나기 전에 cap 위반을 잡아낼 수
// 있도록(size cap 테스트) remux 시간을 늘리기 위함이다.
func slowHLSServer(t *testing.T, dir string, perSegment time.Duration) *httptest.Server {
	t.Helper()
	fs := http.FileServer(http.Dir(dir))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".ts") {
			time.Sleep(perSegment)
		}
		fs.ServeHTTP(w, r)
	}))
}

func TestRunHLSRemux_Success(t *testing.T) {
	// makeHLSFixture가 fixtureDir에 playlist.m3u8 + .ts segment를 쓴다.
	// D2 이후 ffmpeg 호출은 로컬 파일 경로만 받으므로, 플레이리스트 경로를
	// 직접 넘긴다 — httptest 서버가 끼어들지 않는다.
	fixtureDir := t.TempDir()
	playlistName := makeHLSFixture(t, fixtureDir, 1)
	localPlaylist := filepath.Join(fixtureDir, playlistName)

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "out.mp4")

	err := runHLSRemux(context.Background(), localPlaylist, outPath, nil, testMaxBytes)
	if err != nil {
		t.Fatalf("runHLSRemux: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if len(data) < 8 {
		t.Fatalf("output too short: %d bytes", len(data))
	}
	// MP4 컨테이너 시그니처: bytes [4:8]은 "ftyp"여야 한다.
	if string(data[4:8]) != "ftyp" {
		t.Errorf("output is not MP4 (no ftyp box): % x", data[:16])
	}
}

// TestRunHLSRemux_CtxCancelPropagates: 예전 TestRunHLSRemux_ContextCancel은
// HTTP 위의 느린 segment로 ffmpeg를 지연시켰다. D2 이후 ffmpeg는 로컬 파일만
// 읽으므로(네트워크 없음) 같은 방식으로 I/O에서 지연시킬 수 없다. 대신
// 배선 자체를 검증한다 — ctx가 트리거될 때까지 블록하는 stub runFfmpeg가
// 동일한 속성을 확인한다(외부 ctx 취소가 프로세스에 전파됨). ffmpeg가
// 필요 없다(바이너리 없이도 돈다).
func TestRunHLSRemux_CtxCancelPropagates(t *testing.T) {
	orig := runFfmpeg
	runFfmpeg = func(ctx context.Context, args []string, stderr io.Writer) error {
		<-ctx.Done()
		return ctx.Err()
	}
	t.Cleanup(func() { runFfmpeg = orig })

	ctx, cancel := context.WithCancel(context.Background())
	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "out.mp4")

	var wg sync.WaitGroup
	var err error
	wg.Add(1)
	go func() {
		defer wg.Done()
		err = runHLSRemux(ctx, "/local/playlist.m3u8", outPath, nil, testMaxBytes)
	}()

	// 취소하기 전에 goroutine이 runFfmpeg를 호출할 시간을 잠깐 준다.
	time.Sleep(50 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("runHLSRemux did not return after ctx cancel")
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want errors.Is(err, context.Canceled)", err)
	}
}

// TestWatchOutputFile_DetectsOversize는 ffmpeg의 버퍼링된 출력과 독립적으로
// progress watcher의 size-cap 강제 경로를 검증한다 — 수동 writer가 파일을
// cap 너머까지 키우면 watcher는 onOversize를 정확히 한 번 호출하고 반환해야
// 한다.
func TestWatchOutputFile_DetectsOversize(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "growing.bin")
	if err := os.WriteFile(tmpPath, nil, 0644); err != nil {
		t.Fatal(err)
	}

	// Writer goroutine이 20 ms마다 100 B를 추가한다 — 약 10 라운드 후 파일이
	// 512 B 상한을 넘는다.
	stopWrite := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWrite:
				return
			case <-ticker.C:
				f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					return
				}
				_, _ = f.Write(bytes.Repeat([]byte("A"), 100))
				_ = f.Close()
			}
		}
	}()
	defer func() { close(stopWrite); <-writerDone }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var oversizeCalls atomic.Int64
	watchOutputFile(ctx, tmpPath, 50*time.Millisecond, 512, nil, func() {
		oversizeCalls.Add(1)
	})

	if oversizeCalls.Load() != 1 {
		t.Errorf("onOversize called %d times, want 1", oversizeCalls.Load())
	}
}

// TestWatchOutputFile_EmitsProgress는 progress 경로를 검증한다 — 파일이
// 커질 때 cb.Progress가 단조 비감소 크기로 호출되어야 한다.
func TestWatchOutputFile_EmitsProgress(t *testing.T) {
	dir := t.TempDir()
	tmpPath := filepath.Join(dir, "growing.bin")
	if err := os.WriteFile(tmpPath, nil, 0644); err != nil {
		t.Fatal(err)
	}

	stopWrite := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		ticker := time.NewTicker(30 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopWrite:
				return
			case <-ticker.C:
				f, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_APPEND, 0644)
				if err != nil {
					return
				}
				_, _ = f.Write(bytes.Repeat([]byte("B"), 200))
				_ = f.Close()
			}
		}
	}()

	var mu sync.Mutex
	var seen []int64
	cb := &Callbacks{
		Progress: func(n int64) {
			mu.Lock()
			seen = append(seen, n)
			mu.Unlock()
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	watchOutputFile(ctx, tmpPath, 50*time.Millisecond, 1<<30, cb, func() {})

	close(stopWrite)
	<-writerDone

	mu.Lock()
	defer mu.Unlock()
	if len(seen) == 0 {
		t.Fatal("expected ?? progress sample, got 0")
	}
	for i := 1; i < len(seen); i++ {
		if seen[i] < seen[i-1] {
			t.Errorf("progress non-monotonic: %v", seen)
			break
		}
	}
}

func TestRunHLSRemux_ExitError(t *testing.T) {
	requireFFmpeg(t)
	// D2 이후 runHLSRemux는 로컬 파일만 읽는다. 디스크에 존재하지 않는
	// segment를 가리키는 플레이리스트로 ffmpeg를 강제 실패시킨다.
	fixtureDir := t.TempDir()
	badPlaylist := filepath.Join(fixtureDir, "bad.m3u8")
	err := os.WriteFile(badPlaylist, []byte("#EXTM3U\n"+
		"#EXT-X-VERSION:3\n"+
		"#EXT-X-TARGETDURATION:6\n"+
		"#EXTINF:6.0,\n"+
		"nonexistent-segment.ts\n"+
		"#EXT-X-ENDLIST\n"), 0644)
	if err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	outPath := filepath.Join(outDir, "out.mp4")

	err = runHLSRemux(context.Background(), badPlaylist, outPath, nil, testMaxBytes)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var exitErr *ffmpegExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("got %T (%v), want *ffmpegExitError", err, err)
	}
	// stderr 내용이 아닌 exit 코드를 단언한다 — ffmpeg의 stderr 문구는
	// 버전과 loglevel 설정에 따라 달라진다.
	if exitErr.exitCode == 0 {
		t.Errorf("exitCode = 0, want non-zero")
	}
}

// TestClassifyHLSRemuxError는 sentinel → FetchError.Code 매핑을 고정한다.
// 향후 리팩터링이 ffmpeg_missing을 조용히 ffmpeg_error로 합쳐 운영자 오설정
// 케이스를 가리거나, ctx 실패에서 download_timeout과 network_error를 바꿔
// 끼는 일이 없도록 한다.
func TestClassifyHLSRemuxError(t *testing.T) {
	cases := []struct {
		name string
		in   error
		want string
	}{
		{"deadline exceeded", context.DeadlineExceeded, "download_timeout"},
		{"canceled", context.Canceled, "network_error"},
		{"too large", errHLSTooLarge, "too_large"},
		{"ffmpeg missing", errFFmpegMissing, "ffmpeg_missing"},
		{"exit error", &ffmpegExitError{exitCode: 1, stderr: "oops"}, "ffmpeg_error"},
		{"other", errors.New("random"), "ffmpeg_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ferr := classifyHLSRemuxError(tc.in)
			if ferr.Code != tc.want {
				t.Errorf("code = %q, want %q", ferr.Code, tc.want)
			}
			if ferr.Unwrap() != tc.in {
				t.Errorf("wrapped err not preserved")
			}
		})
	}
}

// TestRunHLSRemux_ArgvLocalOnly: runHLSRemux 수준의 argv 불변식
// (spec AC-10 / AC-11 일부). ffmpeg argv를 캡처해 다음을 단언한다:
//   - -protocol_whitelist는 정확히 file,crypto (http/https/tcp/tls 등 금지)
//   - -i 입력은 http:// 또는 https:// prefix를 갖지 않는다.
// E3가 fetchHLS 수준에서 materialize → runHLSRemux 전체 흐름을 다루는
// 통합 테스트를 추가한다.
func TestRunHLSRemux_ArgvLocalOnly(t *testing.T) {
	captured := captureFfmpeg(t)

	outPath := filepath.Join(t.TempDir(), "out.mp4")
	err := runHLSRemux(context.Background(), "/local/playlist.m3u8", outPath, nil, testMaxBytes)
	if err != nil {
		t.Fatalf("runHLSRemux: %v", err)
	}
	if len(*captured) != 1 {
		t.Fatalf("captured calls = %d, want 1", len(*captured))
	}
	args := (*captured)[0]

	// -protocol_whitelist 값을 찾아 검증한다.
	whitelistIdx := -1
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-protocol_whitelist" {
			whitelistIdx = i + 1
			break
		}
	}
	if whitelistIdx == -1 {
		t.Fatalf("-protocol_whitelist not in argv: %v", args)
	}
	if args[whitelistIdx] != "file,crypto" {
		t.Errorf("-protocol_whitelist = %q, want %q", args[whitelistIdx], "file,crypto")
	}
	for _, forbidden := range []string{"http", "tcp", "tls", "udp", "rtp", "pipe", "async"} {
		if strings.Contains(args[whitelistIdx], forbidden) {
			t.Errorf("-protocol_whitelist contains forbidden token %q: %q",
				forbidden, args[whitelistIdx])
		}
	}

	// -i 입력이 로컬임을 확인한다(원격 URL이 아님).
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-i" {
			input := args[i+1]
			if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
				t.Errorf("-i input is a remote URL: %q", input)
			}
		}
	}
}

// TestRunFfmpegSwap_StubBypassesBinary는 AC-10 / AC-11 argv 불변식
// 테스트가 사용하는 captureFfmpeg 계약을 고정한다 — stub이 설치되면
// runHLSRemux가 프로덕션 경로를 완전히 대체해 실제 ffmpeg는 실행되지 않고,
// argv가 캡처되고, 출력 경로에 stub MP4가 만들어져 fetchHLS의 후속 rename
// / Stat이 성공한다. argv 검증 테스트가 ffmpeg가 없는 머신에서도 돌아가게
// 하는 전제 조건이다.
func TestRunFfmpegSwap_StubBypassesBinary(t *testing.T) {
	captured := captureFfmpeg(t)

	outPath := filepath.Join(t.TempDir(), "out.mp4")
	err := runHLSRemux(context.Background(),
		"https://example.com/playlist.m3u8", outPath, nil, testMaxBytes)
	if err != nil {
		t.Fatalf("runHLSRemux: %v", err)
	}
	if len(*captured) != 1 {
		t.Fatalf("captured calls = %d, want 1; argv = %v", len(*captured), *captured)
	}
	stat, err := os.Stat(outPath)
	if err != nil {
		t.Fatalf("stub MP4 missing: %v", err)
	}
	if stat.Size() < 8 {
		t.Errorf("stub MP4 too small: %d bytes", stat.Size())
	}
	// sanity: runHLSRemux는 첫 번째 인자를 그대로 -i 입력으로 전달한다.
	// D2 이후 프로덕션 호출자는 여기 로컬 플레이리스트 경로를 이미
	// 갖고 있어야 한다 — TestRunHLSRemux_ArgvLocalOnly가 실제 사용에서
	// 원격 URL이 ffmpeg에 닿지 못함을 강제한다. 이 테스트는 swap
	// 메커니즘만 검증하므로 dummy URL을 쓴다.
	args := (*captured)[0]
	for i, a := range args {
		if a == "-i" && i+1 < len(args) {
			if args[i+1] != "https://example.com/playlist.m3u8" {
				t.Errorf("-i arg = %q, want the dummy first arg passed in", args[i+1])
			}
		}
	}
}
