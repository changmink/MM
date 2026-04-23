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
- [ ] C1: `internal/convert` 패키지 — `RemuxTSToMP4(ctx, src, dstDir, baseName, cb) (*Result, error)` + `Callbacks{OnStart, OnProgress}` + `ErrFFmpegMissing` sentinel + `FFmpegExitError` + 500 ms size watcher + 1 MiB/250 ms throttle + atomic `.convert-*.mp4` → `.mp4` rename. 테스트 8개(success/OnStart/progress monotone/atomic/ctx cancel/non-zero exit/ffmpeg missing/stderr captured, ffmpeg 없는 환경은 skip).
- [ ] C2: `handler.handleConvert` (`POST /api/convert`) — JSON body `{paths, delete_original}` 검증(1..50), per-path `media.SafePath`/`.ts` 확장자/목표 `.mp4` 충돌 검사, SSE `start/progress/done/error/summary`, per-file 10분 timeout, per-path mutex, `delete_original` 시 원본+사이드카(`.thumb/{name}.ts.jpg[.dur]`) 삭제 best-effort(실패 시 `warnings: ["delete_original_failed"]`). 라우트 등록. 테스트 17개(4xx 4, 검증 에러 4, 성공 1, case-insensitive 1, 충돌 1, delete 2, 배치 2, 부분 실패 1, cancel 1, ffmpeg missing 1). **체크포인트: `curl -N -X POST .../api/convert` 수동 확인 후 C3 진입.**
- [ ] C3: frontend — `web/index.html` 변환 모달 + `#convert-all-btn` + `app.js?v=14` bump, `web/style.css` `.convert-btn`/`.convert-all-btn`/모달 스타일, `web/app.js`에 `buildVideoGrid`의 `.ts` 카드 버튼, `renderView`에서 visible TS 개수 기반 `#convert-all-btn` 표시, `openConvertModal`/`submitConvert`/SSE 파싱(`readSSE` 헬퍼), `CONVERT_ERROR_LABELS` 한국어 매핑, `loadBrowse()` 재호출.
- [ ] C4: E2E 수동 검증 — plan.md Phase 15 C4의 9개 시나리오 통과 (단일 변환 / 원본 유지 / 원본 삭제 + 사이드카 정리 / 409 충돌 / 폴더 일괄 3개 / 취소 + tmp 정리 / ffmpeg_error / 필터 연동 / 모바일 / 기존 기능 회귀).
