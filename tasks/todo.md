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
- [ ] VT-1: media.IsVideo() 헬퍼 추가 + 테스트
- [ ] VT-2: thumb.GenerateFromVideo() + IsBlankFrame() 구현
- [ ] VT-3: placeholder.jpg embed (placeholder.go + placeholder.jpg)
- [ ] VT-4: handleThumb 동영상 분기 + placeholder fallback
- [ ] VT-5: browse.go — 동영상 thumb_available 포함
- [ ] VT-6: 테스트 (thumb_test, handler/thumb_test, browse_test)

## Phase 8 — 동영상 길이 표시 (`feature/video-duration`)
- [ ] VD-1: thumb 패키지 — ProbeDuration export + Read/Write/PathSidecar 추가, GenerateFromVideo가 사이드카 작성하도록 수정 + 테스트
- [ ] VD-2: browse handler — entry에 `duration_sec *float64` 추가, 사이드카 read + 기존 썸 백필 로직, 테스트
- [ ] VD-3: frontend — `formatDuration` 헬퍼, `buildVideoGrid`에 `.duration-badge` 렌더링, CSS 추가
- [ ] VD-4: E2E 수동 검증 (신규 / 기존 마이그레이션 / placeholder / 모바일 뷰)

## Phase 9 — URL Import (`feature/url-image-import`)
- [x] UI-1: `internal/urlfetch` 패키지 — `Fetch` + `NewClient` (스킴/헤더/사이즈/Content-Type 검증, 임시파일 → atomic rename, `_N` 충돌 회피, warnings) + `fetch_test.go` (httptest mock origin 13개 케이스)
- [x] UI-2: `handler.handleImportURL` (`POST /api/import-url`) — Handler에 `urlClient` 필드, batch sequential 처리, 성공 후 thumbPool 제출 + `import_url_test.go` 9개 케이스, 라우트 등록 → `curl` 단독 검증 통과 후 UI-4 진입
- [x] UI-3: `index.html` + `style.css` — "URL에서 가져오기" 버튼 + `#url-modal` (textarea + 결과 영역) + CSS
- [x] UI-4: `app.js` — DOM refs, openURLModal/closeURLModal/submitURLImport, error code → 한국어 라벨, 닫을 때 succeeded 있으면 browse 새로고침
- [x] UI-5: E2E 수동 검증 — 기본 이미지 다운로드 확인 완료 (Phase 10에서 확장 재검증)

## Phase 10 — URL Import 확장 (동영상/음악 + SSE progress)
- [x] URL-V1: `urlfetch` 확장 — Content-Type allowlist(image/video/audio) + 2 GiB 캡 + 10분 타임아웃 + `Callbacks{Start,Progress}` throttled 콜백 + 테스트 (기존 테스트 갱신 + 신규 9개 케이스)
- [x] URL-V2: `handleImportURL` SSE 전환 — `text/event-stream`, URL당 `start`/`progress`/`done`/`error`, 마지막 `summary`, 음악은 thumbPool skip, 클라이언트 취소 시 배치 조기 중단
- [x] URL-V3: frontend — 버튼 라벨/hint 갱신, URL별 진행 행(이름/바/상태), SSE fetch+ReadableStream 파싱, 상태별 색, `summary` 배지
- [x] URL-V4: E2E 수동 검증 (이미지/동영상/MP3/혼합/2GiB 초과/unsupported/모바일)
