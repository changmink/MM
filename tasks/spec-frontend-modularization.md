# Spec: 프론트엔드 모듈 분할 (web/app.js 분해)

> 부모 SPEC: [`/SPEC.md`](../SPEC.md). 이 문서는 **§2~§3 전반의 프론트엔드 구현 구조**만 다룬다 — 제품 행동·API·SSE 이벤트 스키마는 변경하지 않는다.
>
> **Status: implemented** — 12개 spec 목표 + 후속 5개(`convertImage` / `convertWebp` / `sseConvertModal` / `modalDismiss` / `dragSelect`) 추가 분할로 총 17개 모듈 머지 완료.
>
> **Note:** 본 spec은 historical record. 본문에 등장하는 "12개 .js 파일" 수치(§3.1, §7 Acceptance Criteria 포함)와 cache-bust 버전(`?v=29`)은 분할 시점 baseline이며 갱신하지 않는다. 현재 모듈 수는 17개, 현재 cache-bust 버전은 `web/index.html`이 단일 출처(작성 시점 `?v=41`).

## 1. Objective

`web/app.js`가 단일 파일 2,408 라인으로 비대해져 변경·리뷰·국소 추적이 모두 비싸졌다. 19개의 `// ── X ─` 섹션이 이미 자연스러운 도메인 경계를 그리고 있으므로, 이를 **번들러 없이 브라우저 네이티브 ES Modules**로 분리한다.

**목표**
- 단일 책임 파일들로 분할하여 각 모듈을 ~600라인 이하로 유지.
- `splitExtension`·view state·SSE throttle 등 서버와 계약된 부분의 위치를 명확히 한다.
- 신규 기여자가 "이 동작을 고치려면 어디 파일?" 질문에 5초 안에 답할 수 있다.

**Non-goals (이번 작업에서 절대 안 함)**
- 동작 변경. 발견된 버그는 별도 follow-up 이슈로 분리.
- 빌드 도구·번들러·트랜스파일러·타입 시스템 도입.
- 신규 기능 추가, 리팩토링 김에 끼워넣는 청소.
- CSS/HTML 구조 변경 (모듈 경계와 무관).
- 자동 테스트 인프라 도입 (별도 spec에서 평가).

**Target user:** 이 코드베이스를 유지보수하는 개인 개발자(본인).

---

## 2. Strategy

### 2.1 모듈 시스템

브라우저 네이티브 **ES Modules**.
- `index.html`은 `<script type="module" src="/main.js?v=29"></script>` 한 줄.
- 모든 모듈은 `web/` 직속에 평면 배치 (`web/state.js`, `web/dom.js`, …). 서브디렉토리 없음 — 12개 안팎의 파일이라 깊이가 불필요.
- `import`/`export`는 **named only**. default export 금지 (grep 가능성↓).
- 상대 경로 `import './state.js'` 사용. 확장자 명시 (브라우저 ESM 규칙).

### 2.2 cache busting

`index.html`의 `?v=N` 쿼리는 **entry 파일에만** 부착 (`/main.js?v=29`). 내부 import는 상대 경로 그대로. 브라우저가 entry 파일을 새로 받으면 의존 그래프도 fresh fetch한다 (Chrome/Firefox/Edge 모두). 만약 캐시 이슈가 발생하면 `index.html`의 단일 버전 숫자만 올린다.

> **Trade-off:** 일부 환경에서 모듈 단위 캐싱이 더 보수적일 수 있다. 그 경우 임시 우회로 hard reload (Ctrl+F5)를 안내한다. 의존성 트리 전체에 `?v=` 부착은 수작업 비용이 너무 커서 제외.

### 2.3 공유 상태

`state.js`에 mutable export. `let` 변수는 setter 함수와 함께 export.

```js
// state.js
export let currentPath = '/';
export function setCurrentPath(p) { currentPath = p; }

export const view = { sort: 'name:asc', q: '', type: 'all' };
export const selectedPaths = new Set();
// ...
```

- 객체/Set/Array는 직접 export하고 mutation으로 갱신 (현재 코드와 동등 의미).
- 원시값 `let`은 반드시 setter 경유 — 다른 모듈에서 직접 재할당 불가.
- 이벤트 버스/observer 패턴 도입하지 않음 (스코프 초과).

### 2.4 DOM 참조

`dom.js`에서 한 번에 모두 조회하여 단일 객체로 export.

```js
// dom.js
export const $ = {
  breadcrumb:    document.getElementById('breadcrumb'),
  fileList:      document.getElementById('file-list'),
  // ... (84줄 → camelCase 키)
};
```

- 모든 모듈은 `import { $ } from './dom.js'`로 사용.
- ID는 `kebab-case` (HTML), 키는 `camelCase` (JS) — 변환 규칙 일관.
- HTML이 module 스크립트보다 먼저 파싱되므로 `getElementById`는 즉시 안전 (`<script type="module">`은 기본 defer).

---

## 3. Project Structure

### 3.1 최종 `web/` 레이아웃

```
web/
  index.html          (수정: <script type="module" src="/main.js?v=29">)
  style.css           (변경 없음)
  main.js             ← 신규 entry, init/wiring
  state.js            ← state + 상수
  dom.js              ← DOM refs $
  util.js             ← 작은 helper
  router.js           ← URL ↔ view + popstate + toolbar wiring
  browse.js           ← browse + render + lightbox + audio
  fileOps.js          ← upload + delete + rename + dnd(move) + folder create
  urlImport.js        ← URL import 모달 + SSE consumer
  urlImportJobs.js    ← 백그라운드 잡 부트스트랩 + subscribe
  convert.js          ← TS→MP4 모달 + SSE consumer
  tree.js             ← 사이드바 트리 + 모바일 토글
  settings.js         ← 설정 모달
```

총 **12개 .js 파일** (entry 포함). `app.js`는 작업 마지막 단계에서 삭제한다.

### 3.2 모듈별 책임 + 라인 수 (대략)

| 모듈 | 출처 (현재 app.js 라인) | 예상 LoC | 핵심 export |
|---|---|---|---|
| `state.js` | 3–21, 86–96 | ~110 | `currentPath`, `view`, `selectedPaths`, `playlist`, `lbIndex`, `dragSrcPath(s)`, `imageEntries`, `videoEntries`, `visibleFilePaths`, `allEntries`, `playlistIndex`, 상수 (`SORT_VALUES`, `TYPE_VALUES`, `CLIP_MAX_BYTES`, `CLIP_MAX_DURATION_SEC`, `TREE_INIT_DEPTH`, `DND_MIME`), 그리고 setter들 |
| `dom.js` | 24–82 | ~85 | `$` (단일 객체) |
| `util.js` | 1920–1925 (`splitExtension`), 2011–2017, 2020–2055, 1803–1807 (`parentDir`) | ~70 | `iconFor`, `formatSize`, `formatDuration`, `esc`, `splitExtension`, `parentDir`, `rewritePathAfterFolderRename` |
| `router.js` | 98–130, 2253–2272 | ~70 | `readViewFromURL`, `syncURL`, `syncToolbarUI`, `wireToolbar`, popstate listener |
| `browse.js` | 132–595 | ~480 | `browse`, `renderView`, `applyView`, `isClip`, `visibleTSPaths`, 그리드/테이블 빌더, 선택 제어, lightbox, audio player |
| `fileOps.js` | 596–715, 1780–1801, 1802–1907, 1908–2017 | ~330 | `uploadFiles`, `openFolderModal`, `deleteFile`, `deleteFolder`, `attachDragHandlers`, `attachDropHandlers`, `moveFile(s)`, `openRenameModal`, `submitRename`, `splitExtension` 사용 |
| `urlImport.js` | 717–1273 | ~580 | `openURLModal`, `closeURLModal`, `submitURLImport`, `consumeSSE`, `handleSSEEvent`, `updateURLBadge`, `URL_ERROR_LABELS` |
| `urlImportJobs.js` | 1275–1581 | ~310 | `bootstrapURLJobs`, `subscribeToJob`, `applyJobSnapshotToBatch`, `cancelURLAt`, `cancelBatchAll`, `dismissBatch`, `dismissAllFinishedBatches` |
| `convert.js` | 1583–1778 | ~200 | `openConvertModal`, `submitConvert`, `handleConvertSSEEvent`, `CONVERT_ERROR_LABELS` |
| `tree.js` | 2057–2251 | ~210 | `loadTree`, `buildTreeNode`, `toggleNode`, `highlightTreeCurrent`, `syncSidebarSticky`, `setSidebarOpen` |
| `settings.js` | 2274–2397 | ~130 | `openSettingsModal`, `submitSettings`, `SETTINGS_*` 상수 |
| `main.js` | 2399– (Init 섹션) | ~80 | DOMContentLoaded init, 핸들러 wiring |

각 모듈 ≤ 600 라인을 목표. 가장 큰 `urlImport.js`가 580으로 경계 직전. 추후 더 분할은 별도 작업.

### 3.3 의존 그래프

```
main.js
  ├─ state.js
  ├─ dom.js
  ├─ router.js → state, dom, browse
  ├─ browse.js → state, dom, util, fileOps (rename/delete/dnd attach)
  ├─ fileOps.js → state, dom, util, browse (loadBrowse 재호출)
  ├─ urlImport.js → state, dom, util, urlImportJobs, browse (loadBrowse)
  ├─ urlImportJobs.js → state, dom, util, urlImport (rendering helpers)
  ├─ convert.js → state, dom, util, browse (loadBrowse)
  ├─ tree.js → state, dom, util, browse (browse 함수 호출)
  └─ settings.js → state, dom
```

**규칙: 순환 import 절대 금지.** `urlImport.js` ↔ `urlImportJobs.js`는 양방향 참조가 자연스러우므로 한쪽이 다른 쪽의 헬퍼 함수만 받도록 설계 (jobs → import 의 rendering helper만, 반대 금지). 이를 위해 `setRowStatus`·`ensureURLRow`·`applyURLStateToRow`는 `urlImport.js`에 두고 `urlImportJobs.js`가 import한다. 실수로 순환이 생기면 브라우저 콘솔에 에러는 안 뜨고 일부 export가 `undefined`가 되니 테스트 시점에 발견된다.

### 3.4 마이그레이션 순서 (커밋 단위)

각 단계가 **그 자체로 정상 동작**하도록 진행한다 (그린 → 그린).

1. `state.js` + `dom.js` + `util.js` 추출. `app.js` 상단을 빈 import로 교체. 테스트: 모든 페이지 행동 수동 확인.
2. `router.js` 추출. popstate / toolbar / URL sync 동작 확인.
3. `tree.js` 추출.
4. `settings.js` 추출.
5. `convert.js` 추출.
6. `urlImport.js` + `urlImportJobs.js` 추출 (둘 동시 — 강결합).
7. `fileOps.js` 추출.
8. `browse.js` 추출. 남은 `app.js`는 init 코드만.
9. `app.js` → `main.js`로 이름 변경. `index.html`의 script 태그를 `<script type="module" src="/main.js?v=29">`로 교체. 이전 `app.js?v=28` 참조 제거.

각 단계 마지막에 한 번씩 **§5 회귀 체크리스트** 실행.

---

## 4. Code Style

- **언어:** 'use strict' 모듈 모드 (ES module은 자동 strict). 파일 상단의 `'use strict';` 선언은 제거.
- **export:** named only. `export function foo() {}` 또는 `export { foo, bar }` 끝.
- **import:** 관련 모듈끼리 그루핑, 빈 줄로 구분. `import { $ } from './dom.js'` 패턴 일관.
- **이름:** 함수·변수는 camelCase, 상수는 UPPER_SNAKE_CASE, 모듈 파일명은 camelCase + `.js`.
- **주석:** 현재 app.js의 영문 주석을 그대로 유지. 새로 추가하는 주석은 한글. 모듈 상단 한 줄 요약 주석 추가 (`// browse.js — 디렉토리 조회·렌더·라이트박스·오디오 플레이어`).
- **JSDoc 금지** — 기존 코드에도 없음.
- **모듈 내부 헬퍼**는 `export` 없이 둠. 다른 모듈이 필요하면 그때 export.
- **부수 효과 코드(addEventListener 등)**는 `main.js`에서만 호출. 모듈은 함수만 export하고 자기 스스로 wiring하지 않음. 단, `state.js`·`dom.js`처럼 정의 자체가 동작인 경우는 예외.

---

## 5. Testing Strategy

### 5.1 자동 검증

- `go test ./...` 통과 — 백엔드 변경 0이지만 사고 방지로 매 커밋 실행.
- `go vet ./...` 통과.
- JS 단위/e2e 테스트는 **이 spec의 범위 밖**. 별도 평가 후 결정.

### 5.2 수동 회귀 체크리스트

각 마이그레이션 단계 완료 시 브라우저 (Chrome 최신 + Firefox 최신)에서:

**브라우저 콘솔**
- [ ] 페이지 로드 시 `Uncaught ReferenceError`, `SyntaxError`, `Failed to fetch dynamically imported module` 0건.
- [ ] 모든 import가 실패 없이 해결.

**Browse / 라우팅**
- [ ] `/` 로드 → 파일 목록 렌더, breadcrumb 표시.
- [ ] 폴더 클릭 → URL `?path=/sub` push, 목록 갱신.
- [ ] 뒤로가기 → 이전 폴더 복원, toolbar 동기화.
- [ ] URL 직접 입력 (`?path=/x&sort=size:desc&type=video&q=test`) → 모든 위젯 동기화 + 필터 반영.
- [ ] 정렬·검색·타입 변경 → URL `replaceState` (히스토리 안 쌓임).
- [ ] 기본값(`name:asc`/빈 검색/`all`)은 URL에서 생략됨.

**렌더링**
- [ ] 이미지 그리드, 동영상 그리드, 기타 테이블 모두 렌더.
- [ ] 움짤 필터: GIF + 50MiB 이하 30초 이하 동영상만 노출.
- [ ] 썸네일 lazy 로드 동작.
- [ ] 동영상 duration 오버레이 표시.

**Lightbox / 오디오**
- [ ] 이미지 클릭 → lightbox, ←→키로 이동 (visible set 한정).
- [ ] 동영상 클릭 → lightbox 비디오.
- [ ] 음악 클릭 → 하단 플레이어, playlist 표시, 자동 다음 곡.
- [ ] 필터로 가려진 항목은 prev/next에 안 잡힘.

**파일 작업**
- [ ] 업로드 (드래그 + 클릭), 진행률 표시.
- [ ] 폴더 생성, 이름 규칙 위반 에러 표시.
- [ ] rename: 파일 확장자 고정, dotfile carveout, case-only rename.
- [ ] 삭제: 파일/폴더, 폴더는 재귀.
- [ ] 다중 선택 → 사이드바 폴더로 D&D 이동.
- [ ] 외부 OS 파일 D&D는 업로드존, 내부 D&D는 폴더로 이동 (구분 정상).

**URL Import**
- [ ] 모달 오픈, 멀티라인 URL 제출.
- [ ] SSE 진행 이벤트 표시 (1 MiB / 250 ms throttle).
- [ ] 모달 닫아도 다운로드 계속 (배경 잡), 헤더 배지 표시.
- [ ] 새로고침 후 진행 중 잡 복원 (`bootstrapURLJobs`).
- [ ] HLS playlist 가져오기 정상.
- [ ] 개별/배치/전체 dismiss·cancel.

**TS → MP4 변환**
- [ ] 다중 TS 선택 후 변환, SSE 진행, 원본 삭제 옵션.

**설정 모달**
- [ ] max MiB / timeout 분 저장 → 다음 import에 반영.

**사이드바 트리**
- [ ] 초기 로드 시 depth 2까지 표시.
- [ ] chevron 클릭 → lazy 확장.
- [ ] 현재 경로 highlight.
- [ ] 모바일 토글 (햄버거).

### 5.3 빠른 smoke 명령

```bash
go test ./... && go vet ./... && go run ./cmd/server
```

서버 띄우고 위 체크리스트 핵심 5개 (browse, lightbox, upload, urlImport, tree)만 1분 안에 훑는다.

---

## 6. Boundaries

### Always do
- 한 번에 한 단계만 추출하고 그 단계가 그린일 때 다음 단계 시작 (§3.4 순서).
- 추출 시 함수·상수의 **이름·시그니처·동작 동등성** 유지. 리네이밍 금지.
- ES module 표준만 사용 (`import`/`export`/named exports).
- `splitExtension`·`fileExtension`(서버) 규칙 동기화 — `util.js`로 옮길 때 동작 비교.
- progress emit 임계값 1 MiB / 250 ms 그대로.
- URL 쿼리스트링이 `sort`/`q`/`type`의 단일 출처임을 유지 (`router.js`).
- 신규 모듈은 `// 모듈명 — 한 줄 요약` 주석으로 시작.

### Ask first
- 모듈 경계 변경 (위 표와 다른 분할).
- 12개 파일을 더 잘게 쪼개거나 합치기.
- entry 파일 이름이 `main.js`가 아닌 다른 것 (예: `index.js`, `app.module.js`).
- 빌드 도구 도입 (Vite, esbuild, rollup 등) — CLAUDE.md "의존성 빌드 없음" 위배 시.
- TypeScript / JSDoc type 시스템 도입.
- 발견된 버그 수정을 같은 PR에 포함.

### Never do
- 빌드 도구·번들러·트랜스파일러 도입.
- npm 의존성 추가 (`package.json` 신설 금지).
- 동작 변경 — UI/네트워크/SSE/URL/키 입력 모든 행동이 비트-호환.
- `localStorage` / `sessionStorage` / `IndexedDB` 사용.
- URL 쿼리 외부에 view state 저장.
- 서버 `internal/handler/files.go`의 `fileExtension`과 `splitExtension` 규칙 분기.
- progress emit 임계값 변경.
- 한 모달의 ID/HTML을 다른 모달과 합치기.
- 순환 import (A → B → A).
- default export.
- `<script src="...">` (non-module) 와 module 스크립트 혼용.
- IIFE wrapper 추가 (module이 이미 자체 스코프).
- `app.js?v=N`을 다른 파일이 import하기 (entry 외에는 캐시 버스터 부착 금지).

---

## 7. Acceptance Criteria

- [ ] `web/`에 12개 `.js` 파일 (`main.js` 포함, `app.js` 삭제됨).
- [ ] `index.html`이 `<script type="module" src="/main.js?v=29">` 단일 라인으로 entry 로드.
- [ ] 각 모듈 ≤ 600 LoC.
- [ ] 브라우저 콘솔에 에러 0건.
- [ ] `go test ./...` `go vet ./...` 통과.
- [ ] §5.2 수동 회귀 체크리스트 모든 항목 통과.
- [ ] 의존 그래프에 순환 없음 (수동 검증 또는 madge 같은 도구 1회 사용).
- [ ] `git diff --stat`이 `web/app.js` 삭제 + 신규 12개 파일 추가로 깔끔히 표현됨.
- [ ] 모든 신규 파일 상단에 한 줄 요약 주석.

---

## 8. Out of Scope (별도 spec 필요 시 분리)

- JS 자동 테스트 인프라 (Vitest, Playwright, ...).
- TypeScript 마이그레이션.
- CSS 모듈화 (`web/style.css` 910 라인은 별도 평가).
- HTML 템플릿화 (현재 inline string으로 그리드/테이블 생성).
- 접근성·성능 개선 (별도 작업).
- 발견된 모든 코드 스멜 (refactor 요구 모음 → 별도 todo).
