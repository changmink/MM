package hls

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// hlsMaxKeyBytes는 AES-128 키 파일 크기의 상한이다. 실제 키는 16 바이트
// (128 비트)이며, 64 KiB는 키 URI를 통해 임의 바이트를 밀어넣는 공격을
// 거부하는 방어적 천장이다.
const hlsMaxKeyBytes = int64(64) << 10

// hlsMaxInitBytes는 #EXT-X-MAP init 세그먼트 크기의 상한이다. fMP4 init
// 세그먼트는 보통 수 KiB 수준이다. 16 MiB는 비정상적으로 큰 컨테이너
// 초기화를 허용하면서도 공격자의 남용을 막는다.
const hlsMaxInitBytes = int64(16) << 20

// segmentExtWhitelist는 어떤 URL path 확장자가 로컬 파일 확장자로 그대로
// 사용될지 정한다. 그 외에는 ".bin"으로 폴백해 ffmpeg의 fmt sniff가 분류를
// 담당한다.
var segmentExtWhitelist = map[string]struct{}{
	".ts":  {},
	".m4s": {},
	".mp4": {},
	".aac": {},
	".m4a": {},
	".vtt": {},
}

// hlsMaterializeParallelism은 한 HLS 배치 안에서 동시에 받을 segment·key·
// init 자원의 상한. CDN edge가 다중 hit에 친화적이라 4까지 안전하지만,
// origin이 실제로는 단일 호스트인 경우 rate-limit을 유발할 수 있어 8 이상은
// 회피한다. 단일 사용자 LAN 환경 가정이라 settings 노출은 over-engineering.
// 변경 시 TestMaterializeHLS_DownloadsEntriesInParallel의 상한 검증도 함께
// 갱신할 것.
const hlsMaterializeParallelism = 4

// downloadOne은 urlStr을 client로 가져와 body를 destPath에 쓴다(destPath는
// O_CREATE|O_EXCL로 생성되므로, 호출자는 materializeHLS의 temp 디렉터리
// 내에서 destPath가 고유함을 보장해야 한다). 전송에는 두 종류의 상한이
// 적용된다:
//
//   - perResourceMax > 0: Content-Length가 상한을 넘으면 사전 거부하고,
//     body가 상한을 초과하면 런타임에 중단한다. 0이면 이 검사를 끈다.
//   - remainingBytes: HLS materialize 세션 전체가 공유하는 누적 카운터.
//     읽힌 바이트마다 차감되며, 카운터가 음수가 되려는 순간 다운로드를 중단한다.
//
// 상한 위반 시 부분 파일을 제거하고 errHLSTooLarge를 반환한다. HTTP 에러는
// fmt.Errorf("http %d", status), dial / TLS / private-network 에러는 client.Do
// 가 반환한 그대로 전파된다(보호된 클라이언트는 *url.Error를 통해
// errPrivateNetwork를 표면화하므로 호출 측에서 errors.Is(err, errPrivateNetwork)
// 가 정상 동작한다).
func downloadOne(
	ctx context.Context,
	client *http.Client,
	urlStr string,
	destPath string,
	perResourceMax int64,
	remainingBytes *atomic.Int64,
) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("http %d", resp.StatusCode)
	}

	// 서버가 Content-Length를 제공한 경우만 preflight한다. ContentLength가
	// 0 이하(chunked / 미상)면 건너뛰어도 안전하다 — 런타임 상한이 oversize
	// body를 잡아낸다.
	if cl := resp.ContentLength; cl > 0 {
		if perResourceMax > 0 && cl > perResourceMax {
			return 0, errHLSTooLarge
		}
		if cl > remainingBytes.Load() {
			return 0, errHLSTooLarge
		}
	}

	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return 0, err
	}

	written, copyErr := copyWithCaps(f, resp.Body, perResourceMax, remainingBytes)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(destPath)
		return written, copyErr
	}
	if closeErr != nil {
		_ = os.Remove(destPath)
		return written, closeErr
	}
	return written, nil
}

// materializeHLS는 파싱된 미디어 플레이리스트를 hlsTempDir 아래의 자기
// 완결 디렉터리 트리로 풀어낸다. 모든 segment·key·init 자원을 client로
// 다운로드해(매 fetch마다 DNS 검증·IP 고정이 수행됨), 모든 URI를 상대
// 로컬 파일명으로 교체한 "playlist.m3u8" 재작성 플레이리스트를 쓴다.
// 재작성된 플레이리스트 경로와 총 다운로드 바이트를 반환한다 — 호출자는
// 총량을 SSE progress 회계에 전달하고 localPlaylistPath를 ffmpeg의 -i
// 인자로 사용한다.
//
// 상한 정책:
//   - segment는 누적 remainingBytes 카운터를 공유하며 per-resource 상한이 없다.
//   - key: hlsMaxKeyBytes (64 KiB)
//   - init segment: hlsMaxInitBytes (16 MiB)
//
// 실패 모드: 다운로드 에러가 발생하면 부분 합계와 함께 즉시 반환한다.
// 어느 에러 경로에서든 hlsTempDir 제거는 호출자의 책임이다.
func materializeHLS(
	ctx context.Context,
	client *http.Client,
	pl *mediaPlaylist,
	hlsTempDir string,
	remainingBytes *atomic.Int64,
	cb *Callbacks,
) (localPlaylistPath string, totalDownloaded int64, err error) {
	if pl == nil {
		return "", 0, fmt.Errorf("nil playlist")
	}

	type materializeJob struct {
		entry          playlistEntry
		destPath       string
		perResourceMax int64
	}

	segIdx, keyIdx, initIdx := 0, 0, 0
	// nameByLineIdx는 재작성 가능한 각 라인에 대응하는 로컬 상대 파일명을
	// 기록해, 두 번째 패스가 로컬 전용 플레이리스트를 충실히 출력할 수 있게 한다.
	nameByLineIdx := make(map[int]string, len(pl.entries))
	jobs := make([]materializeJob, 0, len(pl.entries))

	for _, e := range pl.entries {
		var (
			destName       string
			perResourceMax int64
		)
		switch e.kind {
		case entrySegment:
			destName = fmt.Sprintf("seg_%04d%s", segIdx, segmentExt(e.uri))
			segIdx++
			perResourceMax = 0 // 누적 상한만 적용된다.
		case entryKey:
			destName = fmt.Sprintf("key_%d.bin", keyIdx)
			keyIdx++
			perResourceMax = hlsMaxKeyBytes
		case entryInit:
			destName = fmt.Sprintf("init_%d%s", initIdx, segmentExt(e.uri))
			initIdx++
			perResourceMax = hlsMaxInitBytes
		default:
			return "", totalDownloaded, fmt.Errorf("unknown entry kind: %d", e.kind)
		}

		destPath := filepath.Join(hlsTempDir, destName)
		nameByLineIdx[e.lineIdx] = destName
		jobs = append(jobs, materializeJob{
			entry:          e,
			destPath:       destPath,
			perResourceMax: perResourceMax,
		})
	}

	downloadCtx, cancelDownloads := context.WithCancel(ctx)
	defer cancelDownloads()

	jobsCh := make(chan materializeJob)
	var (
		wg                sync.WaitGroup
		errOnce           sync.Once
		firstErr          error
		total             atomic.Int64
		progressMu        sync.Mutex
		lastReportedBytes int64
		lastEmit          = time.Now()
	)

	reportProgress := func(currentTotal int64) {
		if cb == nil || cb.Progress == nil {
			return
		}
		progressMu.Lock()
		defer progressMu.Unlock()
		now := time.Now()
		delta := currentTotal - lastReportedBytes
		if delta >= progressByteThreshold || now.Sub(lastEmit) >= progressTimeThreshold {
			cb.Progress(currentTotal)
			lastReportedBytes = currentTotal
			lastEmit = now
		}
	}

	workerCount := hlsMaterializeParallelism
	if len(jobs) < workerCount {
		workerCount = len(jobs)
	}
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobsCh {
				n, err := downloadOne(downloadCtx, client, job.entry.uri.String(), job.destPath, job.perResourceMax, remainingBytes)
				if err != nil {
					errOnce.Do(func() {
						firstErr = err
						cancelDownloads()
					})
					continue
				}
				currentTotal := total.Add(n)
				reportProgress(currentTotal)
			}
		}()
	}

	// dispatch 라벨로 select 빠져나오기 — runImportURLWorkers와 같은 패턴.
	// 워커가 downloadCtx로 다시 ctx를 확인하므로 race로 한 job을 더 보낸 뒤
	// cancel을 알아채도 안전하다 (워커가 ctx-aware downloadOne에서 즉시
	// 종료).
dispatch:
	for _, job := range jobs {
		select {
		case <-downloadCtx.Done():
			break dispatch
		case jobsCh <- job:
		}
	}
	close(jobsCh)
	wg.Wait()

	totalDownloaded = total.Load()
	if firstErr != nil {
		return "", totalDownloaded, firstErr
	}
	if err := ctx.Err(); err != nil {
		return "", totalDownloaded, err
	}

	if cb != nil && cb.Progress != nil {
		progressMu.Lock()
		if totalDownloaded > lastReportedBytes {
			now := time.Now()
			delta := totalDownloaded - lastReportedBytes
			if delta >= progressByteThreshold || now.Sub(lastEmit) >= progressTimeThreshold {
				cb.Progress(totalDownloaded)
				lastReportedBytes = totalDownloaded
				lastEmit = now
			}
		}
		progressMu.Unlock()
	}

	// 두 번째 패스: rawLines를 재작성한다.
	//   - 원격 리소스를 materialize한 라인은 URI를 방금 쓴 로컬 파일명으로
	//     교체한다.
	//   - 인식하지 못한 태그 라인(#EXT-X-MEDIA, #EXT-X-SESSION-DATA,
	//     #EXT-X-PRELOAD-HINT, LL-HLS 확장, 미래 RFC 태그 등)은 URI="..."
	//     속성을 URI=""로 정규화한 채 통과시킨다. ffmpeg의
	//     -protocol_whitelist file,crypto가 이미 원격 fetch를 차단하지만,
	//     여기서 한 번 더 정규화해 — 향후 whitelist가 완화돼도 parser가 놓친
	//     인식되지 않은 태그를 통한 SSRF 재개를 막는다.
	//   - 그 외 모든 라인(#EXTM3U, #EXTINF, #EXT-X-VERSION,
	//     #EXT-X-BYTERANGE, 빈 줄, 위에서 이미 재작성된 segment URI)은
	//     그대로 통과한다.
	out := make([]string, len(pl.rawLines))
	for i, line := range pl.rawLines {
		if newName, ok := nameByLineIdx[i]; ok {
			out[i] = rewritePlaylistLine(line, newName)
			continue
		}
		out[i] = stripUnrecognizedURIAttr(line)
	}

	localPlaylistPath = filepath.Join(hlsTempDir, "playlist.m3u8")
	if err := os.WriteFile(localPlaylistPath, []byte(strings.Join(out, "\n")), 0644); err != nil {
		return "", totalDownloaded, err
	}
	return localPlaylistPath, totalDownloaded, nil
}

// segmentExt는 segment / init URL의 로컬 파일 확장자를 정한다 — 안전한
// whitelist에 있으면 원래 확장자를 유지하고, 없으면 ".bin"으로 폴백해
// (-allowed_extensions ALL 아래) ffmpeg의 fmt sniff가 처리하게 한다.
func segmentExt(u *url.URL) string {
	ext := strings.ToLower(path.Ext(u.Path))
	if _, ok := segmentExtWhitelist[ext]; ok {
		return ext
	}
	return ".bin"
}

// stripUnrecognizedURIAttr는 materializeHLS가 로컬 파일을 만들지 않은 태그
// 라인의 URI="..." 속성을 비운다. 태그가 아닌 라인은 그대로 통과시킨다.
// 이는 defense-in-depth다 — parseMediaPlaylist는 #EXT-X-KEY와 #EXT-X-MAP만
// URI 출처로 인식하지만, RFC 8216 + LL-HLS + 미래 확장에는 더 많은 태그가
// 정의돼 있다(#EXT-X-SESSION-DATA, #EXT-X-PRELOAD-HINT, #EXT-X-PART 등).
// ffmpeg의 protocol whitelist가 이미 원격 fetch를 막지만, URL 문자열 자체를
// 무력화하면 가설적인 whitelist 완화 상황에서도 인식되지 않은 태그가
// SSRF로 변하는 것을 막는다.
func stripUnrecognizedURIAttr(line string) string {
	trim := strings.TrimSpace(line)
	if !strings.HasPrefix(trim, "#") {
		return line
	}
	if !strings.Contains(line, `URI="`) {
		return line
	}
	return uriAttrRE.ReplaceAllLiteralString(line, `URI=""`)
}

// rewritePlaylistLine은 EXT-X-KEY / EXT-X-MAP 속성 라인의 URI를 교체하거나,
// segment의 URI 라인 전체를 로컬 상대 이름으로 교체한다. segment URI 라인의
// 선행 공백은 보존되어 ffmpeg의 플레이리스트 파서가 동일한 구조를 본다.
// segment URI 라인은 RFC 8216 §4.3에 따라 독립적이므로, 후행 공백·주석은
// 보존하지 않는다(어차피 유효한 HLS가 아니다).
func rewritePlaylistLine(line, newName string) string {
	trim := strings.TrimSpace(line)
	if strings.HasPrefix(trim, "#EXT-X-KEY") || strings.HasPrefix(trim, "#EXT-X-MAP") {
		// ReplaceAllLiteralString을 써서 replacement 안의 regex backreference
		// 해석을 피한다(newName은 $ 등이 없는 로컬 파일명이지만, 문자
		// 그대로 처리하는 게 계약을 명확히 보여준다).
		return uriAttrRE.ReplaceAllLiteralString(line, fmt.Sprintf(`URI=%q`, newName))
	}
	// segment URI 라인: 선행 공백을 보존하고 URI 본체만 교체한다.
	leading := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	return leading + newName
}

// copyWithCaps는 per-resource 상한과 공유 누적 카운터를 강제하면서 src를
// dst로 스트리밍한다. 상한을 위반하면 들어갈 만큼만 쓰고 errHLSTooLarge를
// 반환한다 — 부분 파일은 호출자가 제거한다. 누적 차감은 CAS 루프를 사용해,
// 동시 HLS segment 다운로드가 공유 예산을 초과 인출하지 못하게 한다.
func copyWithCaps(dst io.Writer, src io.Reader, perResourceMax int64, remaining *atomic.Int64) (int64, error) {
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, rerr := src.Read(buf)
		if n > 0 {
			if perResourceMax > 0 && written+int64(n) > perResourceMax {
				return written, errHLSTooLarge
			}
			need := int64(n)
			for {
				cur := remaining.Load()
				if cur < need {
					return written, errHLSTooLarge
				}
				if remaining.CompareAndSwap(cur, cur-need) {
					break
				}
			}

			if _, werr := dst.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			return written, rerr
		}
	}
}
