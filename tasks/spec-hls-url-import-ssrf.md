# Spec: HLS URL Import — DNS rebinding 방어 강화

> 부모 SPEC: [`/SPEC.md`](../SPEC.md). 이 문서는 그중 **§2.6 URL Import / §2.6.1 HLS 스트림 다운로드**의 SSRF 방어 모델을 강화한다.
>
> 선행: PR #4 (`feat(urlfetch): block private network imports`, merged) — 일반 HTTP 경로의 사설 IP 차단 + DNS rebinding 차단(IP literal pin 방식).
>
> 관련 이슈: [#5 — Harden HLS URL import against DNS rebinding](https://github.com/changmink/MM/issues/5)
>
> 핸드오프: [`tasks/handoff-hls-url-import-ssrf.md`](handoff-hls-url-import-ssrf.md)
>
> **Status: merged** — SPEC §2.6.1로 흡수, `internal/urlfetch/dialer.go` 등이 머지됨. 본 문서는 historical record.

---

## 1. Objective

### 현재 문제

PR #4에서 일반 HTTP URL import 경로는 다음 방식으로 DNS rebinding을 닫았다.

1. Go 코드가 hostname을 DNS 해석.
2. 결과 IP가 loopback / private / link-local / multicast / unspecified면 거부.
3. 통과한 IP literal로 직접 dial.

HLS 경로는 다르다. 현재 흐름(`internal/urlfetch/hls.go`):

1. Go 클라이언트가 master playlist를 fetch (보호 클라이언트 사용 — DNS 검증됨).
2. master에서 BANDWIDTH 최고 variant 선택, hostname을 한 번 DNS 검증.
3. **ffmpeg에 variant URL 문자열을 그대로 넘김** (`runHLSRemux`).
4. ffmpeg가 variant playlist + 모든 segment + key 파일을 자체 DNS 해석으로 fetch.

3·4단계 사이에서 악성 DNS 서버는 Go 검증 시점에는 public IP를, ffmpeg fetch 시점에는 사설 IP를 응답하여 **DNS rebinding으로 SSRF 우회**가 가능하다. 이는 SPEC §9 Boundaries의 acknowledged risk로 명시돼 있다.

### 목표

ffmpeg가 공격자 제어 hostname을 직접 DNS 해석하지 않도록 HLS 파이프라인을 재설계한다.

핵심 불변식: **ffmpeg의 입력은 항상 검증된 local 파일 경로**여야 한다. 원격 URL은 ffmpeg 손에 닿지 않는다.

### Target user

단일 사용자(개인). 단, 본 강화 작업은 단일 사용자 LAN 모델을 넘어 외부 노출 시나리오에서도 견고해야 한다(이슈 #5의 명시적 요구).

### Non-goals

- DASH(`.mpd`) 지원 — 기존과 동일하게 범위 외.
- DRM / Widevine / Fairplay — 기존 spec 그대로 미지원.
- Live HLS 전용 분기 — 기존 timeout/size 상한 정책 유지.
- AES-128 외 암호 방식(SAMPLE-AES 등) — ffmpeg 처리에 위임, 실패 시 `ffmpeg_error`.
- Audio/subtitle alternate rendition group(`#EXT-X-MEDIA`) — 기존과 동일하게 BANDWIDTH 최고 variant만 처리.
- Segment 병렬 다운로드 — 순차 다운로드. 추후 별도 spec.
- HTTP Range로 segment 부분 이어받기 — 범위 외.

---

## 2. Scope

### In scope

- `internal/urlfetch/hls.go`: master + variant playlist 본문을 Go가 받고 파싱. segment URI, key URI, init segment URI(`#EXT-X-MAP`)를 모두 추출, DNS 검증, Go 클라이언트로 다운로드, local 경로로 재작성한 playlist를 임시 디렉터리에 저장.
- `internal/urlfetch/fetch.go`: 기존 `Resolver`/`WithResolver`/`AllowPrivateNetworks` 인프라 재사용. 새 헬퍼는 같은 패키지에 추가.
- `runHLSRemux`: ffmpeg 호출 인자를 local-only로 변경 (`-protocol_whitelist file,crypto`, 입력은 local 파일).
- 임시 파일 정리: 새 `.urlimport-hls-<random>/` 디렉터리 단위 cleanup.
- 테스트: fake resolver 주입을 활용한 DNS rebinding 회귀 테스트 + 기존 HLS fixture 회귀 (real ffmpeg 의존 케이스는 skip 패턴 유지).
- `SPEC.md` §2.6.1 / §9 Boundaries 업데이트 — Known limitations 항목 제거 또는 "약화된 한계"로 재작성.

### Out of scope

- `internal/handler/import_url.go` 등 핸들러 레이어. SSE 이벤트 스키마(`start`/`progress`/`done`/`error`/`summary`) 그대로 유지.
- `web/app.js` 클라이언트 코드 변경 없음 (계약 동일).
- 기존 PR #4의 일반 HTTP 경로 — 손대지 않음.
- TS→MP4 변환(`internal/convert`) — 무관.

---

## 3. Design

### 3.1 새 흐름 개요

```
[client]
   │  POST /api/import-url  (URL=https://attacker.example/master.m3u8)
   ▼
[handler/import_url.go: runImportJob]
   │  fetchOneJob → urlfetch.Fetch(ctx, client, rawURL, ...)
   ▼
[urlfetch.Fetch]                                ← 변경 없음
   │  GET master.m3u8 → resp (Go 보호 클라이언트, IP-pin)
   ▼
[urlfetch.fetchHLS]                             ← 본 spec의 변경 지점
   │  1. master 본문 read (1 MiB cap)
   │  2. parseMasterPlaylist → variantURL 결정
   │  3. variantURL 이 master 와 다르면:
   │       └─ Go 클라이언트로 variant playlist 새로 fetch (IP-pin)
   │     같으면 master 본문을 variant playlist 로 재사용
   │  4. parseMediaPlaylist → segment URIs + key URI + init segment URI 수집
   │     (모두 base 기준 resolve)
   │  5. 임시 디렉터리 destDir/.urlimport-hls-<random>/ 생성
   │  6. 모든 segment + key + init 을 Go 클라이언트로 순차 다운로드:
   │       - DNS 검증 (이미 보호 클라이언트가 dial 시점에 검증)
   │       - 누적 바이트가 maxBytes 초과 시 → too_large
   │       - 파일명: seg_0000.ts, seg_0001.ts, ..., key_0.bin, init.mp4
   │  7. local-playlist.m3u8 작성 — segment/key/init URI를 모두 local 경로로 재작성
   │  8. runHLSRemux(ctx, localPlaylistPath, outputMP4Path, ...)
   │       └─ ffmpeg -protocol_whitelist file,crypto -allowed_extensions ALL
   │                 -i <localPlaylistPath> -c copy -bsf:a aac_adtstoasc
   │                 -f mp4 -movflags +faststart -y <outputMP4Path>
   │  9. outputMP4 → destDir 로 atomic rename
   │ 10. 임시 디렉터리 .urlimport-hls-<random>/ 통째로 삭제
   ▼
[urlfetch.Result] → fetchOneJob → SSE done
```

### 3.2 키 결정 사항 (트레이드오프)

#### D-1. 왜 segment를 Go가 미리 다 받고 ffmpeg에 넘기나

| 옵션 | 장점 | 단점 |
|---|---|---|
| **A. 모든 segment 사전 다운로드(채택)** | DNS rebinding 완전 차단, ffmpeg가 네트워크 미사용, 디버깅 쉬움 | 디스크/시간 비용이 처음부터 발생. live는 사실상 미지원 |
| B. 로컬 HTTP proxy를 띄워 ffmpeg가 거기에만 연결 | 스트리밍 가능 | proxy 자체 보안 표면 추가, 포트 충돌, 동시성 복잡 |
| C. variant URL의 hostname을 IP literal로 치환 후 ffmpeg에 전달 | 간단 | HTTPS SNI/인증서 검증 깨짐, Host header 수동 설정 어려움, 부분 방어 |

A는 "ffmpeg 입력은 local 파일"이라는 단순 불변식을 만들어 검증 가능성이 압도적으로 높다. live HLS는 어차피 timeout/size로 잘리는 케이스(기존 동작)이고, VOD 위주 사용 환경에서 사전 다운로드 비용은 ffmpeg가 어차피 다 받았을 바이트량과 동일하다 — **중복 비용 없음**.

#### D-2. ffmpeg `-protocol_whitelist`

기존: `http,https,tls,tcp,crypto`
신규: `file,crypto`

`file`만으로 local 경로 + AES-128 key 파일 접근 가능. `crypto`는 ffmpeg 내부 AES 처리에 필요. 네트워크 프로토콜은 모두 제거.

`-allowed_extensions ALL`: ffmpeg는 보안 상 m3u8/ts/aac 등 일부 확장자만 default로 따른다. 우리가 segment 파일명을 `seg_0000.ts`로 규격화하면 default whitelist에 포함되지만, 외부 stream의 mp4/m4s/aac/m4a 등도 정상 segment일 수 있어 ALL로 명시. local 파일이고 우리가 다 받은 것이라 widening 위험 없음.

#### D-3. 임시 디렉터리 위치

`destDir/.urlimport-hls-<random>/` (예: `/data/Movies/.urlimport-hls-abc123/`).

- destDir 안에 두면 atomic rename이 동일 파일시스템에서 일어나 빠르고 EXDEV 회피.
- `.`-prefix이라 browse 핸들러의 숨김 필터가 자동 적용.
- 실패/취소/성공 어느 경로에서도 `defer os.RemoveAll`로 일괄 정리.

#### D-4. progress 이벤트 의미

기존: `progress.received` = 출력 MP4 임시 파일 size (ffmpeg 진행).

신규에서는 두 단계로 나뉜다.
- Phase 1 (Go segment fetch): 누적 다운로드 바이트.
- Phase 2 (ffmpeg remux): 출력 MP4 size.

**클라이언트 호환성** 때문에 단일 카운터로 노출한다 — Phase 1 진행 중에는 누적 다운로드 바이트, Phase 2 진행 중에는 (Phase 1 누적량) + (출력 MP4 size — Phase 1 누적량 차감). 단조 증가 보장. 클라이언트는 의미를 모르고 그냥 단조 증가로 보면 된다.

대안: Phase 1 동안 progress 0 만 emit (현재 코드의 자연 결과). UX 떨어지지만 단순.

**채택**: Phase 1 = 누적 다운로드 바이트, Phase 2 = 출력 MP4 size로 단순화하되 monotonicity는 max(prev, cur)로 강제. 두 단계 사이의 비대칭은 SPEC §2.6.1에 한 줄 기록.

#### D-5. AES-128 key 처리

`#EXT-X-KEY:METHOD=AES-128,URI="https://attacker/key.bin",IV=...`

- URI는 base 기준 resolve.
- Go 클라이언트로 다운로드 (DNS 검증).
- 임시 디렉터리에 `key_0.bin` 으로 저장.
- playlist 안 URI를 `key_0.bin` (상대 경로)으로 재작성.
- IV 속성은 그대로 유지.
- METHOD가 `NONE` 또는 `SAMPLE-AES` 외이면 그대로 ffmpeg에 위임 — 단, URI가 있으면 무조건 사전 다운로드.

여러 key rotation: `#EXT-X-KEY` 등장 순서대로 `key_0`, `key_1`, ... 번호 부여.

#### D-6. `#EXT-X-MAP` (init segment, fMP4)

`#EXT-X-MAP:URI="init.mp4",BYTERANGE=...` — fMP4 스트림의 초기화 segment.

- URI 처리는 segment와 동일.
- `init.mp4`로 저장, playlist URI 재작성.
- BYTERANGE는 그대로 유지 — local file에 대해 ffmpeg가 처리.

#### D-7. `#EXT-X-BYTERANGE`

segment의 부분 범위. 다운로드 단계에서는 **전체 segment 파일을 받아야** 한다 (Range 요청도 이론적으로 가능하지만 같은 파일이 여러 BYTERANGE로 참조될 때 중복 다운로드 발생). playlist의 BYTERANGE 속성은 그대로 유지하면 ffmpeg가 local file에서 byte range를 읽는다.

#### D-8. 새 리소스 한도

| 항목 | 값 | 이유 |
|---|---|---|
| Master playlist 본문 | 1 MiB | 기존 유지 (`hlsMaxPlaylistBytes`) |
| Variant playlist 본문 | 1 MiB | master와 동일 캡 적용 |
| Segment 개수 | 10,000 | 6초 segment 기준 약 16시간 — 일반 VOD 영화·강의 충분, 종일 라이브 녹화는 미커버. `url_import_max_bytes` 누적 cap이 못 막는 요청 수 폭주 방어 |
| Key 파일 크기 | 64 KiB | AES-128 raw key는 16 byte. 64 KiB는 매우 관대한 캡 |
| Init segment 크기 | 16 MiB | fMP4 init segment는 보통 수 KiB. 캡은 도구적 |
| 누적 다운로드 바이트 | `url_import_max_bytes` (§2.7) | 기존 동일. segment 다운로드 누적 + 출력 MP4 모두에 적용 |

각 individual fetch는 누적 카운터를 공유 — 어느 단계에서든 초과 시 `too_large`.

#### D-9. Segment 다운로드 실패 처리

- HTTP 4xx/5xx → `http_error` (segment URL 포함, 단 redact 적용)
- TLS 실패 → `tls_error`
- 사설 IP → `private_network`
- 파일시스템 쓰기 실패 → `write_error`
- segment 1개라도 실패하면 전체 import 실패 — 부분 결과 폐기, 임시 디렉터리 정리.

#### D-10. fetcher 재사용 vs 새 함수

- 기존 `Fetch(ctx, client, rawURL, destDir, ...)`는 Content-Type 분기/HLS 감지/사이즈 검증/atomic rename까지 포함하는 high-level API.
- segment fetch에는 그게 다 필요 없다 — 단순 "URL → local file, IP-pin, size cap, error 분류" 정도.
- 새 함수 `downloadOne(ctx, client, urlStr, destPath string, remainingBytes int64) (bytesRead int64, err error)`를 `internal/urlfetch/`에 추가. content-type 검증 안 함 (segment는 ffmpeg가 검증).

### 3.3 코드 구조 변화

#### `internal/urlfetch/hls.go` 새 함수

```go
// 새 함수
func parseMediaPlaylist(body []byte, base *url.URL) (*mediaPlaylist, error)
type mediaPlaylist struct {
    rawLines []string         // 원본 라인 (재작성 시 출력 베이스)
    keys     []playlistKey    // #EXT-X-KEY 항목 목록 (line index, resolved URL)
    initSeg  *playlistInit    // #EXT-X-MAP 항목 (있으면)
    segments []playlistSeg    // #EXTINF 다음의 segment 라인 (line index, resolved URL)
}

// playlist 안의 모든 원격 리소스를 Go 클라이언트로 받고 local 경로로 재작성한 새 playlist 본문을 돌려준다.
// 임시 파일은 모두 hlsTempDir 안에 생성된다.
func materializeHLS(
    ctx context.Context,
    client *http.Client,
    pl *mediaPlaylist,
    hlsTempDir string,
    remainingBytes *atomic.Int64,    // 누적 cap 카운터, 호출자와 공유
    cb *Callbacks,                    // progress emit
) (localPlaylistPath string, fetchedBytes int64, err error)

// segment / key / init 한 개를 받아 destPath에 쓰고 받은 바이트 수를 돌려준다.
// remainingBytes 가 0 이하가 되면 errHLSCapExceeded.
func downloadOne(
    ctx context.Context,
    client *http.Client,
    urlStr string,
    destPath string,
    remainingBytes *atomic.Int64,
) (int64, error)
```

#### `runHLSRemux` 시그니처 변경

```go
// 이전
func runHLSRemux(ctx, variantURL, tmpPath string, cb *Callbacks, maxOutputBytes int64) error

// 이후
func runHLSRemux(ctx, localPlaylistPath, outputPath string, cb *Callbacks, maxOutputBytes int64) error
```

이름은 유지(역할 동일 — ffmpeg 호출 + watcher), 첫 인자만 의미 변경. ffmpeg argv도 위 D-2대로 바뀐다.

#### `runFfmpeg` 함수 변수 — argv invariant 테스트용

ffmpeg 호출 자체를 패키지 레벨 함수 변수로 추출해 AC-10 / AC-11 (argv 격리 검증)을 단위 테스트로 잡는다. 핵심 invariant("ffmpeg는 절대 원격 URL을 직접 받지 않는다")가 미래 회귀에 의해 깨지면 자동으로 실패하도록 가드한다.

```go
// hls.go (새 패키지 var)
//
// runFfmpeg는 테스트가 swap할 수 있는 ffmpeg 실행 진입점이다. argv invariant
// (입력은 local 파일 only, -protocol_whitelist는 file,crypto only)를 단위
// 테스트로 검증하기 위해 추출한다. production은 defaultRunFfmpeg를 사용.
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

`runHLSRemux` 안의 기존 `exec.CommandContext("ffmpeg", ...)` + `cmd.Start/Wait` 호출은 `runFfmpeg(ctx, argv, &stderrBuf)`로 대체. watcher goroutine, kill 로직, exit code 분류는 그대로 유지 — `runFfmpeg`는 "argv를 받아 실행하고 종료를 기다린다"만 책임진다.

테스트 안전 장치: 모든 swap 테스트는 `t.Cleanup(func() { runFfmpeg = defaultRunFfmpeg })`로 원복. 병렬 테스트는 이 변수를 건드리는 케이스끼리는 `t.Parallel()` 사용 금지(또는 별도 `serial.go` 빌드 태그). 다행히 invariant 테스트는 두세 개 정도라 병렬화 손실은 무시 가능.

#### `fetchHLS` 흐름 변경

다이어그램 §3.1대로. 단계 추가만 있고 기존 외부 contract(반환 타입, 에러 코드)는 그대로.

### 3.4 새 에러 코드

기존 코드 유지 + 다음 1개 추가 검토:

- `private_network` — 이미 PR #4에서 정의. 재사용.
- `hls_too_many_segments` — segment 개수 cap 초과. 새 코드.
- `hls_playlist_too_large` — 기존. variant playlist에도 동일 적용.

`hls_too_many_segments`는 `too_large`로 묶을 수도 있으나, 운영자가 cap 조정이 필요할 때 구분되는 식별자가 유용하다. **결정**: 별도 코드 추가, SPEC §5.1 에러 코드 표에 등재.

### 3.5 SPEC.md 업데이트 범위

- §2.6.1 본문 — "다운로드 흐름" 7단계가 새 14단계 정도로 확장됨. 명확히 다시 씀.
- §2.6 step 5 — HLS 단서절("ffmpeg는 자체 DNS 해석 ...우회될 수 있다 — §9 Boundaries 참조") 제거.
- §5.1 에러 코드 표 — `hls_too_many_segments` 추가.
- §9 Always — HLS 항목 보강 ("ffmpeg는 항상 local 파일만 입력으로 받음", "playlist/segment/key는 모두 보호 클라이언트로 사전 다운로드").
- §9 Known limitations — DNS rebinding 한계 항목 **삭제**. 다른 한계(live timeout, progress.total 누락, TOCTOU)는 그대로 유지.

---

## 4. Acceptance Criteria

표준 통과 시나리오 (기존 회귀):

- [ ] **AC-1.** 정상 master + variant + segment(들) → 출력 MP4 정상 생성, `done` 이벤트 수신, `summary{succeeded:1}`.
- [ ] **AC-2.** 정상 media-only playlist (master 없이 직접 .m3u8) → 동일 결과.
- [ ] **AC-3.** 기존 fixture `TestFetch_HLS_*` 모두 통과 (단, real ffmpeg 의존 케이스는 skip 정책 유지).

DNS rebinding / private network 시나리오 (본 spec 핵심):

- [ ] **AC-4.** Master playlist는 public IP로 resolve, **variant URL이 사설 IP**로 resolve → `private_network`. (variant 새 fetch 단계에서 IP-pin이 자동 차단)
- [ ] **AC-5.** Variant playlist 안 **segment URL이 사설 IP**로 resolve → `private_network`. segment 다운로드 단계에서 차단.
- [ ] **AC-6.** **`#EXT-X-KEY` URI가 사설 IP**로 resolve → `private_network`.
- [ ] **AC-7.** **`#EXT-X-MAP` URI가 사설 IP**로 resolve → `private_network`.
- [ ] **AC-8.** **DNS rebinding**: fake resolver가 master fetch 시 public IP, variant fetch 시 private IP를 응답 → variant 단계에서 `private_network`. (ffmpeg는 호출되지 않음)
- [ ] **AC-9.** **DNS rebinding**: master + variant는 public, segment 1개에서 private → `private_network`. (segment 단계에서 차단, 그때까지 받은 다른 segment는 임시 디렉터리에 있다가 cleanup)

ffmpeg 격리 검증:

- [ ] **AC-10.** ffmpeg argv 검사 — `runFfmpeg` 함수 변수(§3.3)를 swap한 단위 테스트가 capture한 argv에서 `-i` 다음 인자가 절대 경로(local file), `http://`·`https://` prefix 미포함. 정상 happy-path + master/variant/segment + key URI + init segment 시나리오 각각에 대해 검증.
- [ ] **AC-11.** ffmpeg `-protocol_whitelist` 인자가 `file,crypto` 만 포함 — `http`/`https`/`tcp`/`tls`/`udp`/`rtp`/`pipe` 미포함. AC-10과 같은 swap 테스트로 같이 검증.

리소스 한도:

- [ ] **AC-12.** Variant playlist 본문 1 MiB 초과 → `hls_playlist_too_large`.
- [ ] **AC-13.** Segment 개수 10,000 초과 playlist → `hls_too_many_segments`. 일반 VOD(영화/강의)는 cap 도달 불가, 종일 라이브 녹화 변환 시도는 cap에 막힘 — 의도된 trade-off.
- [ ] **AC-14.** 누적 다운로드 바이트가 `url_import_max_bytes` 초과 → `too_large`. (segment 다운로드 단계에서 차단)
- [ ] **AC-15.** 출력 MP4 크기가 `url_import_max_bytes` 초과 → `too_large` (기존 동작 유지).

cleanup:

- [ ] **AC-16.** AC-4~9 + AC-12~15 어느 실패 경로에서든 `destDir/.urlimport-hls-*/` 디렉터리 잔존 없음.
- [ ] **AC-17.** 성공 경로에서도 `.urlimport-hls-*/` 정리, 출력 MP4만 destDir에 남음.
- [ ] **AC-18.** request context cancel → ffmpeg kill + 임시 디렉터리 정리, `network_error` 또는 `cancelled`.
- [ ] **AC-19.** request context deadline → 동일 + `download_timeout`.

ABI 호환:

- [ ] **AC-20.** SSE 이벤트 시퀀스가 기존과 동일: `register` → `queued` → `start` → (`progress` 0..N) → `done`/`error` → `summary`. `start.total` 0 유지 (스트림 크기 사전 추정 불가).
- [ ] **AC-21.** `progress.received`는 단조 증가 (Phase 1: 누적 다운로드, Phase 2: 누적 다운로드 + 출력 MP4 증가분).

문서 정렬:

- [ ] **AC-22.** SPEC.md §9 Known limitations에 HLS DNS rebinding 항목이 더 이상 없다.
- [ ] **AC-23.** SPEC.md §2.6 step 5에 HLS 우회 단서절이 없다.

---

## 5. Open Questions

- **Q1.** Phase 1 progress 의미를 단일 카운터로 노출하는 결정(D-4)이 클라이언트(`web/app.js`)의 progress bar UX와 잘 맞는가? 단조 증가만 보장되면 어떤 단위든 그대로 % 환산이 안 되긴 하지만, 현재도 HLS는 indeterminate라 큰 차이는 없어 보인다. 확인 필요.
- **Q2.** `runHLSRemux`의 watcher가 폴링하는 대상 파일이 변경된다(이전: 출력 임시 MP4, 이후: 동일하지만 시작이 늦어짐). progress 이벤트 throttle 상수(1 MiB / 250 ms)는 그대로 적용한다 — 명시적 변경 불요.
- **Q3.** 동일 URL을 여러 `#EXT-X-KEY`가 참조하는 경우 중복 다운로드를 피해야 하나? 단순화를 위해 첫 구현은 무중복(URL hash로 dedup)을 시도하지 않고 매번 새로 받는다. cap 카운터에 산입. 추후 최적화 별도.
- **Q4.** Live HLS의 `#EXT-X-PLAYLIST-TYPE`이 `EVENT` 또는 부재(즉 sliding window)일 때, 우리가 한 번 fetch한 variant playlist는 시점 t의 스냅샷이다. ffmpeg가 받기 전에 segment가 만료될 수 있다. 단일 사용자 LAN 모델에서 live는 어차피 acknowledged limitation이므로 추가 대응 불요.

---

## 6. Test Strategy

### 6.1 단위 테스트

- `parseMediaPlaylist`: master/media 구분, segment·key·init URI 추출, BYTERANGE 보존, relative URL resolve, 빈 라인/주석 무시 — 표 테스트.
- `downloadOne`: HTTP 200/4xx/5xx 분기, size cap, IP-pin 차단(fake resolver), context cancel.
- `materializeHLS`: 5단계 mock playlist + httptest server로 segment serve, 출력 디렉터리에 모든 파일 존재 확인 + playlist URI 재작성 확인.

### 6.2 통합 테스트

- `TestFetch_HLS_*` 기존 케이스 (real ffmpeg) — 회귀.
- `TestFetch_HLS_DNSRebinding_VariantPrivate` — fake resolver: master 호스트=public, variant 호스트=private. 기대: `private_network`.
- `TestFetch_HLS_DNSRebinding_SegmentPrivate` — segment URL 호스트=private. 기대: `private_network`.
- `TestFetch_HLS_KeyURIPrivate` — `#EXT-X-KEY` URI 호스트=private.
- `TestFFmpegInvocation_LocalOnly` — `runFfmpeg` 함수 변수(§3.3)를 capture-only 스텁으로 swap. 스텁은 argv를 기록하고 빈 출력 MP4를 만들어 후속 `os.Stat` 통과시킨다. 검증: argv 안 모든 `-i` 다음 인자가 absolute local path + `http`/`https` prefix 무, `-protocol_whitelist` 값이 `file,crypto` 정확히 일치. master/variant 분리 케이스, segment 다수 + key + init 케이스 두 시나리오를 각각 돌린다. AC-10 / AC-11.
- `TestFetch_HLS_TooManySegments`, `TestFetch_HLS_VariantPlaylistTooLarge` — 한도.
- `TestFetch_HLS_TempDirCleaned_OnFailure` / `_OnSuccess` — `.urlimport-hls-*/` 정리.

### 6.3 테스트 인프라 새 항목

- Fake resolver는 PR #4의 `Resolver` 인터페이스 + `WithResolver` 옵션 그대로 사용. 호출 횟수별 다른 IP를 돌려주는 helper(`sequenceResolver`) 추가.
- ffmpeg 의존 케이스는 `exec.LookPath("ffmpeg")` skip 패턴 유지 — DNS rebinding 회귀 테스트는 ffmpeg 호출 전 단계에서 끝나므로 ffmpeg 없어도 실행 가능 (별도 group).
- `httptest.NewServer`는 127.0.0.1 바인딩 → 클라이언트는 `AllowPrivateNetworks()` 설정 + fake resolver로 사설/공개 분기 테스트.

### 6.4 검증 명령

```bash
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test ./internal/urlfetch -run "TestFetch_HLS|TestParseMediaPlaylist|TestDownloadOne|TestMaterializeHLS" -v
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test ./internal/urlfetch ./internal/handler
GOCACHE=/tmp/go-build-file-server-hls-ssrf go test ./...
```

---

## 7. Boundaries (본 작업 한정)

**항상 할 것:**

- ffmpeg 입력은 항상 검증된 local 파일 경로. argv에 원격 URL이 들어가면 안 된다 — assertion 또는 검증 테스트로 강제.
- 모든 원격 fetch는 보호 클라이언트(`publicOnlyDialContext`)를 통해서만. `http.Get` 직접 호출 금지.
- 임시 디렉터리는 `destDir/.urlimport-hls-<random>/` 패턴으로 통일. 다른 위치(예: `/tmp`) 사용 금지.
- 실패/취소/성공 모두 `defer os.RemoveAll(hlsTempDir)`로 정리.
- segment/key/init 각 fetch마다 누적 cap 카운터 갱신 — 단일 cap이 segment 다운로드와 출력 MP4 모두를 덮어야 한다.
- 새 fetch 함수가 추가되면 그 함수도 ctx 첫 인자, error wrap 패턴 일관 적용.

**하지 않을 것:**

- ffmpeg `-protocol_whitelist`에 `http`/`https`/`tcp`/`tls`/`udp`/`rtp`/`pipe`/`async` 추가.
- ffmpeg argv에 hostname 또는 URL 직접 전달.
- 임시 디렉터리를 destDir 밖에 생성 (예: `os.TempDir()`) — atomic rename에 EXDEV 위험.
- segment 다운로드를 병렬화 (이번 spec 범위 외).
- DASH 포맷 처리 코드를 추가 (별도 spec).
- 기존 외부 API 시그니처(`urlfetch.Fetch`) 변경.
- SSE 이벤트 스키마 추가/변경.
- 클라이언트(`web/app.js`) 코드 수정 — 계약 동일.

**Known limitations (이번 spec 작업 후 남는 것):**

- Live HLS: 기존 timeout/size 정책에 따라 잘림. 본 spec과 무관.
- Sliding window playlist에서 fetch 도중 segment 만료 → ffmpeg 단계에서 fail. 단일 사용자 LAN에선 무시.
- 매우 긴 VOD (수만 segment)는 사전 다운로드 비용으로 첫 progress까지의 지연이 길어진다. 클라이언트에 indeterminate progress 표시는 그대로 유지.

---

## 8. Implementation Phases (개요 — 상세는 후속 `/agent-skills:plan`)

- **Phase A. 테스트 인프라**: `sequenceResolver` 헬퍼 추가, 기존 fixture 재구성, ffmpeg argv 캡처 test double 설계.
- **Phase B. media playlist 파서**: `parseMediaPlaylist` + 단위 테스트. 기존 `parseMasterPlaylist`는 변경 없음.
- **Phase C. segment/key/init 다운로더**: `downloadOne` + `materializeHLS` (단, ffmpeg 미호출 단계까지). `private_network` / `too_large` / `hls_too_many_segments` / `hls_playlist_too_large` 분기 + 임시 디렉터리 cleanup. 단위/통합 테스트.
- **Phase D. ffmpeg 호출 전환**: `runHLSRemux` 인자/`-protocol_whitelist` 변경. 기존 fixture 회귀 + AC-10/AC-11.
- **Phase E. fetchHLS 통합**: 흐름 §3.1 그대로 연결. AC-1~9 회귀 + DNS rebinding 회귀 추가.
- **Phase F. SPEC.md 업데이트**: §2.6 단서절 제거, §2.6.1 본문 재작성, §5.1 에러 코드 추가, §9 Always 보강 + Known limitations 삭제. AC-22/AC-23.
- **Phase G. 머지 준비**: 전체 `go test ./...` + `go vet ./...` + 커밋 정리. PR 본문에 issue #5 close.

---

## 9. References

- 핸드오프: [`tasks/handoff-hls-url-import-ssrf.md`](handoff-hls-url-import-ssrf.md)
- 이슈: https://github.com/changmink/MM/issues/5
- 선행 PR: https://github.com/changmink/MM/pull/4
- 부모 SPEC: [`/SPEC.md`](../SPEC.md) §2.6, §2.6.1, §5.1, §9
- 코드 시작점: `D:/file-server-hls-ssrf/internal/urlfetch/hls.go`, `internal/urlfetch/fetch.go`
