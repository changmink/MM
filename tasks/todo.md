# TODO — Folder Sidebar + Drag-to-Move

> Spec: [`spec-sidebar-dnd.md`](./spec-sidebar-dnd.md) · Plan: [`plan.md`](./plan.md)
> Branch: `feature/folder-sidebar-and-dnd`

## Phase 1 — Backend
- [ ] T1: `media.MoveFile` 헬퍼 + 단위 테스트 (rename + EXDEV fallback + sidecar + suffix)
- [ ] T2: `PATCH /api/file` 핸들러 + 통합 테스트
- [ ] T3: `GET /api/tree` 핸들러 + walker + 단위 테스트

### ▶ Checkpoint A
- [ ] `go build ./...` clean
- [ ] `go test ./...` green
- [ ] curl 수동: `PATCH /api/file`, `GET /api/tree`

## Phase 2 — Frontend shell
- [ ] T4: 사이드바 HTML/CSS shell + 메인 폴더 섹션 제거 + 모바일 햄버거 토글
- [ ] T5: 트리 렌더링 + lazy expand + 현재 위치 하이라이트 + 폴더 변경 후 reload

### ▶ Checkpoint B
- [ ] 사이드바 폴더 탐색 전체 동작
- [ ] 폴더 생성/삭제 시 사이드바 갱신
- [ ] 모바일 토글 정상
- [ ] 기존 기능 회귀 없음

## Phase 3 — Drag and drop
- [ ] T6: 드래그 시작 + 드롭 대상 핸들러 (커스텀 MIME으로 업로드 zone과 분리)
- [ ] T7: `moveFile` API 호출 + UI 갱신 + 에러 처리

### ▶ Checkpoint C
- [ ] 모든 미디어 타입 드래그 가능
- [ ] 사이드바·breadcrumb 드롭 동작
- [ ] 충돌 시 자동 suffix
- [ ] 외부 파일 업로드와 충돌 없음

## Phase 4 — Polish
- [ ] T8: 접근성 + 빈 상태 + 에러 폴리시

### ▶ Checkpoint D — Ready for review
- [ ] `go test ./...` green
- [ ] 데스크탑 + 모바일 수동 통과
- [ ] SPEC.md에 사이드바/이동 API 흡수
- [ ] PR 생성
