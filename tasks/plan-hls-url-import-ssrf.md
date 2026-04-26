# Plan: HLS URL Import — DNS rebinding 방어 강화

> 부모 spec: [`tasks/spec-hls-url-import-ssrf.md`](spec-hls-url-import-ssrf.md) (커밋 `9957f0c`)
> 핸드오프: [`tasks/handoff-hls-url-import-ssrf.md`](handoff-hls-url-import-ssrf.md)
> 이슈: [#5](https://github.com/changmink/MM/issues/5)
> 브랜치: `feature/harden-hls-url-import-ssrf`
> 워크트리: `D:/file-server-hls-ssrf/`

본 문서는 spec §8의 7단계 phase를 실행 가능한 task 단위로 나눠 acceptance criteria · 검증 명령 · 의존성을 명시한다. 각 task는 가능한 한 vertical slice(단일 단계로 컴파일·테스트 가능)로 구성했고, 순수 인프라 task(A1·A2)는 layer임을 명시한다.

---

## 1. 개요 (한 단락)

PR #4가 이미 `Resolver` 인터페이스 + `WithResolver` + `AllowPrivateNetworks` 옵션과 IP-pin dial 인프라를 깔아둔 덕분에, 본 작업의 실 코드 변경은 **`urlfetch/hls.go`에 집중**된다. 핵심은 (1) media playlist 파서, (2) segment/key/init 다운로더, (3) playlist URI 재작성, (4) ffmpeg 호출 시 `-protocol_whitelist file,crypto` + local 파일 입력으로 전환. argv invariant 테스트는 새 `runFfmpeg` 함수 변수 swap 패턴으로 검증. SPEC.md 업데이트는 마지막 단계.

총 13개 task, 7개 phase, 5개 체크포인트.

**진행 정책 (2026-04-26 사용자 결정):**
- 체크포인트는 **자동 진행** — 사용자 검토를 위해 멈추지 않는다. 단, 각 phase 끝에 검증 명령을 돌리고 결과를 사용자 메시지로 보고한 뒤 다음 phase 시작.
- segment/init 파일 확장자: **원본 보존** (whitelist 안에서). C2 task 참조.
- `runFfmpeg` swap이 병렬 테스트와 충돌(R5): 코드 review 룰로만 막음. 추가 가드 없이 진행.
- `fetchHLS` 호출 시그니처 전파(`*http.Client` 추가): `Fetch`도 함께 수정. 외부 API는 변경 없음.

---

## 2. 의존 그래프

```
Phase A (test infra)
   │  A1: sequenceResolver
   │  A2: capture-only ffmpeg stub
   │
   ▼ (체크포인트 ①)
Phase B (parser)
   │  B1: parseMediaPlaylist + 단위 테스트
   │
   ▼
Phase C (downloader + materializer)
   │  C1: downloadOne (단일 리소스)
   │  C2: materializeHLS (orchestration)
   │  C3: 임시 디렉터리 생성·정리 패턴
   │
   ▼ (체크포인트 ②)
Phase D (ffmpeg 호출 전환)
   │  D1: runFfmpeg 함수 변수 추출
   │  D2: runHLSRemux 시그니처 + argv 변경
   │
   ▼ (체크포인트 ③)
Phase E (fetchHLS 통합)
   │  E1: fetchHLS 흐름 §3.1로 재배선
   │  E2: DNS rebinding 회귀 테스트 4종
   │  E3: argv invariant 테스트 (AC-10/AC-11)
   │
   ▼ (체크포인트 ④)
Phase F (SPEC.md 업데이트)
   │  F1: §2.6 단서절 제거 + §2.6.1 본문 재작성
   │  F2: §5.1 에러 코드 + §9 Always·Known limitations 갱신
   │
   ▼
Phase G (머지 준비)
   │  G1: 전체 회귀 + 커밋 정리 + PR 본문
   │
   ▼ (체크포인트 ⑤ — 머지)
```

**비차단 병렬 가능 구간 없음** — 모든 phase가 순차. spec이 단일 파일을 깊게 고치는 작업이라 두 task가 같은 함수를 동시에 만지면 충돌 위험이 크다.

---

## 3. Phase A — 테스트 인프라 (layer task, no production change)

이 단계는 vertical slice가 아니라 **순수 인프라**다. 이후 모든 phase가 의존하므로 먼저 둔다. 코드 변경 없는 phase라 bisect로 잡을 회귀가 없다는 점이 trade-off.

### Task A1 — `sequenceResolver` 테스트 헬퍼 추가

- [ ] **목표**: 호출 횟수별로 다른 IP를 반환하는 fake resolver. DNS rebinding 회귀 테스트 (AC-8, AC-9)에 필수.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/helpers_test.go` (기존 파일에 추가, 또는 새 `resolver_test_helpers_test.go` 생성)
- **시그니처**:
  ```go
  // sequenceResolver returns a Resolver that yields a different set of IPs on each
  // LookupIPAddr call, cycling through provided answers in order. Used by DNS
  // rebinding tests where the resolver must answer "public" first and "private"
  // on a subsequent lookup. Calls past len(answers) return the last entry.
  type sequenceResolver struct {
      mu      sync.Mutex
      answers []map[string][]net.IPAddr  // index = call ordinal, key = host
      calls   int
  }
  func newSequenceResolver(answers ...map[string][]net.IPAddr) *sequenceResolver
  func (r *sequenceResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
  ```
- **AC**:
  - 같은 host에 대해 호출 1회차는 answers[0][host], 2회차는 answers[1][host], ... 반환
  - len(answers) 초과 시 마지막 entry 반환 (변하지 않는 정상 도메인 시뮬레이션)
  - 등록되지 않은 host는 `&net.DNSError{IsNotFound: true}`
  - 호출 횟수 검증을 위한 `r.Calls()` getter
- **검증**:
  ```bash
  GOCACHE=/tmp/go-build-file-server-hls-ssrf go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestSequenceResolver -v
  ```
  (헬퍼 자체에 대한 표 테스트 1개 함께 작성 — order-of-call 검증)
- **복잡도**: S
- **의존**: 없음

### Task A2 — `runFfmpeg` capture-only 스텁 패턴 정의

- [ ] **목표**: argv invariant 테스트(AC-10, AC-11) 작성 시 사용할 swap 패턴 확정. 이 task는 **테스트 helper 작성만**, 실제 production `runFfmpeg` 도입은 D1에서.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/hls_remux_test.go` (기존 파일에 helper 추가)
- **헬퍼 시그니처**:
  ```go
  // captureFfmpeg replaces runFfmpeg with a stub that records argv and creates
  // a stub MP4 at the output path so callers' subsequent os.Stat / rename
  // succeed. Returns a function that restores the original runFfmpeg and a
  // pointer to the captured argv (multi-call slice).
  //
  // Use only after D1 lands runFfmpeg as a package var. Tests that need this
  // helper before D1 must skip with t.Skip("requires runFfmpeg var (D1)").
  func captureFfmpeg(t *testing.T) (calls *[][]string)
  ```
- **AC**:
  - swap 시 `t.Cleanup`으로 원복 자동
  - 스텁이 `args[len(args)-1]`을 출력 파일 경로로 간주, 4-byte ftypbox 헤더 작성 (`os.Stat` 통과용)
  - 여러 번 호출 시 각 호출의 argv를 별도 슬라이스로 누적
- **검증**: 이 task 자체는 D1 이후에야 실행 가능 — 헬퍼 정의만 하고 호출하는 테스트는 D2/E3에 작성. **`go vet ./internal/urlfetch` 통과**가 즉시 검증의 전부.
- **복잡도**: S
- **의존**: 없음 (헬퍼만, 실 사용은 D1 이후)

> ### 체크포인트 ① — Phase A 종료
> 사용자 검토 포인트:
> - `sequenceResolver`의 cycling 정책이 의도와 맞는지 (AC-8/AC-9 시나리오를 표현 가능?)
> - `captureFfmpeg` swap 패턴이 D1 설계와 일치하는지
>
> 검증 한 줄: `go vet ./internal/urlfetch && go test ./internal/urlfetch -run TestSequenceResolver`
>
> 커밋: `test(urlfetch): HLS DNS rebinding 회귀 테스트 인프라 추가`

---

## 4. Phase B — Media Playlist 파서

### Task B1 — `parseMediaPlaylist` + 단위 테스트

- [ ] **목표**: media playlist 본문에서 segment / `#EXT-X-KEY` / `#EXT-X-MAP` URI를 모두 추출하고 base 기준 resolve. 출력은 후속 단계에서 URI를 재작성할 수 있는 indexed structure.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/hls.go` (`parseMasterPlaylist` 인근에 신규 함수)
- **시그니처**:
  ```go
  type playlistEntry struct {
      lineIdx  int      // raw lines 안에서의 위치 (0-based)
      uri      *url.URL // resolved against base
      kind     entryKind // segment | key | init
      // key 전용: METHOD, IV, KEYFORMAT 등 attribute 원본 라인은 rawLines에 남는다
  }
  type mediaPlaylist struct {
      rawLines []string         // 원본 라인 (URI 재작성 대상 인덱스만 변경)
      entries  []playlistEntry  // 등장 순서, 다운로드/재작성 대상
  }
  func parseMediaPlaylist(body []byte, base *url.URL) (*mediaPlaylist, error)
  ```
- **AC**:
  - `#EXTINF:...` 다음 줄을 segment로 인식 (주석/빈 줄 skip 후 첫 비어있지 않은 라인)
  - `#EXT-X-KEY:METHOD=NONE` 은 entry로 추가하지 않음 (URI 없음)
  - `#EXT-X-KEY:METHOD=AES-128,URI="..."` 는 URI 추출, attribute parsing은 quoted/unquoted 모두 처리
  - `#EXT-X-MAP:URI="..."` 동일하게 처리
  - 상대 URL은 `base.ResolveReference`로 절대화
  - scheme이 `http`/`https` 외이면 `errHLSVariantScheme` (재사용)
  - segment 개수 > 10,000 → `errHLSTooManySegments` (새 sentinel, spec D-8)
  - 빈 body / 주석만 있는 body → 에러 없이 빈 entries
  - 표 테스트 케이스: media-only 1 segment, 다중 segment, key 1개 + segments, key rotation (key1 → segs → key2 → segs), init segment + segments, BYTERANGE 보존, relative URL, 잘못된 scheme, segment 폭주 (10,001개)
- **검증**:
  ```bash
  GOCACHE=/tmp/go-build-file-server-hls-ssrf go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestParseMediaPlaylist -v
  GOCACHE=/tmp/go-build-file-server-hls-ssrf go vet ./internal/urlfetch
  ```
- **복잡도**: M (HLS 문법 edge case, BYTERANGE 보존 처리)
- **의존**: A1 (resolver 헬퍼는 이 task에 직접 필요 없지만 후속 통합 테스트에서 필요)

> ### 체크포인트 ② — Phase B 종료
> 검토 포인트:
> - 표 테스트 커버리지가 spec §3.2 D-5 / D-6 / D-7 (key·init·byterange) 모두 다루는지
> - `entryKind` enum 노출 범위가 후속 phase에 충분한지 (private 유지 가능?)
>
> 검증: 위 명령 + 새 테스트 모두 PASS
> 커밋: `feat(urlfetch): media playlist 파서 — segment/key/init URI 추출`

---

## 5. Phase C — Downloader + Materializer

이 단계가 본 작업의 코어. 단일 task로 묶기엔 무거워 3개로 분해.

### Task C1 — `downloadOne` 단일 리소스 다운로더

- [ ] **목표**: 보호 클라이언트로 한 URL을 받아 destPath에 쓰고 받은 바이트 수를 반환. 누적 cap 카운터 공유.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/hls.go` (또는 별도 `D:/file-server-hls-ssrf/internal/urlfetch/hls_download.go`)
- **시그니처**:
  ```go
  // downloadOne fetches urlStr through client and writes the body to destPath
  // (created with O_CREATE|O_EXCL). It charges every byte read against
  // remainingBytes (atomic.Int64). When remainingBytes reaches 0 mid-read it
  // returns errHLSCapExceeded. Per-resource max additionally caps the single
  // file size — for AES-128 keys (max 64 KiB), init segments (max 16 MiB),
  // segments (no per-resource cap, only cumulative).
  func downloadOne(
      ctx context.Context,
      client *http.Client,
      urlStr string,
      destPath string,
      perResourceMax int64,    // 0 means no per-resource cap
      remainingBytes *atomic.Int64,
  ) (int64, error)
  ```
- **AC**:
  - HTTP 200 정상 → 파일 작성 + 누적 카운터 차감, 받은 바이트 수 반환
  - HTTP 4xx/5xx → `&FetchError{Code: "http_error"}` (호출자가 분류 시 사용)
  - TLS / DNS / dial 에러 → 그대로 wrap (호출자가 `classifyHTTPError` 재사용)
  - private IP (보호 클라이언트가 자동 차단) → `errPrivateNetwork` 통해 surface
  - perResourceMax > 0 이고 응답 Content-Length가 perResourceMax 초과 → `errHLSCapExceeded` (사전 차단)
  - 본문 읽기 중 perResourceMax 또는 remainingBytes 초과 → `errHLSCapExceeded` (런타임 차단), 부분 파일 삭제
  - context cancel/deadline → ctx.Err() wrap
  - O_CREATE|O_EXCL — 같은 destPath가 이미 있으면 에러 (호출자가 임시 디렉터리 안에서만 쓰니 충돌 시 버그)
- **검증**:
  ```bash
  go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestDownloadOne -v
  ```
  - 표 테스트: 정상, http 404, content-length 초과, runtime byte 초과, private IP (sequenceResolver), context cancel
- **복잡도**: M
- **의존**: A1 (sequenceResolver로 private IP 케이스)

### Task C2 — `materializeHLS` orchestration

- [ ] **목표**: parsed mediaPlaylist + http client → 임시 디렉터리에 모든 리소스 다운로드 + 재작성된 playlist 파일 작성. ffmpeg는 호출하지 않음.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/hls.go` (또는 같은 곳 `hls_download.go`)
- **시그니처**:
  ```go
  // materializeHLS turns a parsed media playlist into a self-contained directory
  // tree under hlsTempDir: every segment, key, and init resource is downloaded
  // through client (so DNS validation and IP-pin happen for each fetch), and a
  // rewritten playlist named "playlist.m3u8" is written with all URIs replaced
  // by relative local paths. Returns the rewritten playlist path and total
  // bytes downloaded (for progress accounting).
  func materializeHLS(
      ctx context.Context,
      client *http.Client,
      pl *mediaPlaylist,
      hlsTempDir string,
      remainingBytes *atomic.Int64,
      cb *Callbacks,
  ) (localPlaylistPath string, totalDownloaded int64, err error)
  ```
- **AC**:
  - 모든 entry를 등장 순서대로 sequential download (병렬화 out of scope)
  - 파일 명명 규칙: segment → `seg_0000.<ext>`, `seg_0001.<ext>`, ... ; key → `key_0.bin`, `key_1.bin`, ... ; init → `init.<ext>`
    - **segment / init 확장자는 원본 URL path의 확장자 보존**. 허용 목록: `.ts`, `.m4s`, `.mp4`, `.aac`, `.m4a`, `.vtt`. 외이거나 누락이면 `.bin`. `-allowed_extensions ALL`이 받쳐주므로 ffmpeg fmt sniff 신뢰.
    - key URI는 raw bytes라 항상 `.bin`
    - 인덱스는 등장 순서대로 0부터 증가 (zero-padded 4자리)
  - 재작성된 playlist의 segment URI는 `seg_0000.ts` 같은 상대 경로 (절대 경로 아님 — ffmpeg가 `-i <playlistPath>`로 받으면 base directory 기준으로 자동 resolve)
  - `#EXT-X-KEY` URI는 `key_0.bin` 등 상대 경로로 재작성, `IV`, `METHOD`, 기타 속성은 원본 그대로 보존
  - `#EXT-X-MAP` URI는 `init.mp4`로 재작성, BYTERANGE 보존
  - rawLines 의 다른 모든 line은 그대로 출력 (`#EXT-X-VERSION`, `#EXT-X-TARGETDURATION`, `#EXT-X-MEDIA-SEQUENCE`, `#EXT-X-ENDLIST`, `#EXTINF`, ...)
  - `cb.Progress`가 non-nil이면 spec §3.2 D-4 규칙대로 누적 다운로드 바이트 emit (progressByteThreshold/progressTimeThreshold 재사용)
  - 한 entry라도 실패하면 즉시 반환, 호출자 책임으로 임시 디렉터리 cleanup
  - 임시 디렉터리는 호출자가 사전 생성하고 넘김 (이 task는 디렉터리 만들지 않음)
- **검증**:
  ```bash
  go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestMaterializeHLS -v
  ```
  - 통합 테스트: httptest.Server로 master/segment/key serve, materializeHLS 호출, 결과 디렉터리에 모든 파일 존재 + playlist 본문이 상대 경로로 재작성됐는지 검증
- **복잡도**: L (URI 재작성 + 명명 + 진행 콜백)
- **의존**: B1, C1

### Task C3 — 임시 디렉터리 생성·정리 패턴 — `fetchHLS` 안에 wire

- [ ] **목표**: `destDir/.urlimport-hls-<random>/` 디렉터리 생성·`defer os.RemoveAll`. 이 자체는 task가 아니지만 E1에 의존 없는 기반이라 별도로 분리.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/hls.go` (`fetchHLS` 안)
- **AC**:
  - `os.MkdirTemp(destDir, ".urlimport-hls-*")` 사용 (random suffix 보장)
  - 디렉터리 생성 실패 → `&FetchError{Code: "write_error"}`
  - `defer os.RemoveAll(hlsTempDir)` — 성공/실패/패닉 무관 정리
  - 출력 MP4는 임시 디렉터리 안이 아니라 `destDir/.urlimport-*.tmp` 별도 (기존 패턴 재사용) — atomic rename target과 임시 디렉터리는 분리. **재고**: 출력 MP4도 같은 임시 디렉터리에 두면 cleanup이 simpler. **결정**: 임시 디렉터리 안에 `output.mp4` 생성, 성공 시 그 파일만 destDir로 atomic rename, 그 후 임시 디렉터리 삭제.
- **검증**: E1 통합 테스트에서 함께 검증 (별도 단위 테스트 불요)
- **복잡도**: S
- **의존**: 없음 (코드 골격만 추가, 호출은 E1)

> ### 체크포인트 ② — Phase C 종료 (이전 체크포인트와 통합)
> 검토 포인트:
> - 파일 명명 규칙(`seg_0000.ts` 강제) — `.m4s`/`.aac`를 받았을 때 ffmpeg가 fmt sniff로 정상 처리할지 실측 필요. 이슈 발생 시 원본 확장자 보존 모드로 fallback (D2에서 `-allowed_extensions ALL`로 받쳐줌)
> - `materializeHLS`의 progress emit 단위가 클라이언트 UX에 거슬리지 않는지
>
> 검증: `go test ./internal/urlfetch -run "TestParseMediaPlaylist|TestDownloadOne|TestMaterializeHLS"` 모두 PASS
> 커밋: `feat(urlfetch): HLS segment/key/init Go-side 다운로더 + playlist 재작성`

---

## 6. Phase D — ffmpeg 호출 전환

### Task D1 — `runFfmpeg` 함수 변수 추출

- [ ] **목표**: ffmpeg 실행 진입점을 패키지 var로 분리. spec §3.3.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/hls.go`
- **변경**:
  ```go
  // hls.go (새 var, 기존 const hlsWatchInterval 직후쯤)
  var runFfmpeg = defaultRunFfmpeg
  
  func defaultRunFfmpeg(ctx context.Context, args []string, stderr io.Writer) error {
      cmd := exec.CommandContext(ctx, "ffmpeg", args...)
      cmd.Stderr = stderr
      if err := cmd.Start(); err != nil {
          return err
      }
      return cmd.Wait()
  }
  ```
- **AC**:
  - 기존 `runHLSRemux` 안의 `exec.CommandContext("ffmpeg", ...)` + `cmd.Start()` + `cmd.Wait()` 호출이 `runFfmpeg(ctx, args, &stderr)` 한 줄로 대체
  - watcher goroutine, kill 로직(`cmd.Process.Kill`), exit code 추출(`*exec.ExitError`)은 유지 — 단, kill은 `runFfmpeg`가 internal로 처리하면 어렵다. **재고**: kill은 `exec.CommandContext`의 ctx 전파로 자동 처리되므로 size cap watcher에서는 별도 ctx (`watchCtx`)를 만들고 size 초과 시 ctx cancel로 ffmpeg를 종료시키는 패턴으로 재구성. 즉 `runFfmpeg`가 ctx를 받아 그 ctx가 cancel되면 자동으로 죽도록.
  - **수정 결정**: `runFfmpeg(ctx context.Context, args []string, stderr io.Writer) error` — `cmd.Process.Kill()`을 외부에서 호출할 필요 없게 ctx로 통일. watcher goroutine이 sizeExceeded 시 별도 cancel func을 호출 → ffmpeg 종료.
  - production 동작은 변경 없음 (행동 동등 refactor)
  - 모든 기존 fixture 회귀 PASS
- **검증**:
  ```bash
  go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run "TestRunHLSRemux|TestFetch_HLS" -v
  ```
- **복잡도**: M (kill 패턴 ctx 전파로 재구성 필요)
- **의존**: 없음 (D2와 함께 묶일 수 있지만 분리해서 행동 등동 단계 vs argv 변경 단계로 bisect 가능하게)

### Task D2 — `runHLSRemux` 시그니처 + argv 변경

- [ ] **목표**: 첫 인자가 원격 URL → local playlist path. ffmpeg `-protocol_whitelist`를 `file,crypto`로 좁힘.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/hls.go`
- **변경**:
  - `func runHLSRemux(ctx, variantURL, tmpPath ...)` → `func runHLSRemux(ctx, localPlaylistPath, outputPath ...)`
  - argv:
    ```go
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
    ```
  - `-rw_timeout`은 file:// 입력에 무의미하니 **제거**
- **AC**:
  - 기존 `TestRunHLSRemux_*` 테스트는 시그니처 변경에 맞춰 호출자 측에서 사전에 master.m3u8 디렉터리를 마련하고 그 디렉터리 안의 playlist 경로를 넘기도록 수정 (real ffmpeg가 file 입력으로 동일 결과 생성)
  - argv invariant 테스트(`TestFFmpegInvocation_LocalOnly`)는 `runFfmpeg` swap으로 captured argv를 검증 — `-i` 다음이 절대 경로(local), `http://`/`https://` 미포함, `-protocol_whitelist` 값이 정확히 `file,crypto`
  - watcher 동작은 그대로 (출력 MP4 파일 size 폴링)
- **검증**:
  ```bash
  go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run "TestRunHLSRemux|TestFFmpegInvocation_LocalOnly" -v
  ```
- **복잡도**: M (기존 테스트 fixture 호출자 수정)
- **의존**: A2 (capture helper), D1

> ### 체크포인트 ③ — Phase D 종료
> 검토 포인트:
> - `-rw_timeout` 제거가 file 입력에서 의도대로 무영향인지 (real ffmpeg가 정상 동작?)
> - `-allowed_extensions ALL`이 보안 측면에서 받아들일 만한지 (local file only이므로 OK라고 판단)
>
> 검증: `go test ./internal/urlfetch -run "TestRunHLSRemux|TestFFmpegInvocation"` 모두 PASS
> 커밋: `feat(urlfetch): runHLSRemux를 local playlist 입력으로 전환 (-protocol_whitelist file,crypto)`

---

## 7. Phase E — `fetchHLS` 통합

### Task E1 — `fetchHLS` 흐름 §3.1로 재배선

- [ ] **목표**: spec §3.1 다이어그램대로 master fetch → variant fetch (필요 시) → parseMediaPlaylist → materializeHLS → runHLSRemux → atomic rename → cleanup.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/hls.go` (`fetchHLS` 함수 본문)
- **변경 골격**:
  ```go
  func fetchHLS(ctx, resp, parsed, rawURL, destDir, relDir, warnings, maxBytes, cb, allowPrivateNetworks, resolver) (*Result, *FetchError) {
      // 1. master body read (1 MiB cap)
      masterBody, err := readPlaylistBody(resp, hlsMaxPlaylistBytes)
      ...
      _ = resp.Body.Close()
      
      // 2. variant URL 결정 (기존 parseMasterPlaylist 재사용)
      variantURL, err := parseMasterPlaylist(masterBody, parsed)
      ...
      
      // 3. variantURL이 master와 다르면 새로 fetch — 같은 client 사용 (IP-pin 자동)
      var mediaBody []byte
      var mediaBase *url.URL
      if sameURL(variantURL, parsed) {
          mediaBody = masterBody
          mediaBase = parsed
      } else {
          mediaBody, err = fetchPlaylistBody(ctx, client, variantURL.String(), hlsMaxPlaylistBytes)
          ...
          mediaBase = variantURL
      }
      
      // 4. parse media playlist
      pl, err := parseMediaPlaylist(mediaBody, mediaBase)
      ...
      
      // 5. 임시 디렉터리 생성 (C3)
      hlsTempDir, err := os.MkdirTemp(destDir, ".urlimport-hls-*")
      ...
      defer os.RemoveAll(hlsTempDir)
      
      // 6. materializeHLS — segment/key/init 다운로드 + playlist 재작성
      remaining := atomic.Int64{}
      remaining.Store(maxBytes)
      localPlaylistPath, downloaded, err := materializeHLS(ctx, client, pl, hlsTempDir, &remaining, cb)
      ...
      
      // 7. start callback
      if cb != nil && cb.Start != nil {
          cb.Start(deriveHLSFilename(parsed), 0, "video")
      }
      
      // 8. ffmpeg remux to hlsTempDir/output.mp4
      outputPath := filepath.Join(hlsTempDir, "output.mp4")
      remainingForOutput := remaining.Load()  // ffmpeg watcher에 별도 카운터로 전달, max는 이 값
      if err := runHLSRemux(ctx, localPlaylistPath, outputPath, cb, remainingForOutput); err != nil {
          return nil, classifyHLSRemuxError(err)
      }
      
      // 9. atomic rename 출력 → destDir
      stat, err := os.Stat(outputPath)
      ...
      finalName, didRename, err := renameUnique(outputPath, destDir, deriveHLSFilename(parsed))
      ...
      
      // 10. 임시 디렉터리 정리는 defer 가 처리
      
      return &Result{...}, nil
  }
  ```
- **AC**:
  - spec §3.1 다이어그램 모든 단계 구현
  - `client` 파라미터를 `fetchHLS`가 받을 수 있도록 호출자(`Fetch`)에서 전달 (기존엔 resp만 받았는데 이제 추가 fetch 필요)
  - 기존 `TestFetch_HLS_*` 회귀 PASS (master·media·기존 happy paths)
  - private network 자동 차단 — `client`가 `publicOnlyDialContext`이므로 segment/key/variant fetch가 모두 자동 검증됨
- **검증**:
  ```bash
  go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestFetch_HLS -v
  ```
- **복잡도**: L (호출 시그니처 전파, 기존 테스트 회귀)
- **의존**: B1, C1, C2, C3, D1, D2

### Task E2 — DNS rebinding 회귀 테스트 4종

- [ ] **목표**: AC-4, AC-5, AC-6, AC-7, AC-8, AC-9 통합 테스트.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/hls_dns_rebinding_test.go` (신규)
- **테스트 목록**:
  - `TestFetch_HLS_VariantPrivate_Rejected` — master는 public, variant URL이 사설 IP로 resolve → `private_network` (AC-4)
  - `TestFetch_HLS_SegmentPrivate_Rejected` — variant playlist 안 segment URL 호스트가 사설 IP → `private_network` (AC-5)
  - `TestFetch_HLS_KeyURIPrivate_Rejected` — `#EXT-X-KEY` URI 호스트가 사설 IP → `private_network` (AC-6)
  - `TestFetch_HLS_InitURIPrivate_Rejected` — `#EXT-X-MAP` URI 호스트가 사설 IP → `private_network` (AC-7)
  - `TestFetch_HLS_DNSRebinding_VariantHost` — `sequenceResolver`로 master fetch=public, variant fetch=private → `private_network` (AC-8)
  - `TestFetch_HLS_DNSRebinding_SegmentHost` — segment fetch 시점에만 private 응답 → `private_network` (AC-9)
- **검증**:
  ```bash
  go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestFetch_HLS_.*Private -v
  go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestFetch_HLS_DNSRebinding -v
  ```
- **복잡도**: M
- **의존**: A1 (sequenceResolver), E1

### Task E3 — argv invariant 테스트 (AC-10, AC-11)

- [ ] **목표**: `runFfmpeg`를 swap해서 argv 캡처 후 검증.
- **파일**: `D:/file-server-hls-ssrf/internal/urlfetch/hls_remux_test.go` 또는 새 `hls_argv_test.go`
- **테스트**: `TestFFmpegInvocation_LocalOnly`
  - 시나리오 1: master + 다중 segment (no key, no init)
  - 시나리오 2: master + variant + segment + key + init segment
  - 검증: 모든 호출의 argv에서 `-i` 다음 인자가 절대 경로 + `http`/`https` prefix 무, `-protocol_whitelist` 값이 정확히 `file,crypto`
- **검증**:
  ```bash
  go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestFFmpegInvocation_LocalOnly -v
  ```
- **복잡도**: S
- **의존**: A2 (captureFfmpeg), D1, E1

> ### 체크포인트 ④ — Phase E 종료
> 검토 포인트:
> - DNS rebinding 회귀 테스트 6개가 spec §4 AC-4~9를 모두 빠짐없이 cover하는지
> - argv 검증이 master 단계까지 cover하는지 (master fetch는 `Fetch`/`urlfetch.NewClient`가 처리하므로 ffmpeg argv와 무관 — but documented)
> - 기존 `TestFetch_HLS_*` 회귀가 모두 PASS인지
>
> 검증: `go test ./internal/urlfetch -count=1` (캐시 무시, 전체 통과)
> 커밋:
> - `feat(urlfetch): fetchHLS를 Go-side 사전 다운로드 + local ffmpeg 입력으로 재배선`
> - `test(urlfetch): HLS DNS rebinding 회귀 + ffmpeg argv invariant 테스트`

---

## 8. Phase F — SPEC.md 업데이트

### Task F1 — §2.6 단서절 제거 + §2.6.1 본문 재작성

- [ ] **목표**: HLS 우회 가능성 단서절(§2.6 step 5)을 제거하고 §2.6.1 다운로드 흐름을 새 14단계로 갱신.
- **파일**: `D:/file-server-hls-ssrf/SPEC.md`
- **수정 위치**:
  - line 297 부근: "단, ffmpeg는 ... DNS rebinding을 받는 호스트에 대해서는 우회될 수 있다 — §9 Boundaries 참조." 부분 삭제 (PR #4가 추가한 단서)
  - §2.6.1 line 337~346 (기존 7단계 다운로드 흐름) → spec §3.1 흐름 14단계로 재작성. ffmpeg argv 변경 + 임시 디렉터리 정책 + 누적 cap 적용 명시
  - 기존 §2.6.1 line 359~362 (보안 항목) 갱신: `-protocol_whitelist file,crypto`, "variant/segment URL의 스킴은 Go 측이 검증" 등
- **AC**:
  - PR #4가 도입한 단서절(§2.6 step 5의 ffmpeg 우회) 흔적이 없다 (AC-23)
  - 새 §2.6.1 본문이 코드와 정확히 일치 (단계별 명시)
- **검증**: 사람의 시각 검토 + grep으로 잔존 확인
  ```bash
  grep -n "ffmpeg는 자체 DNS\|우회될 수 있다" "D:/file-server-hls-ssrf/SPEC.md"
  ```
  → 결과 없어야 함
- **복잡도**: M (긴 본문 다시 쓰기)
- **의존**: E1 (구현이 끝나야 정확한 단계 기술 가능)

### Task F2 — §5.1 에러 코드 + §9 Always·Known limitations 갱신

- [ ] **목표**: `hls_too_many_segments` 추가, §9 Boundaries 갱신.
- **파일**: `D:/file-server-hls-ssrf/SPEC.md`
- **수정**:
  - §5.1 에러 코드 표(line 647~ 부근)에 `"hls_too_many_segments"` 행 추가
  - §9 Always (line 1000 HLS 행)에 다음 추가:
    - "playlist/segment/key는 모두 보호 클라이언트로 사전 다운로드"
    - "ffmpeg는 항상 local 파일만 입력으로 받음 (`-protocol_whitelist file,crypto`)"
    - "임시 디렉터리는 destDir 내부에 random suffix로 생성 + 종료 시 통째 정리"
  - §9 Known limitations (line 1020 부근의 "URL import SSRF 정책 ..." 문단) **삭제**. 대체로 한 줄: "URL import는 일반 HTTP / HLS 모두 DNS 해석 결과를 IP-pin해 dial하므로 DNS rebinding을 차단한다."는 §9 Always에 흡수.
  - 다른 Known limitations 항목(folder rename TOCTOU, HLS live timeout, progress.total 누락, HLS tmp TOCTOU)는 그대로 유지
- **AC**:
  - SPEC.md §9 Known limitations에서 HLS DNS rebinding 항목이 더 이상 없다 (AC-22)
  - `hls_too_many_segments` 코드가 §5.1 표에 있다
- **검증**:
  ```bash
  grep -n "DNS rebinding\|hls_too_many_segments" "D:/file-server-hls-ssrf/SPEC.md"
  ```
  → DNS rebinding은 §9 Always에서 한 번만 등장, hls_too_many_segments는 §5.1 표에 등장
- **복잡도**: S
- **의존**: F1과 함께 묶거나 분리 가능

> ### 체크포인트 ④ 통합 — Phase F 종료
> 검토 포인트:
> - SPEC와 코드가 wire-protocol 단위까지 일치하는지 (특히 임시 디렉터리 명명, 에러 코드 식별자)
> - Known limitations의 다른 항목(live, progress.total)은 손대지 않았는지
>
> 검증: 위 grep 명령들 + 파일 시각 검토
> 커밋: `docs(spec): HLS DNS rebinding 한계 제거 + 새 흐름 §2.6.1 갱신`

---

## 9. Phase G — 머지 준비

### Task G1 — 전체 회귀 + 커밋 정리 + PR 본문

- [ ] **AC**:
  - `go test ./...` 전체 통과 (ffmpeg 의존 케이스 skip은 OK)
  - `go vet ./...` clean
  - 커밋 로그가 phase 단위로 정리되어 있고 각 메시지가 한글 본문 + 영문 prefix 컨벤션
  - PR 본문이 issue #5를 close (`Closes #5`), AC 목록이 spec 링크로 정리, 검증 명령 cheat sheet 포함
- **파일**: PR 본문은 별도 파일이 아닌 `gh pr create` 명령으로 작성
- **검증**:
  ```bash
  go test -C "D:/file-server-hls-ssrf" ./... 2>&1 | tail -10
  go vet -C "D:/file-server-hls-ssrf" ./...
  git -C "D:/file-server-hls-ssrf" log --oneline develop..HEAD
  ```
- **복잡도**: S
- **의존**: F2 (모든 phase 완료 후)

> ### 체크포인트 ⑤ — 머지
> - 사용자 검토: 커밋 시퀀스, PR 본문 초안
> - merge 방식: `feature/url-import-review-round*` 패턴(`git merge --no-ff`) 또는 GitHub PR — 사용자에게 확인
> - 머지 후 issue #5 자동 close 확인

---

## 10. 리스크 / 미해결 이슈

| ID | 리스크 | 완화 |
|---|---|---|
| R1 | `-rw_timeout` 제거 후 file 입력에서 ffmpeg가 stuck 가능성 (이론상 없음) | D2 검증 시 `TestRunHLSRemux_Success` 회귀로 확인 |
| R2 | 원본 확장자 보존 시 ffmpeg가 `-allowed_extensions ALL`로도 인식 못하는 변종 (예: 대문자, 비표준) | 보수적 whitelist (`.ts`/`.m4s`/`.mp4`/`.aac`/`.m4a`/`.vtt`) 외는 `.bin`으로 강등. `.bin`이면 fmt sniff 의존 — 정상 HLS는 위 허용 목록 안에 들어오니 실위험 낮음 |
| R3 | 사전 다운로드로 첫 progress까지 지연 (대용량 VOD) | 수용 — spec §7 Known limitations에 명시. UI는 indeterminate 표시 |
| R4 | sequential download 비용 (segment 수 × per-fetch 지연) | 수용 — 병렬화는 별도 spec |
| R5 | `runFfmpeg` 글로벌 var swap이 병렬 테스트와 충돌 | swap 테스트에 `t.Parallel()` 사용 금지 — D1 코멘트에 명시, 테스트 작성 시 lint으로 잡기 어려움 → 코드 review 룰 |
| R6 | 임시 디렉터리 random 충돌 (10조분의 1 미만) | `os.MkdirTemp`의 random suffix가 충분 |

---

## 11. 검증 명령 cheat sheet

```bash
# Phase별 단위 테스트
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestSequenceResolver -v   # A1
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestParseMediaPlaylist -v # B1
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestDownloadOne -v        # C1
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestMaterializeHLS -v     # C2
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestRunHLSRemux -v        # D2
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestFetch_HLS -v          # E1
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run "TestFetch_HLS_.*Private|TestFetch_HLS_DNSRebinding" -v  # E2
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test -C "D:/file-server-hls-ssrf" ./internal/urlfetch -run TestFFmpegInvocation -v   # E3

# 전체 회귀 (Phase G)
go test -C "D:/file-server-hls-ssrf" ./...
go vet -C "D:/file-server-hls-ssrf" ./...
```

---

## 12. 진행 상황 (체크박스 — 진행 중에 갱신)

- [ ] **Phase A** — 테스트 인프라
  - [ ] A1: sequenceResolver
  - [ ] A2: captureFfmpeg helper
- [ ] **Phase B** — 파서
  - [ ] B1: parseMediaPlaylist
- [ ] **Phase C** — 다운로더 + materializer
  - [ ] C1: downloadOne
  - [ ] C2: materializeHLS
  - [ ] C3: 임시 디렉터리 패턴 (E1과 함께 land)
- [ ] **Phase D** — ffmpeg 호출 전환
  - [ ] D1: runFfmpeg 함수 변수
  - [ ] D2: runHLSRemux 시그니처 + argv
- [ ] **Phase E** — 통합
  - [ ] E1: fetchHLS 재배선
  - [ ] E2: DNS rebinding 회귀 6종
  - [ ] E3: argv invariant 2종
- [ ] **Phase F** — SPEC 업데이트
  - [ ] F1: §2.6 / §2.6.1
  - [ ] F2: §5.1 / §9
- [ ] **Phase G** — 머지
  - [ ] G1: 전체 회귀 + PR
