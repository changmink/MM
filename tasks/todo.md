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
