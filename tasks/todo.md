# TODO

## Phase 1 — 기반
- [x] T0: Go 모듈 초기화, 디렉토리 구조, main.go HTTP 서버

## Phase 2 — 백엔드
- [x] T1: GET /api/browse — 디렉토리 탐색
- [x] T2: POST /api/upload — 파일 업로드 + 섬네일 비동기 생성
- [x] T4: GET /api/thumb — 섬네일 반환 (lazy 생성)
- [x] T3: GET /api/stream — Range 스트리밍
- [x] T5: DELETE /api/file — 파일 삭제

## Phase 3 — 프론트엔드
- [x] T6: 파일 브라우저 UI (index.html + style.css + app.js 골격)
- [x] T7: 이미지 갤러리 (섬네일 그리드 + 라이트박스)
- [x] T8: 동영상/음악 플레이어
- [x] T9: 업로드 UI (드래그 앤 드롭 + 진행률)

## Phase 4 — 배포
- [x] T10: Dockerfile + docker-compose.yml
- [x] T11: path traversal 방어 + 통합 테스트

## Phase 5 — TS 트랜스코딩
- [x] T12: Dockerfile에 ffmpeg 설치 (apk add) — 이미 구현됨
- [x] T13: media.IsTS() 헬퍼 추가 — 이미 구현됨
- [x] T14: stream.go에 .ts 분기 + streamTS() 구현 — 이미 구현됨
- [ ] T15: stream_test.go에 트랜스코딩 테스트 추가
- [ ] T16: docker compose --build 후 브라우저 검증

## Phase 6 — 폴더 생성/삭제
- [x] T-F1: handleFolder 구현 (POST/DELETE /api/folder) + 라우트 등록
- [x] T-F2: files_test.go에 폴더 생성/삭제 테스트 추가
- [x] T-F3: index.html + style.css — 새 폴더 버튼 + 모달 UI
- [x] T-F4: app.js — createFolder(), deleteFolder(), buildTable() 분기 수정

## Phase 7 — 동영상 섬네일
- [x] VT-1: media.IsVideo() 헬퍼 추가 + 테스트
- [x] VT-2: thumb.GenerateFromVideo() + IsBlankFrame() 구현
- [x] VT-3: placeholder.jpg embed (placeholder.go + placeholder.jpg)
- [x] VT-4: handleThumb 동영상 분기 + placeholder fallback
- [x] VT-5: browse.go — 동영상 thumb_available 포함
- [x] VT-6: 테스트 (thumb_test, handler/thumb_test, browse_test)
- [x] VT-7: thumb.Pool.worker가 media.IsVideo 기반 분기 (업로드/URL-import 경로에서 MP4 async 썸네일 생성) + 업로드 분기 TypeImage||TypeVideo + 삭제 시 .dur 사이드카 정리

## Phase 8 — 동영상 길이 표시 (`feature/video-duration`)
- [x] VD-1: thumb 패키지 — ProbeDuration export + Read/Write/PathSidecar 추가, GenerateFromVideo가 사이드카 작성하도록 수정 + 테스트
- [x] VD-2: browse handler — entry에 `duration_sec *float64` 추가, 사이드카 read + 기존 썸 백필 로직, 테스트
- [x] VD-3: frontend — `formatDuration` 헬퍼, `buildVideoGrid`에 `.duration-badge` 렌더링, CSS 추가
- [x] VD-4: E2E 수동 검증 (신규 / 기존 마이그레이션 / placeholder / 모바일 뷰)

## Phase 8.1 — 리뷰 후속 (review hardening)
- [x] R-1: WriteDurationSidecar atomic write (temp+rename) + NaN/Inf/<=0 검증
- [x] R-2: ReadDurationSidecar에서 poisoned 값(NaN/Inf/<=0) 거부
- [x] R-3: ffprobe 5초 타임아웃 (exec.CommandContext)
- [x] R-4: thumb.LookupDuration / thumb.BackfillDuration 분리 — handler 디커플링
- [x] R-5: browse handler per-request backfill budget (probe ≤1 회)
- [x] R-6: SPEC §5.1 entry 예시에 `mime` 필드 추가

## Phase 9 — 파일/폴더 이름 변경 (`feature/file-rename`)
- [x] R-1: 백엔드 PATCH /api/file — handleFile 메서드 스위치, renameFile (확장자 고정 + 사이드카 rename), validateName, 테스트 (성공/확장자/사이드카/409/400/404/traversal)
- [x] R-2: 백엔드 PATCH /api/folder — handleFolder에 PATCH case, renameFolder (루트 차단), 테스트
- [x] R-3: 프론트엔드 — rename 모달 (index.html + style.css), app.js의 openRenameModal/submitRename, buildTable/buildImageGrid/buildVideoGrid에 "이름 변경" 버튼 추가, 키보드 Enter/Escape 지원
- [x] R-4: E2E 수동 검증 — 파일/폴더 rename, 확장자 방어, 409/400 메시지, 썸네일·duration 오버레이 유지, 회귀 체크 (삭제/업로드/스트리밍)

## Phase 9.1 — rename 리뷰 후속 (Phase 9 review hardening)
- [x] H-1: dotfile carveout (fileExtension) + stripTrailingExt plain Ext
- [x] H-2: atomicRenameFile = os.Link + os.Remove (TOCTOU 방지)
- [x] H-3: case-only rename carveout (strings.EqualFold)
- [x] H-4: length overflow 재검증 (base + ext > 255 → 400)

## Phase 10 — URL Import (`feature/url-image-import`)
- [x] UI-1: `internal/urlfetch` 패키지 — `Fetch` + `NewClient` (스킴/헤더/사이즈/Content-Type 검증, 임시파일 → atomic rename, `_N` 충돌 회피, warnings) + `fetch_test.go` (httptest mock origin 13개 케이스)
- [x] UI-2: `handler.handleImportURL` (`POST /api/import-url`) — Handler에 `urlClient` 필드, batch sequential 처리, 성공 후 thumbPool 제출 + `import_url_test.go` 9개 케이스, 라우트 등록 → `curl` 단독 검증 통과 후 UI-4 진입
- [x] UI-3: `index.html` + `style.css` — "URL에서 가져오기" 버튼 + `#url-modal` (textarea + 결과 영역) + CSS
- [x] UI-4: `app.js` — DOM refs, openURLModal/closeURLModal/submitURLImport, error code → 한국어 라벨, 닫을 때 succeeded 있으면 browse 새로고침
- [x] UI-5: E2E 수동 검증 — 기본 이미지 다운로드 확인 완료 (Phase 11에서 확장 재검증)

## Phase 11 — URL Import 확장 (동영상/음악 + SSE progress)
- [x] URL-V1: `urlfetch` 확장 — Content-Type allowlist(image/video/audio) + 2 GiB 캡 + 10분 타임아웃 + `Callbacks{Start,Progress}` throttled 콜백 + 테스트 (기존 테스트 갱신 + 신규 9개 케이스)
- [x] URL-V2: `handleImportURL` SSE 전환 — `text/event-stream`, URL당 `start`/`progress`/`done`/`error`, 마지막 `summary`, 음악은 thumbPool skip, 클라이언트 취소 시 배치 조기 중단
- [x] URL-V3: frontend — 버튼 라벨/hint 갱신, URL별 진행 행(이름/바/상태), SSE fetch+ReadableStream 파싱, 상태별 색, `summary` 배지
- [x] URL-V4: E2E 수동 검증 (이미지/동영상/MP3/혼합/2GiB 초과/unsupported/모바일)

## Phase 12 — 폴더 사이드바 + 드래그 이동 (`feature/folder-sidebar-and-dnd`) — spec [`spec-sidebar-dnd.md`](./spec-sidebar-dnd.md)
- [x] T1: `media.MoveFile` 헬퍼 (rename + EXDEV fallback + sidecar + suffix) + 12 unit tests
- [x] T2: `PATCH /api/file` body `{"to":"..."}` move; PATCH dispatch by body shape (name=>rename / to=>move) + 11 tests
- [x] T3: `GET /api/tree?path=&depth=` (depth-bounded, `.thumb` excluded, has_children flag) + 13 tests
- [x] T4: 사이드바 HTML/CSS shell + 메인 폴더 섹션 제거 + 모바일 햄버거 토글
- [x] T5: 트리 렌더 + lazy expand (depth=2 init, chevron으로 lazy) + 현재 위치 하이라이트 + 폴더 변경 후 reload
- [x] T6+T7: 카드/행 dragstart, 사이드바·breadcrumb dropTarget, 커스텀 MIME으로 업로드 zone과 분리, `moveFile` PATCH 호출
- [x] T8: 트리 fetch 실패 재시도 버튼, focus-visible outline, delete-btn aria-label
- [x] T9: 트리 노드 hover/focus rename 버튼(✎) — 현재/조상 폴더 rename 시 currentPath 재작성 + rename 후 트리 reload

## Phase 13 — HLS URL Import (`feature/hls-url-import`) — spec [`SPEC.md §2.6.1`](../SPEC.md)
- [x] H1: `internal/urlfetch/hls.go` — `isHLSResponse` + `parseMasterPlaylist` (BANDWIDTH 비교, 상대 URL resolve, 스킴 이중 검증) + `hls_test.go` (감지 7케이스 + 파서 8케이스)
- [x] H2: `runHLSRemux` — ffmpeg `-protocol_whitelist http,https,tls,tcp,crypto` spawn, 500ms size watcher, 2 GiB kill, ctx 취소 → 프로세스 종료 + 테스트 (ffmpeg skip 가드, 정상 remux, ctx cancel, MaxBytes override 오버플로, non-zero exit, progress ≥1)
- [x] H3: `Fetch` HLS 분기 통합 — 1 MiB 본문 cap, variant 선택, 파일명 `.mp4` 강제 + `extension_replaced` 항상 부착, type="video", tmp → atomic rename + 통합 테스트 (정상 media/master, text/plain 폴백, 대문자 확장자, 플레이리스트 초과, file:// variant, 빈 플레이리스트 → ffmpeg_error)
  - **체크포인트**: 백엔드 완결. `curl -N` 으로 공개 HLS URL import 수동 검증
- [x] H4: `sseStart.Total` JSON 태그에 `omitempty` 부착 + 기존 테스트 회귀 체크 + `total` 필드 부재 검증 테스트
- [x] H5: frontend — `URL_ERROR_LABELS`에 `ffmpeg_error`/`hls_playlist_too_large` 추가, `total` 없을 때 `.url-row-indeterminate` 클래스 + CSS 좌→우 애니메이션, `app.js?v=N` 버전 bump
- [x] H6: E2E 수동 검증 — 공개 HLS URL (Mux test stream `test-streams.mux.dev`), master playlist 파싱 + MP4 저장 + 썸네일 + duration 생성 확인. 실사용 중 `audio/mpegurl` 레거시 CT 발견하여 `37c3024`로 지원 추가. 브라우저 UI indeterminate bar 동작 확인.

## Phase 14 — 정렬·필터 툴바 (`feature/sort-filter`) — spec [`SPEC.md §2.5.2`](../SPEC.md)
- [x] SF-1: SPEC.md §2.5.2 + plan.md Phase 14 추가 (선행 커밋, 구현 없음)
- [x] SF-2: 툴바 UI + URL 동기 + 정렬·필터 적용 단일 슬라이스 — `index.html` 툴바 마크업(type 버튼 5·검색 input·sort select) · `style.css` 툴바 규칙 · `app.js` `view` 상태·`readViewFromURL`·`syncURL`·`applyView`·`renderView`·`syncToolbarUI` + 타입/검색/정렬 이벤트 바인딩 + `browse()`/popstate 연동 + 0결과 문구 분기 + lightbox/playlist 재설정 + `app.js?v=13` bump
- [x] SF-3: E2E 수동 검증 — plan.md Phase 14 S6의 10개 시나리오 통과

## Phase 15 — TS → MP4 영구 변환 (`feature/ts-to-mp4`) — spec [`SPEC.md §2.3.3`](../SPEC.md), plan [`plan.md` Phase 15](./plan.md)
- [x] C1: `internal/convert` 패키지 — `RemuxTSToMP4(ctx, src, dstDir, baseName, cb) (*Result, error)` + `Callbacks{OnStart, OnProgress}` + `ErrFFmpegMissing` sentinel + `FFmpegExitError` + 500 ms size watcher + 1 MiB/250 ms throttle + atomic `.convert-*.mp4` → `.mp4` rename. 테스트 7개 (Docker에서 7/7 pass).
- [x] C1.5: pre-existing fixture 버그 수정 — `makeTestTS`(stream_test.go, convert_test.go)를 `mpeg2video+mp2` → `libx264+aac`로 전환. `aac_adtstoasc` 비트스트림 필터 호환 확보. `TestStream`·`TestStreamTSCached` 동반 회복.
- [x] C2: `handler.handleConvert` (`POST /api/convert`) — JSON body `{paths, delete_original}` 검증(1..50), per-path `media.SafePath`/`.ts` 확장자/목표 `.mp4` 충돌 검사, SSE `start/progress/done/error/summary`, per-file 10분 timeout, per-path mutex, `delete_original` 시 원본+사이드카(`.thumb/{name}.ts.jpg[.dur]`) 삭제 best-effort(실패 시 `warnings: ["delete_original_failed"]`). 라우트 등록. 테스트 16개 (Docker에서 전체 pass). curl 체크포인트는 Go 통합 테스트로 대체 검증(SSE 헤더/스키마).
- [x] C3: frontend — `web/index.html` 변환 모달 + `#convert-all-btn` + `app.js?v=14` bump, `web/style.css` `.convert-btn`/`.convert-all-btn`/모달 스타일, `web/app.js`에 `buildVideoGrid`의 `.ts` 카드 버튼, `renderView`에서 visible TS 개수 기반 `#convert-all-btn` 표시, `openConvertModal`/`submitConvert`/`consumeSSE` 일반화 + `handleConvertSSEEvent`, `CONVERT_ERROR_LABELS` 한국어 매핑, close 시 `AbortController.abort()` + 성공 건 있으면 `loadBrowse()`.
- [x] C4: E2E 수동 검증 — plan.md Phase 15 C4의 9개 시나리오 통과 확인. Docker 컨테이너(`file_server-server`) feature/ts-to-mp4 이미지로 교체 후 브라우저에서 단일/일괄 변환, 원본 보존·삭제, 409 충돌, 취소, 필터 연동, 모바일, 회귀 모두 정상.

## Phase 16 — 움짤 필터 (`feature/clip-filter`) — spec [`SPEC.md §2.5.3`](../SPEC.md)
- [x] CF-1: SPEC.md §2.5.3 + plan.md Phase 16 추가 (선행 커밋, 구현 없음)
- [x] CF-2: 움짤 필터 단일 슬라이스 — `index.html` 타입 세그먼트 6번째 버튼 + `app.js?v=14` · `app.js` `TYPE_VALUES`에 `clip` 추가 + `applyView` clip 분기 + 이미지·동영상·움짤 배타 분류(움짤 조건 파일은 이미지/동영상 탭에서 제외, 전체 탭은 영향 없음)
- [x] CF-3: 수동 검증 — 브라우저 확인 완료

## Phase 17 — 다운로드 설정 UI (`feature/download-settings`) — spec [`SPEC.md §2.7`](../SPEC.md)
- [x] S1: `internal/settings` 패키지 — `Store` + `Snapshot`/`Update` + `Validate`/`RangeError` + atomic write (temp+fsync+rename) + 기본값(10 GiB / 30 분) fallback + 8개 테스트(Default·Validate 8 subcase·Missing file·Corrupt JSON·Out-of-range on disk·RoundTrip·RejectsOutOfRange·AtomicWriteLeavesNoTmp)
- [x] S2: `urlfetch` 하드코드 상수 제거 — `MaxBytes`/`TotalTimeout` 상수 삭제, `Fetch(..., maxBytes)` 시그니처, `missing_content_length` 거부 제거(런타임 카운터로 대체), `NewClient` `Timeout` 제거(ctx로 대체), `fetchHLS`/`runHLSRemux`에 maxBytes 전달, `Handler.settings` 필드 + `settingsSnapshot()` 헬퍼, `Register(mux, dataDir, webDir, store)` 4번째 인자, `handleImportURL`에서 per-batch snapshot → `fctx=WithTimeout(perURLTimeout)` + `Fetch(..., maxBytes, cb)`, 테스트 갱신(`testMaxBytes`, `TestFetch_NoContentLength_Succeeds`, `TestFetch_NoContentLength_RuntimeCap`, Register 콜 사이트 벌크 업데이트)
- [x] S3: `handleSettings` (`GET`/`PATCH` `/api/settings`) — GET은 `Snapshot()` JSON, PATCH는 `DisallowUnknownFields` + `Update` → `RangeError`면 `400 {"error":"out_of_range","field":"..."}`, 그 외 실패는 `500 write_failed`, store nil(test harness)이면 `500 settings disabled`. 테스트 8개 (GET defaults, PATCH round-trip, Out-of-range 4 subcase, Malformed JSON, Unknown field, LandsInImportURL, MethodNotAllowed 3 subcase)
- [x] S4: 프론트엔드 — `index.html`에 헤더 `⚙` 버튼 + `#settings-modal` (MiB number input, 분 number input, `.settings-hint` GiB 환산, error p, 저장/취소), URL 모달 hint를 "용량/타임아웃은 ⚙ 설정에서 조정"으로 교체, `app.js?v=16` bump, `style.css` `.settings-btn`·`.settings-field`·`.settings-label`·`.settings-hint` 규칙, `app.js`에 DOM refs 8개 + `SETTINGS_MAX_MIB_MIN/MAX`·`SETTINGS_TIMEOUT_MIN/MAX`·`SETTINGS_FIELD_LABELS` + `openSettingsModal`/`closeSettingsModal`/`updateSettingsMaxHint`/`submitSettings`/`showSettingsError`, 키보드 Escape/Enter + click-outside 지원
- [x] S5: 수동 E2E 검증 — plan.md Phase 17 S5의 10개 시나리오 통과 (docker compose up --build 후 브라우저 확인 완료)

## Phase 18 — 리뷰 후속 (review fixes, `feature/review-fixes`)
- [x] F-4 (high): URL import AbortController — `submitURLImport`에서 `urlAbort = new AbortController()` 생성 + fetch에 `signal` 전달, `closeURLModal`에서 submitting 상태면 `urlAbort.abort()`, catch에서 `AbortError` 무시 + finally에서 `urlAbort = null`. `app.js?v=18` bump.
- [x] F-5 (high): URL error label에서 하드코드 수치 제거 — `too_large: '크기 상한 초과'`, `download_timeout: '다운로드 타임아웃'`로 변경.
- [x] F-6 (medium): `tree.go` 에러 로깅 — walkTree subdir err 및 dirHasSubdirs read err 지점에 `slog.Debug`, `dirHasSubdirs`는 io.EOF만 정상 종료 나머지는 err 반환. `log/slog`·`io` import 추가. 기존 Tree 테스트 7 + ErrorCases 7 subcase 전부 통과.

### 하드닝 대기 (record-only)
- [ ] H-SYMLINK: `media.SafePath` 심볼릭 링크 방어 — 현재 `filepath.Join` 순수 문자열 검사라 `/data` 내부에 symlink가 심어지면 루트 탈출 가능. `filepath.EvalSymlinks`를 SafePath 말미에 추가하고 결과도 다시 prefix 검증. 위협 모델(단일 사용자 LAN, upload는 일반 파일만 생성)상 실제 공격 벡터는 좁지만 심층 방어 차원에서 개선 가치 있음. 별도 phase로 분리 — read-only 경로(browse/tree/stream/thumb)부터 적용, upload/rename 후 재검증 여부 결정 필요.

## Phase 19 — URL Import 백그라운드 진행 (`feature/url-import-background`) — spec [`spec-url-import-background.md`](./spec-url-import-background.md), plan [`plan.md` Phase 19](./plan.md)
- [x] B1: 서버 — `Handler.importSem chan struct{}` (context-aware 세마포어) + `sseQueued` 타입 + `handleImportURL`에서 queued 이벤트 emit 후 acquire/release + 단위 테스트 3개 추가 (Queued once / Serialization / Canceled while waiting). 스트리밍 테스트용 `streamingRecorder` / `waitForPhase` / `postImportStreaming` 헬퍼 도입. 기존 phase 단언 4개 업데이트(`[queued start done summary]`).
- [x] B2: 클라이언트 리팩토링 — `urlSubmitting`/`urlAbort`/`urlAnySucceeded` 전역을 `urlBatches[]` + `urlBatchSeq`로 교체, `anyBatchActive`/`anyBatchSucceeded` 파생 헬퍼, `ensureURLRow(batch, ...)` 및 `handleSSEEvent(batch, ev)` batch-aware 시그니처, row DOM에 `data-batch` 속성 부여, progress 룩업을 `batch.rowEls.get(index)`로 전환, `handleSSEEvent` switch에 `queued` 자리 주석 추가, `app.js?v=18→19` bump. 동작 불변 — 단일 배치 흐름 그대로 (close = abort all active 유지, open 시 `urlBatches.length = 0`).
- [x] B3: `closeURLModal` abort 제거 (뷰 숨김 전용) + 헤더 `#url-badge` 추가 (`index.html` + `style.css` pill 스타일, `--accent`/`--danger` 계열 + hover) + `updateURLBadge`/`maybeFinalize` 헬퍼 + `submitURLImport` finally에서 `maybeFinalize` 트리거 + `handleSSEEvent` done/error에서 배지 카운터 실시간 갱신 + `openURLModal`에 진행 중 분기(row 보존) + HTTP error 시 batch pop + 에러 전용 완료는 3초 linger. `app.js?v=19→20` bump.
- [x] B4: `updateConfirmButton()` 헬퍼 도입 — 상태별 confirm 라벨 자동 전환("가져오기"/"새 배치 추가"/"가져오는 중..."). `urlSubmittingNow` 플래그로 POST phase만 재진입 차단(SSE 시작 시 해제 → 새 배치 추가 가능). `submitURLImport`에 `appending = anyBatchActive()` 분기 — 진행 중이면 `urlRows` 유지 + `.url-batch-divider` 삽입(라벨 `urlBatches.length` 기반) + rows append. 입력 textarea는 submit 직후 자동 초기화. `handleSSEEvent` switch에 `queued` case 추가 — 해당 batch의 모든 row를 "대기 중 (순서 대기)"로 전환. per-batch `summary` 표시 제거, `maybeFinalize`에서 모든 batch aggregate 성공/실패 집계 표시로 이관(다중 배치 summary overwrite 문제 해결). HTTP error 경로에서 appending 케이스는 이 batch의 rows + divider DOM 정리. `app.js?v=20→21` bump, `.url-batch-divider` CSS 추가.
- [x] B5 (docs): `SPEC.md §2.5`(모달 UX), `§2.6`(배치 직렬화 / 백그라운드 진행 / 설정 스냅샷 시점 항목 추가), `§5.1`(배치 단위 흐름 + `queued` 스키마) 본문 갱신. `spec-url-import-background.md` 상단에 `Status: merged` 노트. **수동 E2E 10개 시나리오는 별도 검증 라운드에서 수행** (이 todo는 문서 반영분만 완료 표기).

## Phase 20 — URL Import 잡 영속성 (`feature/url-import-persistence`) — spec [`spec-url-import-persistence.md`](./spec-url-import-persistence.md), plan [`plan.md` Phase 20](./plan.md)

새로고침/탭 재오픈 시 잡 유지. 인메모리 잡 레지스트리(`internal/importjob`) + handler ctx ≠ job ctx 분리. 다중 탭 fan-out, 개별 URL/배치 취소, 종료 잡 dismiss.

- [x] J1: `internal/importjob` 모듈 — `Job`/`Registry`/`Status`/`Event`/`URLState`/`Summary`/`JobSnapshot` 타입 + `Subscribe`/`Publish` (drop-on-full, buffer 64) + `Cancel`/`CancelURL` + 메서드 + `Create`/`Get`/`List`/`Remove`/`RemoveFinished`/`CancelAll` + ID format `imp_[a-z2-7]{8}` (5 byte crypto/rand → base32lower). 단위 테스트 11개 (Subscribe broadcast / SlowConsumer drop isolation / Unsubscribe closes / Cancel propagates ctx / CancelURL targets only N + idempotent / Create ID format + state / RejectsWhenFull (cap override) + finished doesn't count / Remove rejects active + 404 unknown / RemoveFinished leaves active / CancelAll cancels all active / List sorted by createdAt). 외부 caller 없음 — J3에서 통합.
- [x] J2: graceful shutdown — `cmd/server/main.go`에 `signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)` 패턴, `<-serverCtx.Done()` → `httpServer.Shutdown(10s)` → `h.Close()`. `Handler.serverCtx` 필드 (defaulted `context.Background()`). **시그니처 변경 대신 functional Option 패턴**: `Register(mux, dataDir, webDir, store, opts ...Option)` + `WithServerCtx(ctx) Option` — 50+ 기존 테스트 콜 사이트 무변경. 빌드/vet/전체 테스트 통과. 수동 SIGINT는 8080 포트 점유로 deferred (표준 패턴이라 영향 낮음).
- [x] J3: `handleImportURL` registry 통합 — `Handler.registry` 필드 + `Register`에서 `importjob.New(serverCtx)` 초기화 + `Close`에서 `CancelAll + WaitAll(5s)`. `Job.Done()` chan + `SetStatus` terminal 전이 시 자동 close + `Registry.WaitAll(timeout)` 추가. `handleImportURL` 흐름: registry.Create (429 on `ErrTooManyJobs`) → Subscribe → 워커 goroutine 분리 (`runImportJob`, job.Ctx 사용 — request ctx 아님) → `register` 이벤트 핸들러 직접 write (Publish 안 함, per-request metadata) → events 채널 drain → SSE write → summary 도달 또는 r.Context().Done() 시 리턴. `fetchOneJob`에 per-URL ctx `RegisterURLCancel`/`Unregister` (J5에서 활용) + `isCancelled` 헬퍼로 fetch err를 cancel/fail 분기. 잡 status 결정: succeeded≥1 → completed, cancelled>0 → cancelled, else failed. `importjob.SetMaxQueuedForTesting` 테스트 시임 추가. 기존 16 회귀 + 신규 3 (HandlerDisconnect_JobContinues, Register_FirstEvent, TooManyJobs) 모두 통과. `TestImportURL_SSE_ClientCancelled_StopsBatch` → `TestImportURL_HandlerDisconnect_JobContinues`로 의미 반전 교체. `TestImportURL_Queued_CanceledWhileWaiting`는 request ctx cancel 대신 `bJob.Cancel()`로 변경.
- [x] J4: `GET /api/import-url/jobs` + `GET /api/import-url/jobs/{id}/events`. **서버**: `import_url_jobs.go` 신규 — `handleJobsRoot` (GET 405 외), `handleJobsByID` (path suffix 분기 — `/events` 외 모두 404), `handleSubscribeJob` (`{phase:"snapshot",job:JobSnapshot}` 첫 프레임 + 라이브 스트림, terminal 잡은 snapshot 후 자동 close). 라우트 2개 등록 (`/jobs` 정확 + `/jobs/` prefix). 핸들러 테스트 8개 (Empty / ActiveAndFinished / MethodNotAllowed / NotFound / FinishedReturnsSnapshotAndCloses / ActiveReceivesLiveEvents / BadRoute / SubscribeMethodNotAllowed). **클라이언트**: `bootstrapURLJobs` (페이지 로드 시 GET /jobs → 활성/종료 batch 복원 + 활성은 EventSource 구독), `restoreJobBatch` (snapshot → DOM 행 + 카운터), `applyURLStateToRow` (idempotent state 적용), `subscribeToJob` (EventSource + 자동 재연결, summary 시 명시적 close), `register`/`snapshot`/`removed` phase handler 추가, `summary`에서 EventSource 분기 (POST flow는 finally가 처리). app.js?v=22→23. 수동 브라우저 검증은 J6에서 수행 (포트 8080 충돌로 J4 라운드 deferred).
- [x] J5: cancel + dismiss API + UI. **서버**: `POST /api/import-url/jobs/{id}/cancel?index=N` (개별 URL — running이면 CancelURL 호출, pending이면 status를 cancelled로 마킹 + emit), `POST .../cancel` (batch), `DELETE /api/import-url/jobs/{id}` (terminal만, active=409), `DELETE /api/import-url/jobs?status=finished` (filter 필수, 누락=400). `Job.URLStatus`/`URLCount` 헬퍼 추가, worker 루프에 pre-cancel skip 로직 추가. 9개 핸들러 테스트 (Cancel batch / per-URL pending / NotFound / AlreadyFinished / BadIndex / Delete active 409 / Delete finished 204 / Delete NotFound / DeleteFinished + bare 400). **클라이언트**: row별 ✕ 버튼 (status-done/error에서 hide), 배치 header(divider 폐기) — label + "전체 취소" / "닫기" 버튼 (`updateBatchControls` 동기화), modal footer "완료 항목 모두 지우기" 버튼, action 헬퍼 4개 (cancelURLAt/cancelBatchAll/dismissBatch/dismissAllFinishedBatches). `URL_ERROR_LABELS`에 `cancelled: 취소됨` 추가. CSS: `.url-batch-header` flex layout, `.url-row-cancel` hover→danger, `.btn-subtle` modal action. app.js?v=23→24.
- [x] J6 (docs): `SPEC.md §2.6` 본문 — 진행 이벤트 흐름에 register 추가, 백그라운드 진행 / 잡 레지스트리 / 취소 / 이력 dismiss / 활성 잡 cap / 서버 재시작 휘발 / 워커 panic 보호 / 로그 redact / URL 길이 cap 항목 추가. `§5.1` POST /api/import-url 보강(register/cancelled/429), 신규 엔드포인트 5종 (GET /jobs, GET /jobs/{id}/events, POST /cancel, DELETE /jobs/{id}, DELETE ?status=finished). `spec-url-import-persistence.md` 상단 `Status: merged`. **수동 E2E (8개 시나리오) 별도 라운드**에서 수행 — 포트 8080 로컬 충돌로 이번 머지에서는 deferred (서버 단위 테스트가 모든 상태 전이를 커버).

## Phase 21 — 폴더 트리 전체 가시성 (`feature/tree-full-visible`) — spec [`spec-tree-full-visible.md`](./spec-tree-full-visible.md)

페이지 스크롤 시 사이드바·업로드 존이 가려지던 문제 해결. 사이드바 sticky-until-bottom + 업로드 존 sticky 고정.

- [x] TFV-1: `web/style.css` — `.sidebar` 의 `height/overflow-y` 제거 + `align-self: start`. `.upload-zone` 에 `position: sticky; top: var(--header-h); z-index: 5; background: var(--bg)` 추가. 모바일 드로어(<600px) 분기 그대로.
- [x] TFV-2: `web/app.js` — `syncSidebarSticky()` 추가, `top = headerH - max(0, sidebarH - (viewportH - headerH))` 계산. `loadTree()` / `toggleNode()` 끝, `window.resize`, `ResizeObserver(sidebar)` 에서 호출. 모바일에서는 인라인 `top` 제거. `app.js?v=26→27`.
- [x] TFV-3: chromedp 기반 e2e 테스트 — `internal/handler/web_sticky_e2e_test.go` 2개 시나리오(짧은 트리: 사이드바·업로드 존 pin / 긴 트리: 첫 노드 페이지 상단 가시 + 마지막 노드 페이지 하단 스크롤 후 가시). `go.mod` 에 `github.com/chromedp/chromedp` 추가. Chrome 부재 시 ExecAllocator 가 자체 에러로 빠진다.
- [x] TFV-4: `SPEC.md §2.5` 한 줄 추가 + 본 todo entry.

## Phase 22 — 다중 파일 선택 이동 UI (`feature/multi-file-move-ui`) — spec [`spec-multi-file-move-ui.md`](./spec-multi-file-move-ui.md)

- [x] MSM-1: SPEC.md + tasks/spec-multi-file-move-ui.md + todo entry 작성
- [x] MSM-2: 툴바 전체 선택/선택 해제 UI + visible 파일 기준 선택 상태 관리
- [x] MSM-3: 카드/테이블 개별 체크박스 + 선택 상태 스타일
- [x] MSM-4: 선택 묶음 drag payload + 기존 drop target에서 순차 move 처리
- [x] MSM-5: 회귀 테스트 및 Docker Compose API 검증

## Phase 23 — 프론트엔드 모듈 분할 (`feature/frontend-modularization`) — spec [`spec-frontend-modularization.md`](./spec-frontend-modularization.md), plan [`plan-frontend-modularization.md`](./plan-frontend-modularization.md)

`web/app.js` 2,408 라인을 12개 ES module로 분해. 동작 변경 0. 번들러 미사용(브라우저 네이티브 ESM). 각 단계가 그 자체로 동작 — 단계 종료마다 spec §5.2 회귀 체크.

- [x] FM-0: `feature/frontend-modularization` 브랜치 생성 + 베이스라인 smoke (browse/lightbox/upload/urlImport/tree)
- [x] FM-1: `state.js` + `dom.js` + `util.js` 추출 (앵커: app.js 3–96, 1920–2055) — `let` 변수는 setter 경유, DOM ref는 `$` 객체 단일 export, `index.html` script 태그를 `type="module"`로 전환. **체크포인트: §5.2 전체**
- [x] FM-2: `router.js` 추출 (98–130, 2253–2272) — `wireRouter()`/`wireToolbar()` 함수화 + `main`에서 호출
- [x] FM-3: `tree.js` 추출 (2057–2251)
- [x] FM-4: `settings.js` 추출 (2274–2397)
- [x] FM-5: `convert.js` 추출 (1583–1778) — 자체 `consumeSSE` 유지 (공통화는 별도 작업)
- [x] FM-6: `urlImport.js` + `urlImportJobs.js` 동시 추출 (717–1581) — 순환 발생 시 `urlImportRow.js` 분리(13개 됨). 진행 throttle 1 MiB/250 ms 상수 미변경 grep 확인
- [x] FM-7: `fileOps.js` 추출 (596–715, 1780–2017) — upload + delete + rename + dnd + folder create
- [x] FM-8: `browse.js` 추출 (132–595) — browse + render + lightbox + audio. **체크포인트: §5.2 전체 + Chrome+Firefox**
- [x] FM-9: `app.js → main.js` 이름 변경 + `index.html`의 `<script type="module" src="/main.js?v=29">` 갱신 + 모든 모듈 상단 한 줄 요약 주석. **체크포인트: spec §7 acceptance criteria 전부**

**완료 노트:** spec 목표 12개를 초과한 17개로 분할되어 머지 — `dragSelect.js`, `sseConvertModal.js`, `modalDismiss.js`, `convertImage.js`, `convertWebp.js` 5개가 추가로 도출됨.

검증 시 매 단계: `go test ./... && go vet ./...` + 브라우저 콘솔 0 에러 + 해당 도메인 회귀.

## Phase 24 — 폴더 이동 + 사이드바 폴더 작업 정리 + 0.0.1 릴리즈 (`feature/folder-move-and-release-v0.0.1`) — spec [`SPEC.md §2.1.2 / §2.1.3 / §10`](../SPEC.md), plan [`plan.md` Phase 24](./plan.md)

폴더 이동 백엔드 신설(`media.MoveDir` + `PATCH /api/folder {to}`) + 사이드바 트리 🗑 + 새 폴더 버튼 사이드바 이전 + 폴더 DnD + 0.0.1 릴리즈 노트.

- [x] F1: `internal/media/move.go` — `MoveDir(srcAbs, destDir)` + `ErrSrcNotDir`/`ErrDestExists`/`ErrCircular`/`ErrCrossDevice` sentinel. `move_test.go` 6 케이스(Success / DestExists / Circular_Self / Circular_Descendant / PrefixFalsePositive `/tmp/a` ↛ `/tmp/ab` / NotADir + DestNotFound). 사이드카는 폴더 rename과 동일 원리로 자동 따라감.
- [x] F2: `internal/handler/files.go` — `handleFolder` PATCH를 `patchFolder` dispatcher로 교체(`patchFile` 패턴), `moveFolder` 신설(루트·동일부모 가드 + `media.MoveDir` 에러 매핑). `files_test.go` 10 케이스(Success / BothFields / MissingFields / RootRejected / DestNotDir / DestNotFound / Circular / SameDir / Conflict / Traversal). **체크포인트 ①**: curl 6단계로 백엔드 단독 검증.
- [x] F3: `web/index.html` 새 폴더 버튼을 `<header>`에서 `<aside id="sidebar">` 내부 `.sidebar-header`로 이전, `main.js` 버전 v=29→v=30. `web/style.css` `.sidebar-header` 규칙. `web/tree.js` `wireTree(deps)`에 `deleteFolder` 주입 + `buildTreeNode`에 `.tree-delete` 🗑 버튼 추가. `web/main.js` `wireTree({...,deleteFolder})` 갱신.
- [x] F4: `web/state.js` 필요 시 `dragSrcIsDir` 추가. `web/tree.js` `.tree-node-row.draggable=true` + `dragstart`/`dragend` (`DND_MIME` payload `{src,paths,isDir:true}` + Firefox용 `text/plain` fallback). `web/browse.js` `buildTable`에서 `is_dir` 분기 제거하여 폴더 행도 draggable. `web/fileOps.js` `attachDragHandlers`에 `is_dir` 반영, `canDropMoveTo`에 자기 자손 거부, drop 핸들러에 `payload.isDir` 분기 + `moveFolder(srcPath, destDir)` 신설(`PATCH /api/folder {to}` + `rewritePathAfterFolderRename` navigate + `_loadTree()`). `index.html` 버전 v=30→v=31. **체크포인트 ②**: 브라우저 E2E 10케이스.
- [x] F5: `README.md` features 목록을 SPEC §2.1과 동기, "사이드바에서 폴더 작업" 동선 한 줄 추가, "0.0.1 릴리즈 노트" 섹션 신설(F1~F4 변경 + breaking change 없음 명시). **체크포인트 ③**: SPEC §10 5개 항목 모두 체크.

## Phase 25 — PNG → JPG 변환 (`feature/png-to-jpg`) — spec [`SPEC.md §2.8`](../SPEC.md), plan [`plan.md` Phase 25](./plan.md)

PNG 자동/수동 변환. `disintegration/imaging` 재사용 (신규 의존성 없음). 알파는 흰 배경 합성, quality 90 고정. settings 토글로 자동 변환 ON/OFF (기본 ON). 수동 변환은 동기 JSON 응답 (SSE 아님 — PNG 변환은 빨라서 progress 불필요).

- [x] PJ1: `internal/imageconv` 패키지 — `ConvertPNGToJPG(srcPath, destPath, quality)` + 흰 배경 합성(`draw.Draw` over) + atomic write(`os.CreateTemp` → `jpeg.Encode` → `os.Rename`) + quality 0..100 검증. 8 단위 테스트 통과. `imaging` 모듈이 indirect → direct로 승격(`go mod tidy`).
- [x] PJ2: `settings.AutoConvertPNGToJPG bool` 필드 추가 (기본 true) + `New()`의 `loadFile`에서 `map[string]json.RawMessage` 1차 디코드로 키 부재 감지 → true 강제. settings 모달 체크박스 + `web/settings.js` GET/PATCH 양쪽 처리. main.js v=32→33. settings/handler 테스트 추가 (TestNew_LegacyMissingAutoConvertKey, TestUpdate_AutoConvertToggle, TestSettings_PATCH_BooleanTypeMismatch, TestSettings_PATCH_AutoConvertToggle).
- [x] PJ3: `handleUpload` PNG 자동 변환 분기 — `.pngconvert-*.png` 임시 → `imageconv.ConvertPNGToJPG` → 신규 `renameToUniqueDest` 헬퍼(`os.Link` + `os.Remove` race-free 패턴, `_N.jpg` 자동 suffix). 변환 실패 시 원본 PNG로 폴백 + warnings:["convert_failed"] (응답 201 유지). 응답에 converted/warnings 필드. `files_test.go` 7 신규 케이스 모두 통과. `web/fileOps.js`의 annotateUploadResult로 한국어 inline 메시지 (.progress-note ok/warn). main.js v=33→34.
- [x] PJ4: `POST /api/convert-image` 동기 JSON 핸들러 — convert_image.go + convert_image_test.go 신규 + 라우트 `requireSameOrigin`. 30초 timeout, decode_failed/encode_failed/write_failed/already_exists/not_png/canceled 등 7개 에러 코드, delete_original 시 원본 + `.thumb/{name}.png.jpg` 삭제(non-empty dir 막아서 portable 검증). 12 통합 테스트 모두 통과. **체크포인트 ①**: curl 5단계 검증 완료(정상/충돌/delete/traversal/405).
- [x] PJ5: frontend — `web/convertImage.js` 신규 모듈(시작 → 변환 중 → 닫기 라벨 자동 전환, AbortController로 close 시 진행 취소). `web/browse.js`의 buildImageGrid에서 PNG 카드(`mime === 'image/png'`)에 "JPG로 변환" 버튼 + visiblePNGPaths 헬퍼 + `updateConvertPNGAllBtn`. `#convert-image-modal` 마크업, dom.js 9개 ref 추가, main.js wireConvertImage 호출, .png-convert-btn + .convert-png-all-btn(보라 계열) CSS, main.js v=34→35. **체크포인트 ② — 수동 브라우저 E2E 12 시나리오는 별도 라운드** (curl 기반 sanity는 모두 통과 — 자동 업로드 변환/알파/OFF/수동 단건+배치+delete_original+충돌).

## Phase 26 — 선택한 PNG만 변환 (`feature/png-selected-convert`) — spec [`SPEC.md §2.8.2`](../SPEC.md), plan [`plan.md` Phase 26](./plan.md)

selection-aware 모드 전환. 백엔드 변경 없음 (`POST /api/convert-image` 재사용). Phase 22 `selectedPaths` 인프라 그대로 활용 — 폴더 자동 제외(§2.1.3) + visible 동기화(`syncSelectionWithVisible`)도 기존 정책 유지.

- [x] PS-1: SPEC §2.8.2 일괄 변환 트리거 항목을 selection-aware 모드 전환 사양으로 교체 + 수동 테스트 시나리오 1건 추가. tasks/plan.md Phase 26 섹션 신규. tasks/todo.md Phase 26 entry 작성. 구현 없음 (선행 커밋).
- [x] PS-2: `web/browse.js` — `selectedVisiblePNGPaths(visible)` 헬퍼 신규 + `updateConvertPNGAllBtn(visible)` 분기 추가 (selection 0이면 visible PNG 전체, 1+ 이면 selection ∩ visible PNG). 라벨 두 종류("모든 PNG 변환 (M개)" / "선택 PNG 변환 (N개)"). dataset.paths도 분기. `web/index.html` main.js v=35→36. `go test ./... && go vet ./...` 통과.
- [x] PS-3: chromedp e2e 자동화 — `internal/handler/web_png_select_e2e_test.go`의 5개 시나리오(baseline / partial / mixed / png-zero / nav-reset) 모두 통과 (1.6초). 회귀(카드 단일 변환·TS 변환·업로드 자동 변환)는 기존 단위 테스트로 보장.

## Phase 27 — Rubber-band 영역 선택 (`feature/drag-select`) — spec [`SPEC.md §2.5.4`](../SPEC.md), plan [`plan.md` Phase 27](./plan.md)

빈 영역 드래그로 사각형을 그려 visible 카드를 일괄 선택. Phase 22 `selectedPaths` 재사용, 백엔드 변경 없음. 데스크톱 (>600px) 한정.

- [x] DS-1: SPEC §2.5.4 신설(활성 조건/상호작용/modifier/대상/시각/Non-goals/서버 변경 없음) + §2.5 글머리 한 줄 추가. tasks/plan.md Phase 27 섹션 신규. tasks/todo.md Phase 27 entry. 구현 없음(선행 커밋).
- [x] DS-2: 신규 `web/dragSelect.js` — wireDragSelect 진입점, 빈 영역 판정(closest 검사), 5px threshold, overlay div 생성, 카드 rect mousedown 시점 캐시, intersect 판정, modifier 분기(replace/additive), ESC 시작-시점 selection 복원, mouseup cleanup, ≤600px 비활성. `web/main.js` wire 호출 추가. `web/style.css` `.drag-select-overlay` 규칙. `web/index.html` 버전 bump (v=37).
- [x] DS-3: chromedp e2e 자동화 — `internal/handler/web_drag_select_e2e_test.go`의 6개 시나리오(short_drag / rect_selects / card_no_rubberband / ctrl_additive / esc_restores / mobile_disabled) 모두 통과 (2.6초). 마우스 이벤트는 `input.DispatchMouseEvent`(MouseMoved/MousePressed/MouseReleased)로 시뮬레이션, modifier는 `WithModifiers(input.ModifierCtrl)`. 회귀(클릭/이동/PNG selection 연동/텍스트 선택)는 기존 단위 테스트로 보장.

## Phase 28 — 라이트박스 내 삭제 (`feature/lightbox-delete`) — spec [`SPEC.md §2.5.5`](../SPEC.md), [`spec-lightbox-delete.md`](./spec-lightbox-delete.md), plan [`plan.md` Phase 28](./plan.md)

원본 이미지·동영상을 라이트박스로 열어둔 상태에서 🗑 버튼 또는 `Delete` 키로 현재 항목을 삭제. 이미지는 다음으로 이동(마지막이면 닫기), 동영상은 닫고 폴더 새로고침. 기존 `DELETE /api/file` 재사용 — 백엔드 변경 없음.

- [x] LD-1: SPEC §2.5.5 신설 + §2.5 글머리 한 줄 추가. tasks/spec-lightbox-delete.md 신규. tasks/plan.md Phase 28 섹션 신규. tasks/todo.md Phase 28 entry. 구현 없음(선행 커밋).
- [x] LD-2: `web/index.html` 라이트박스에 `<button class="lb-delete" id="lb-delete">🗑</button>` 추가 + main.js v=37→38. `web/style.css` `.lb-delete` 위치(`top: 16px; right: 72px;`). `web/dom.js` `$.lbDelete` ref. `web/state.js` `lbCurrentVideoPath` mutable export + setter.
- [x] LD-3: `web/fileOps.js` `deleteFile(path, opts)` 시그니처 확장(`opts.skipBrowse`, return boolean). `web/browse.js` `openLightboxVideo`에서 `setLbCurrentVideoPath(entry.path)` + close 경로 통합(`closeLightbox()` 추출) + `deleteCurrentLightboxItem()` + 클릭/`Delete` 키 바인딩. 기존 카드 썸네일 삭제 회귀 없음.
- [x] LD-4: chromedp e2e 자동화 — `internal/handler/web_lightbox_delete_e2e_test.go`의 6개 시나리오(image_delete_advances / image_delete_last_closes / image_delete_keyboard / video_delete_closes / confirm_cancel_no_change / delete_key_inactive_when_closed) 모두 통과 (5.6초). confirm dialog는 시나리오마다 `window.confirm = () => <bool>` 오버라이드로 제어. Delete 키는 `kb.Delete` 사용.
- [x] LD-5: docker compose --build → 8 시나리오 수동 검증(이미지 advance/close/Delete 키, 동영상 close, confirm 취소, 사이드카 정리, 카드 삭제 회귀, prev/next 회귀) 통과.

## Phase 29 — 움짤 → animated WebP 변환 (`feature/clip-to-webp`) — spec [`SPEC.md §2.9`](../SPEC.md), plan [`plan.md` Phase 29](./plan.md)

GIF + 짧은 동영상(≤50 MiB && ≤30s)을 animated WebP로 영구 변환. ffmpeg `libwebp` (multi-frame 자동 promote) SSE. 움짤 게이트 서버 재검증 + audio 자동 drop + warning. UI는 움짤 탭 한정 일괄 버튼 + 카드별 버튼. **자동 업로드 변환 없음** (수동만 — PNG→JPG와 다른 정책). settings 의존 없음(고정 파라미터).

- [x] CW0: SPEC §2.9 신설 + §3/§4/§5/§5.1/§8/§9 갱신 + tasks/plan.md Phase 29 + tasks/todo.md Phase 29 entry 작성. 구현 없음(선행 커밋).
- [x] CW1: `internal/convert/webp.go` 신규 — `EncodeWebP(ctx, src, dstDir, baseName, cb)` (ffmpeg `libwebp` + multi-frame 자동 promote, atomic temp `.webpconvert-*.webp` → rename) + `ProbeStreamInfo(srcPath) (durationSec, hasAudio, err)` 헬퍼 + `webp_test.go` 12 케이스 (alpine 컨테이너에서 12/12 pass + 기존 RemuxTSToMP4 7건 회귀 통과). Phase 0 검증에서 alpine ffmpeg 6.1의 `libwebp_anim` single-frame 회귀 발견 → SPEC §2.9/§3 인코더 표기 동시 정정. animated webp 검증은 RIFF VP8X chunk + animation flag 직접 파싱.
- [x] CW2: `internal/handler/convert_webp.go` 신규 + `Handler.webpLocks sync.Map` 필드 + `Register`에 `requireSameOrigin(h.handleConvertWebP)` 라우트. POST `/api/convert-webp` SSE — 게이트 재검증(GIF 무조건 / 동영상은 50MiB+30s, duration은 thumb 캐시→backfill / 그 외 unsupported_input), 목표 `.webp` 충돌 거부, per-path lock, 5분 timeout, audio_dropped warning, delete_original 시 원본+사이드카 삭제(.jpg+.dur). 통합 테스트 19 케이스 (alpine 컨테이너 19/19 pass — 4xx 5종 + 게이트 6종 + happy path 4종 + delete/충돌 3종 + ffmpeg_missing). **체크포인트 ②**: 통합 테스트가 SSE 헤더/스키마/에러 매트릭스 전부 커버 — curl 단독 라운드는 CW4 docker compose 검증으로 통합 (Phase 15 C2 선례 일관).
- [x] CW3: frontend — `web/convertWebp.js` 신규 모듈(SSE 클라이언트, TS→MP4 `convert.js` 패턴 미러), `index.html` `#convert-webp-modal` + `#convert-webp-all-btn` + main.js v=38→v=39, `style.css` `.webp-convert-btn`(right:88px — TS+움짤 동시 케이스에서 `.convert-btn`과 안 겹치도록)+`.convert-webp-all-btn`(민트 계열) + 모달, `dom.js` 10개 ref 추가, `main.js` `wireConvertWebP({browse})` 주입, `browse.js` GIF 카드 / `isClip` 동영상 카드에 "WEBP" 버튼 + 움짤 탭(`view.type === 'clip'`) 한정 일괄 버튼 + `isClip`/`visibleClipPaths`/`selectedVisibleClipPaths` export. 한국어 에러 라벨 12종 + warning 라벨 2종. node --check 4개 파일 syntax OK + docker compose 띄워 GET /main.js·/convertWebp.js·/api/convert-webp(405) 검증.
- [x] CW4: chromedp e2e — `web_clip_to_webp_e2e_test.go` 6 시나리오 (clip 탭 일괄 / selection 라벨 / 카드 버튼 매핑 / video 탭 숨김 / image 탭 숨김 / clear-selection 복귀) 모두 통과. **체크포인트 ②/③ 통합 라이브 라운드** — `docker compose up -d --build` 후 컨테이너에 fixture(짧은 mp4 with audio + clip.gif + 35초 mp4 + jpg) 주입 → curl SSE 호출로 정상 batch(audio_dropped warning) / VP8X+Animation flag(offset 20 = 0x12) / audio strip(ffprobe 빈 stream) / not_clip(35초) / unsupported_input(jpg) / already_exists(재요청) 모두 기대대로 동작. 브라우저 UI 라운드 (12개 시나리오 — 카드 hover 버튼·일괄 버튼·모바일·회귀 스모크)는 사용자 검증으로 별도 라운드.
- [x] CW5: WebP 를 움짤 분류에 포함 — 사용자 follow-up. SPEC §2.5.3 갱신(`mime === 'image/webp'` 무조건 움짤, `image` 탭에서 GIF·WebP 모두 제외). SPEC §2.9 갱신(WebP 는 분류상 움짤이지만 변환 입력으로는 `unsupported_input` 거부 — 결과물 재변환 의도 없음, 클라이언트도 `isClipConvertable` 헬퍼로 일괄 paths 에서 webp 제외). `web/browse.js` `isClip` 갱신 + `isClipConvertable` 신설. main.js v=39→v=40. e2e 시나리오 8개로 확장 (webp_card_in_clip_tab / webp_only_selection_hides_button + dataset.paths 에 webp 미포함 검증). 라이브 browse 응답에서 clip.webp / short.webp 의 `mime: image/webp` 분류 정상 확인.

## Phase 30 — 움짤 카드 자동재생 부담 완화 (`feature/clip-hover-playback`) — spec [`SPEC.md §2.5.6`](../SPEC.md), plan [`plan.md` Phase 30](./plan.md)

GIF/WebP 카드가 동시 자동재생되어 저사양 PC 에 부담. 클라이언트 only — 평시 정적 첫 프레임(`/api/thumb`), hover/IntersectionObserver 시만 `/api/stream` 토글로 재생. `thumb.Generate` 가 이미 GIF/WebP 첫 프레임을 jpg 로 추출하고 `serveImageThumb` lazy 가 backstop 이라 서버 무변경. 움짤 탭 한정 카드 grid `minmax(240px, 1fr)`.

- [x] HP0: SPEC §2.5.6 신설 + §2.5 글머리 1줄 + tasks/plan.md Phase 30 + tasks/todo.md Phase 30 entry. 구현 없음(선행 커밋).
- [x] HP1: `web/browse.js` `buildImageGrid` GIF/WebP 분기 + 신규 헬퍼 `attachClipHoverPlayback(card)` (matchMedia `(hover: hover)` true → hover 토글, false → 모듈 레벨 IntersectionObserver) + `renderFileList` 에서 움짤 탭 시 `image-grid-clip` 클래스 부착. `web/style.css` `.image-grid.image-grid-clip` 한 줄. `web/index.html` v=40→v=41.
- [x] HP2: `internal/handler/web_clip_hover_playback_e2e_test.go` 신규 — 4 시나리오 (desktop hover toggles src / mobile IO toggles src / clip tab grid 240px / non-clip tab keeps default). mobile 시뮬은 `Page.addScriptToEvaluateOnNewDocument` 으로 `window.matchMedia` stub.
- [x] HP3: `docker compose up -d --build` 후 수동 검증 통과 (사용자 확인) — 움짤 탭 카드 240px 폭, 정적 첫 프레임 표시, hover 시 단일 카드만 재생, mouseleave 시 즉시 정지. HP4 폴백으로 webp 카드 첫 프레임 정상 출력 확인.
- [x] HP4: animated WebP thumb 폴백 — 사용자 검증 라운드에서 webp 카드 첫 프레임 미표시 발견. imaging 의 정적 webp 디코더가 VP8X+ANIM 거부, ffmpeg 디코더도 alpine 빌드에서 동일 거부. `libwebp-tools` 의 webpmux 로 첫 프레임 single-frame webp 추출 → dwebp 로 png 변환 → imaging.Open(png) chain. Dockerfile `libwebp-tools` 추가, `internal/thumb/thumb.go` 의 `Generate` 에 webp 분기 + `decodeAnimatedWebPFirstFrame` 헬퍼 + `ErrWebPMuxMissing` sentinel. `TestGenerateAnimatedWebP` 단위 테스트 + 라이브 검증 (clip.webp / short.webp / clip.gif / short.mp4 모두 GET /api/thumb 200, .thumb 사이드카 정상 생성).

## Phase 32 — web/browse.js 분해 (`feature/browse-decomposition`) — spec [`spec-browse-decomposition.md`](./spec-browse-decomposition.md)

`web/browse.js` 752줄 → browse 슬림 + 5 신규 모듈로 분해. 행위 변경 0, 모든 export 시그니처 보존, chromedp e2e 6개 + 수동 12 시나리오 통과 = acceptance. 백엔드 변경 없음.

- [x] BD-0: spec accepted + Phase 32 entry 작성. 구현 없음(선행 commit). `9e5c366` `1f7c416`
- [x] BD-1: `web/clipPlayback.js` 분리 — HOVER_CAPABLE / clipIOInstance / attachClipHoverPlayback / isClip / isClipConvertable (5+IO state). browse.js named import + isClip re-export로 외부 surface 보존. clipPlayback.js 69줄, browse.js 752→693. `TestClipHoverPlayback_E2E` 4 시나리오 통과.
- [x] BD-2: `web/visiblePaths.js` 분리 — visibleTSPaths/visiblePNGPaths/visibleClipPaths/selectedVisible(PNG|Clip)Paths + updateConvert(All|PNGAll|WebPAll)Btn 8 함수. browse.js 5 함수 re-export로 surface 보존, isClipConvertable import 는 visiblePaths 로 이전. visiblePaths.js 97줄, browse.js 693→621. `TestPNGSelectedConvert_E2E` 5 + `TestClipToWebP_E2E` 8 시나리오 통과.
- [x] BD-3: `web/selection.js` 분리 — 7 함수(syncSelectionWithVisible/renderSelectionControls/updateCardSelection/syncCardSelectionStates/refreshSelectionUI/setSelected/bindEntrySelection) + wireSelection({computeVisible}) deps 주입(applyView/allEntries 사이클 회피, plan §6 #4 패턴). dragSelect.js import 경로 → selection.js. selection.js 127줄, browse.js 621→536. `TestDragSelect_E2E` 6 + `TestPNGSelectedConvert_E2E` 5 + BD-1·2 회귀 통과.
- [x] BD-4: `web/cardBuilders.js` 분리 — sectionTitle / buildImageGrid / buildVideoGrid / buildTable. build*는 onOpen 콜백을 매-호출-주입(plan §3 BD-4 권장: handleClick은 browse.js orchestrator로 남김). cardBuilders.js 201줄(≤250 AC pass), browse.js 536→354. 회귀 발견 1건: openLightboxImage/Video의 esc(...) 호출이 import 정리 시 누락(plan §6 #7 — node --check 미캐치, chromedp가 final acceptance). 6 web e2e 모두 통과.
- [ ] BD-5: `web/lightbox.js` 분리 — openLightboxImage / openLightboxVideo / closeLightbox / deleteCurrentLightboxItem / playAudio / loadPlaylistTrack / renderPlaylist + `wireBrowse`의 lightbox·audio·키보드 부분 이전. `web_lightbox_delete_e2e_test.go` 통과.
- [ ] BD-6: `web/browse.js` 슬림화 + cache-bust v=41→v=42. `wireBrowse`는 분리 모듈의 `wireXxx` delegate. AC-1(≤250 라인) 검증.
- [ ] BD-7: 수동 브라우저 검증 12 시나리오 통과 + spec Status `merged` 갱신.

## Phase 31 — 팀 리뷰 3회차 follow-up (`feature/team-review-3-followup`) — handoff [`handoff-team-review-3-followup.md`](./handoff-team-review-3-followup.md)

`feature/team-review-2-important` 브랜치 22 커밋을 5축 단일 리뷰한 결과로 식별된 후속 정리. **본 브랜치 머지 차단성 없음** — Critical 0건, Important 3건 + Suggestion 6건 모두 별도 PR로 처리. 상세 배경·권장 구현·우선순위는 핸드오프 문서 단일 출처.

- [x] FU3-I-1: SSE/JSON 헬퍼 3중복 통합 — `internal/handlerutil` 신설(WriteJSON/WriteError/AssertFlusher/WriteSSEHeaders/WriteSSEEvent/NewSSEEmitter), 네 곳(handler.go·handler/sse.go·import/common.go·convert/common.go)을 1줄 thin wrapper로 통합. 호출 사이트 변경 0, handlerutil_test.go 7 케이스. `b6cfd97`
- [x] FU3-I-2-A: `*_compat_test.go` + import/common.go의 export wrapper 5개에 `Deprecated: transitional shim` 주석 추가 — 핸드오프 FU3-I-2 참조 링크. `d8aeca7`
- [ ] FU3-I-2-B: 부모 `internal/handler/import_url_test.go` 1262줄을 `internal/handler/import/` 서브패키지로 이전 + `import_url_compat_test.go` / `convert_compat_test.go` 삭제 (정공, 다음 사이클).
- [ ] FU3-I-3: `imageconv.ConvertPNGToJPG`에 ctx 시그니처 추가 — A.2 cleanup goroutine은 timeout 후 늦은 출력만 정리하고 hang 자체는 방어 못 함. 별도 spec 검토 후 진행.
- [x] FU3-S-1: `internal/urlfetch/hls/download.go` materialize dispatch 루프를 labeled break + post-send 재검 제거로 정리 — `runImportURLWorkers` 패턴과 일관. `511720d`
- [x] FU3-S-2: `importURLWorkers=2` / `hlsMaterializeParallelism=4` 상수 옆에 근거 주석. `ce0dacf`
- [x] FU3-S-3: `runImportURLWorkers` 워커 진입부 이중 cancellation guard 의도 주석 (fast-path임을 명시). `ce0dacf`
- [ ] FU3-S-4: `internal/urlfetch/hls/helpers_test.go`의 stub `NewClient`를 `newTestClient`로 개명 — production stub과 명명 분리 (다음 사이클).
- [x] FU3-S-5: handlerutil/sse.go `NewSSEEmitter` godoc + convert/common.go sseEmitter wrapper 주석으로 함께 처리 (FU3-I-1 묶음에 포함). `b6cfd97`
- [x] FU3-S-6: `upload.go uploadPNGAutoConvert` close 에러 wrap 통일 — A.1 패턴(`fmt.Errorf("...: %w", ...)`)과 일관화. `511720d`
