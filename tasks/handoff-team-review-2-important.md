# 팀 리뷰(2회차) Important 후속 핸드오프

> **Status: ready for final verification / PR** — Phase 1 (Group D) + Phase 2 (Group A) + Phase 3 (Group B) + Phase 4 (Group C: C.2/C.3) 완료. C.1은 별도 spec으로 분리되어 본 핸드오프 범위에서 제외.

## 진행 상황 (2026-05-03 갱신)

### 작업 브랜치
- 현재: `feature/team-review-2-important` (develop에서 분기)
- HEAD: `8b80a38` (C.3 commit — import URL 배치 병렬 처리)
- develop 대비 20개 커밋 누적, 작업 트리 clean

### 완료 — Phase 1 (Group D, 문서 정합화)
| 커밋 | 작업 |
|---|---|
| `1018e56` | docs(tasks): 핸드오프 문서 추가 |
| `263a44a` | docs(spec): SPEC §5/§9에 limits.go MaxBytesReader 캡 등재 (D.1) |
| `eeff5a6` | docs(spec): frontend-modularization·hls-ssrf·plan Status 정합화 (D.2-5) |
| `5069bd1` | docs: imageconv 누락·SPEC §10·requireSameOrigin 단일 출처 정리 (D.6) |
| `5b3977c` | docs(readme): D.1 follow-up — body-size cap 정책을 API 절에 등재 |
| `5c25e35` | docs(readme): v0.0.2 릴리즈 노트 + 현재 버전 표기 갱신 (handoff 외 발견) |

### 완료 — Phase 2 (Group A, 코드 안정성)
| 커밋 | 작업 |
|---|---|
| `dd40cd3` | fix(handler): upload 정상 경로 dst.Close 에러 매핑 + 부분 파일 정리 (A.1) |
| `7552087` | fix(handler): convert_image timeout 시 고아 출력 파일 정리 (A.2) |
| `e36694b` | fix(handler): streamTS 1차 캐시 hit fd 이중 close 정리 (A.3) |
| `f822537` | refactor(handler): writeJSON 헬퍼로 정상 응답 Encode 에러 일관 로깅 (A.4) |
| `1fef0ee` | style(handler): tree.go err == io.EOF → errors.Is (A.5) |
| `828b586` | fix(handler): validateName Windows 예약 문자/제어문자/예약 basename 차단 (A.6 — 서버+클라+테스트 40개) |
| `22e082e` | refactor(handler): runImportJob을 cancelRemainingURLs/finalizeBatch로 분해 (A.7) |

### 완료 — Phase 3 (Group B, 아키텍처 정리)
| 커밋 | 작업 |
|---|---|
| `57703ee` | feat(ffmpeg): internal/ffmpeg 패키지로 ffmpeg/ffprobe 호출 통합 (B.3) |
| `ebd18b3` | docs(media): 책임 두 축 명시 (B.4) |
| `3cfc84e` | refactor(urlfetch): hls 서브패키지 분리 (B.2) |
| `5e1cf7d` | refactor(handler): import/convert 서브패키지 분리 (B.1) |

### 완료 — Phase 4 (Group C, 성능 hot path — C.1 제외)
| 커밋 | 작업 |
|---|---|
| `2720dfa` | perf(urlfetch): HLS materialize segment 병렬화 (C.2) |
| `8b80a38` | perf(handler): import URL 배치 병렬 처리 (C.3) |

### 진행 중 의사결정 (다음 세션이 그대로 따를 것)
- **A.2**: 핸드오프의 옵션 B 채택 — `imageconv.ConvertPNGToJPG` 시그니처는 그대로 두고 핸들러 측 best-effort cleanup goroutine으로 처리. 옵션 A(ctx 추가)는 PNG decode/JPEG encode 자체가 ctx를 honor하지 않아 실효성 없다고 판단.
- **A.6**: wire 에러 코드는 `"invalid name"` 단일 유지 (SPEC §5 보존). 더 상세한 진단은 클라이언트 `validateRenameInput`에서 한글 메시지로 노출.
- **A.4**: `writeJSON` 헬퍼는 `handler.go`의 `writeError` 바로 위에 배치. 이미 err 체크가 있던 `import_url_jobs.go` 2곳은 변경하지 않음 (PR 표면 최소화).
- **C.1 (streamTS pipe streaming)**: 별도 spec으로 분리. fragmented MP4 호환성·`-c copy` 실패 fallback 등 trade-off가 spec 수준 검토 필요. **본 핸드오프에서는 제외.**
- **C.2**: `materializeHLS`는 표준 라이브러리 worker pool로 4-동시 병렬화. 새 dependency 없이 `copyWithCaps` CAS 루프 + `phase1Total atomic.Int64` 적용. 검증: `go test ./...`, `go test -race ./internal/urlfetch/...`.
- **C.3**: process-wide `importSem`은 batch 간 직렬화 용도로 유지하고, batch 내부 URL fetch만 worker 2개로 병렬화. summary는 병렬 완료 순서와 무관하도록 최종 URL 상태 snapshot에서 계산. 검증: `go test ./...`, `go test -race ./internal/urlfetch/... ./internal/handler/...`.
- **README v0.0.2 릴리즈 노트**: Phase 1 진행 중 발견된 누락. handoff 외 작업이지만 같은 docs 그룹이라 묶어 처리.

### 남은 작업

본 핸드오프 범위의 Important 작업은 완료됨.

- ~~B.3~~ 완료: `57703ee`
- ~~B.2~~ 완료: `3cfc84e`
- ~~B.1~~ 완료: `5e1cf7d`
- ~~B.4~~ 완료: `ebd18b3`
- ~~C.2~~ 완료: `2720dfa`
- ~~C.3~~ 완료: `8b80a38`
- ~~C.1~~: 별도 spec으로 분리 (위 의사결정 참고). 본 브랜치에서 추가 진행하지 않음.

### 다음 세션 작업 시작 절차
1. `git checkout feature/team-review-2-important` (이미 분기됨)
2. `git log --oneline develop..HEAD | head -20`로 누적 커밋 확인
3. 최종 검증 권장:
   - `go build ./...`
   - `go vet ./...`
   - `go test ./...`
   - `go test -race ./internal/urlfetch/... ./internal/handler/...`
4. 검증 통과 후 PR/merge 준비. C.1은 별도 spec 작업으로 분리.

### 머지 시점 결정 사항 (다음 세션 또는 그 이후)
- Phase 3 + Phase 4 완료 상태. `develop`로 머지할지, Phase 단위 PR로 나눌지 결정 필요.
- 본 브랜치는 docs(Phase 1) + fix/refactor(Phase 2) + arch(Phase 3) + perf(Phase 4) 혼재 — 한 번에 머지하면 PR이 커지지만 logical 그룹별 commit 분할이 이미 되어 있어 review 부담은 작다.

---

## 원본 컨텍스트 (작업 시작 시점, 2026-05-03)

- 작업 브랜치 시작점: `develop` (clean, `dd7fdbe`)
- 코드 변경 없음 — 본 문서가 작업 시작점
- 이전 1회차 핸드오프(`tasks/handoff-team-review-critical-fixes.md`)는 모두 머지 완료

## 컨텍스트

`/team-review` 4축 병렬 리뷰 결과:

| Reviewer | Score | Verdict |
|---|---:|---|
| Architecture | 86/100 | APPROVE WITH NOTES |
| Code Quality | 86/100 | REQUEST CHANGES |
| Performance | 82/100 | APPROVE WITH NOTES |
| Documentation | 86/100 | APPROVED WITH NOTES |
| **평균** | **85.0/100** | — |

종합 verdict: **APPROVE WITH NOTES** — Critical 0건, 강점은 보안 경계(SafePath/SameOrigin)·settings snapshot·panic recovery·테스트 깊이(~13,661줄). 약점은 `internal/handler/` 비대화·ffmpeg 호출 추상화 부재.

## 작업 우선순위

| 그룹 | 작업 | 등급 | 차단성 | 권장 순서 |
|---|---|---|---|---|
| D | 문서 정합화 (5건) | doc Important | 비차단 | 1 (가장 빠름) |
| A | 코드 안정성 (6건) | code Important | 비차단 | 2 |
| C | 성능 hot path (3건) | perf High | 비차단 (체감 큼) | 3 |
| B | 아키텍처 정리 (4건) | arch Important | 비차단 (큰 변경) | 4 |

권장:
- D는 단일 docs PR로 묶기.
- A는 6개 sub-task를 logical 묶음(close 에러군 / errors.Is군 / validateName) 으로 2-3 PR.
- C는 각 작업 분리 PR (회귀 표면 다름).
- B는 가장 마지막 — B.3(ffmpeg 통합)을 먼저 하면 다른 작업 표면이 줄어든다.

## 반드시 먼저 읽을 문서

1. `CLAUDE.md` — 한글 규칙, requireSameOrigin, settings snapshot, 에러 코드 컨벤션
2. `SPEC.md` §2.6 (URL import) / §5 (API) / §9 (Boundaries)
3. `tasks/handoff-team-review-critical-fixes.md` — 1회차 핸드오프 (머지된 패턴 참조)
4. 이 문서

---

# Group A — 코드 안정성 (Code Quality, 6건)

## A.1 — upload.go 정상 경로 dst.Close() 에러 무시

### 배경

`internal/handler/upload.go:121-122` — non auto-convert 경로에서 `io.Copy(dst, part)` 후 `dst.Close()` 반환값이 무시된다. NFS·네트워크 마운트는 close 시점에 deferred write/sync 에러를 반환할 수 있고, 이 경우 잘림(truncated) 업로드가 201로 응답된다. `uploadPNGAutoConvert`(line 187-193)는 close 에러를 체크하므로 동작이 비대칭.

리뷰 인용:
> [code] 일부 OS·FS는 close 시점에 deferred write/sync 에러를 반환(특히 NFS)하므로 잘림 업로드가 201 응답으로 보고될 수 있다.

### 영향 파일

- `D:/file_server/internal/handler/upload.go:121-122`

### 권장 구현

```go
// io.Copy 후
if cerr := dst.Close(); cerr != nil {
    _ = os.Remove(finalPath)
    writeError(w, r, http.StatusInternalServerError, "write file failed", cerr)
    return
}
```

`copyErr != nil` 분기에서는 close 결과를 버려도 무방 (이미 cleanup 진행 중).

### 테스트

`internal/handler/upload_test.go`에 `TestUpload_CloseError_RemovesPartialFile` 추가. 표준 `os.File`로는 close 에러 주입이 어렵다 — `io.WriteCloser` 인터페이스로 주입 가능한 helper로 sink 추출하거나 `t.Skip("non-portable: requires close-error injection")`로 가드. 최소 커버리지로 정상 close → 201 회귀만 보호.

---

## A.2 — convert_image.go 고아 goroutine / 출력 파일

### 배경

`internal/handler/convert_image.go:143-167` — `imageconv.ConvertPNGToJPG`가 `context.Context`를 받지 않아 timeout/cancel 시 goroutine이 계속 돌고, 사용자가 cancel한 후에도 `dstAbs`가 디스크에 남는다. 핸들러는 2초만 join 대기 후 반환. 다음 요청이 같은 이름의 PNG를 변환하면 `already_exists`로 거부되거나 browse가 갑자기 나타난 .jpg를 보여 UX 혼선이 발생한다. 1000개 일괄 변환에서 timeout이 누적되면 goroutine 압박.

### 영향 파일

- `D:/file_server/internal/imageconv/imageconv.go` — `ConvertPNGToJPG` 시그니처
- `D:/file_server/internal/handler/convert_image.go:143-167` — 호출 측 cleanup

### 권장 구현

**옵션 A** (정공): `ConvertPNGToJPG(ctx, srcPath, dstPath)` 시그니처 변경 + 내부에서 ctx 체크 (`os.OpenFile`/`imaging.Open` 호출 사이).

**옵션 B** (간단): 시그니처는 유지하되 핸들러에서 timeout 시 `<-done`을 nil-not-blocking으로 두고 best-effort cleanup goroutine 추가:

```go
select {
case err := <-done:
    if err != nil { /* ... */ }
case <-time.After(convertTimeout):
    writeError(w, r, http.StatusGatewayTimeout, "convert_timeout", nil)
    go func() {
        <-done
        _ = os.Remove(dstAbs)
    }()
    return
}
```

옵션 A 권장 — 단일 출처에서 cancel을 다룬다.

### 테스트

`internal/handler/convert_image_test.go`에 `TestConvertImage_CancelDuringConvert_RemovesPartialOutput` 추가. `imageconv` 테스트 후크(예: `imageconv.MaxPixels = 1`)로 fast-fail을 강제하거나, 큰 PNG 입력으로 자연 timeout 유도.

---

## A.3 — stream.go 1차 캐시 hit fd 이중 close

### 배경

`internal/handler/stream.go:70-79` — 1차 캐시 hit 경로에서 `defer cached.Close()` 등록 직후 `cached.Stat()` 실패 시 `cached.Close()`를 한 번 더 호출. Go `*os.File.Close`는 두 번째 호출이 `os.ErrClosed`로 막히지만, fd가 재할당되는 경합 환경에서 다른 파일을 닫는 패턴. 2차 hit 경로(line 85-93)에는 명시적 close가 없어 일관성도 깨짐.

### 영향 파일

- `D:/file_server/internal/handler/stream.go:70-79`

### 권장 구현

명시적 `cached.Close()`(line 78) 제거. `defer cached.Close()`가 fall-through 시 닫아준다.

```go
cached, err := os.Open(cachePath)
if err == nil {
    defer cached.Close()
    info, err := cached.Stat()
    if err != nil {
        // fall-through to lock-and-retry below — defer가 닫는다
    } else {
        http.ServeContent(...)
        return
    }
}
```

### 테스트

기존 `stream_test.go`의 cache hit 케이스 통과로 회귀 보호. fd 이중 close 자체 검증은 fault injection이 어려움 — 패턴 정리 자체가 가치.

---

## A.4 — JSON Encode 반환 에러 미체크 (다수)

### 배경

`writeError`(handler.go)와 `handleListJobs`/`handleDeleteFinishedJobs`는 fcc3c55에서 정리되었지만, 정상 응답 경로 다수가 `json.NewEncoder(w).Encode(...)` 반환을 무시한다. 클라이언트 disconnect 중 응답 인코딩 실패가 디버깅 불가.

### 영향 파일 (확인 필요)

- `D:/file_server/internal/handler/browse.go:118-122`
- `D:/file_server/internal/handler/files.go:163`
- `D:/file_server/internal/handler/folders.go:134, 216, 259`
- `D:/file_server/internal/handler/settings.go:31, 69`
- `D:/file_server/internal/handler/tree.go:90`
- `D:/file_server/internal/handler/upload.go:152-160`
- `D:/file_server/internal/handler/convert_image.go:73`

### 권장 구현

`internal/handler/handler.go` 또는 `internal/handler/sse.go` 옆에 헬퍼 추가:

```go
func writeJSON(w http.ResponseWriter, r *http.Request, code int, body any) {
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    if err := json.NewEncoder(w).Encode(body); err != nil {
        slog.Debug("response encode failed",
            "method", r.Method, "path", r.URL.Path, "err", err)
    }
}
```

각 핸들러의 직접 Encode 호출을 `writeJSON`으로 일괄 치환. `writeError`도 내부에서 같은 경로 사용 가능.

### 테스트

기존 핸들러 테스트가 status·body 검증을 통과하면 회귀 없음. 별도 추가 불필요.

---

## A.5 — tree.go errors.Is 비표준 비교

### 배경

`internal/handler/tree.go:182` — `if err == io.EOF`. Go 1.13 이후 권장: `errors.Is(err, io.EOF)`. 같은 파일의 다른 곳은 `errors.Is/As` 패턴인데 여기만 예외.

### 영향 파일

- `D:/file_server/internal/handler/tree.go:182`

### 권장 구현

```go
if errors.Is(err, io.EOF) {
    return false, nil
}
```

`errors` import 이미 있는지 확인 (없으면 추가).

### 테스트

기존 `tree_test.go` 통과로 회귀 보호. 추가 불필요.

---

## A.6 — validateName Windows 예약 문자/제어문자/예약 basename

### 배경

`internal/handler/names.go:19-32` `validateName` — Windows 예약 문자(`<`, `>`, `:`, `"`, `|`, `?`, `*`)와 NUL/제어문자(`<0x20`)를 차단하지 않는다. 예약 basename(`CON`/`PRN`/`NUL`/`AUX`/`COM1..9`/`LPT1..9`)도 미차단. `media.SafePath`가 traversal은 막지만 `os.Mkdir("a:b")`가 ERROR_INVALID_NAME으로 떨어지면 사용자에게 generic 500. 단일 사용자 가정이라 critical은 아니지만 rename UX가 깨진다. **클라이언트 측 `web/util.js`도 동일 규칙으로 동기화 필요** (CLAUDE.md "Rename 확장자" 정책).

### 영향 파일

- `D:/file_server/internal/handler/names.go:19-32`
- `D:/file_server/internal/handler/names_test.go` (또는 신규)
- `D:/file_server/web/util.js` (검증 분기 추가)

### 권장 구현

서버 측:

```go
var reservedRunes = "<>:\"|?*"

func validateName(name string) error {
    // 기존 . / .. / "" / 슬래시 검사 유지
    for _, c := range name {
        if c < 0x20 {
            return fmt.Errorf("invalid name: control char")
        }
        if strings.ContainsRune(reservedRunes, c) {
            return fmt.Errorf("invalid name: reserved char %q", c)
        }
    }
    base := strings.ToUpper(stripTrailingExt(name))
    switch base {
    case "CON", "PRN", "AUX", "NUL":
        return fmt.Errorf("invalid name: reserved basename")
    }
    if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
        if base[3] >= '1' && base[3] <= '9' {
            return fmt.Errorf("invalid name: reserved basename")
        }
    }
    return nil
}
```

클라이언트 측 `web/util.js`의 rename 검증에 같은 규칙 추가. CLAUDE.md "Rename 확장자" 동일 규칙 정책 준수.

### 테스트

`names_test.go`에 table-driven 케이스 추가:
- `"a:b"`, `"a*b"`, `"a\x00b"`, `"a\x1fb"`, `"CON"`, `"con.txt"`, `"COM1"`, `"LPT9"` 모두 reject
- `"COM"`(숫자 없음), `"COM10"`(2자리), `"NULL"`(예약 아님) 모두 통과

---

## A.7 — (보너스) runImportJob 분해

### 배경

`internal/handler/import_url.go:259-321` `runImportJob`은 panic recovery·cancel 경로·per-URL cancel skip·summary 산정이 한 함수에 모두 들어 있다. 신규 옵션(예: per-batch concurrency) 추가 시 이중 카운트 회귀 가능성. 현재는 정확하지만 표면이 크다.

### 영향 파일

- `D:/file_server/internal/handler/import_url.go:259-321`

### 권장 구현

작은 helper 두 개로 분리:

```go
func cancelRemaining(job *importjob.Job, fromIdx int) (cancelledCount int) { /* ... */ }
func summarizeBatch(succeeded, failed, cancelled int) jobSummary { /* ... */ }
```

동작 보존, 테스트 표면 동일.

### 테스트

기존 `import_url_test.go`의 batch cancel·summary 케이스 통과 = 회귀 없음. 분해 후 helper 단위 테스트 1-2개 추가 권장.

---

## Group A 검증

```bash
go build ./...
go vet ./...
go test ./internal/handler -run "TestUpload|TestConvertImage|TestStream|TestTree|TestValidateName|TestImportURL" -v
go test ./...
```

---

# Group B — 아키텍처 정리 (4건)

## B.1 — internal/handler 패키지 분리

### 배경

`internal/handler/` 17개 source 파일이 5축 책임(브라우징/CRUD/변환·스트림/import/인프라)으로 비대화. 신규 변환 기능 추가 시 비용이 가파르게 오른다.

### 영향 파일

- `D:/file_server/internal/handler/import_url.go`, `import_url_jobs.go` → `internal/handler/import/` 신설
- `D:/file_server/internal/handler/convert.go`, `convert_image.go`, `convert_webp.go` → `internal/handler/convert/` 신설

### 권장 구현

점진적 분리. 가장 결합도 낮은 두 묶음부터:

1. `handler/import` 서브패키지로 `import_url.go` + `import_url_jobs.go` 이전
2. `handler/convert` 서브패키지로 `convert.go` + `convert_image.go` + `convert_webp.go` 이전

`*Handler`를 컴포지션으로 노출 — 서브패키지가 dataDir·thumbPool·urlClient·settings에 접근. 분리 후 root `handler`는 ~10 파일로 절반 축소.

신중히: `requireSameOrigin` 등록은 root에서 유지 (CLAUDE.md 정책 일관성).

### 테스트

기존 `_test.go` 패키지 이동 + import 경로 갱신. 새 검증 불필요. `go build ./...` + `go test ./...` 통과가 acceptance.

---

## B.2 — internal/urlfetch/hls 서브패키지 분리

### 배경

`internal/urlfetch/fetch.go`(370줄) + `hls.go`(800줄) + `hls_download.go`(305줄)가 한 패키지에 섞여 있다. wire에서는 같은 `/api/import-url`이지만 internal 책임은 다르다. CLAUDE.md "`urlfetch/`: HTTP 다운로드 + HLS materialize/remux" 한 줄 자체가 책임 두 축을 시인.

### 영향 파일

- `D:/file_server/internal/urlfetch/hls.go`, `hls_download.go`, `hls_*_test.go` → `internal/urlfetch/hls/` 서브패키지

### 권장 구현

- `urlfetch/hls/` 서브패키지 생성, hls 관련 파일 이동
- 공통 타입(`Callbacks`, `FetchError`, `Result`)은 부모 `urlfetch/` 유지
- `urlfetch.Fetch`의 HLS 분기에서 `hls.Fetch(ctx, client, ...)` 호출
- B.3(ffmpeg 통합) 후 진행하면 ffmpeg LookPath 중복도 동시 정리됨

### 테스트

기존 `hls_*_test.go` 패키지 변경 + import 경로 조정. `private_network_test.go`, `hls_dns_rebinding_test.go`도 같이 이동.

---

## B.3 — internal/ffmpeg 패키지로 ffmpeg/ffprobe 호출 통합

### 배경

`stream.go`/`thumb/thumb.go`/`convert/convert.go`/`convert/webp.go`/`urlfetch/hls.go`/`convert/webp.go::ProbeStreamInfo` **6곳**에서 `exec.LookPath("ffmpeg")` + `exec.CommandContext(...)` 패턴이 복붙되어 있다. timeout·stderr 캡처·exit code 분류 정책 변경이 6곳을 동시 수정해야 한다. `convert.ErrFFmpegMissing`과 `urlfetch.errFFmpegMissing`이 별개로 정의됨.

### 영향 파일

- 신규: `D:/file_server/internal/ffmpeg/ffmpeg.go`
- 갱신: `internal/handler/stream.go`, `internal/thumb/thumb.go`, `internal/convert/convert.go`, `internal/convert/webp.go`, `internal/urlfetch/hls.go`

### 권장 구현

```go
// internal/ffmpeg/ffmpeg.go
package ffmpeg

var ErrMissing = errors.New("ffmpeg not found")

type ExitError struct {
    Stderr string
    err    error
}

// Run executes ffmpeg with the given args and captures stderr.
// Returns ErrMissing if the binary is unavailable, ExitError on non-zero exit.
func Run(ctx context.Context, args ...string) error { /* ... */ }

func Probe(ctx context.Context, args ...string) ([]byte, error) { /* ... */ }

// RunWithStderr exposes the raw stderr buffer for callers that need progress parsing.
func RunWithStderr(ctx context.Context, stderr io.Writer, args ...string) error { /* ... */ }
```

기존 6곳의 `exec.LookPath` + `CommandContext` 블록을 `ffmpeg.Run` / `ffmpeg.Probe` 호출로 대체. 핸들러 측은 `errors.Is(err, ffmpeg.ErrMissing)` 한 곳에서 체크 → 모든 변환 핸들러가 일관된 에러 코드(`ffmpeg_missing`) 반환.

`convert.ErrFFmpegMissing`, `urlfetch.errFFmpegMissing`은 deprecated 표시 후 다음 단계에 제거.

### 테스트

`internal/ffmpeg/ffmpeg_test.go` 신규 — `exec.LookPath` 모킹 어려우니 환경변수로 PATH 격리하는 helper 사용. 기존 `convert_test.go`/`thumb_test.go`의 ffmpeg-skip 패턴 그대로 통과.

### 주의

이 작업은 표면이 가장 크다. **단독 PR로 분리 + 단일 커밋 머지** 권장. B.2(urlfetch/hls 분리)와 같이 묶지 말 것.

---

## B.4 — media 패키지 책임 분리 (단기는 doc.go만)

### 배경

`media`가 leaf 패키지인데 (a) 타입 분류·MIME (`types.go`), (b) SafePath·MoveFile·MoveDir(`path.go`/`move.go`)까지 같이 들고 있다. `thumb`/`urlfetch`가 (a) 때문에 import하는데 (b)도 transitive하게 끌고 온다.

### 영향 파일

- `D:/file_server/internal/media/doc.go` (신규) — 단기
- 장기: `internal/media/types/`, `internal/media/fsop/` 분리

### 권장 구현

**단기 (이번 핸드오프 범위):**

```go
// internal/media/doc.go
// Package media 는 단일 사용자 미디어 서버의 leaf 유틸 패키지다.
//
// 책임은 두 축:
//   1. 타입 분류·MIME (types.go)         — 의존성 없음, 모든 패키지가 import 가능
//   2. SafePath·MoveFile·MoveDir         — 파일시스템 의존, handler/folders 등이 사용
//
// 향후 (b)가 더 커지면 internal/media/types 와 internal/media/fsop 으로 분리.
package media
```

**장기 (별도 phase):**
- `internal/media/types/` — `types.go` (deps-free)
- `internal/media/fsop/` — `path.go`, `move.go`
- `thumb`/`urlfetch`는 `media/types`만 import하면 그래프가 명확히 단방향.

현재 규모에서 분리는 ROI 낮음. doc.go 한 줄로 의도 명시 = 충분.

### 테스트

`go vet ./...` 통과로 충분.

---

## Group B 검증

```bash
go build ./...
go vet ./...
go test ./...
```

---

# Group C — 성능 hot path (3건)

## C.1 — streamTS 캐시 미스 first-hit 응답 차단

### 배경

`internal/handler/stream.go:67` `streamTS` — TS 캐시 미스 첫 요청은 ffmpeg가 tmp 파일 remux를 끝낼 때까지 응답 본문 0 byte. **1 GiB TS 기준 TTFB ≈ 5–15초**. 클라이언트 비디오 플레이어는 그동안 hang. 캐시 hit 이후엔 영향 없으므로 단일 사용자 빈도는 낮지만 첫 재생 UX는 측정 가능하게 나쁘다.

### 영향 파일

- `D:/file_server/internal/handler/stream.go` `streamTS`
- `D:/file_server/internal/convert/convert.go` `RemuxTSToMP4` — argv 공유 helper 후보

### 권장 구현

ffmpeg stdout을 `pipe:1` + `-movflags frag_keyframe+empty_moov`로 묶어 응답 writer에 직접 흘리고, 백그라운드에서 동일 출력을 디스크 캐시에 fan-out. TTFB → 첫 keyframe 시점(~수백 ms).

```go
args := []string{
    "-i", srcPath,
    "-c", "copy",
    "-movflags", "frag_keyframe+empty_moov",
    "-f", "mp4",
    "pipe:1",
}
// stdout = io.MultiWriter(httpResponseWriter, cacheTempFile)
// cacheTempFile은 ffmpeg 종료 후 atomicRename으로 캐시 자리에
```

Range 요청은 캐시 hit 경로가 그대로 처리하므로 미스 경로만 바뀌면 된다.

### Trade-off

- fragmented MP4는 일부 구형 플레이어에서 seek 정확도가 다르다 → **첫 응답에서만 fragmented, 캐시된 후엔 기존 faststart MP4 서빙하는 hybrid가 안전**
- ffmpeg pipe 모드는 일부 input 코덱에서 `-c copy` 실패 — fallback으로 기존 tmp-file 경로 유지 필요

### 테스트

- `internal/handler/stream_test.go`에 `BenchmarkStreamTS_TTFB_CacheMiss_Pipe_vs_TempFile` 신규 — pipe 효과 정량화 기준
- 기존 cache hit 케이스 회귀 보호
- 수동: `curl -o /dev/null -w "%{time_starttransfer}\n" -H "Range: bytes=0-1023" http://localhost:8080/api/stream?path=/big.ts` — 캐시 미스/히트 두 케이스

### 주의

`streamLocks` 락 보호 유지. pipe 출력 도중 클라이언트 disconnect → ffmpeg 프로세스 cleanup 검증 (ctx 전파 + pipe close).

---

## C.2 — materializeHLS 직렬 segment 다운로드

### 배경

`internal/urlfetch/hls_download.go:142` `materializeHLS` — segment / key / init를 한 개씩 직렬 fetch. 6초 segment × 600개(1시간 VOD) × RTT 50 ms + body 100 KB ÷ 1 MB/s = ≈ **90s wall**. 4-병렬만 해도 60–70% 단축 측정 가능.

### 영향 파일

- `D:/file_server/internal/urlfetch/hls_download.go:142` `materializeHLS`
- `D:/file_server/internal/urlfetch/hls.go:597` `phase1Total` 클로저 캡처
- `D:/file_server/internal/urlfetch/hls_download.go` `copyWithCaps` (Load → Add 비원자 패턴)

### 권장 구현

```go
import "golang.org/x/sync/errgroup"

g, gctx := errgroup.WithContext(ctx)
g.SetLimit(hlsParallelism) // 기본 4
for _, e := range pl.entries {
    e := e
    g.Go(func() error {
        return downloadEntry(gctx, e)
    })
}
if err := g.Wait(); err != nil { /* ... */ }
```

**필수 동시 수정:**
1. `copyWithCaps`의 `remaining.Load()` → `remaining.Add(-n)` 비원자 패턴을 **CAS 루프(`CompareAndSwap`)**로 교체. 주석에 이미 race 가능성이 적혀 있다.
2. `hls.go:597` `phase1Total` 클로저 캡처를 `atomic.Int64`로 교체 — 병렬 progress 합산 race 방지.
3. `progressCh` writer는 이미 채널 기반이라 큰 변경 없음.

### Trade-off

- origin이 per-IP rate limit을 거는 CDN이면 4-동시 안전, 8 이상은 throttle 위험
- settings로 `hlsParallelism` 노출 검토 (CLAUDE.md `settingsSnapshot` 패턴 준수) — 기본 4 권장

### 테스트

- `internal/urlfetch/hls_materialize_test.go`에 100-segment mock origin 직렬/병렬 wall 비교 벤치마크
- 기존 `hls_download_test.go` 통과 확인 — race detector 활성: `go test -race ./internal/urlfetch/...`

---

## C.3 — URL 배치 직렬 처리

### 배경

`internal/handler/import_url.go:264` — 한 번에 하나만 fetch. 500-URL 배치 × 평균 5s/URL = **41분 wall**. 사용자가 SSE 진행을 보는 동안 의미 있는 throughput 손실.

### 영향 파일

- `D:/file_server/internal/handler/import_url.go:259-321` `runImportJob`

### 권장 구현

per-job 내부 worker pool (2–4) 도입. **`importSem`(프로세스 전역 batch 직렬화)은 그대로 두고** batch 안의 URL만 동시화.

```go
const urlWorkers = 2 // 또는 settings 노출
sem := make(chan struct{}, urlWorkers)
var wg sync.WaitGroup
for idx, u := range urls {
    sem <- struct{}{}
    wg.Add(1)
    go func(idx int, u string) {
        defer wg.Done()
        defer func() { <-sem }()
        fetchOneJob(...)
    }(idx, u)
}
wg.Wait()
```

cancel 처리: per-URL goroutine 안에서 `select { case <-job.Ctx().Done(): }` 우선 분기 유지.

### Trade-off

- 동시 URL은 디스크 쓰기 단편화 → SSD에서는 무시 가능, HDD에서 약간 손실
- 실패 모드/취소 책임 매트릭스가 늘어나 테스트 면적 큼 — A.7 (`runImportJob` 분해) 후 진행 권장

### 테스트

- `import_url_test.go`에 per-batch 동시 fetch 케이스 추가
- cancel 도중 per-URL goroutine cleanup 검증
- `TestImportURL_BatchCancel_NoOrphanFetches` 같은 race detector 케이스

---

## Group C 검증

```bash
go build ./...
go test -race ./internal/urlfetch/... ./internal/handler/...
go test ./...
```

수동:
- C.1: 1 GiB TS 첫 재생 TTFB
- C.2: 100-segment HLS materialize wall-clock
- C.3: 100-URL 배치 wall-clock

---

# Group D — 문서 정합화 (5건)

## D.1 — SPEC §5 / §9에 limits.go MaxBytesReader 등재

### 배경

`internal/handler/limits.go`가 `maxUploadBytes=100 GiB` / `maxJSONBodyBytes=64 KiB`로 mutating 핸들러 진입부에 `http.MaxBytesReader`를 박아 두었고 초과 시 `413 too_large` 반환. 하지만 SPEC §5의 어떤 엔드포인트(`POST /api/upload`, `PATCH /api/file`, `PATCH /api/folder`)의 4xx 표에도 `413 too_large`가 없고 §9 "항상 할 것"에도 미명시. 진단/보안적으로 노출되는 응답이라 spec에 잠그는 것이 맞다.

### 영향 파일

- `D:/file_server/SPEC.md` §5 (API), §9 (Boundaries)

### 수정 항목

1. **`POST /api/upload` 4xx 표** — `413 {"error": "too_large"}` 행 추가, "본문 100 GiB 초과" 한 줄
2. **`PATCH /api/file` 4xx 표** — `413 {"error": "too_large"}`, "JSON body 64 KiB 초과"
3. **`PATCH /api/folder` 4xx 표** — 동일
4. **§9 "항상 할 것"** — "Mutating 진입부에서 `http.MaxBytesReader`로 streaming/메모리 적재 cap 적용 (upload 100 GiB / JSON 64 KiB)" 한 줄

### 검증

`grep -n "too_large" SPEC.md internal/handler/*.go` 결과가 wire 코드와 1:1 일치하는지 확인.

---

## D.2 — spec-frontend-modularization "12개" → 17개

### 배경

`tasks/spec-frontend-modularization.md` §3.1·§7 Acceptance Criteria가 `총 12개 .js 파일`로 단정한 채로 있다. 실제 17개 (12 + dragSelect + sseConvertModal + modalDismiss + convertImage + convertWebp). 이미 머지되어 historical record.

### 영향 파일

- `D:/file_server/tasks/spec-frontend-modularization.md` §3.1, §7

### 수정 항목

상단 Status 헤더 바로 아래 한 줄 추가:

```
> **Note:** 본 spec은 `Status: implemented` (historical record). 결과는 17개 모듈로 분할됨 — 12 + dragSelect / sseConvertModal / modalDismiss / convertImage / convertWebp. 본문 "12개" 수치는 분할 시점 baseline.
```

본문 "12개" 수치는 갱신 불필요 (이력 보존 가치).

---

## D.3 — spec/plan-frontend-modularization v=29 → historical 표시

### 배경

`tasks/spec-frontend-modularization.md` lines 32·39·86·153·285, `tasks/plan-frontend-modularization.md` lines 39·80·223·251·284 모두 `?v=29`로 굳어 있다. 실제 `web/index.html:228`은 `v=41` (Phase 30 HP1까지 bump).

### 영향 파일

- `D:/file_server/tasks/spec-frontend-modularization.md`
- `D:/file_server/tasks/plan-frontend-modularization.md`

### 수정 항목

각 문서 상단 Status 헤더 바로 아래 한 줄 추가:

```
> **Note:** 버전 숫자는 historical — 현재 cache-bust 버전은 `web/index.html`이 단일 출처. 본문 `?v=29`는 분할 시점의 baseline.
```

본문 `?v=29`는 변경 불필요.

---

## D.4 — spec-hls-url-import-ssrf Status 갱신

### 배경

`tasks/spec-hls-url-import-ssrf.md` line 11 `Status: draft — 구현 시작 전`. handoff(`tasks/handoff-hls-url-import-ssrf.md`)는 `Status: merged`. 실제로 SPEC §2.6.1 본문이 흡수되었고 `internal/urlfetch/dialer.go` 등이 머지됨.

### 영향 파일

- `D:/file_server/tasks/spec-hls-url-import-ssrf.md`

### 수정 항목

```
Status: merged — SPEC §2.6.1로 흡수, 본 문서는 historical record
```

---

## D.5 — tasks/plan.md 운영 정책 한 줄

### 배경

`tasks/plan.md`(4485 라인)는 phase별 완료 기준의 historical 누적이다. 명시적 Status 헤더 부재로 신규 작업자가 archive vs 진행 상태 판단 어렵다.

### 영향 파일

- `D:/file_server/tasks/plan.md`

### 수정 항목

상단 한 줄 추가:

```
> **운영 정책:** 진행 추적은 `tasks/todo.md`. 본 문서는 phase별 완료 기준의 historical 누적이며 새 phase 추가 시에만 갱신.
```

---

## D.6 — (보너스) CLAUDE.md / SPEC §10 정합 미세 정리

### 배경

doc-reviewer가 추가로 발견한 minor 부정합:

1. **CLAUDE.md "한눈 레이아웃"에 `internal/imageconv/` 누락** — SPEC §4(line 741)는 정확. CLAUDE.md `internal/` 트리 line 44–45 사이에 추가:

   ```
   imageconv/               PNG → JPG 변환 (§2.8) — disintegration/imaging 기반
   ```

2. **SPEC §10 "0.0.1 릴리즈 범위"** — 모든 항목 `[ ]` 미체크(lines 1618–1622). README는 이미 0.0.1 릴리즈 노트 발행, todo.md Phase 24 모두 `[x]`. → §10 모든 박스 `[x]`로 갱신, 또는 §10 framing을 "이미 릴리즈된 0.0.1의 범위 회고"로 변경.

3. **`requireSameOrigin` 라우트 목록 단일 출처화** — SPEC §5.3 / CLAUDE.md `같은-출처 보호` / README §보안 세 곳에 분산. 향후 새 mutating 라우트 추가 시 세 곳 갱신 필요. → SPEC §5.3을 단일 출처로 못 박고 CLAUDE.md/README는 "상세는 SPEC §5.3" 한 줄.

### 영향 파일

- `D:/file_server/CLAUDE.md`
- `D:/file_server/SPEC.md` §10
- `D:/file_server/README.md`

### 검증

마크다운 렌더 + `grep -n "requireSameOrigin\|imageconv" SPEC.md CLAUDE.md README.md`로 일관성 확인.

---

## Group D 검증

마크다운 렌더(GitHub 또는 VSCode preview) + `git diff` 검토.

---

# 전체 검증 (모든 작업 후)

```bash
go build ./...
go vet ./...
go test -race ./...
```

수동:
- A.1·A.2: 업로드/변환 timeout/cancel 시 부분 파일 정리 확인
- A.6: 잘못된 이름 rename 시 사용자에게 보이는 메시지 확인 (server + client)
- B.3: 모든 ffmpeg 호출 경로(stream/thumb/convert/convert_webp/hls)가 동일한 `ffmpeg_missing` 에러 코드 반환
- C.1: 큰 TS 첫 재생 TTFB 단축 체감
- C.2: HLS 다운로드 wall-clock 단축
- D 전체: 마크다운 렌더 + 부정합 grep 검사

# 커밋 분할 권장

CLAUDE.md 컨벤션 (`type(scope): 메시지`):

| # | 커밋 | 작업 |
|---|---|---|
| 1 | `docs: SPEC §5/§9에 limits.go MaxBytesReader 캡 등재` | D.1 |
| 2 | `docs(spec): frontend-modularization·hls-ssrf·plan Status 정합화` | D.2 + D.3 + D.4 + D.5 |
| 3 | `docs: imageconv/SPEC §10/requireSameOrigin 단일 출처 정리` | D.6 |
| 4 | `fix(handler): upload dst.Close 에러 매핑 + 부분 파일 정리` | A.1 |
| 5 | `fix(handler): convert_image timeout 시 고아 출력 정리` | A.2 |
| 6 | `fix(handler): streamTS 1차 캐시 hit fd 이중 close 정리` | A.3 |
| 7 | `refactor(handler): writeJSON 헬퍼로 Encode 에러 일관 로깅` | A.4 |
| 8 | `style(handler): tree.go err == io.EOF → errors.Is` | A.5 |
| 9 | `fix(handler): validateName Windows 예약 문자/제어문자 차단` | A.6 (server + web/util.js) |
| 10 | `refactor(handler): runImportJob을 cancelRemaining/summarizeBatch로 분해` | A.7 |
| 11 | `feat(ffmpeg): internal/ffmpeg 패키지로 ffmpeg/ffprobe 호출 통합` | B.3 (먼저!) |
| 12 | `refactor(urlfetch): hls 서브패키지 분리` | B.2 |
| 13 | `refactor(handler): import/convert 서브패키지 분리` | B.1 |
| 14 | `docs(media): 책임 두 축 명시(types vs fsop) doc.go 추가` | B.4 |
| 15 | `perf(urlfetch): HLS materialize segment 병렬화 (errgroup, 4-동시)` | C.2 (+ copyWithCaps CAS 루프) |
| 16 | `perf(handler): import URL 배치 per-job worker pool (2-4)` | C.3 |
| 17 | `perf(handler): streamTS 캐시 미스 first-hit pipe 스트리밍` | C.1 |

권장 머지 순서:
- **D 모두** (안전, 빠른 인지 보정) →
- **A 위주** (작은 fix 묶음) →
- **B.3** (ffmpeg 통합 — 다른 작업 표면 축소) →
- **B.2 → B.1 → B.4** (서브패키지 분리) →
- **C.2 → C.3 → C.1** (성능 — 회귀 표면 점진 확대)

C.1(streamTS pipe)은 trade-off가 있어 spec-수준 검토 후 진행 권장. PR 본문에 fragmented MP4 호환성·ffmpeg `-c copy` 실패 fallback 명시 필수.

# 참고 후보 (Suggestion 등급 — 본 핸드오프 범위 외)

본 핸드오프는 Important 17건만 다룬다. Suggestion 12건+ (1회차 핸드오프와 마찬가지로 시간 여유 있을 때 묶기 좋음):

- `pathLocks` 헬퍼 추출 — `streamLocks`/`convertLocks`/`webpLocks` 3중 sync.Map 패턴 정리
- `submitOrLazy(src, dst)` 헬퍼 — `thumbPool.Submit` false 시 lazy 폴백 정책 단일화
- `upload.go:153` `map[string]interface{}` → `uploadResponse` struct
- `limits.go:18` `var maxUploadBytes` → `const` + 테스트 hook
- `browse.go:130` `lookupVideoDuration` backfill을 thumbPool로 비동기화
- `thumb/pool.go:30` `jobs` capacity `workers*4` → `workers*16`
- `upload.go:186` PNG auto-convert 디스크 왕복 1회 절약
- `thumb.IsBlankFrame` `*image.NRGBA` 직접 `Pix` 순회 fast-path
- `import_url.go:358` per-URL progress writer goroutine 제거 (handler가 직접 drain)
- `urlfetch/fetch.go:528` `progressReader.Read` `time.Now()` 호출 빈도 정리

상세는 본 세션의 `/team-review` 출력 참조 (대화 로그 상단).
