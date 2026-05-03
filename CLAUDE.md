# CLAUDE.md

Guidance for Claude Code and other AI agents working in this repo. Not product documentation — that is in [README.md](README.md) and [SPEC.md](SPEC.md).

## 언어 규칙

- 모든 소통은 한글로 진행한다.
- 코드를 제외한 모든 기록은 한글로 남긴다 — 주석, `.md` 파일, 커밋 로그, PR 본문 등.
- 코드 식별자(함수·변수·타입명)와 라이브러리/도구 이름은 영문 그대로 둔다.

## Project at a glance

- **개인용 단일 사용자** 로컬 네트워크 미디어 서버. 인증 없음, 동시성 가정 느슨.
- **Go 1.26 + net/http stdlib** (라우터 프레임워크 없음 — `http.ServeMux` 직접 사용). module path는 도메인 prefix 없는 `file_server` — 외부 import 대상 아님(단일 바이너리 가정), 새 패키지 import는 `file_server/internal/...` 형태.
- **Frontend:** vanilla HTML/CSS/JS — 진입점 `web/main.js`(ES module), 17개 모듈로 분해되어 있다. 번들러·외부 의존성 없음 — 수정 후 브라우저 새로고침만으로 확인.
- **외부 도구:** `ffmpeg` / `ffprobe` (썸네일·TS 스트림·TS→MP4·HLS import 필수). 테스트 일부가 바이너리 요구.

## 한눈 레이아웃

```
cmd/server/main.go         진입점 (설정 로드 + handler.Register + graceful shutdown)
internal/
  handler/                 HTTP 엔드포인트 — 각 파일이 라우트군 하나
    handler.go             Handler 구조체, Register, writeError, requireSameOrigin
    sse.go                 SSE bootstrap 헬퍼 (assertFlusher / writeSSEHeaders / sseEmitter)
    names.go               file/folder/upload 공유 유틸 (validateName, atomicRenameFile, createUniqueFile, ...)
    browse.go tree.go      디렉터리 조회
    files.go               PATCH/DELETE /api/file — 파일 rename/delete/move + 사이드카 정리
    folders.go             POST/PATCH/DELETE /api/folder — 폴더 create/rename/delete/move
    upload.go              POST /api/upload + PNG → JPG 자동 변환 헬퍼
    stream.go              Range 스트리밍 + TS 실시간 remux (`.cache/streams/`)
    thumb.go               /api/thumb (lazy 생성 포함)
    import_url.go          URL/HLS 다운로드 SSE 핸들러
    import_url_jobs.go     /api/import-url/jobs — 백그라운드 잡 list/cancel/dismiss/SSE 재구독
    convert.go             TS→MP4 영구 변환 SSE 핸들러
    convert_image.go       /api/convert-image — 이미지 포맷 변환
    convert_webp.go        /api/convert-webp — 움짤(GIF/짧은 동영상) → animated WebP 변환 SSE
    settings.go            GET/PATCH /api/settings
  media/                   타입 판별, MIME, SafePath, MoveFile (path traversal 차단)
  thumb/                   이미지·동영상 썸네일 + duration 사이드카, 워커 풀
  urlfetch/                HTTP 다운로드 + HLS materialize/remux (SSE 용 Callbacks hook)
  convert/                 TS→MP4 ffmpeg remux runner + 움짤→WebP 인코딩(§2.9)
  imageconv/               PNG → JPG 변환 (§2.8) — disintegration/imaging 기반, 흰 배경 합성
  settings/                JSON 영속화 + snapshot/update
  importjob/               URL import 백그라운드 잡 registry — id 발급/상태 추적/취소
web/                       index.html + style.css + 17개 ES module (main.js 진입)
tasks/                     plan/spec/handoff/todo 개발 메모 (작업 단위 마크다운)
```

## 자주 쓰는 명령

```bash
go test ./...              # 전체 테스트 (ffmpeg/ffprobe 필요한 케이스는 없으면 skip)
go test ./internal/handler -run TestFoo -v
go build ./...             # 전 패키지 빌드 검증
go vet ./...               # 정적 분석 (CI 미연결 — 수동 실행 권장)
go run ./cmd/server        # 로컬 실행 (DATA_DIR / WEB_DIR 환경변수)
docker compose up -d       # 컨테이너로 실행
```

포매팅/린트 별도 설정 없음 — 표준 `gofmt` 준수.

## 꼭 지켜야 할 규칙

### 경로
- 사용자 입력 경로는 **항상 `media.SafePath(h.dataDir, rel)`** 를 거쳐서 절대 경로로 바꾼다. 직접 `filepath.Join(dataDir, rel)` 금지 — path traversal 차단이 여기에 있다.
- API는 클라이언트에 `/`-prefixed slash 경로를 돌려주고(`filepath.ToSlash`), 내부는 OS 경로로 다룬다.

### 같은-출처 보호
- 변경 작업 핸들러는 **`requireSameOrigin`** 으로 감싸서 등록 — 래핑 대상 라우트 전체 목록과 읽기 전용 예외는 SPEC §5.3 단일 출처. 새 mutating 라우트를 추가하면 SPEC §5.3 목록과 `Register`의 wrap 호출을 같이 갱신한다.

### 원자성
- 새 파일 쓰기는 **temp 파일 → fsync(있으면) → `os.Rename`** 패턴. 예시: `settings.writeFile`, `convert.RemuxTSToMP4`, `urlfetch.Fetch`.
- `createUniqueFile`는 `O_CREATE|O_EXCL`로 동명 업로드의 TOCTOU 방지. 수동 Stat→Create 대체 금지.
- 파일 rename은 `atomicRenameFile`(os.Link + os.Remove). 대소문자만 다른 rename은 플레인 `os.Rename` 폴백 — 대소문자 무시 FS에서 Link가 EEXIST로 실패하기 때문.

### 동시성
- `Handler`에 `streamLocks`·`convertLocks`·`webpLocks` `sync.Map`이 있어 **per-path 뮤텍스**로 같은 소스의 ffmpeg 호출을 직렬화. 새로운 ffmpeg 경로를 추가하면 비슷한 패턴으로 보호.
- 썸네일 생성은 `thumb.Pool`(CPU 수만큼 워커). `Submit`이 false 반환하면 `/api/thumb`의 lazy 경로가 대신 생성 — 실패 시 서버를 세우지 말고 그쪽으로 위임.
- SSE 쓰기는 핸들러 goroutine 하나에서만. fetcher/converter는 **채널로 이벤트만 넘기고** flush는 핸들러가 수행.

### Settings 패턴
- URL import/HLS/convert 모두 요청 **시작 시점**에 `h.settingsSnapshot()`로 값을 고정해서 다운스트림에 전달. 런타임에 PATCH가 들어와도 진행 중 요청은 원래 값 유지. 새 흐름을 추가하면 같은 규칙을 따른다.
- `settings.Store`가 nil이면 `settings.Default()`로 폴백 — 테스트 편의 목적이므로 프로덕션 경로에서 nil 전달 금지.

### 섬네일 사이드카
- 이미지/동영상 원본 파일 옆 `.thumb/{name}.jpg` (+ 동영상은 `.thumb/{name}.jpg.dur`). 파일 rename/delete 시 사이드카도 함께 정리해야 한다 (`renameThumbSidecars`, delete 핸들러 참고).
- 동영상 duration은 `thumb.LookupDuration` 캐시 → 없으면 `thumb.BackfillDuration`으로 ffprobe 1회. browse 핸들러에 **backfill budget(=1)** 이 걸려 있어 폴더 하나 조회에 ffprobe가 폭주하지 않게 방어 중. 이 상한을 유지.

### 진행 이벤트 throttle
- `urlfetch`, `urlfetch/hls`, `convert` 모두 동일 상수: **1 MiB 또는 250 ms** 중 먼저 도달한 쪽에서 progress emit. 새 스트리밍 작업이 생기면 같은 값을 맞춘다 (`progressByteThreshold`, `progressTimeThreshold`).

### 에러 응답
- `writeError(w, r, code, msg, err)` 하나로 통일. 5xx면 `slog.Error`, 4xx + err != nil이면 `slog.Warn`(client 실수 진단용), 그 외는 무로깅. 직접 `http.Error` 쓰지 말 것.
- 클라이언트에 노출되는 에러 코드는 짧은 ASCII 식별자(`out_of_range`, `too_large`, `unsupported_content_type`, `ffmpeg_error`, `cross_device`...). 원문 stderr나 내부 경로는 응답 본문으로 내보내지 말 것.

### Rename 확장자
- 파일 rename은 **원본 확장자 고정**. 서버의 `fileExtension`(dotfile carveout 포함)·`stripTrailingExt`와 `web/util.js`의 `splitExtension`은 **동일 규칙**이어야 한다. 한쪽만 바꾸면 UX가 깨진다.

## 숨김 경로

browse에서 dot-prefix 필터로 숨기는 디렉터리 — 새 기능을 추가할 때 이 규칙을 깨지 말 것:
- `.thumb/` — 썸네일·duration 사이드카
- `.cache/streams/` — TS 실시간 remux 캐시 (hash key)
- `.config/settings.json` — 다운로드 설정

## 프론트엔드

- `web/main.js`가 진입점, 도메인 로직은 17개 ES module로 분해. 번들러·프레임워크·외부 의존성 없음 — 수정 후 브라우저 새로고침만으로 확인.
- 모듈 역할:
  - `browse.js` — 폴더 내 파일 목록 렌더, 정렬/검색/필터 적용
  - `router.js` — URL 쿼리(`path`/`sort`/`q`/`type`) 파싱·동기, history 관리
  - `tree.js` — 사이드바 폴더 트리
  - `fileOps.js` — drag/drop, 폴더 이동, rename/delete 모달
  - `dragSelect.js` — 그리드 드래그 박스 선택
  - `urlImport.js` — URL/HLS import SSE 클라이언트
  - `urlImportJobs.js` — 백그라운드 잡 복원/취소/dismiss (`/api/import-url/jobs`)
  - `convert.js` — TS→MP4 변환 SSE 클라이언트 (sseConvertModal에 endpoint·라벨 주입)
  - `convertWebp.js` — 동영상 → WebP 움짤 SSE 클라이언트 (동일 패턴)
  - `convertImage.js` — 이미지 포맷 변환 모달
  - `sseConvertModal.js` — `/api/convert`·`/api/convert-webp` 공유 모달 팩토리 (start/progress/done/error/summary 처리)
  - `modalDismiss.js` — 폼 모달 ESC + 백드롭 클릭 닫기 헬퍼 (lightbox·settings 제외)
  - `settings.js` — 설정 모달
  - `dom.js`, `state.js`, `util.js` — 공용 DOM 헬퍼·전역 상태·유틸
- 모듈 간 의존은 `main.js`에서 콜백 주입으로 끊는다. 예: `urlImportJobs.js`의 cancel/dismiss를 `urlImport.js`에 `setURLImportDeps`로 주입 — 직접 import로 사이클 만들지 말 것.
- 정렬·필터·타입·검색 상태는 **URL 쿼리가 진실**: `sort`, `q`, `type`. 기본값(`name:asc` / 빈 검색 / `all`)은 URL에서 생략. `localStorage` 사용 금지.
- 탐색(폴더 이동)은 `pushState`, 툴바 변경은 `replaceState`.
- lightbox / playlist는 항상 **현재 visible 결과**로 갱신 — 필터로 가려진 항목은 prev/next 대상에서도 빠져야 한다.
- "움짤" 필터 상수는 `CLIP_MAX_BYTES` / `CLIP_MAX_DURATION_SEC`. GIF는 무조건 움짤, 동영상은 크기+duration 둘 다 통과해야 함 (SPEC §2.5.3).

## 테스트 관례

- `httptest.NewRecorder` + Handler 직접 호출. 서버를 띄우지 않는다.
- 임시 디렉터리는 `t.TempDir()`. 테스트 끝나면 자동 정리되니 수동 cleanup 금지.
- ffmpeg 필요 케이스는 `exec.LookPath("ffmpeg")`로 확인 후 `t.Skip` — 같은 패턴으로 추가.
- `internal/urlfetch`에는 HTTP mock 헬퍼가 있음 (`helpers_test.go`). 새 케이스도 여기 맞춰서.
- 테이블 주도 테스트 선호. 케이스 이름은 한국어 섞어도 무방 (기존 코드 그대로).

## 커밋 컨벤션

`git log`에서 확인 — `type(scope): 메시지` 형식, 한국어 본문 OK.

| prefix | 의미 |
|---|---|
| feat | 기능 추가 |
| fix | 버그 수정 |
| refactor | 동작 변경 없는 구조 개선 |
| test | 테스트만 |
| docs | SPEC.md / README 등 |
| plan / spec | `tasks/` 문서 |
| style | 포매팅·UI 미세 조정 |
| merge | feature 브랜치 머지 |

브랜치 전략: `feature/<slug>` → `develop`로 머지 (default 브랜치). 릴리즈 시점에 `develop` → `main`로 머지하고 **태그는 `main`에서 자른다** (v0.0.2 이후 적용 — `v0.0.1`만 예외적으로 develop에서 자름). origin에는 `develop`과 `main`만 있고 `master`는 더 이상 없다. worktree 사용 시 `worktree-feature+<slug>` 이름으로 보이는 경우도 있음.

## 작업 시 팁

- 기능 추가 전에 **`SPEC.md`부터 확인** — 여기가 단일 출처. 차이가 있으면 SPEC를 먼저 업데이트하고 구현. `tasks/todo.md`의 체크리스트로 진행 상황 추적.
- 파일 하나 읽기 전에 전체 구조(`ls internal`, 이 문서의 "레이아웃")를 먼저 보면 의존 방향이 보인다: `cmd → handler → (media/thumb/urlfetch/convert/imageconv/settings/importjob) → media`. `media`는 최하위, 상향 의존 금지.
- SSE 경로 클라이언트 파서: `/api/import-url`은 `web/urlImport.js`, `/api/import-url/jobs/*` 재구독은 `web/urlImportJobs.js`, `/api/convert`·`/api/convert-webp`는 `web/sseConvertModal.js`(모달·이벤트 처리) + `web/convert.js`·`web/convertWebp.js`(endpoint·라벨 주입). 서버 이벤트 스키마는 `start / progress / done / error / summary`로 공통화 — 핸들러 수정 시 해당 모듈도 같이 확인.
- 변경이 UI에 보이는 경우 실제 브라우저에서 확인할 것 — 타입 체크만으로는 기능 검증이 안 된다.
