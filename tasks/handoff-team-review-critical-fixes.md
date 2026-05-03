# 팀 리뷰 후속 수정 핸드오프

> **Status: merged** — Critical 3건(작업 1·2·3) + Important 묶음(작업 4·5.1·6·7) 모두 develop에 머지. 작업 5.2(event delegation)는 5.1 적용 후 효과 대비 회귀 위험이 커서 보류 — 후속 PR 후보로 남겨둠.

## 현재 상태

- 작업 브랜치: `develop` (clean)
- 기준 브랜치: `origin/develop` (`73fd53a`)
- 새 세션에서 이어서 진행
- 코드 변경 없음 — 본 문서가 작업 시작점

## 컨텍스트

`/team-review` 결과 (Architecture · Code Quality · Performance · Documentation 4개 리뷰어 병렬 실행) Critical 3건 + Important 다수가 식별되었다. 본 핸드오프는 머지 차단(Critical) + 다음 마일스톤 안에 정리할 가치 있는 항목(Important)을 모두 작업 단위로 정리한다. Suggestion 등급은 본 문서 끝의 "참고 후보"에 짧게 정리.

리뷰 종합 verdict: **REQUEST CHANGES** — 아키텍처 견고, 보안 invariant 준수, 다만 머지 전 3건 정리 필요.

## 작업 우선순위

| 작업 | 등급 | 차단성 | 권장 순서 |
|---|---|---|---|
| 1 — Upload / JSON body cap | Critical | 머지 차단 | 1 (선) |
| 2 — README 갱신 | Critical (doc) | 머지 차단 | 2 |
| 3 — todo.md 체크 갱신 | Critical (doc) | 머지 차단 | 3 |
| 4 — 백엔드 hardening 묶음 | Important | 다음 마일스톤 | 4 |
| 5 — 프론트엔드 perf | Important | 다음 마일스톤 | 5 |
| 6 — browse.go N+1 thumb stat | Important | 다음 마일스톤 | 6 |
| 7 — 문서 부분 부정합 정리 | Important (doc) | 다음 마일스톤 | 7 |

작업 1·2·3은 단일 PR로 묶어도 무방. 작업 4·5·6은 각각 분리 PR 권장 (회귀 표면 다름). 작업 7은 코드 PR과 끼워 묶기 좋음.

## 반드시 먼저 읽을 문서

1. `CLAUDE.md` — 한글 규칙, 같은-출처 보호, 원자성, settings 패턴 등
2. `SPEC.md` §2.7 (settings) / §5 (API) / §10 (0.0.1 릴리즈 범위)
3. `tasks/todo.md` — Phase 23/24/26/27/30 (체크 누락분 확인)
4. 이 문서

## 작업 1 — Upload / JSON body 무한 입력 방어 (코드)

### 배경

업로드 본문에 size cap이 없어 stuck/악의적 클라이언트가 디스크를 채울 수 있다. JSON 핸들러 두 곳도 `io.ReadAll(r.Body)`로 streaming 캡 없이 메모리에 로드.

리뷰 인용:
> [code-quality + perf] **multipart `io.Copy(dst, part)`가 무한 스트리밍** — 단일 사용자 LAN이라도 stuck client 한 명이 디스크 fill 가능.

### 영향 파일

- `D:/file_server/internal/handler/upload.go:39` — `r.MultipartReader()` 호출 직전에 cap 적용
- `D:/file_server/internal/handler/files.go:38` — `patchFile`의 `io.ReadAll(r.Body)`
- `D:/file_server/internal/handler/folders.go:36` — `patchFolder`의 `io.ReadAll(r.Body)`

### 권장 구현

`internal/handler/names.go` 또는 신규 `internal/handler/limits.go`에 공유 const 정의:

```go
// maxUploadBytes는 multipart 업로드 한 건의 절대 상한. 단일 사용자 LAN 환경에서
// 정상 미디어 한 개의 한계 — 이보다 큰 파일은 거의 항상 stuck/악의 클라이언트.
// settings로 노출하는 것은 추후 phase에서 검토 (현재 SPEC §2.7은 URL import만
// 사용자 노출).
const maxUploadBytes = 100 << 30 // 100 GiB

// maxJSONBodyBytes는 PATCH /api/file·/api/folder의 JSON body 상한. 정상
// 페이로드는 수백 바이트.
const maxJSONBodyBytes = 64 << 10 // 64 KiB
```

각 핸들러 진입부:

```go
// upload.go handleUpload — r.MultipartReader() 직전
r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)

// files.go patchFile / folders.go patchFolder — io.ReadAll 직전
r.Body = http.MaxBytesReader(w, r.Body, maxJSONBodyBytes)
```

업로드 측은 `io.Copy` 도중 `*http.MaxBytesError`를 받으면 `413 too_large`로 매핑. JSON 측은 `read body failed`로 자연 흡수되거나 명시적으로 413 분기를 추가.

### 에러 코드 컨벤션

CLAUDE.md "에러 응답" 섹션: 짧은 ASCII 식별자. `too_large`는 이미 url import에서 쓰이는 코드 — 재사용 권장.

### 테스트

- `internal/handler/upload_test.go` (없으면 신규) — `maxUploadBytes + 1` 바이트 multipart로 413 + `too_large` 검증.
- `internal/handler/files_test.go` patchFile 케이스에 `maxJSONBodyBytes + 1` 바이트 JSON 본문 추가.
- `internal/handler/folders_test.go` 또는 기존 patch 테스트에 동형 케이스.

### 수동 검증

```bash
# upload — 100 GiB는 부담스러우니 const를 임시로 1 MiB로 낮춰 검증 권장
dd if=/dev/zero bs=1M count=2 | curl -X POST -F "file=@-" http://localhost:8080/api/upload?path=/

# patchFile / patchFolder
head -c 70000 /dev/urandom | base64 | curl -X PATCH -H "Origin: http://localhost:8080" \
  --data-binary @- http://localhost:8080/api/file?path=/foo.txt
```

## 작업 2 — README.md 갱신 (문서)

### 배경

Phase 23 이후 features / 구조 / API 표가 README와 어긋나 있다 (doc-reviewer 검토). 사용자(=본인)가 가장 먼저 보는 문서인데 정보 격차가 크다.

### 영향 파일

- `D:/file_server/README.md`

### 수정 항목

1. **`주요 기능` 섹션** — 다음 항목 추가:
   - 다중 파일 체크박스 선택 + 사이드바 폴더로 일괄 이동 (Phase 22 — 일부 이미 있음, 표현 보강)
   - rubber-band 영역 선택 (SPEC §2.5.4, Phase 27)
   - 라이트박스 내 🗑 / Delete 키 삭제 (SPEC §2.5.5, Phase 28)
   - 움짤 카드 hover/IO 자동재생 (SPEC §2.5.6, Phase 30)
   - PNG → JPG 자동/수동 변환 (SPEC §2.8, Phase 25/26)
   - 움짤 → animated WebP 변환 (SPEC §2.9, Phase 29)
   - settings에 PNG 자동 변환 ON/OFF 토글 (Phase 25)

2. **`API 요약` 표** — 누락 endpoint 추가:
   - `POST /api/convert-image` — PNG → JPG 동기 변환 (Phase 25)
   - `POST /api/convert-webp` — 움짤 → animated WebP SSE (Phase 29)
   - `PATCH /api/file?path=` 설명을 `rename(`{name}`) 또는 이동(`{to}`)`로 (현재는 rename만 표기, 폴더와 비대칭)

3. **`구조` 섹션** — `web/` 라인 갱신:
   ```
   web/              index.html + style.css + main.js + 16개 ES module
   ```
   `internal/` 트리에 `imageconv/` 추가:
   ```
   imageconv/      PNG → JPG 변환 (흰 배경 합성, atomic write)
   ```

4. **`움짤 필터 설명`** (`주요 기능 → 탐색 UX`) — 현재 "GIF + 짧고 작은 동영상"을 **"GIF/WebP + 짧고 작은 동영상"**으로 (Phase 29 CW5에서 WebP 무조건 움짤 분류 결정).

5. **`기술 스택`** — `libwebp-tools` (alpine apk: webpmux + dwebp) 한 줄 추가. animated WebP 첫 프레임 추출에 의존 (Phase 30 HP4).

### 검증

- 마크다운 렌더 (GitHub 또는 VSCode preview)로 표 정렬 확인.
- `SPEC.md §2.5/§2.8/§2.9`와 features 항목 1:1 대조.

## 작업 3 — tasks/todo.md 체크 갱신 (문서)

### 배경

doc-reviewer가 발견한 가장 큰 부채: 머지된 작업이 `[ ]` 상태로 남아 다음 작업자/세션이 "아직 안 됨"으로 오판한다.

### 영향 파일

- `D:/file_server/tasks/todo.md`

### 체크해야 할 항목

git 로그와 코드 상태로 확인된 완료 작업:

- **Phase 23 FM-0 ~ FM-9** (lines 174–183): 17개 ES 모듈 분할 완료. 실제 결과는 spec 목표(12개)보다 5개 더 분할됨 — `dragSelect.js`, `sseConvertModal.js`, `modalDismiss.js`, `convertImage.js`, `convertWebp.js`. 모두 `[x]`로.
- **Phase 24 F1 ~ F5** (lines 191–195): 폴더 이동 + 0.0.1 릴리즈. 코드/`README.md`의 0.0.1 릴리즈 노트가 증거. 모두 `[x]`로.
- **Phase 26 PS-1, PS-2** (lines 211–212): selection-aware PNG 변환. 커밋 `301f04b feat(web): selection-aware PNG 일괄 변환 (Phase 26 PS-2)` 존재. PS-3는 이미 `[x]`. PS-1/PS-2 `[x]`로.
- **Phase 27 DS-1, DS-2** (lines 219–220): rubber-band 영역 선택. 커밋 `353a900 feat(web): Rubber-band 영역 선택 (Phase 27 DS-2)` 존재. DS-3는 이미 `[x]`. DS-1/DS-2 `[x]`로.
- **Phase 30 HP0, HP1, HP2** (lines 248–250): 움짤 hover playback. 커밋 `701ef88 feat(clip-hover-playback): hover/IO src 토글 (HP1)`, `b00ebfc test(clip-hover-playback): chromedp e2e (HP2)`, `3087241 docs(clip-hover-playback): SPEC §2.5.6 + plan/todo Phase 30 (HP0)` 존재. HP3/HP4는 이미 `[x]`. HP0/HP1/HP2 `[x]`로.

### 검증

```bash
# 각 phase의 [ ] 개수가 줄었는지 확인
grep -c "^\- \[ \]" D:/file_server/tasks/todo.md
```

Phase 18 `H-SYMLINK`(line 133)와 Phase 5 `T15/T16`(lines 27–28)은 의도적 미완료(record-only) — 건드리지 말 것.

## 검증 (전체 작업 후)

```bash
go build ./...
go vet ./...
go test ./internal/handler -run "TestUpload|TestPatchFile|TestPatchFolder" -v
go test ./...
```

수동 검증은 작업 1의 "수동 검증" 절 + README 마크다운 렌더 + todo.md `git diff` 검토.

## 커밋 분할 권장

CLAUDE.md 컨벤션:

1. `fix(handler): upload/JSON body 무한 입력 방어 (MaxBytesReader)` — 작업 1
2. `docs: README features/API/구조를 Phase 23-30 반영` — 작업 2
3. `docs(todo): Phase 23/24/26/27/30 머지 완료분 체크 갱신` — 작업 3

작업 1만 PR 한 줄(`fix:`), 나머지 둘은 docs-only로 묶어도 무방. 단일 사용자 운영이라 PR 단위는 자유.

## 작업 4 — 백엔드 hardening 묶음 (코드, Important)

### 배경

코드 품질 리뷰가 발견한 잠재 hang / TOCTOU / 잘못된 status code / panic 처리 누락 / 로그 노이즈 5건. 각각 단독으로는 작지만 모두 운영 안정성 영역이라 한 번에 정리하면 효율적.

### 4.1 — `extractFrame` ffmpeg timeout 부재

**파일:** `D:/file_server/internal/thumb/thumb.go:281`

손상된 동영상 입력 시 ffmpeg가 영구 hang → `thumb.Pool` 워커 goroutine 영구 블로킹 → 풀 고갈. 같은 파일의 `videoDuration`(line 265)은 이미 `probeTimeout`을 쓰는데 `extractFrame`만 빠졌다.

**픽스:**

```go
// extractFrame 시그니처에 ctx 추가하거나 함수 안에서 derive
func extractFrame(srcPath, dstPath string, atSec float64) error {
    ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
    defer cancel()
    cmd := exec.CommandContext(ctx, "ffmpeg", ...)
    // ...
}
```

호출자(`GenerateFromVideo` 안 3회, 50%/25%/75%)는 시그니처 그대로 두거나 ctx를 외부에서 주입. `probeTimeout`은 같은 파일 안에 이미 정의되어 있다.

**테스트:**
- `internal/thumb/thumb_test.go`에 hung-binary 시뮬레이션 어렵다 — 짧은 timeout(예: 100ms)로 const를 임시 override하는 테스트 helper로 대체 가능. 또는 `t.Skip` 가드로 ffmpeg 부재 시 통과.

### 4.2 — `createFolder` TOCTOU

**파일:** `D:/file_server/internal/handler/folders.go:235-244`

`os.Stat(targetAbs)` → `os.Mkdir(targetAbs, 0755)` 사이에 동시 creator가 끼면 둘 다 통과하거나 둘 다 실패한다. `os.Mkdir` 자체가 EEXIST를 반환하므로 Stat은 redundant + 위험.

**픽스:**

```go
// 기존 Stat 블록 제거. errors / io/fs import 추가.
if err := os.Mkdir(targetAbs, 0755); err != nil {
    if errors.Is(err, fs.ErrExist) {
        writeError(w, r, http.StatusConflict, "already exists", nil)
        return
    }
    writeError(w, r, http.StatusInternalServerError, "mkdir failed", err)
    return
}
```

**테스트:** `folders_test.go`에 fan-out 동시 createFolder 테스트 추가 (20 goroutines, 1개만 성공 + 19개 409 검증 — `TestConcurrentUploadSameNameNoClobber` 패턴 참고).

### 4.3 — cross_device → 500 status code

**파일:** `D:/file_server/internal/handler/folders.go:111`, `folders.go:184` 부근 (실제 라인은 동일 패턴 두 곳)

`media.MoveDir`가 `ErrCrossDevice`를 반환하면 현재 `500 cross_device`. 단일 볼륨 가정 미충족은 논리적으로 precondition — 5xx는 server malfunction을 의미해서 운영 도구가 오해한다.

**픽스:** `http.StatusBadRequest` (또는 `StatusUnprocessableEntity` 422). 코드 식별자 `cross_device` 유지.

```go
case errors.Is(err, media.ErrCrossDevice):
    writeError(w, r, http.StatusBadRequest, "cross_device", nil)  // err은 nil로 — 5xx 아니라 client 정보용
```

**테스트:** 직접 트리거가 어렵다(다른 볼륨 마운트 필요) — `media.MoveDir`를 mockable로 만들거나, 기존 `media/move_test.go`의 EXDEV 시뮬레이션이 있는지 확인 후 핸들러 단에선 단위 테스트로 status mapping만 검증.

### 4.4 — 워커 panic recovery double-publish

**파일:** `D:/file_server/internal/handler/import_url.go:215-230` (worker 함수의 defer recover 블록)

`runImportJob` 워커가 정상 종료할 때 line 324에서 `summarizeURLs` → `Publish(summary)`, line 326-333에서 `SetStatus(terminal)`. 사이에서 panic이 발생하면 defer recover 블록의 `summarizeURLs` 재호출이 새 summary를 publish해서 클라이언트가 중복 summary를 받을 수 있다.

**픽스 옵션 A** (간단): `SetStatus`를 `Publish` 직전으로 옮긴다. terminal 상태 진입 후의 panic은 어차피 ack 후라 무해.

**픽스 옵션 B** (방어적): `Job` 구조체에 `summary *sseSummary` mutex-protected 필드 추가. `summarizeURLs`가 `j.summary == nil`일 때만 publish.

옵션 A 권장. 코드 변경 한 줄.

**테스트:** `import_url_test.go`에 `TestImportURL_WorkerPanicDuringSummary_NoDoublePublish` 추가. 워커가 의도적으로 panic하도록 fetcher mock 사용.

### 4.5 — `writeError` 로그 레벨 + Encode 결과 무시

**파일:** `D:/file_server/internal/handler/handler.go:178-191`

현재 `if code >= 500 || err != nil`이라 4xx + parse error도 `slog.Error`로 찍혀 routine client 실수에 로그가 스팸. 또 `json.Encode` 결과를 무시 → 클라이언트 mid-error 끊김 진단 불가.

**픽스:**

```go
func writeError(w http.ResponseWriter, r *http.Request, code int, msg string, err error) {
    switch {
    case code >= 500:
        slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "status", code, "msg", msg, "err", err)
    case err != nil:
        slog.Warn("request rejected", "method", r.Method, "path", r.URL.Path, "status", code, "msg", msg, "err", err)
    }
    w.Header().Set("Content-Type", "application/json")
    w.WriteHeader(code)
    if encErr := json.NewEncoder(w).Encode(map[string]string{"error": msg}); encErr != nil {
        slog.Debug("error response encode failed", "err", encErr)
    }
}
```

**테스트:** 기존 핸들러 단위 테스트가 status·body 검증으로 통과하는지만 확인 (로그 레벨은 비기능).

### 4.6 — ffmpeg / webpmux / dwebp stderr 무캡처 (선택)

**파일:** `D:/file_server/internal/thumb/thumb.go:96-105`, `internal/thumb/pool.go:39-50`

`webpmux extract: %w` 같은 wrap만 하고 stderr는 버린다. 만성 libwebp 실패 시 운영자가 원인 파악 불가. `internal/convert/convert.go`의 `FFmpegExitError` 패턴(stderr → bytes.Buffer 캡처)을 참고.

**픽스:**

```go
var stderr bytes.Buffer
cmd := exec.CommandContext(ctx, "webpmux", ...)
cmd.Stderr = &stderr
if err := cmd.Run(); err != nil {
    return fmt.Errorf("webpmux extract: %w (stderr: %s)", err, stderr.String())
}
```

또는 `convert.FFmpegExitError`를 `internal/thumb`로 옮기거나 `internal/exec` 같은 공통 helper로 추출 — 본 작업 범위 밖, 일단 인라인 캡처로.

**Pool 측:** `pool.go:39-50` 워커가 에러를 swallow 한다. rate-limited `slog.Warn`으로 변환 (예: `sync.Map[src]time.Time`로 src 단위 1분 throttle, 또는 단순 디버그 로그).

### 4.7 — 검증

```bash
go build ./...
go vet ./...
go test ./internal/handler -run "TestCreateFolder|TestMoveFolder|TestImportURL" -v
go test ./internal/thumb -v
go test ./...
```

## 작업 5 — 프론트엔드 perf (코드, Important)

### 배경

Performance 리뷰의 hot path 분석 결과. browse 디렉터리에 카드가 200+ 개일 때 체크박스 토글마다 jank가 발생한다. 단일 사용자 LAN이라도 본인이 매일 쓰는 hot path.

### 5.1 — `setSelected → renderView()` 전체 재구축 제거

**파일:** `D:/file_server/web/browse.js:358` 부근 `setSelected` 함수

현재 체크박스 한 개 토글이 `renderView()`를 호출해서 N개 카드 모두 teardown + 재생성한다. GIF/WebP 자동재생도 함께 리셋된다.

**픽스:** selection 토글 시 다음만 갱신:
- 영향 받은 카드 element의 `.selected` class + 내부 checkbox `checked` 상태
- `renderSelectionControls()` (전체 선택 UI)
- `updateConvertImageAllBtn()` / `updateConvertPNGAllBtn()` / `updateConvertWebpAllBtn()` 셋

`renderView()`는 sort/filter/search/browse path 변경에만 호출.

**구현 단서:**
- `setSelected(path, on)`이 카드 DOM ref를 알 방법이 없다면 `Map<path, HTMLElement>`를 `renderFileList` 시점에 채워 둔다 (state.js 또는 browse.js 모듈 스코프).
- 또는 `$.fileList.querySelector(`[data-path="${CSS.escape(path)}"]`)`로 즉석 lookup (CSS escape 필수).

### 5.2 — `renderFileList` event delegation

**파일:** `D:/file_server/web/browse.js:297` 부근 `renderFileList`

500개 카드 × 카드당 4–6개 `addEventListener`로 listener가 2000+ 개 매번 재할당. event delegation으로 `$.fileList` 한 곳에 위임.

**픽스 패턴:**

```js
// renderFileList 안에서 카드별 addEventListener 모두 제거.
// renderFileList 호출 후 한 번만 (idempotent guard 필요):
if (!$.fileList.dataset.delegated) {
    $.fileList.addEventListener('click', (e) => {
        const card = e.target.closest('[data-path]');
        if (!card) return;
        const action = e.target.closest('[data-action]')?.dataset.action;
        switch (action) {
            case 'lightbox': openLightbox(card.dataset.path); break;
            case 'rename':   openRenameModal(card.dataset.path); break;
            case 'delete':   deleteFile(card.dataset.path); break;
            // ...
        }
    });
    $.fileList.dataset.delegated = '1';
}
```

카드 build 함수들(`buildVideoGrid`, `buildImageGrid`, `buildTable`)은 button에 `data-action`만 붙이면 됨. drag handler는 별도 dragstart/dragend라 위임이 까다로우니 그대로 두거나 별도 phase.

### 5.3 — 검증

```bash
# 단위 테스트는 chromedp e2e (web_*_e2e_test.go)가 회귀 잡아준다
go test ./internal/handler -run "Test.*E2E" -v
```

수동: 200+ 카드 디렉터리에서 체크박스 클릭 시 GIF/WebP 자동재생 유지되는지 + 클릭 → 선택 표시 latency 1프레임 안에 처리되는지.

## 작업 6 — `browse.go` N+1 thumb stat (코드, Important)

### 배경

`handleBrowse`가 entry마다 `os.Stat(thumbPath)`로 사이드카 존재 여부 확인 → 1000개 디렉터리 = 1000 syscall. 핵심 hot path.

### 영향 파일

- `D:/file_server/internal/handler/browse.go` — 사이드카 stat 루프

### 픽스

루프 진입 전 `.thumb` 디렉터리를 한 번 `os.ReadDir`로 읽어 `map[string]bool` 구축. 카드별 lookup은 O(1).

```go
thumbSet := make(map[string]bool)
if entries, err := os.ReadDir(filepath.Join(absDir, ".thumb")); err == nil {
    for _, e := range entries {
        thumbSet[e.Name()] = true
    }
}
// 이후 루프에서 thumb_available은 thumbSet[name+".jpg"]로 체크.
```

`.thumb`이 없으면 `os.ReadDir`가 not exist 에러 — 빈 map 그대로 사용 (모든 entry `thumb_available: false`로 자연 처리).

duration 사이드카(`.dur`)도 같은 map에서 lookup 가능 (확장자만 다름) — 같은 ReadDir 결과 재사용.

### 테스트

`browse_test.go`에 `TestHandleBrowse_500Entries_SingleStatPerThumbDir` 같은 syscall count 단언은 어렵지만, 기존 `thumb_available` 검증 테스트가 통과하면 동작 회귀 없음. perf 검증은 `BenchmarkHandleBrowse`(신규) 또는 `strace -c` 수동.

### 주의

CLAUDE.md "browse 핸들러에 backfill budget(=1)이 걸려 있어 폴더 하나 조회에 ffprobe가 폭주하지 않게 방어 중. 이 상한을 유지." — 본 작업은 `os.Stat`만 줄이는 거라 ffprobe budget과 무관. 그래도 PR 본문에 "budget=1 invariant 유지" 명시.

## 작업 7 — 문서 부분 부정합 정리 (Important)

### 배경

doc-reviewer가 발견한 SPEC.md / CLAUDE.md / spec-frontend-modularization.md의 부분 부정합. 코드 변경 없이 갱신만.

### 7.1 — `SPEC.md §4` web/ 트리 + 본문 `app.js` 참조

**파일:** `D:/file_server/SPEC.md`

- §4 Project Structure의 `web/` 라인 (line 744-747 부근): 현재 `app.js`. → `index.html / style.css / main.js / state.js / dom.js / util.js / router.js / browse.js / fileOps.js / tree.js / settings.js / urlImport.js / urlImportJobs.js / convert.js / convertImage.js / convertWebp.js / sseConvertModal.js / modalDismiss.js / dragSelect.js`로 갱신.
- 본문 내 `app.js` 직접 참조 4곳 (검색: `grep -n "app\.js" SPEC.md`):
  - §2.3.2 "포맷팅은 클라이언트(`app.js`)" → `web/util.js`(formatDuration)
  - §2.5.1 "클라이언트(`web/app.js`, `web/style.css`)만 수정" → `web/main.js + 도메인 모듈`
  - §2.9 "TS→MP4의 `convert.js`를 참고" — 모듈 경로 그대로 (이건 정확)
  - 그 외 grep 결과에 따라

### 7.2 — `CLAUDE.md` 누락 항목

**파일:** `D:/file_server/CLAUDE.md`

3개 항목:

1. **convert_webp.go 설명** (line 37): 현재 "동영상 → WebP 움짤 변환 SSE". GIF 입력도 받음 → "**움짤(GIF/짧은 동영상) → animated WebP 변환 SSE**".
2. **Same-origin 라우트 목록** (line 69 부근): `/api/convert`만 있고 `/api/convert-image`, `/api/convert-webp` 누락. 둘 다 추가.
3. **per-path 뮤텍스 목록** (line 77 부근): "streamLocks·convertLocks 두 개" → "**streamLocks·convertLocks·webpLocks 세 개**". `handler.go:28`에 webpLocks 정의 확인.

### 7.3 — `tasks/spec-frontend-modularization.md` Status

**파일:** `D:/file_server/tasks/spec-frontend-modularization.md:5` 부근

현재 `Status: draft — 사용자 승인 대기`. 실제로는 implemented + 17개로 분할(spec 목표 12개 초과). → `Status: implemented (12 + 5: convertImage / convertWebp / sseConvertModal / modalDismiss / dragSelect)`.

### 7.4 — `tasks/spec-multi-file-move-ui.md` / `tasks/spec-tree-full-visible.md` 모듈 경로

각 spec에 `web/app.js` 참조 잔존 (doc-reviewer 검출). Status 헤더에 `merged` 표기 + 본문 모듈 경로를 분할 후 위치로 갱신. 이미 머지된 작업이라 best-effort — 새 작업자가 spec 따라 못 찾는 경우만 우선 정리.

### 7.5 — `tasks/handoff-hls-url-import-ssrf.md` Outcome

이미 머지된 작업의 핸드오프 문서. 상단에 `Status: merged` + 한 줄 Outcome 추가. → 현재 핸드오프(본 문서)도 작업 완료 후 동일 패턴 적용 권장.

### 검증

마크다운 렌더 + `grep -n "app\.js" SPEC.md CLAUDE.md README.md` 결과가 의도한 잔존(예: 모듈 이름 `app.js`로 끝나는 외부 라이브러리 등)만 남는지 확인.

## 전체 검증 (모든 작업 후)

```bash
go build ./...
go vet ./...
go test ./...
```

수동:
- 작업 1: upload size cap 트리거 (`maxUploadBytes` 임시 1 MiB로 낮춰 검증)
- 작업 5: 200+ 카드 디렉터리 체크박스 토글 jank 사라졌는지
- 작업 6: 큰 디렉터리 browse 응답 latency 비교 (전/후)
- 작업 2·3·7: 마크다운 렌더 + `git diff` 검토

## 커밋 분할 권장

CLAUDE.md 컨벤션 (`type(scope): 메시지`):

| # | 커밋 | 작업 |
|---|---|---|
| 1 | `fix(handler): upload/JSON body 무한 입력 방어 (MaxBytesReader)` | 1 |
| 2 | `docs: README features/API/구조를 Phase 23-30 반영` | 2 |
| 3 | `docs(todo): Phase 23/24/26/27/30 머지 완료분 체크 갱신` | 3 |
| 4 | `fix(thumb): extractFrame ffmpeg 호출에 timeout 부착` | 4.1 |
| 5 | `fix(handler): createFolder TOCTOU + cross_device 4xx 매핑` | 4.2 + 4.3 |
| 6 | `fix(handler): import job 워커 panic recovery double-publish 방지` | 4.4 |
| 7 | `refactor(handler): writeError 로그 레벨 분리 + Encode 결과 캡처` | 4.5 |
| 8 | `fix(thumb): ffmpeg/webpmux/dwebp stderr 캡처 + pool 에러 로그` | 4.6 |
| 9 | `perf(web): selection 토글 시 영향 카드만 갱신 (renderView 우회)` | 5.1 |
| 10 | `perf(web): renderFileList event delegation으로 listener 재할당 제거` | 5.2 |
| 11 | `perf(handler): browse 사이드카 stat을 ReadDir 1회 + map lookup으로` | 6 |
| 12 | `docs: SPEC/CLAUDE의 모듈 경로·convert_webp·webpLocks 정합화` | 7 |

작업 4·5는 sub-항목별 분리 PR도 허용. 작업 7은 작업 2·3과 묶어서 단일 docs PR로 합치는 것도 무방.

## 참고 후보 (Suggestion 등급 — 본 핸드오프 범위 외)

리뷰 종합 보고서에서 비차단 개선으로 표시된 것. 시간 여유 있을 때 한 PR로 묶기 좋다.

- `handler/stream.go streamTS` ↔ `convert/convert.go RemuxTSToMP4` 동일 ffmpeg argv 중복 → 공유 helper.
- `streamLocks/convertLocks/webpLocks` 동형 sync.Map 패턴 3회 → `keyedLocks` 1개로.
- `Pool.Shutdown` / `Handler.Close`에 `sync.Once` idempotence guard.
- 사이드카 경로 helper `thumbSidecarPath(parentDir, baseName)` 추출 — `files.go:96`, `convert.go:218`, `convert_webp.go:255`, `convert_image.go:214` 동형 코드 일원화.
- `import_url_jobs.go:84` 경로 파싱 trailing-slash 명시.
- `convert.go classifyConvertError` parent ctx의 `DeadlineExceeded` 분기 추가.
- `browse.go backfillBudget := 1` → 패키지 const화.
- `redactURL`의 `q.Set` 다중 값 손실 케이스.
- `thumb.NewPool(runtime.NumCPU())` → `min(NumCPU, 4)` 또는 settings 노출.
- `web/browse.js sort` — `Intl.Collator` 모듈 스코프 캐싱.
- `IsBlankFrame`에서 `*image.NRGBA`/`*image.YCbCr` 타입 스위치 fast-path.

상세는 본 세션의 `/team-review` 출력 참조 (대화 로그 상단).
