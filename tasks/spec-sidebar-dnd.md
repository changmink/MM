# Spec: Folder Sidebar + Drag-to-Move

> 부모 SPEC: [`/SPEC.md`](../SPEC.md). 이 문서는 그중 **2.5 프론트엔드 UI**와 **5 API Design**을 확장한다.

## 1. Objective

현재 폴더 탐색은 메인 영역 상단의 "폴더" 섹션과 breadcrumb로만 가능해서, 깊이 들어간 폴더로 옮기거나 다른 형제 폴더로 점프하기가 번거롭다. 또 파일을 다른 폴더로 옮기려면 다운로드→재업로드를 해야 한다.

**목표:**
1. 데스크탑 좌측에 **폴더 트리 사이드바**를 두어 어느 화면에서든 폴더 구조 한눈에 파악 + 한 번의 클릭으로 이동
2. 파일 카드/행을 **사이드바 폴더 또는 메인 영역 내 폴더**로 **드래그하여 이동**

**Target user:** 단일 사용자(개인). 데스크탑 + 모바일 브라우저.

**Success criteria:**
- 데스크탑(>600px): 좌측 폴더 트리 항상 표시, 현재 폴더 하이라이트
- 모바일(≤600px): 햄버거 버튼으로 사이드바 오버레이 토글
- 파일 카드/행을 폴더 노드(사이드바) 또는 폴더 카드(메인의 다른 위치)에 드롭하면 백엔드로 이동되고 UI 즉시 반영
- 동일 이름 충돌 시 업로드와 동일하게 `_1`, `_2` suffix 자동 부여
- 썸네일(`.thumb/{name}.jpg`)과 duration 사이드카(`.thumb/{name}.jpg.dur`)도 함께 이동

---

## 2. Scope

### In scope (v1)
- 좌측 폴더 트리 사이드바 (depth=2 초기 로드, 그 아래는 클릭 시 lazy load)
- 메인 영역의 **"폴더" 섹션 제거** (사이드바와 중복)
- 파일 드래그 → 폴더 드롭 → 이동 (썸네일/사이드카 포함)
- 파일 이동 API: `PATCH /api/file?path=` body `{"to": "/dest/dir"}`
- 트리 데이터 API: `GET /api/tree?path=&depth=`
- 모바일 햄버거 토글

### Out of scope (v2 이후)
- **폴더 자체의 드래그 이동** (재귀·충돌 처리 복잡 → 별도 spec)
- 다중 파일 선택 후 일괄 이동
- 키보드 cut/paste
- 트리 검색·필터
- 트리 수동 정렬 (현재는 알파벳)

---

## 3. UX Spec

### 3.1 레이아웃 (데스크탑, >600px)

```
┌──────────────────────────────────────────────────────────────┐
│ Header: 🏠 Media Server   Breadcrumb: 홈/movies   [+ 새 폴더] │
├─────────────┬────────────────────────────────────────────────┤
│ 📁 홈        │  Upload zone                                   │
│  📂 movies   │                                                │
│   📁 2024    │  [이미지 그리드 / 동영상 그리드 / 음악 / 기타] │
│   📁 2025    │                                                │
│  📁 photos   │  ※ "폴더" 섹션은 제거됨                        │
│  📁 music    │                                                │
└─────────────┴────────────────────────────────────────────────┘
```

- 사이드바 폭: 240px (CSS 변수 `--sidebar-w`)
- 사이드바 배경: `var(--surface)`, 우측 1px border
- 트리 들여쓰기: depth마다 16px
- 노드 hover: `background: rgba(255,255,255,0.05)`
- 현재 폴더 노드: `background: var(--accent)`, `color: white`
- 빈 폴더는 펼치기 chevron 비활성

### 3.2 트리 동작
- 기본 상태: 루트(`/`)와 그 직계 자식(depth=1)까지 펼쳐서 표시
- 폴더 옆 ▶ chevron 클릭 → expand. 첫 expand 시 `GET /api/tree?path=<that>&depth=1`로 lazy load
- 노드 텍스트(이름) 클릭 → 해당 폴더로 `browse()` 이동, breadcrumb·메인 갱신
- 폴더 생성/삭제 시 사이드바 트리에서 해당 부분 갱신 (전체 reload 가능 — 작은 트리는 단순함이 우선)
- 정렬: 이름 알파벳 오름차순 (대소문자 구분 없음)

### 3.3 모바일 (≤600px)
- Header 좌측에 햄버거 버튼 `☰` 추가
- 사이드바는 기본 숨김. 햄버거 클릭 시 좌측에서 슬라이드 오버레이 (z-index 90, 반투명 배경)
- 트리 노드 클릭으로 폴더 이동하면 사이드바 자동 닫힘

### 3.4 드래그 앤 드롭 (파일 이동)

**드래그 시작:**
- 이미지 카드, 동영상 카드, 음악/기타 테이블 행에 `draggable="true"`
- `dragstart`에서 `dataTransfer.setData('application/x-fileserver-move', JSON.stringify({src: entry.path}))`
- 동시에 `dataTransfer.effectAllowed = 'move'`
- 시각: 드래그 중인 요소에 `.dragging` 클래스 (opacity 0.4)

**드롭 대상:**
- 사이드바의 모든 폴더 노드
- breadcrumb의 각 segment (현재 위치 제외)
- (메인 영역엔 폴더 섹션이 없어졌으므로 메인은 드롭 대상 아님)

**드롭 대상 시각화:**
- `dragenter`/`dragover`에서 대상 노드에 `.drop-target` 클래스 (테두리 강조 + 배경 `rgba(79,142,247,0.15)`)
- `dragleave`/`drop`에서 클래스 제거
- 자기 자신이 속한 폴더(이미 그 폴더에 있는 파일)에 드롭 시도 시 시각적 거부(클래스 부여하지 않음) + drop 무시

**업로드 zone과의 구분:**
- 업로드 zone의 `dragover`/`drop`에서 `dataTransfer.types`에 `'Files'`가 있을 때만 처리(외부 OS 파일)
- 내부 카드 드래그는 `'application/x-fileserver-move'` MIME만 가지므로 자연 분리됨

**드롭 후 동작:**
1. `PATCH /api/file?path=<src>` body `{"to": "<destDir>"}` 호출
2. 성공 시 응답의 새 경로로 UI 갱신 (`browse(currentPath, false)` 재호출)
3. 실패 시 `alert('이동 실패: <reason>')`

### 3.5 접근성
- 사이드바 트리 노드는 `<button>` 또는 `<a tabindex="0">`로 키보드 포커스 가능
- chevron은 별도 `<button aria-expanded="true|false">`
- 햄버거 버튼 `aria-label="폴더 메뉴 열기"`, `aria-expanded`
- 드래그앤드롭은 키보드 대체 수단 없음(v2에서 검토). 마우스 사용자 한정 enhancement.

---

## 4. API Design (변경)

### 4.1 신규: `GET /api/tree?path=&depth=`

폴더 전용 재귀 트리. 파일은 포함하지 않는다.

**Query params:**
- `path` (default `/`): 시작 경로
- `depth` (default `1`, max `5`): `path`로부터 몇 단계 자식까지 내려갈지. `1`은 직계 자식만.

**Response 200:**
```json
{
  "path": "/",
  "name": "",
  "children": [
    {
      "path": "/movies",
      "name": "movies",
      "has_children": true,
      "children": [
        { "path": "/movies/2024", "name": "2024", "has_children": false, "children": [] },
        { "path": "/movies/2025", "name": "2025", "has_children": true, "children": null }
      ]
    },
    { "path": "/photos", "name": "photos", "has_children": false, "children": [] }
  ]
}
```
- `has_children`: depth 한도 도달 후에도 자식 폴더 유무를 알려 chevron 렌더링 가능하게 함
- `children: null` → "더 있지만 fetch 안 했음" (lazy load 트리거)
- `children: []` → 진짜 비어있음
- 에러: `404` (path 없음), `400` (invalid path/depth)

### 4.2 신규: `PATCH /api/file?path=`

파일 이동 (이름 변경은 v1 미포함, dest는 디렉토리만).

**Body:**
```json
{ "to": "/movies/2024" }
```

**동작:**
1. `path`로 지정된 파일을 `to` 디렉토리로 이동
2. 동일 이름이 dest에 있으면 업로드와 동일하게 `name_1.ext`, `name_2.ext` 자동 부여 (`createUniqueFile`과 동일 전략)
3. 썸네일·duration 사이드카가 있으면 함께 이동:
   - `<srcDir>/.thumb/<name>.jpg` → `<destDir>/.thumb/<finalName>.jpg`
   - `<srcDir>/.thumb/<name>.jpg.dur` → `<destDir>/.thumb/<finalName>.jpg.dur`
4. 사이드카 이동은 best-effort (실패해도 본체 이동은 성공으로 처리, 로그 남김)

**응답:**
- 성공 `200 OK`:
  ```json
  { "path": "/movies/2024/film.mp4", "name": "film.mp4" }
  ```
- 동일 디렉토리로 이동 (`to`가 이미 src의 부모): `400 {"error": "same directory"}`
- src가 디렉토리: `400 {"error": "cannot move directory"}` (v1 제약)
- dest가 디렉토리 아님 / 미존재: `400 {"error": "invalid destination"}`
- src 미존재: `404`
- traversal: `400 {"error": "invalid path"}`

### 4.3 변경 없음
기존 `browse`, `upload`, `folder`, `file DELETE`, `stream`, `thumb`은 그대로.

---

## 5. Implementation Plan

### Backend (Go)
- `internal/handler/tree.go` 추가 — `handleTree` 핸들러 + 재귀 walker (depth 한도)
- `internal/handler/files.go` 확장 — `handleFile`에 `PATCH` 분기 추가, `moveFile()` 함수
- `internal/media/path.go` 재사용 — dest 검증
- `cmd/server/main.go` 라우터에 `/api/tree` 등록

### Frontend (Vanilla JS)
- `web/index.html` — `<aside id="sidebar">` 추가, 모바일 햄버거 버튼 추가
- `web/style.css` — sidebar 레이아웃, 트리 노드 스타일, drop-target 시각, 모바일 오버레이
- `web/app.js`:
  - `loadTree()` / `renderTree()` / `expandNode()` — 트리 빌드·lazy load
  - `renderFileList()`에서 `dirs` 섹션 제거 (사이드바가 대체)
  - `attachDragHandlers(card, entry)` — 카드/행에 dragstart 핸들러
  - `attachDropHandlers(node, destPath)` — 트리·breadcrumb 노드에 dragover/drop 핸들러
  - `moveFile(src, destDir)` — `PATCH` 호출 + UI 갱신
  - 모바일 토글 로직

### Tests
- 단위:
  - `handler.handleTree` — depth 한도, has_children 정확성
  - `moveFile` — 충돌 시 unique suffix, 사이드카 이동, traversal 거부
- 통합:
  - `PATCH /api/file` happy path + 충돌 + invalid dest

---

## 6. Code Style 예시

```go
// internal/handler/tree.go
type treeNode struct {
    Path        string      `json:"path"`
    Name        string      `json:"name"`
    HasChildren bool        `json:"has_children"`
    Children    []treeNode  `json:"children"` // nil = not loaded; [] = empty
}

func (h *Handler) handleTree(w http.ResponseWriter, r *http.Request) { ... }
```

```js
// web/app.js — 드래그 시작
function attachDragHandlers(el, entry) {
  el.draggable = true;
  el.addEventListener('dragstart', (e) => {
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('application/x-fileserver-move',
      JSON.stringify({ src: entry.path }));
    el.classList.add('dragging');
  });
  el.addEventListener('dragend', () => el.classList.remove('dragging'));
}
```

---

## 7. Boundaries

**항상 (Always)**
- `media.SafePath`로 src/dest 모두 검증 (path traversal 방지)
- 파일 이동은 같은 볼륨 내 `os.Rename` 우선 시도, 크로스 디바이스 에러면 copy+remove fallback
- 사이드카 이동 실패는 본체 성공을 가리지 않음 (로그 남기되 200 반환)

**먼저 물을 것 (Ask first)**
- 폴더 자체 드래그 이동 추가 → v2 별도 spec
- `/api/tree`를 SSE/WebSocket으로 실시간 업데이트 → 단순 polling/refetch가 우선

**절대 안 함 (Never)**
- 사이드바 트리에 파일까지 표시 (트리 비대화)
- 드래그 도중 자동 폴더 펼침 (UX 복잡, v1 제외)
- 이동 시 사용자에게 덮어쓰기 묻기 (자동 suffix로 해결)

---

## 8. Open Questions

없음 (사용자 답변으로 모두 해결).

---

## 9. Branch / Worktree

- Branch: `feature/folder-sidebar-and-dnd`
- Worktree: `C:/Users/chang/Projects/file_server-sidebar-dnd`
- Base: `master` @ `ed9ee94`
