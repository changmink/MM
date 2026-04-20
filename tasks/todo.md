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
- [ ] T12: Dockerfile에 ffmpeg 설치 (apk add)
- [ ] T13: media.IsTS() 헬퍼 추가
- [ ] T14: stream.go에 .ts 분기 + streamTS() 구현
- [ ] T15: stream_test.go에 트랜스코딩 테스트 추가
- [ ] T16: docker compose --build 후 브라우저 검증

## Phase 6 — 폴더 생성/삭제
- [x] T-F1: handleFolder 구현 (POST/DELETE /api/folder) + 라우트 등록
- [x] T-F2: files_test.go에 폴더 생성/삭제 테스트 추가
- [x] T-F3: index.html + style.css — 새 폴더 버튼 + 모달 UI
- [x] T-F4: app.js — createFolder(), deleteFolder(), buildTable() 분기 수정
