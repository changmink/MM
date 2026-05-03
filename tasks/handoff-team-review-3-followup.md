# 팀 리뷰(3회차) follow-up 핸드오프

> **Status: ready for next session** — 진행 추적은 [`tasks/todo.md` Phase 31](./todo.md). 본 문서는 작업별 배경·권장 구현·검증의 단일 출처.

## 컨텍스트

`feature/team-review-2-important` 브랜치(develop 대비 22 커밋)를 `/agent-skills:review` 5축 단일 리뷰로 검증한 결과:

- **Critical: 0건**
- **Important: 3건** — 본 브랜치 머지 차단성 없음, 후속 정리 가능
- **Suggestion: 6건** — 가독성·일관성 개선, 모두 비차단

본 핸드오프(2회차) 작업은 **그대로 머지 권장**. 본 follow-up은 별도 phase에서 처리.

리뷰 일자: 2026-05-03 (`feature/team-review-2-important` HEAD `73b5430`)

## 우선순위·머지 권장 순서

| # | 작업 | 등급 | 권장 순서 | PR 묶음 |
|---|---|---|---|---|
| FU3-I-1 | SSE/JSON 헬퍼 통합(`handlerutil`) | Important | 1 | 단일 PR |
| FU3-S-2 | 워커 개수 근거 주석 | Suggestion | 2 | docs PR (S-2/3/5 묶기) |
| FU3-S-3 | runImportURLWorkers 이중 가드 주석 | Suggestion | 2 | docs PR |
| FU3-S-5 | sseEmitter mutex 의도 주석 | Suggestion | 2 | docs PR |
| FU3-S-1 | HLS dispatch labeled break | Suggestion | 3 | refactor PR (S-1/6 묶기) |
| FU3-S-6 | upload.go close 에러 wrap 통일 | Suggestion | 3 | refactor PR |
| FU3-I-2-A | compat shim transitional 주석 | Important | 4 | docs PR |
| FU3-I-2-B | 테스트 서브패키지 이전 | Important | 5 | refactor PR (큼) |
| FU3-S-4 | hls helpers stub 이름 변경 | Suggestion | 5 | refactor PR |
| FU3-I-3 | imageconv ctx 보강 | Important | 6 | feat PR (별도 spec 검토) |

권장 cadence:
- **즉시**: FU3-I-1 단일 PR + docs 묶음 PR(S-2/3/5) + refactor 묶음 PR(S-1/6) + I-2-A
- **다음 사이클**: I-2-B (테스트 이전, 1100+ 줄), S-4 (stub 개명)
- **별도 spec 후 진행**: I-3 (`imageconv` ctx 시그니처 변경)

---

## FU3-I-1 — 동일한 SSE/JSON 헬퍼가 3 패키지에 byte-identical 복제

### 배경

2회차 B.1(`5e1cf7d` refactor(handler): import/convert 서브패키지 분리)의 transitional artifact로 `writeJSON` / `writeError` / `assertFlusher` / `writeSSEHeaders` / `writeSSEEvent` 다섯 함수가 다음 세 곳에 동일 코드로 존재:

- `internal/handler/handler.go:185-220`
- `internal/handler/import/common.go:41-97` (`sseEmitter` 추가 보유)
- `internal/handler/convert/common.go:31-96` (`sseEmitter` 추가 보유)

한 곳만 고치고 다른 두 곳을 빠뜨리면 클라이언트 noise / 로그 정책(slog level) drift가 즉시 발생.

### 권장 구현

`internal/handlerutil` (또는 `internal/httpio`) 패키지로 추출:

```go
// internal/handlerutil/json.go
package handlerutil

import (
    "encoding/json"
    "log/slog"
    "net/http"
)

func WriteJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    if err := json.NewEncoder(w).Encode(body); err != nil {
        slog.Debug("response encode failed",
            "method", r.Method, "path", r.URL.Path, "err", err,
        )
    }
}

func WriteError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) { /* ... */ }
```

```go
// internal/handlerutil/sse.go
package handlerutil

func AssertFlusher(w http.ResponseWriter, r *http.Request) http.Flusher { /* ... */ }
func WriteSSEHeaders(w http.ResponseWriter) { /* ... */ }
func WriteSSEEvent(w http.ResponseWriter, flusher http.Flusher, payload any) { /* ... */ }
func NewSSEEmitter(w http.ResponseWriter, flusher http.Flusher) func(any) { /* mutex 보호 */ }
```

세 곳의 호출 사이트를 `handlerutil.WriteJSON(...)` 등으로 일괄 치환. 부모 `handler` 패키지의 unexported `writeJSON`/`writeError`는 thin wrapper로 유지(기존 호출 사이트 보존).

### 영향 파일

- 신규: `internal/handlerutil/{json.go,sse.go,*_test.go}`
- 갱신:
  - `internal/handler/handler.go:185-220` → `handlerutil` import
  - `internal/handler/import/common.go:41-97` → `handlerutil` import (전체 삭제)
  - `internal/handler/convert/common.go:31-96` → `handlerutil` import (전체 삭제)
- 호출 사이트: `internal/handler/import/{import_url,jobs}.go`, `internal/handler/convert/{convert,image,webp}.go` 등에서 `writeError(...)` → `handlerutil.WriteError(...)`

### 검증

```bash
grep -rn "func writeJSON\|func writeError" internal/    # → handlerutil/ 1곳만
go build ./...
go vet ./...
go test ./...
```

### 주의

`writeJSON` / `writeError` 가 부모 `handler` 패키지에 unexported로 남아 있어도 무방 — 한 줄 wrapper로 패키지 내 호출 사이트 보존. 핵심은 **로직이 한 곳에만 있는 것**.

---

## FU3-I-2 — `*_compat_test.go` 셰임의 production export 노출

### 배경

2회차 B.1(`5e1cf7d`) 분리 시 부모 `internal/handler/` 패키지의 기존 테스트가 unprefixed 이름(`recoverImportJob`, `summarizeURLs`, `redactURL`, `maxImportURLLength`, `sseStart` 등)을 그대로 호출할 수 있도록:

1. 서브패키지가 캡 wrapper를 export — `internal/handler/import/common.go:99-117`의 `RecoverImportJob`, `SummarizeURLs`, `SummaryEvent`, `NormalizeURLs`, `RedactURL`
2. 부모 패키지의 `*_compat_test.go`가 그 wrapper를 re-import해 lower-case 별칭을 만듦 — `internal/handler/import_url_compat_test.go:1-37`, `internal/handler/convert_compat_test.go:1-3`

문제: **테스트만을 위한 export가 production API surface에 노출됨**. 외부 import는 없지만 패키지 godoc/IDE 자동완성에 잡힘. 이름 충돌 위험은 없으나 분리의 의도와 불일치.

### 권장 구현 — 옵션 A (단기, 비차단)

wrapper 함수에 transitional 주석 추가. 표면 변경 없음.

```go
// internal/handler/import/common.go

// RecoverImportJob is exported only so the legacy parent-package tests can call
// the internal recoverImportJob helper. Will be removed once import_url_test.go
// migrates into this package.
//
// Deprecated: transitional shim — see tasks/handoff-team-review-3-followup.md FU3-I-2.
func RecoverImportJob(rec any, job *importjob.Job) {
    recoverImportJob(rec, job)
}
```

`SummarizeURLs`, `SummaryEvent`, `NormalizeURLs`, `RedactURL` 모두 동일 패턴.

### 권장 구현 — 옵션 B (정공, 큰 PR)

테스트 자체를 서브패키지로 이전.

- `internal/handler/import_url_test.go` (1262줄) → `internal/handler/import/import_url_test.go`
  - 패키지 선언 `package handler` → `package importurl`
  - 호출 사이트의 `recoverImportJob(...)` → 내부 직접 호출
  - `Register(...)` 부분만 부모 패키지 black-box 테스트가 필요하므로 서브셋만 부모에 남김
- `internal/handler/import_url_compat_test.go` 삭제
- `internal/handler/convert_compat_test.go` 삭제 (`maxConvertWebPPaths` 상수 한 줄짜리 — 사용처 확인 후 삭제)
- 서브패키지의 export wrapper 5개 모두 제거

옵션 B 진행 전 확인:
- `Register(mux, root, root, nil, ...)` 호출 패턴이 서브패키지 내부에서도 가능한지 (현재 `Register`는 `handler.Register` — 서브패키지가 부모를 import하면 cycle. 해결: 일부 테스트는 부모 패키지에 남기고, 단위 테스트만 이전).

### 영향 파일

- 옵션 A: `internal/handler/import/common.go:99-117`
- 옵션 B: `internal/handler/import_url_test.go` 전체, `internal/handler/{import_url,convert}_compat_test.go` 삭제, 서브패키지 wrapper 제거

### 검증

```bash
# 옵션 A
grep -rn "transitional shim\|FU3-I-2" internal/    # 의도 추적 가능
go test ./...

# 옵션 B
go test ./internal/handler/import/...
go test ./internal/handler/...
ls internal/handler/*_compat_test.go    # → no such file
```

---

## FU3-I-3 — A.2 cleanup 고루틴은 늦은 출력만 정리, 진짜 누수는 잔존

### 배경

2회차 A.2(`7552087` fix(handler): convert_image timeout 시 고아 출력 파일 정리)는 timeout 후 `imageconv.ConvertPNGToJPG`가 늦게 성공했을 때 `dstAbs` 파일을 정리하는 best-effort cleanup goroutine을 추가했다. 잘 짠 fix이지만:

- `imageconv.ConvertPNGToJPG`가 영구 hang하면 cleanup 고루틴 + 변환 고루틴 둘 다 누수
- `done` 채널이 buffered(cap=1)이라 변환 고루틴은 단독 진행 가능 — 다만 hang 시 메모리·fd 누수 시간 = 변환 시간

A.2 의사결정 시 옵션 A(`imageconv`에 ctx 추가)는 PNG decode/JPEG encode 자체가 ctx를 honor하지 않아 실효성 없다고 판단해 옵션 B 채택. 회귀가 아닌 **A.2 결정의 알려진 한계**로 본 follow-up에서 별도 처리.

### 권장 구현

후속 작업으로 처리:

1. `imageconv.ConvertPNGToJPG`가 ctx를 받고, decode 직전에 `if err := ctx.Err(); err != nil { return err }` 체크 추가 — 부분적이지만 cancellation propagation 가능
2. 메모리 cap 적용: `imageconv.ErrImageTooLarge` 트리거가 PNG 디코드 진입 후 너무 늦게 발생 — `image.DecodeConfig`로 width×height만 먼저 읽고 cap 초과시 즉시 reject (이미 부분 구현됐다면 wraparound 보강)
3. (또는) `convert/image.go`에서 timeout 후 강제로 별도 worker process를 보내 격리 — over-engineering, 현재 규모에서는 불필요

I-3는 **별도 spec 검토 후 진행** — `internal/imageconv`의 결정과 cap 정책이 서로 얽혀 있어 spec 수준에서 재정리하는 게 안전.

### 영향 파일

- `internal/imageconv/imageconv.go` — `ConvertPNGToJPG` 시그니처 + ctx 체크 + DecodeConfig 우선 호출
- `internal/handler/convert/image.go:142-172` — 호출 측 단순화 (cleanup goroutine 제거 가능)
- A.2 의사결정 주석을 `internal/handler/convert/image.go`에 직접 명시 (현재는 본 핸드오프 문서에만 기록)

### 검증

큰 PNG(~2 GiB) 변환 도중 timeout 발생 시 메모리 사용량이 timeout 시점에 즉시 떨어지는지 (현재는 변환 종료까지 유지). pprof goroutine count 안정성도 확인.

---

## FU3-S-1 — HLS materialize dispatch 루프 labeled break

### 배경

`internal/urlfetch/hls/download.go:232-242`:

```go
for _, job := range jobs {
    select {
    case <-downloadCtx.Done():
        break  // ← select에서만 빠짐
    case jobsCh <- job:
    }
    if downloadCtx.Err() != nil { break }  // for를 빠져나감
}
```

정확성에는 문제 없으나 `runImportURLWorkers`(2회차 C.3)는 동일 패턴을 `dispatch:` 라벨로 깔끔하게 처리(`internal/handler/import/import_url.go:290-302`). 두 곳의 패턴이 분기되어 있음.

### 권장 구현

```go
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
```

후속 `if downloadCtx.Err() != nil { break }` 제거 가능.

### 영향 파일

- `internal/urlfetch/hls/download.go:232-242`

### 검증

`go test -race ./internal/urlfetch/hls/...` — 기존 `TestMaterializeHLS_DownloadFailureReturnsEarly` 통과 + race detector 클린.

---

## FU3-S-2 — 워커 개수 상수의 근거 주석

### 배경

- `internal/handler/import/import_url.go:35` `importURLWorkers = 2`
- `internal/urlfetch/hls/download.go:40` `hlsMaterializeParallelism = 4`

단일 사용자 LAN 가정·SSE 클라이언트 한 명·디스크 I/O bound라는 컨텍스트가 SPEC에는 있어도 코드 옆에는 없음. 향후 튜닝하려는 사람이 "왜 4? 왜 2?" 부터 다시 추적해야 함.

### 권장 구현

```go
// hlsMaterializeParallelism은 한 HLS 배치 안에서 동시에 받을 segment/key/init
// 개수 상한. CDN edge가 다중 hit에 친화적이라 4까지 안전, origin rate-limit
// 유발 가능성을 고려해 8 이상은 회피. settings 노출은 over-engineering 판단.
const hlsMaterializeParallelism = 4
```

```go
// importURLWorkers는 한 URL import 배치 안에서 동시에 받을 URL 개수 상한.
// HLS와 달리 origin이 같다는 보장이 없어 보수적으로 2. process-wide
// importSem(=1)이 배치 간 직렬화를 처리하므로 여기는 batch 내부만 책임.
const importURLWorkers = 2
```

### 영향 파일

- `internal/handler/import/import_url.go:35`
- `internal/urlfetch/hls/download.go:40`

---

## FU3-S-3 — `runImportURLWorkers` 이중 cancellation guard 주석

### 배경

`internal/handler/import/import_url.go:276-285` 워커 진입부 두 가드:

```go
for task := range tasks {
    if job.Ctx().Err() != nil {
        continue
    }
    if job.URLStatus(task.index) == "cancelled" {
        continue
    }
    _ = h.fetchOneJob(...)
}
```

`fetchOneJob`(line 402-404) 내부에 race-close 주석 상세, 워커 진입부에는 없음. 이중 가드 의도가 코드만 보면 모호.

### 권장 구현

```go
for task := range tasks {
    // Two pre-fetch guards: batch cancel (job.Ctx) and per-URL cancel
    // (URLStatus). fetchOneJob's RegisterURLCancel re-checks under j.mu, so
    // these are the cheap fast-path before the lock-protected re-check.
    if job.Ctx().Err() != nil {
        continue
    }
    if job.URLStatus(task.index) == "cancelled" {
        continue
    }
    _ = h.fetchOneJob(...)
}
```

### 영향 파일

- `internal/handler/import/import_url.go:271-288`

---

## FU3-S-4 — `internal/urlfetch/hls/helpers_test.go`의 stub `NewClient` 명명

### 배경

`internal/urlfetch/hls/helpers_test.go:32-43`이 부모 `urlfetch.NewClient`를 mock하기 위해 동일한 이름의 stub을 export. `AllowPrivateNetworks()` 호출 시 사실상 `http.DefaultClient` 반환이라 SSRF 방어가 우회된 상태로 테스트가 진행됨. **테스트 격리는 의도된 설계**지만 mock이라는 점이 함수 이름에서 안 보임 — IDE 자동완성·grep 노이즈 유발.

### 권장 구현

서브패키지 내부 stub의 이름을 `newTestClient` 등으로 명시:

```go
// helpers_test.go
func newTestClient(opts ...testClientOption) *http.Client { /* ... */ }
func testAllowPrivate() testClientOption { /* ... */ }
```

호출 측 (`materialize_test.go`, `download_test.go`, `hls_test.go`, `remux_test.go`) 8곳 정도 일괄 치환.

### 영향 파일

- `internal/urlfetch/hls/helpers_test.go:32-43`
- `internal/urlfetch/hls/{materialize,download,hls,remux}_test.go` — 일괄 치환

### 검증

```bash
grep -n "NewClient\|AllowPrivateNetworks" internal/urlfetch/hls/*_test.go
go test ./internal/urlfetch/hls/...
```

---

## FU3-S-5 — `convert/common.go sseEmitter` mutex 의도 주석

### 배경

`internal/handler/convert/common.go:79-86`:

```go
func sseEmitter(w http.ResponseWriter, flusher http.Flusher) func(any) {
    var mu sync.Mutex
    return func(payload any) {
        mu.Lock()
        defer mu.Unlock()
        writeSSEEvent(w, flusher, payload)
    }
}
```

mutex 필요 이유: `convert.RemuxTSToMP4`의 `OnStart` 콜백은 핸들러 고루틴에서, progress writer 고루틴은 별도 — 두 고루틴이 동시에 `emit`을 호출할 수 있음. 의도 명확하지만 주석이 없음. 향후 리팩터링자가 "단일 고루틴 같은데 왜 mutex?" 의문에 빠질 수 있음.

### 권장 구현

```go
// sseEmitter wraps writeSSEEvent under a mutex so OnStart callbacks (handler
// goroutine) and progress writer goroutine cannot interleave a partial frame.
// import/import_url.go uses a single writer goroutine instead and skips the
// mutex; the convert path's OnStart fires from inside ffmpeg.Run on the
// caller goroutine so it has to share with the writer.
func sseEmitter(w http.ResponseWriter, flusher http.Flusher) func(any) { /* ... */ }
```

### 영향 파일

- `internal/handler/convert/common.go:79-86`

---

## FU3-S-6 — `upload.go` PNG auto-convert close 에러 wrap 일관성

### 배경

2회차 A.1(`dd40cd3`)은 비-conversion 경로에서 close 에러를 5xx로 명시 매핑 + 부분 파일 정리. conversion 경로(`uploadPNGAutoConvert:191-197`)는 close 에러를 raw error로 return:

```go
if closeErr != nil {
    return "", 0, false, warnings, closeErr  // raw error
}
```

호출자(`handleUpload:106-112`)가 이를 받아 `"write file failed"` 5xx로 매핑하므로 wire 동작은 동일하나, A.1의 명시적 wrap 패턴과 비대칭. 한쪽만 고치고 다른쪽 빠뜨리면 운영 로그의 `err` 필드 형태가 갈림.

### 권장 구현

```go
if copyErr != nil {
    return "", 0, false, warnings, fmt.Errorf("copy png temp: %w", copyErr)
}
if closeErr != nil {
    return "", 0, false, warnings, fmt.Errorf("close png temp: %w", closeErr)
}
```

### 영향 파일

- `internal/handler/upload.go:190-197`

### 검증

`go test ./internal/handler -run TestUpload` 통과. 운영 로그에서 두 경로의 err 필드 포맷이 동일한지 수동 확인.

---

## 전체 검증 (모든 작업 후)

```bash
go build ./...
go vet ./...
go test -race ./...
```

수동:
- FU3-I-1: 세 패키지의 import 경로가 `handlerutil`로 통일됐는지 (`grep -rn "handlerutil" internal/`)
- FU3-I-2 옵션 A: `grep -rn "transitional shim\|FU3-I-2"` 의도 추적 가능성
- FU3-I-3: 큰 PNG 변환 timeout 시 메모리·goroutine 즉시 회수 (pprof)
- FU3-S-1: HLS dispatch 패턴이 `runImportURLWorkers`와 일치
- FU3-S-2/3/5: godoc 렌더에서 의도 가시화

## 머지 cadence 권장

**즉시 묶음 (3 PR):**
1. `refactor(handler): SSE/JSON 헬퍼를 internal/handlerutil로 통합` (FU3-I-1)
2. `docs(handler): 워커 상한·이중 가드·sseEmitter 의도 주석` (FU3-S-2/3/5)
3. `refactor: HLS dispatch labeled break + upload close 에러 wrap 일관화` (FU3-S-1/6) + `docs(handler): compat shim transitional 주석` (FU3-I-2-A)

**다음 사이클:**
4. `refactor(handler/import): import_url_test.go를 서브패키지로 이전` (FU3-I-2-B, 1100+ 줄)
5. `refactor(urlfetch/hls): test helper stub을 newTestClient로 개명` (FU3-S-4)

**별도 spec 후:**
6. `feat(imageconv): ConvertPNGToJPG ctx 시그니처 + DecodeConfig 우선 검사` (FU3-I-3) — spec 검토 필수

## 참고

- 1회차 핸드오프: [`tasks/handoff-team-review-critical-fixes.md`](./handoff-team-review-critical-fixes.md) (모두 머지 완료)
- 2회차 핸드오프: [`tasks/handoff-team-review-2-important.md`](./handoff-team-review-2-important.md) (Phase 1-4 완료, 본 follow-up이 후속)
- 진행 추적: [`tasks/todo.md` Phase 31](./todo.md)
