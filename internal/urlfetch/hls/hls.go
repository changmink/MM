package hls

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"file_server/internal/ffmpeg"
)

// Result는 성공한 HLS import의 결과를 담는다.
type Result struct {
	URL      string
	Path     string
	Name     string
	Size     int64
	Type     string
	Warnings []string
}

// FetchError는 Fetch가 반환하는 타입화된 실패 정보다.
type FetchError struct {
	Code string
	Err  error
}

func (e *FetchError) Error() string { return e.Code }
func (e *FetchError) Unwrap() error { return e.Err }

// Callbacks는 진행 중인 HLS fetch를 호출자가 관찰할 수 있게 해준다.
type Callbacks struct {
	Start    func(name string, total int64, fileType string)
	Progress func(received int64)
}

// Deps는 import 사이클 없이 부모 패키지의 헬퍼를 HLS 서브패키지로 전달한다.
type Deps struct {
	ClassifyHTTPError func(error) *FetchError
	RenameUnique      func(tmpPath, destDir, name string) (string, bool, error)
	SanitizeFilename  func(string) string
}

const (
	progressByteThreshold = 1 << 20
	progressTimeThreshold = 250 * time.Millisecond
)

// HLS 처리용 sentinel 에러들. classifyHLSRemuxError는 이들과 함께
// context.Canceled / context.DeadlineExceeded까지 SPEC §5.1에 문서화된
// 안정적인 FetchError.Code 값으로 매핑한다.
var (
	errHLSVariantScheme    = errors.New("invalid_scheme")
	errFFmpegMissing       = ffmpeg.ErrMissing
	errHLSTooLarge         = errors.New("hls_too_large")
	errHLSTooManySegments  = errors.New("hls_too_many_segments")
	errHLSTooManyKeys      = errors.New("hls_too_many_keys")
	errHLSTooManyInits     = errors.New("hls_too_many_inits")
	errHLSDuplicateURIAttr = errors.New("hls_duplicate_uri_attr")
	errHLSMissingMapURI    = errors.New("hls_map_missing_uri")
)

// hlsMaxSegments는 미디어 플레이리스트 하나가 선언할 수 있는 #EXTINF
// 세그먼트 수의 상한이다. 10,000개 ≈ 6초 세그먼트 기준 16시간 VOD ≈
// 일반적인 영화·강의 분량보다 충분히 크지만, 누적 바이트 상한
// (url_import_max_bytes)만으로는 막을 수 없는 "1바이트 × 수백만" 요청률
// 폭주를 차단할 만큼 작다. spec §3.2 D-8 참고.
const MaxSegments = 10000

const hlsMaxSegments = MaxSegments

// hlsMaxKeyEntries는 미디어 플레이리스트 하나가 선언할 수 있는 #EXT-X-KEY
// 로테이션 수의 상한이다. 실제 HLS는 스트림당 키 로테이션이 몇 번을 넘는
// 경우가 드물다 — 256개는 6초 세그먼트 기준 1세그먼트당 1키 로테이션 시
// 약 25분에 해당해 충분히 여유롭다. 적대적 플레이리스트가 키 수천 개를
// 선언하고 각 키가 hlsMaxKeyBytes(64 KiB)까지 차지하면서 실제 세그먼트가
// 발사되기 전에 url_import_max_bytes를 고갈시키는 예산 소진 공격을 막는다.
const hlsMaxKeyEntries = 256

// hlsMaxInitEntries는 #EXT-X-MAP init 세그먼트 수의 상한이다. 표준 HLS는
// (드물게 discontinuity마다 하나씩) 최대 한 개를 사용한다. 4는 다중
// discontinuity가 있는 병적이지만 가능한 플레이리스트도 통과시키되,
// hlsMaxKeyEntries와 동일한 예산 소진 공격은 차단한다.
const hlsMaxInitEntries = 4

// ffmpegExitError는 non-zero로 종료된 ffmpeg와 캡처된 stderr를 함께 감싸,
// 호출자가 진단 컨텍스트를 로그에 노출할 수 있게 한다.
type ffmpegExitError struct {
	exitCode int
	stderr   string
}

func (e *ffmpegExitError) Error() string {
	return fmt.Sprintf("ffmpeg exited %d: %s", e.exitCode, e.stderr)
}

// hlsWatchInterval은 ffmpeg가 실행 중일 때 runner가 tmp 출력 파일을
// 점검하는 주기다. 500 ms는 SSE 피드를 보는 사람에게 적당한 progress 샘플
// 간격이면서, 유휴 remux에 대한 syscall 낭비를 줄이는 균형점이다.
const hlsWatchInterval = 500 * time.Millisecond

// hlsMaxPlaylistBytes는 마스터 플레이리스트를 파싱하기 위해 초기 응답
// 본문에서 읽을 수 있는 최대 바이트 수다. 실제 마스터 플레이리스트는 몇
// KiB 수준이며, 1 MiB는 메모리에 무리 없이 들어가는 충분히 여유로운
// 방어선이다.
const MaxPlaylistBytes = 1 << 20

const hlsMaxPlaylistBytes = MaxPlaylistBytes

// isHLSResponse는 HLS 분기로 들어갈지 결정한다. 1차 신호는 정규 HLS
// Content-Type이다. "audio/mpegurl"은 RFC 8216 이전의 레거시 형태인데
// 여전히 일부 실제 CDN(Mux의 GCS 테스트 스트림, 일부 Akamai 설정 등)이
// 내보낸다 — 이를 HLS로 취급해야 정상 공개 스트림에 대한 거짓
// unsupported_content_type을 피할 수 있다. 폴백은 .m3u8을 text/plain이나
// application/octet-stream으로 잘못 라벨링하는 CDN을 위한 것이며, URL
// path가 명확히 플레이리스트일 때만 적용해 무관한 URL의 일반 텍스트
// 응답이 잘못 분류되지 않게 한다.
func IsResponse(contentType, urlPath string) bool {
	mt, _, _ := mime.ParseMediaType(contentType)
	mt = strings.ToLower(mt)
	switch mt {
	case "application/vnd.apple.mpegurl",
		"application/x-mpegurl",
		"audio/mpegurl",
		"audio/x-mpegurl":
		return true
	}
	if !strings.HasSuffix(strings.ToLower(urlPath), ".m3u8") {
		return false
	}
	switch mt {
	case "", "text/plain", "application/octet-stream":
		return true
	}
	return false
}

func isHLSResponse(contentType, urlPath string) bool {
	return IsResponse(contentType, urlPath)
}

var bandwidthRE = regexp.MustCompile(`BANDWIDTH=(\d+)`)

// parseMasterPlaylist는 HLS 플레이리스트 본문을 검사하고 ffmpeg에 넘길
// URL을 반환한다. 본문이 마스터 플레이리스트(#EXT-X-STREAM-INF 항목이 하나
// 이상)이면 BANDWIDTH 속성이 가장 큰 variant를 선택하고, 동률이면 선언
// 순서로 결정한다. variant가 없으면 본문을 미디어 플레이리스트로 취급하고
// base를 변경 없이 반환한다. 상대 URL은 base 기준으로 해석하며, 해석된
// scheme이 http/https가 아닌 variant는 즉시 거부해 ffmpeg의
// protocol_whitelist를 애플리케이션 계층에서도 한 번 더 막는다.
func parseMasterPlaylist(body []byte, base *url.URL) (*url.URL, error) {
	lines := strings.Split(string(body), "\n")

	var bestURL string
	var bestBW int64 = -1 // -1이면 BANDWIDTH=0인 첫 variant도 선택되도록 한다.

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			continue
		}
		bw := extractBandwidth(line)
		variantLine := ""
		for j := i + 1; j < len(lines); j++ {
			cand := strings.TrimSpace(lines[j])
			if cand == "" || strings.HasPrefix(cand, "#") {
				continue
			}
			variantLine = cand
			i = j
			break
		}
		if variantLine == "" {
			continue
		}
		if bw > bestBW {
			bestBW = bw
			bestURL = variantLine
		}
	}

	if bestURL == "" {
		return base, nil
	}

	parsed, err := url.Parse(bestURL)
	if err != nil {
		return nil, err
	}
	resolved := base.ResolveReference(parsed)
	scheme := strings.ToLower(resolved.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errHLSVariantScheme
	}
	// 선택된 variant가 자기 자신으로 해석되는 (악의적이거나 망가진) 마스터
	// 플레이리스트를 방어한다. 같은 URL을 ffmpeg에 넘기면 같은 마스터로
	// 다시 들어가는 루프가 된다. 이런 경우 미디어 플레이리스트로 취급하도록
	// 폴백한다 — 정말 미디어 플레이리스트면 성공할 것이고, 아니면
	// ffmpeg_error로 실패할 것이라 어느 쪽이든 올바른 결과다.
	if sameURL(resolved, base) {
		return base, nil
	}
	return resolved, nil
}

// entryKind는 playlistEntry를 그 소스 태그로 분류한다. materializeHLS가
// 명명 규칙(seg_NNNN.ext vs key_N.bin vs init.ext)과 URI 재작성 종류를
// 결정하는 데 필요하다.
type entryKind int

const (
	entrySegment entryKind = iota
	entryKey
	entryInit
)

// playlistEntry는 미디어 플레이리스트가 참조하는 원격 리소스 하나를
// 나타낸다. lineIdx는 materializeHLS가 재작성해야 하는 rawLines 요소를
// 가리킨다 — segment의 경우는 URI 라인 자체, #EXT-X-KEY / #EXT-X-MAP의
// 경우는 URI를 속성으로 품은 태그 라인이다.
type playlistEntry struct {
	lineIdx int
	uri     *url.URL
	kind    entryKind
}

// mediaPlaylist는 파싱된 미디어 플레이리스트의 뷰다. rawLines는 입력을
// 그대로 보존해, materializeHLS가 URI 부분만 교체한 거의 동일한 플레이리스트를
// 출력할 수 있게 한다. entries는 ffmpeg가 재작성된 플레이리스트를 소비하기
// 전에 다운로드·재작성이 필요한 외부 리소스들을 모두 열거한다.
type mediaPlaylist struct {
	rawLines []string
	entries  []playlistEntry
}

// uriAttrRE은 #EXT-X-KEY와 #EXT-X-MAP에서 사용하는 URI="..." 속성에서
// 값을 추출한다. 실제 HLS 속성 리스트는 quoted 문자열과 unquoted
// enumeration이 섞인 CSV이지만, 우리가 필요한 URI는 RFC 8216 §4.2에 따라
// 항상 quoted 형태다.
var uriAttrRE = regexp.MustCompile(`URI="([^"]*)"`)

// parseMediaPlaylist는 플레이리스트 본문을 순회하며 모든 외부 리소스
// (#EXTINF 세그먼트, METHOD=NONE을 제외한 #EXT-X-KEY URI, #EXT-X-MAP init
// 세그먼트)를 base 기준으로 해석한 URL과 함께 수집한다. 반환값은:
//   - 해석된 scheme이 http/https가 아닌 URI가 있으면 errHLSVariantScheme
//   - 상한을 넘으면 errHLSTooManySegments / errHLSTooManyKeys / errHLSTooManyInits
//   - 단일 #EXT-X-KEY/#EXT-X-MAP 라인에 URI="..." 속성이 두 개 이상 선언돼
//     있으면 errHLSDuplicateURIAttr (parser는 첫 번째를 취하지만 rewriter는
//     모두 건드릴 수 있어, 둘이 어긋나지 않도록 플레이리스트 자체를 거부)
//   - URI 없는 #EXT-X-MAP에는 errHLSMissingMapURI
//
// RFC 8216 §4.1.1에 따라, #EXTINF와 세그먼트 URI 라인 사이에 미디어
// 플레이리스트는 #EXT-X-DISCONTINUITY, #EXT-X-BYTERANGE,
// #EXT-X-PROGRAM-DATE-TIME 같은 보조 태그를 끼워 넣을 수 있다. pendingSeg
// 래치가 그 사이에서 상태를 유지한다 (`#`로 시작하는 라인은 그대로 보존되며
// 래치를 소비하지 않는다).
//
// 본문이 비었거나 주석만 있는 경우 항목이 없는 플레이리스트를 (에러 없이)
// 반환한다 — fetchHLS는 이를 퇴화한 스트림으로 취급하고 ffmpeg가 자연스럽게
// 실패하도록 둔다.
func parseMediaPlaylist(body []byte, base *url.URL) (*mediaPlaylist, error) {
	rawLines := splitPlaylistLines(body)
	pl := &mediaPlaylist{rawLines: rawLines}

	// 상태: 직전에 #EXTINF를 봤는가? 그렇다면 다음에 오는 주석·빈 줄이 아닌
	// 라인이 그 세그먼트의 URI다.
	pendingSeg := false
	segCount := 0
	keyCount := 0
	initCount := 0

	for i, line := range rawLines {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "#EXTINF"):
			pendingSeg = true
		case strings.HasPrefix(trim, "#EXT-X-KEY"):
			if strings.Count(trim, `URI="`) > 1 {
				return nil, errHLSDuplicateURIAttr
			}
			uriStr := uriAttrValue(trim)
			if uriStr == "" {
				// METHOD=NONE은 URI가 없어 다운로드할 게 없다. URI 없는 다른
				// 태그도 방어적으로 같은 분기로 흐른다.
				continue
			}
			entry, err := makePlaylistEntry(uriStr, base, i, entryKey)
			if err != nil {
				return nil, err
			}
			pl.entries = append(pl.entries, entry)
			keyCount++
			if keyCount > hlsMaxKeyEntries {
				return nil, errHLSTooManyKeys
			}
		case strings.HasPrefix(trim, "#EXT-X-MAP"):
			if strings.Count(trim, `URI="`) > 1 {
				return nil, errHLSDuplicateURIAttr
			}
			uriStr := uriAttrValue(trim)
			if uriStr == "" {
				return nil, errHLSMissingMapURI
			}
			entry, err := makePlaylistEntry(uriStr, base, i, entryInit)
			if err != nil {
				return nil, err
			}
			pl.entries = append(pl.entries, entry)
			initCount++
			if initCount > hlsMaxInitEntries {
				return nil, errHLSTooManyInits
			}
		case strings.HasPrefix(trim, "#"):
			// 그 외 태그(#EXTM3U, #EXT-X-VERSION, #EXT-X-BYTERANGE 등) —
			// rawLines에 그대로 보존하고 entry는 만들지 않는다. materializeHLS
			// 의 rewrite 패스가 여기 등장하는 URI="..." 속성을 ""로 정규화하므로,
			// 향후 ffmpeg whitelist가 완화되더라도 인식되지 않은 태그가 원격
			// URL을 ffmpeg 입력으로 끌고 들어갈 일은 없다.
		case trim == "":
			// 빈 줄 — rawLines에 보존하고 entry는 만들지 않는다.
		default:
			// 주석·빈 줄이 아닌 라인. pendingSeg가 켜져 있으면 세그먼트
			// URI이다. 아니면 미아로 취급해 무시한다 — 인식되지 않은 태그의
			// 연장선일 수도 있다.
			if !pendingSeg {
				continue
			}
			entry, err := makePlaylistEntry(trim, base, i, entrySegment)
			if err != nil {
				return nil, err
			}
			pl.entries = append(pl.entries, entry)
			pendingSeg = false
			segCount++
			if segCount > hlsMaxSegments {
				return nil, errHLSTooManySegments
			}
		}
	}

	return pl, nil
}

func makePlaylistEntry(uriStr string, base *url.URL, lineIdx int, kind entryKind) (playlistEntry, error) {
	parsed, err := url.Parse(uriStr)
	if err != nil {
		return playlistEntry{}, err
	}
	resolved := base.ResolveReference(parsed)
	scheme := strings.ToLower(resolved.Scheme)
	if scheme != "http" && scheme != "https" {
		return playlistEntry{}, errHLSVariantScheme
	}
	return playlistEntry{lineIdx: lineIdx, uri: resolved, kind: kind}, nil
}

// uriAttrValue는 #EXT-X-KEY 또는 #EXT-X-MAP 태그 라인에서 URI 속성 값을
// 추출한다. URI가 없으면 ""를 반환한다.
func uriAttrValue(tagLine string) string {
	m := uriAttrRE.FindStringSubmatch(tagLine)
	if m == nil {
		return ""
	}
	return m[1]
}

// splitPlaylistLines는 CRLF를 LF로 정규화하고 LF로 분리하면서 모든 라인을
// 보존한다 (본문이 newline으로 끝날 때 발생하는 끝의 빈 라인도 포함).
// parseMediaPlaylist가 사용해 rawLines 인덱스가 원본 바이트 레이아웃과
// 일치하도록 만들고, materializeHLS의 rewrite와 일관성을 유지한다.
func splitPlaylistLines(body []byte) []string {
	if len(body) == 0 {
		return nil
	}
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	return strings.Split(normalized, "\n")
}

// sameURL은 두 URL을 scheme/host/path로만 비교한다 — query/fragment는
// 무시한다. 그래야 토큰만 다른 variant 링크도 루프 감지에서 같은 엔드포인트로
// 인식된다.
func sameURL(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Host, b.Host) &&
		a.Path == b.Path
}

func extractBandwidth(line string) int64 {
	m := bandwidthRE.FindStringSubmatch(line)
	if m == nil {
		return 0
	}
	bw, _ := strconv.ParseInt(m[1], 10, 64)
	return bw
}

// runFfmpeg는 runHLSRemux가 ffmpeg를 spawn할 때 호출하는 교체 가능한
// 엔트리 포인트다. 테스트는 실제 바이너리를 띄우지 않고도 argv 불변식
// (spec §4의 AC-10 / AC-11)을 검증할 수 있도록 capture-only stub으로
// 교체한다. 프로덕션은 defaultRunFfmpeg를 쓴다. 교체 계약: 구현은 ctx를
// 존중해야 하며(취소 시 자식을 죽임) 프로세스 stderr를 받은 io.Writer에
// 기록해 로그로 노출되게 해야 한다.
//
// 동시성 주의: runFfmpeg는 패키지 수준 var다. 이를 교체하는 테스트는
// t.Parallel()을 써서는 안 된다 — 런타임 강제 가드 대신 코드 리뷰가 이
// 규칙을 강제한다.
var runFfmpeg = defaultRunFfmpeg

func SetRunFfmpegForTest(fn func(context.Context, []string, io.Writer) error) func() {
	orig := runFfmpeg
	runFfmpeg = fn
	return func() { runFfmpeg = orig }
}

// defaultRunFfmpeg은 바이너리가 없을 때 errFFmpegMissing을 표면화해
// runHLSRemux가 같은 지점에서 short-circuit 하도록 한다 — 동시에 테스트가
// LookPath 검사 자체를 우회할 수 있게 해준다(argv 불변식 테스트는 ffmpeg가
// 필요 없다).
func defaultRunFfmpeg(ctx context.Context, args []string, stderr io.Writer) error {
	return ffmpeg.RunWithStderr(ctx, stderr, args...)
}

// runHLSRemux는 materializeHLS가 같은 디렉터리에 모든 segment·key 파일을
// 이미 풀어놓은 로컬 HLS 플레이리스트를 ffmpeg로 spawn해 outputPath에
// 단일 MP4로 remux 한다. 출력은 maxOutputBytes로 상한이 있으며, watcher가
// hlsWatchInterval마다 출력 파일 크기를 폴링해 상한 초과 시 ffmpeg ctx를
// 취소한다. 컨텍스트 취소는 runFfmpeg가 존중하는 자식 ctx를 통해 ffmpeg를
// 종료시킨다. cb.Progress가 non-nil이면 watcher가 progressReader와 같은
// throttling 규칙(byte OR time threshold)으로 현재 출력 파일 크기를 보고한다.
//
// 보안: ffmpeg는 -protocol_whitelist file,crypto와 -allowed_extensions ALL
// 로 실행된다 — 로컬 파일 읽기만 가능하고 네트워크 접근은 불가능하다.
// 이것이 HLS DNS rebinding 창을 닫는 핵심 불변식이다: 입력이 완전히 로컬
// 플레이리스트이고 참조하는 segment/key도 로컬 파일이라 ffmpeg가 자체
// hostname 해석을 수행할 수 없다. argv 불변식 테스트(AC-10 / AC-11)가 이
// 계약을 고정한다.
//
// 반환값: 종료 0이면 nil; 상한 초과면 errHLSTooLarge; 외부 취소나 deadline
// 이면 ctx.Err(); non-zero 종료면 stderr가 캡처된 *ffmpegExitError; ffmpeg
// 바이너리가 PATH에 없으면 errFFmpegMissing. classifyHLSRemuxError가 이를
// 공개 FetchError.Code 값으로 번역한다.
//
// 실제 관찰성에 대한 메모: ffmpeg의 MP4 muxer는 헤더를 마무리할 수 있을
// 때까지 패킷을 버퍼링하므로, 작은 remux(mdat가 수백 KiB 미만)에서는 출력
// 파일이 입력 끝 근처에서야 나타나고 watcher가 중간 크기를 샘플링하지 못할
// 수 있다. 실제 HLS VOD(수 분짜리 영상)는 버퍼가 주기적으로 flush 되므로
// watcher가 문서화된 대로 동작한다.
func runHLSRemux(ctx context.Context, localPlaylistPath, outputPath string, cb *Callbacks, maxOutputBytes int64) error {
	// -protocol_whitelist file,crypto: ffmpeg는 로컬 파일(materializeHLS가
	// 풀어둔 segment / key / init)만 열 수 있고, #EXT-X-KEY를 위해 AES
	// 복호화 계층만 사용한다. 모든 네트워크 프로토콜이 제거되어 — 이
	// 호출 안에서 ffmpeg가 DNS 조회나 네트워크 fetch를 수행할 방법이 없다.
	// -allowed_extensions ALL: segment·init 파일은 materializeHLS의 whitelist
	// 규칙에 따라 원래 확장자(.m4s, .vtt, .aac 등)를 유지한다. ffmpeg의
	// 기본 확장자 allowlist는 일부 컨테이너에 너무 좁아 우리가 넓힌다 —
	// 모든 입력 경로가 방금 우리가 직접 쓴 로컬 파일이므로 안전하다.
	args := []string{
		"-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file,crypto",
		"-allowed_extensions", "ALL",
		"-i", localPlaylistPath,
		"-c", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-f", "mp4",
		"-movflags", "+faststart",
		"-y", outputPath,
	}

	// ffmpegCtx는 ctx의 자식이라 외부 취소/타임아웃이 프로세스에도 전파된다.
	// watcher는 size-cap을 넘으면 cancelFfmpeg()로 취소하는데, 그 경로 역시
	// ctx를 거치므로 runFfmpeg는 항상 받은 컨텍스트를 통해서만 ffmpeg를
	// 종료한다(out-of-band Kill 없음).
	ffmpegCtx, cancelFfmpeg := context.WithCancel(ctx)
	defer cancelFfmpeg()

	var stderr bytes.Buffer

	// watchCtx는 부모 ctx에서 분리한다: runFfmpeg가 반환된 뒤에 우리가
	// 명시적으로 취소할 때까지 watcher가 계속 폴링하길 원한다. 클라이언트가
	// 시작한 ctx 취소가 마지막 size 샘플의 도착을 막아서는 안 된다.
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	var sizeExceeded atomic.Bool
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watchOutputFile(watchCtx, outputPath, hlsWatchInterval, maxOutputBytes, cb, func() {
			sizeExceeded.Store(true)
			cancelFfmpeg()
		})
	}()

	waitErr := runFfmpeg(ffmpegCtx, args, &stderr)
	cancelWatch()
	<-watchDone

	// errFFmpegMissing은 설정 오류이며 watcher / ctx 검사보다 먼저 표면화해야
	// 한다(그 검사들은 프로세스가 떠 있을 때만 의미가 있다).
	if errors.Is(waitErr, errFFmpegMissing) {
		return errFFmpegMissing
	}
	if sizeExceeded.Load() {
		return errHLSTooLarge
	}
	if ctx.Err() != nil {
		// 외부 취소나 deadline이 size watcher보다 먼저 발동했다.
		return ctx.Err()
	}
	if waitErr != nil {
		exitCode := -1
		stderrText := strings.TrimSpace(stderr.String())
		var ffErr *ffmpeg.ExitError
		if errors.As(waitErr, &ffErr) {
			exitCode = ffErr.ExitCode
			stderrText = ffErr.Stderr
		}
		return &ffmpegExitError{exitCode: exitCode, stderr: stderrText}
	}
	return nil
}

// fetchHLS는 origin URL을 ffmpeg에 절대 노출하지 않으면서 HLS 응답을
// remux된 MP4로 바꾼다. 흐름은 다음과 같다 (spec §3.1):
//
//  1. 이미 발급된 응답에서 마스터 플레이리스트 본문을 읽는다(1 MiB 상한).
//  2. parseMasterPlaylist → variantURL.
//  3. variantURL이 master URL과 다르면 보호된 클라이언트로 variant 플레이리스트
//     본문을 가져온다 (요청마다 IP-pin + DNS 검증).
//  4. variant 본문에 parseMediaPlaylist → segment / key / init 엔트리.
//  5. destDir/.urlimport-hls-<random>/ 를 격리된 작업 디렉터리로 만든다.
//  6. materializeHLS가 같은 보호된 클라이언트로 모든 segment / key / init을
//     다운로드하고 로컬 URI만 갖는 재작성된 플레이리스트를 쓴다.
//  7. runHLSRemux가 그 로컬 플레이리스트로 ffmpeg를 호출한다
//     (-protocol_whitelist file,crypto). ffmpeg는 DNS를 절대 수행하지 않으므로
//     DNS rebinding 창이 닫힌다.
//  8. 출력 MP4를 destDir로 원자적 rename하고, defer로 작업 디렉터리를 정리한다.
//
// 모든 에러는 classifyHTTPError / classifyHLSRemuxError / classifyMaterializeError
// 를 통해 공개 FetchError.Code 값으로 매핑된다. 누적 바이트 상한(maxBytes)은
// 단일 atomic.Int64 카운터를 통해 segment 다운로드와 ffmpeg 출력이 공유한다.
func Fetch(
	ctx context.Context,
	client *http.Client,
	resp *http.Response,
	parsed *url.URL,
	rawURL, destDir, relDir string,
	warnings []string,
	maxBytes int64,
	cb *Callbacks,
	deps Deps,
) (*Result, *FetchError) {
	if deps.ClassifyHTTPError == nil || deps.RenameUnique == nil || deps.SanitizeFilename == nil {
		return nil, &FetchError{Code: "ffmpeg_error", Err: errors.New("missing HLS dependencies")}
	}
	masterBody, err := io.ReadAll(io.LimitReader(resp.Body, hlsMaxPlaylistBytes+1))
	if err != nil {
		return nil, &FetchError{Code: "network_error", Err: err}
	}
	if int64(len(masterBody)) > hlsMaxPlaylistBytes {
		return nil, &FetchError{Code: "hls_playlist_too_large"}
	}
	// variant 플레이리스트와 segment fetch 동안 TCP 연결을 잡고 있지 않도록
	// 적극적으로 닫는다.
	_ = resp.Body.Close()

	variantURL, err := parseMasterPlaylist(masterBody, parsed)
	if err != nil {
		if errors.Is(err, errHLSVariantScheme) {
			return nil, &FetchError{Code: "invalid_scheme", Err: err}
		}
		return nil, &FetchError{Code: "ffmpeg_error", Err: err}
	}

	// variant 본문 출처: parseMasterPlaylist가 원본을 그대로 반환했다면
	// (#EXT-X-STREAM-INF 없음) 마스터 자체, 아니면 보호된 클라이언트로 새로
	// fetch 한다. 이 fetch 경로가 DNS rebinding을 막아준다 — 클라이언트의
	// publicOnlyDialContext가 요청마다 다시 해석하고 IP를 고정한다.
	var variantBody []byte
	var variantBase *url.URL
	if sameURL(variantURL, parsed) {
		variantBody = masterBody
		variantBase = parsed
	} else {
		body, ferr := fetchPlaylistBody(ctx, client, variantURL.String(), deps)
		if ferr != nil {
			return nil, ferr
		}
		variantBody = body
		variantBase = variantURL
	}

	pl, err := parseMediaPlaylist(variantBody, variantBase)
	if err != nil {
		return nil, classifyMediaPlaylistError(err)
	}

	// 작업 디렉터리는 destDir 안에 둔다. 최종 MP4의 원자적 rename이 동일
	// 파일시스템 안에서 일어나도록(EXDEV 없음) 하기 위함이고, browse의
	// dot-prefix 필터가 자동으로 숨겨주기 때문이다. RemoveAll은 무조건
	// 실행되어 — 성공·실패·panic 모두 cleanup으로 수렴한다.
	hlsTempDir, err := os.MkdirTemp(destDir, ".urlimport-hls-*")
	if err != nil {
		return nil, &FetchError{Code: "write_error", Err: err}
	}
	defer os.RemoveAll(hlsTempDir)

	// segment 다운로드와 ffmpeg 출력이 공유하는 단일 누적 카운터 — spec D-9.
	// atomic.Int64로 향후 segment fetch가 병렬화돼도 안전하다.
	remaining := atomic.Int64{}
	remaining.Store(maxBytes)

	// progress 콜백을 감싸 materialize 단계(Phase 1: segment 바이트)와 remux
	// 단계(Phase 2: 출력 MP4 바이트)가 단일 단조 증가 카운터를 발행하도록
	// 한다 — spec D-4. Phase 2 발행은 Phase 1의 합계만큼 offset 된다.
	//
	var phase1Total atomic.Int64
	wrappedCb := cb
	if cb != nil {
		original := cb
		wrappedCb = &Callbacks{
			Start: original.Start,
		}
		if original.Progress != nil {
			wrappedCb.Progress = func(n int64) {
				original.Progress(phase1Total.Load() + n)
			}
		}
	}

	localPlaylistPath, totalDownloaded, mErr := materializeHLS(ctx, client, pl, hlsTempDir, &remaining, wrappedCb)
	if mErr != nil {
		return nil, classifyMaterializeError(mErr, deps)
	}
	phase1Total.Store(totalDownloaded)

	name := deriveHLSFilename(parsed, deps)
	// 확장자는 항상 .mp4로 강제한다(.m3u8에서 remux해 빠져나오기 때문).
	warnings = append(warnings, "extension_replaced")

	if cb != nil && cb.Start != nil {
		cb.Start(name, 0, "video")
	}

	outputPath := filepath.Join(hlsTempDir, "output.mp4")
	if err := runHLSRemux(ctx, localPlaylistPath, outputPath, wrappedCb, remaining.Load()); err != nil {
		return nil, classifyHLSRemuxError(err)
	}

	stat, err := os.Stat(outputPath)
	if err != nil {
		return nil, &FetchError{Code: "write_error", Err: err}
	}

	finalName, didRename, err := deps.RenameUnique(outputPath, destDir, name)
	if err != nil {
		return nil, &FetchError{Code: "write_error", Err: err}
	}
	if didRename {
		warnings = append(warnings, "renamed")
	}

	return &Result{
		URL:      rawURL,
		Path:     path.Join(relDir, finalName),
		Name:     finalName,
		Size:     stat.Size(),
		Type:     "video",
		Warnings: warnings,
	}, nil
}

// fetchPlaylistBody는 보호된 클라이언트로 플레이리스트 URL을 GET 해
// hlsMaxPlaylistBytes 상한으로 자른 본문을 반환한다. 에러는 안정적인
// FetchError 코드로 매핑되므로, 호출자가 추가 wrap 없이 SSE error
// 프레임에 그대로 노출할 수 있다.
func fetchPlaylistBody(ctx context.Context, client *http.Client, urlStr string, deps Deps) ([]byte, *FetchError) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, &FetchError{Code: "invalid_url", Err: err}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, deps.ClassifyHTTPError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &FetchError{Code: "http_error", Err: fmt.Errorf("http %d", resp.StatusCode)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, hlsMaxPlaylistBytes+1))
	if err != nil {
		return nil, &FetchError{Code: "network_error", Err: err}
	}
	if int64(len(body)) > hlsMaxPlaylistBytes {
		return nil, &FetchError{Code: "hls_playlist_too_large"}
	}
	return body, nil
}

// classifyMediaPlaylistError는 parseMediaPlaylist의 sentinel을 공개
// FetchError 코드로 매핑한다. "too many" 상한 세 종류는 단일 wire 코드
// (hls_too_many_segments)를 공유한다 — 운영자는 서버 로그에서 sentinel 이름으로
// segment / key / init flooding을 구분할 수 있다. 인식되지 않은 parser
// 이슈는 기본 ffmpeg_error로 흘려보낸다(방어적 — wire 계약을 좁게 유지).
func classifyMediaPlaylistError(err error) *FetchError {
	switch {
	case errors.Is(err, errHLSVariantScheme):
		return &FetchError{Code: "invalid_scheme", Err: err}
	case errors.Is(err, errHLSTooManySegments),
		errors.Is(err, errHLSTooManyKeys),
		errors.Is(err, errHLSTooManyInits):
		return &FetchError{Code: "hls_too_many_segments", Err: err}
	default:
		return &FetchError{Code: "ffmpeg_error", Err: err}
	}
}

// classifyMaterializeError는 materializeHLS / downloadOne 에러를 공개
// FetchError 코드로 매핑한다. errHLSTooLarge는 "too_large"로,
// ctx 에러는 download_timeout / network_error로 매핑된다. 그 외는 모두
// classifyHTTPError를 거쳐 dial / TLS / private_network / http_error를
// 안정적으로 유지한다.
func classifyMaterializeError(err error, deps Deps) *FetchError {
	switch {
	case errors.Is(err, errHLSTooLarge):
		return &FetchError{Code: "too_large", Err: err}
	case errors.Is(err, context.DeadlineExceeded):
		return &FetchError{Code: "download_timeout", Err: err}
	case errors.Is(err, context.Canceled):
		return &FetchError{Code: "network_error", Err: err}
	default:
		return deps.ClassifyHTTPError(err)
	}
}

// deriveHLSFilename은 URL의 마지막 path 세그먼트에서 확장자를 떼고 .mp4를
// 붙인다. basename이 비었거나 "." / ".."이면 "video.mp4"로 폴백해, remux된
// 출력이 항상 합리적인 파일명을 갖게 한다.
func DeriveFilename(parsed *url.URL, deps Deps) string {
	base := path.Base(parsed.Path)
	if decoded, err := url.PathUnescape(base); err == nil {
		base = decoded
	}
	base = deps.SanitizeFilename(base)
	stem := strings.TrimSuffix(base, path.Ext(base))
	if stem == "" || stem == "." || stem == ".." {
		return "video.mp4"
	}
	return stem + ".mp4"
}

func deriveHLSFilename(parsed *url.URL, deps Deps) string {
	return DeriveFilename(parsed, deps)
}

// classifyHLSRemuxError는 runHLSRemux의 sentinel을 공개 FetchError 코드로
// 매핑한다. ctx.Err()를 먼저 검사하므로, ffmpeg가 non-zero 종료와 함께
// 취소를 반환해도 cancel/timeout이 올바르게 표면화된다. ffmpeg_missing은
// ffmpeg_error와 구분되는 별도 코드다 — 전자는 서버 측 설정 오류
// (운영자가 ffmpeg를 설치해야 함), 후자는 사용자가 손쓸 수 없는 스트림 또는
// 입력 실패다.
func classifyHLSRemuxError(err error) *FetchError {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return &FetchError{Code: "download_timeout", Err: err}
	case errors.Is(err, context.Canceled):
		return &FetchError{Code: "network_error", Err: err}
	case errors.Is(err, errHLSTooLarge):
		return &FetchError{Code: "too_large", Err: err}
	case errors.Is(err, errFFmpegMissing):
		return &FetchError{Code: "ffmpeg_missing", Err: err}
	default:
		// *ffmpegExitError 및 그 외 ffmpeg 레이어 실패를 포함한다.
		return &FetchError{Code: "ffmpeg_error", Err: err}
	}
}

// watchOutputFile은 ctx가 취소될 때까지 tmpPath의 크기 증가를 폴링한다.
// 파일이 maxBytes를 초과하면 onOversize를 한 번 호출하고 반환한다. 그렇지
// 않으면 progressReader와 동일한 byte/time threshold로 throttle하여
// cb.Progress로 매번 관찰된 크기 변화를 전달한다.
//
// runHLSRemux에서 분리해 — ffmpeg의 버퍼링된 출력 동작 없이도 통제된
// 증가 파일을 상대로 폴링 계약을 테스트할 수 있게 했다.
func watchOutputFile(
	ctx context.Context,
	tmpPath string,
	interval time.Duration,
	maxBytes int64,
	cb *Callbacks,
	onOversize func(),
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var lastReported int64
	lastEmit := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fi, err := os.Stat(tmpPath)
			if err != nil {
				continue
			}
			size := fi.Size()
			if size > maxBytes {
				onOversize()
				return
			}
			if size == lastReported {
				continue
			}
			if cb != nil && cb.Progress != nil {
				now := time.Now()
				delta := size - lastReported
				if delta >= progressByteThreshold || now.Sub(lastEmit) >= progressTimeThreshold {
					cb.Progress(size)
					lastReported = size
					lastEmit = now
				}
			}
		}
	}
}
