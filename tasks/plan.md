# Implementation Plan: Folder Sidebar + Drag-to-Move

> Spec: [`spec-sidebar-dnd.md`](./spec-sidebar-dnd.md)
> Branch: `feature/folder-sidebar-and-dnd`
> Worktree: `C:/Users/chang/Projects/file_server-sidebar-dnd`

## 1. Overview

폴더 트리 좌측 사이드바 + 파일 카드 → 폴더 드래그 이동 기능을 추가한다. 백엔드 신규 엔드포인트 2개(`GET /api/tree`, `PATCH /api/file`) + 프런트엔드 사이드바·DnD 추가.

## 2. Architecture Decisions

| 결정 | 근거 |
|------|------|
| 트리 데이터를 별도 `/api/tree`로 노출 (browse 확장 X) | browse는 디렉토리 자세 보기, tree는 네비게이션 — 두 책임 분리 |
| 트리는 lazy load (depth=1 초기 + chevron 클릭 시 추가 fetch) | 큰 트리에서 초기 응답 빨리 띄우기 |
| 파일 이동은 `PATCH /api/file`로 추가, dest는 디렉토리만 | 같은 리소스의 부분 변경 → REST상 PATCH가 적합. v1엔 rename 비포함 |
| 충돌 시 `_N` suffix 자동 부여 (`createUniqueFile` 재사용) | 업로드 동작과 일관됨 → 사용자가 외울 규칙 하나 |
| `os.Rename` 우선, EXDEV 시 copy+remove fallback | named volume 안에서는 rename으로 atomic. 추후 마운트 변경 대비 |
| 사이드카(`.thumb/{name}.jpg`, `.dur`) 이동은 best-effort | 본체 이동 성공 후 사이드카 실패 시 thumb이 lazy 재생성 가능 — 이동을 막을 이유 없음 |
| 사이드바 트리 재로드는 폴더 생성/삭제/이동 후 전체 reload | 트리가 작고 단순함 우선. 부분 갱신은 v2 |
| 메인의 "폴더" 섹션 제거 | 사이드바와 중복 — 사용자 스펙 답변 |
| 모바일은 햄버거 오버레이만 | 미디어쿼리 단일 분기로 충분, drawer 라이브러리 불필요 |

## 3. Dependency Graph

```
Foundation
├── Move helper (internal): handles os.Rename + sidecar + unique name
│        │
│        └── PATCH /api/file handler ──── Slice A: backend move
│                  │
│                  └── frontend moveFile() + drag handlers ─── Slice C
│
└── Tree walker (internal): bounded recursion + has_children
         │
         └── GET /api/tree handler ──── Slice B: tree API
                   │
                   └── frontend renderTree() + lazy expand ─── Slice D

Frontend layout (independent shell):
└── Slice E: sidebar HTML/CSS shell + remove "폴더" section + mobile toggle
```

## 4. Vertical Slice Strategy

Backend 두 슬라이스(A: move, B: tree)는 독립이므로 병렬 가능. 프런트는 E(레이아웃)→D(트리 표시)→C(DnD) 순이 자연스러움 — 사이드바 없으면 드롭할 곳이 없음.

권장 순서:
1. **Task 1-2** (Slice A: backend move) — 가장 위험, fail fast
2. **Task 3** (Slice B: backend tree)
3. **Task 4** (Slice E: frontend shell + 메인 폴더 섹션 제거 + 모바일 토글)
4. **Task 5** (Slice D: frontend tree binding + lazy load + reload)
5. **Task 6-7** (Slice C: frontend DnD)
6. **Task 8** (polish: 접근성, 빈 상태)

## 5. Phases & Tasks

### Phase 1: Backend foundations

#### Task 1: `media.MoveFile` 헬퍼 + 단위 테스트
**Description:** `internal/media`에 파일 이동 로직 분리. `os.Rename` 시도 → `EXDEV` 시 copy+fsync+remove fallback. 사이드카 이동은 best-effort.
**Acceptance:**
- [ ] `MoveFile(srcAbs, destDir string) (finalAbs string, err error)`
- [ ] dest에 동일 이름 있으면 `_1`, `_2` suffix 자동 부여
- [ ] dest가 디렉토리 아니거나 존재 안 하면 에러
- [ ] 사이드카 `.thumb/<name>.jpg` + `.dur` 함께 이동 (각각 try/log)
- [ ] EXDEV 시 copy+fsync+remove fallback
**Verification:** `go test ./internal/media/...` 통과
**Dependencies:** None
**Files:** `internal/media/move.go`, `internal/media/move_test.go`
**Scope:** S

#### Task 2: `PATCH /api/file` 핸들러
**Description:** `handler.handleFile`에 `PATCH` 분기 추가, `media.MoveFile` 호출.
**Acceptance:**
- [ ] `PATCH /api/file?path=<src>` body `{"to":"<destDir>"}` 동작
- [ ] 200 응답: `{"path":"<newRel>","name":"<newName>"}`
- [ ] src 부모 == dest → 400 `same directory`
- [ ] src is dir → 400 `cannot move directory`
- [ ] dest invalid/missing → 400 `invalid destination`
- [ ] src 미존재 → 404
- [ ] traversal → 400
**Verification:** `go test ./internal/handler/ -run TestMove` + 기존 테스트 전부 green
**Dependencies:** Task 1
**Files:** `internal/handler/files.go`, `internal/handler/files_test.go`
**Scope:** S

#### Task 3: Tree walker + `GET /api/tree` 핸들러
**Description:** depth-bounded 재귀로 폴더만 모음. `.`/`.thumb` 등 hidden 제외(browse와 동일 규칙).
**Acceptance:**
- [ ] `GET /api/tree?path=&depth=` 동작 (default `/`, `1`, max `5`)
- [ ] depth 한도까지만 children 채움, 그 너머는 `children: null` + `has_children`만 표시
- [ ] 빈 폴더는 `children: []`
- [ ] 미존재 → 404, traversal → 400, depth>5 → 400
- [ ] 정렬은 이름 알파벳 (대소문자 무시)
- [ ] `Register`에 `mux.HandleFunc("/api/tree", ...)` 한 줄 추가
**Verification:** `go test ./internal/handler/ -run TestTree` 통과
**Dependencies:** None
**Files:** `internal/handler/tree.go`, `internal/handler/tree_test.go`, `internal/handler/handler.go`
**Scope:** S

### Checkpoint A: Backend complete
- [ ] `go build ./...` clean
- [ ] `go test ./...` 전부 통과
- [ ] curl로 `PATCH /api/file`, `GET /api/tree` 수동 확인

### Phase 2: Frontend shell

#### Task 4: 사이드바 HTML/CSS shell + 메인 폴더 섹션 제거 + 모바일 토글
**Description:** `<aside id="sidebar">` 추가, 빈 상태로 렌더, 데스크탑 grid 레이아웃, 모바일 햄버거 토글, `renderFileList`에서 `dirs` 섹션 제거.
**Acceptance:**
- [ ] 데스크탑(>600px): 좌측 240px 사이드바 + 우측 main 그리드 레이아웃
- [ ] 모바일(≤600px): 사이드바 숨김 + 햄버거 버튼 표시 + 토글 동작
- [ ] 햄버거 `aria-label`, `aria-expanded` 갱신
- [ ] 메인 영역에 "폴더" 섹션 표시 안 됨
- [ ] 폴더 클릭 진입은 breadcrumb로만 가능 (사이드바 비어있어도 동작)
**Verification:** 브라우저 수동: 데스크탑/모바일 viewport 둘 다 레이아웃 확인. 기존 업로드/삭제/스트리밍 회귀 없음.
**Dependencies:** None (백엔드와 독립)
**Files:** `web/index.html`, `web/style.css`, `web/app.js`
**Scope:** M

#### Task 5: 트리 렌더링 + lazy expand + 현재 위치 하이라이트
**Description:** `loadTree()` / `renderTree()` / `expandNode()` 추가. 폴더 생성/삭제 후 트리 reload. 현재 폴더 노드 하이라이트.
**Acceptance:**
- [ ] 페이지 로드 시 `GET /api/tree?path=/&depth=2` 호출 (사용자 답변 Q1=opt3)
- [ ] chevron 클릭 시 자식 fetch (`children:null`이었던 노드만)
- [ ] 노드 텍스트 클릭 시 `browse(path)` (모바일이면 사이드바 닫힘)
- [ ] 현재 `currentPath`와 일치하는 노드 하이라이트 (browse 호출 시 갱신)
- [ ] 폴더 생성/삭제 성공 시 트리 reload
- [ ] 빈 폴더는 chevron disabled
**Verification:** 브라우저 수동: 폴더 생성→사이드바에 즉시 보임, 클릭→이동, chevron→펼침. 깊은 트리 lazy load 동작.
**Dependencies:** Task 3, Task 4
**Files:** `web/app.js`, `web/style.css`
**Scope:** M

### Checkpoint B: Sidebar usable
- [ ] 사이드바로 폴더 탐색 전체 흐름 동작
- [ ] 폴더 생성/삭제 시 사이드바 갱신
- [ ] 모바일 토글 정상
- [ ] 기존 기능(업로드/스트리밍/썸네일) 회귀 없음

### Phase 3: Drag and drop

#### Task 6: 드래그 시작 + 드롭 대상 핸들러
**Description:** 이미지 카드, 동영상 카드, 테이블 행에 dragstart/dragend. 사이드바 폴더 노드와 breadcrumb segment에 dragenter/dragover/dragleave/drop. 외부 OS 파일 드롭(업로드)과 구분: `application/x-fileserver-move` 커스텀 MIME.
**Acceptance:**
- [ ] 이미지/동영상 카드, 테이블 행 모두 `draggable=true`
- [ ] 드래그 중 `.dragging` 시각 적용
- [ ] 사이드바 모든 폴더 노드 + breadcrumb segment가 drop target
- [ ] `dragover` 시 `.drop-target` 시각, `dragleave`에서 제거
- [ ] 자기 부모 폴더에 드롭 시 시각 거부 + drop 무시
- [ ] 업로드 zone은 `Files` 타입만 처리 (커스텀 MIME 무시)
- [ ] drop 시 `moveFile(src, destDir)` 호출
**Verification:** 브라우저 수동: 다양한 카드/행을 사이드바·breadcrumb로 드래그하면 시각 반응. OS 파일을 업로드 zone에 드롭하면 그대로 업로드.
**Dependencies:** Task 5
**Files:** `web/app.js`, `web/style.css`
**Scope:** M

#### Task 7: `moveFile` API 호출 + UI 갱신 + 에러 처리
**Description:** `PATCH /api/file` 호출, 성공 시 `browse(currentPath, false)` 재호출, 실패 시 `alert`.
**Acceptance:**
- [ ] PATCH 성공 시 메인 리스트 즉시 갱신 (떠난 파일 사라짐)
- [ ] 트리 reload 불필요 (파일 이동이라 폴더 구조 변동 없음)
- [ ] 4xx/5xx 응답 시 `alert('이동 실패: <error>')`
- [ ] 네트워크 실패 시 alert
**Verification:** 수동: 이동 성공 후 카드 사라짐. 같은 이름 충돌 시 자동 suffix로 성공. dest=src 부모일 때 시각 거부.
**Dependencies:** Task 6
**Files:** `web/app.js`
**Scope:** XS

### Checkpoint C: DnD complete
- [ ] 모든 미디어 타입 드래그 가능
- [ ] 사이드바·breadcrumb 모두 drop 동작
- [ ] 충돌 시 자동 suffix
- [ ] 외부 파일 업로드와 충돌 없음

### Phase 4: Polish

#### Task 8: 접근성 + 빈 상태 + 에러 폴리시
**Description:** 트리 노드 키보드 포커스, chevron `aria-expanded`, 빈 트리 메시지, 트리 fetch 실패 메시지.
**Acceptance:**
- [ ] 트리 노드 Tab으로 포커스 가능, Enter로 진입
- [ ] chevron `aria-expanded` 정확히 토글
- [ ] 트리 fetch 실패 시 사이드바에 "트리 로드 실패" + 재시도 버튼
- [ ] 빈 트리 시 "폴더가 없습니다" 안내
**Verification:** 키보드만으로 사이드바 폴더 진입 가능. 백엔드 죽인 상태에서 새로고침 → 친절한 에러.
**Dependencies:** Task 5
**Files:** `web/app.js`, `web/style.css`, `web/index.html`
**Scope:** S

### Checkpoint D: Ready for review
- [ ] 모든 acceptance criteria 만족
- [ ] `go test ./...` green
- [ ] 데스크탑/모바일 두 모드 모두 수동 통과
- [ ] SPEC.md 업데이트 (또는 feature spec을 흡수)
- [ ] PR 생성

## 6. Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| 크로스 디바이스 이동 시 `os.Rename` 실패 (`EXDEV`) | High — 이동 자체 안 됨 | copy+fsync+remove fallback (Task 1) |
| 사이드카(.dur) 이동 누락으로 duration 사라짐 | Low — UI는 null 처리 | best-effort 이동, 다음 browse에서 재backfill |
| 외부 파일 드롭과 내부 카드 드래그가 동일 zone에서 헷갈림 | Medium | 커스텀 MIME `application/x-fileserver-move`로 분리 |
| 큰 트리(폴더 수천 개) 초기 렌더 지연 | Medium | depth=1 lazy load + 정렬은 백엔드에서 |
| 모바일 햄버거 토글 시 z-index 겹침 (lightbox/오디오) | Low | sidebar z-index 90, lightbox 100 — lightbox가 위 |
| 이동 중 동시 삭제 race | Very Low (단일 사용자) | 200으로 처리, 다음 browse에서 자연 정정 |

## 7. Open Questions
없음.

## 8. Out of Scope
- 폴더 자체 드래그 이동
- 다중 파일 선택
- 키보드 cut/paste
- 트리 검색/필터
- 트리 부분 갱신
