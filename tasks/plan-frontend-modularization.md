# Frontend Modularization — Implementation Plan

> 부모 spec: [`spec-frontend-modularization.md`](./spec-frontend-modularization.md)
> 브랜치: `feature/frontend-modularization`
> 머지 정책: 각 phase 끝나면 `develop`로 머지하지 않고 같은 feature 브랜치 안에서 누적 (큰 리팩터를 한 번에 리뷰).
>
> **Status: implemented (historical)** — 본 plan의 단계·수치(`12개 모듈`, cache-bust `?v=29` 등)는 분할 시점 baseline이며 갱신하지 않는다. 현재 모듈 수는 17개, cache-bust 버전은 `web/index.html`이 단일 출처(작성 시점 `?v=41`).

## 1. 목표 재확인

`web/app.js` 2,408 라인을 12개 ES module로 분해. 동작 변경 0. 각 단계가 그 자체로 동작.

## 2. 의존성 그래프 (수직 슬라이스 순서)

```
[Phase 0] 사전 준비
    │
    ▼
[Phase 1] 토대 (의존 없는 leaf)
  FM-1  state.js  + dom.js + util.js  ← 셋이 함께만 의미가 있음
    │
    ├─────────┬──────────┬──────────┐
    ▼         ▼          ▼          ▼
[Phase 2] 독립 도메인 (병렬 가능, 순서 무관 — 안전 우선 단순한 것부터)
  FM-2  router.js       (URL/view/toolbar wiring — state/dom 의존만)
  FM-3  tree.js         (사이드바 — browse() 호출 외 의존 없음)
  FM-4  settings.js     (모달 — 가장 작고 자족)
  FM-5  convert.js      (TS→MP4 — SSE 패턴 leaf)
    │
    ▼
[Phase 3] URL import 클러스터 (강결합 — 함께)
  FM-6  urlImport.js + urlImportJobs.js  (필요 시 urlImportRow.js 분리)
    │
    ▼
[Phase 4] 큰 도메인 (의존이 가장 많음)
  FM-7  fileOps.js    (upload + delete + rename + dnd + folder create)
  FM-8  browse.js     (browse + render + lightbox + audio)
    │
    ▼
[Phase 5] 마무리
  FM-9  app.js → main.js, index.html script 태그 갱신, ?v=29 bump, 회귀 풀세트
```

### 핵심 의존 사실

- `breadcrumb`는 `attachDropHandlers`(fileOps)를 호출 → **fileOps가 browse보다 먼저 추출되어야 한다**.
- `popstate`/`init`은 `browse()`·`syncToolbarUI()`·`loadTree()`·`bootstrapURLJobs()` 호출 → 마지막 단계까지 `app.js` 본체에 init 코드가 남는다.
- `urlImport` ↔ `urlImportJobs`는 양방향 호출 (jobs는 row 렌더 헬퍼 import, urlImport는 subscribe/cancel import). 순환이 실제로 발생하면 **`urlImportRow.js`** 로 row 헬퍼를 분리. spec의 "12개 파일" 목표 대비 +1로 13개가 되더라도 순환 회피가 우선.
- `tree.js`의 `highlightTreeCurrent`는 `browse()`에서 호출됨 → tree 추출 시 browse가 tree에서 import해야 함. 이는 단방향이므로 안전.
- `convert.js`·`urlImport.js`는 작업 완료 후 `loadBrowse`(== `browse(currentPath, false)`) 재호출 → browse를 import. 단방향.

---

## 3. Phase 0 — 사전 준비

### FM-0: 브랜치 생성 + 베이스라인 회귀
**작업**
- `git checkout -b feature/frontend-modularization` (현재 `develop`에서)
- 현재 `app.js` 동작 베이스라인 캡처: 브라우저로 spec §5.2 핵심 5개 (browse, lightbox, upload, urlImport, tree) 1분 smoke 실행 — 정상 동작 확인
- `go test ./...` `go vet ./...` 그린 확인

**완료 기준**
- 브랜치 생성됨, 베이스라인 smoke 결과 메모 (issue tracker 또는 PR draft에)

---

## 4. Phase 1 — 토대 (state / dom / util)

### FM-1: state.js + dom.js + util.js 추출
**왜 한 PR에 묶나?** 셋 모두 의존이 없고 다른 모든 모듈이 이 셋만 import. 따로 추출하면 임시 import 경로가 두 번 바뀌어 노이즈만 늘어난다.

**작업**
1. `web/state.js` 생성 — app.js 라인 3–21, 86–96 이동.
   - `let` 변수들에 setter 함수 추가: `setCurrentPath`, `setAllEntries`, `setImageEntries`, `setVideoEntries`, `setVisibleFilePaths`, `setLbIndex`, `setPlaylist`, `setPlaylistIndex`, `setDragSrcPath`, `setDragSrcPaths`.
   - `view`, `selectedPaths`는 mutable export (Set/Object 직접 mutation).
   - 상수: `SORT_VALUES`, `TYPE_VALUES`, `CLIP_MAX_BYTES`, `CLIP_MAX_DURATION_SEC`, `TREE_INIT_DEPTH`, `DND_MIME`.
2. `web/dom.js` 생성 — app.js 라인 24–82를 `export const $ = { breadcrumb, fileList, ... }`로 변환.
   - HTML id가 `kebab-case` → 키는 `camelCase`.
3. `web/util.js` 생성 — `iconFor`, `formatSize`, `formatDuration`, `esc`, `splitExtension`, `parentDir`, `rewritePathAfterFolderRename` 이동.
4. `app.js` 본체 상단에 `import { ... } from './state.js'` 등 추가. 기존 정의 부분은 삭제.
5. `app.js` 내부 `let currentPath = ...` 재할당을 `setCurrentPath(...)`로 치환. 다른 `let` 재할당도 동일.
6. `index.html`의 `<script src="/app.js?v=28">` → `<script type="module" src="/app.js?v=29">`로 변경 (확장자 .js 유지, type=module만 추가; rename은 FM-9에서).

**파일/라이브러리**
- 생성: `web/state.js`, `web/dom.js`, `web/util.js`
- 수정: `web/app.js`, `web/index.html`

**완료 기준**
- 브라우저 콘솔 에러 0건.
- spec §5.2 회귀 체크리스트 **전체** 통과 (이번 단계가 가장 위험 — 모든 모듈 행동의 기반).
- `app.js`의 `let` 재할당은 모두 setter 경유.
- `app.js` 어디에도 `document.getElementById` 없음 (`dom.js`로 모두 이동).

**검증 단계**
1. `go test ./... && go vet ./...` 그린.
2. `go run ./cmd/server`, 브라우저 hard reload (Ctrl+F5).
3. 콘솔 0 에러.
4. §5.2 체크리스트 전체.

**리스크**
- `let` → setter 변환에서 누락 시 typo가 즉시 안 잡힘 (런타임 에러 = ReferenceError로 콘솔에 뜸 → 발견 가능).
- 모듈 모드는 자동 strict — 기존 코드에 implicit global이 있으면 깨짐. `'use strict';` 이미 선언되어 있어 차이 없을 가능성 큼.

---

## 5. Phase 2 — 독립 도메인 (작은 leaf부터)

각 task는 별도 커밋. 추출 범위만 다르고 패턴 동일:
- 함수·상수 이동 → `import { ... } from './state.js' / './dom.js' / './util.js'`
- 다른 모듈에서 호출되는 함수만 `export`
- `app.js`에서 `import` 추가, 기존 정의 삭제
- 회귀 체크 (해당 도메인 + smoke)

### FM-2: router.js
**출처**: app.js 라인 98–130 (URL ↔ view), 2253–2272 (Toolbar)
**export**: `readViewFromURL`, `syncURL`, `syncToolbarUI`, `wireToolbar` (toolbar 리스너 등록 함수 — 신규 함수로 추출)
**의존**: `state` (view, currentPath, SORT_VALUES, TYPE_VALUES, setCurrentPath), `dom`, `browse` (popstate에서 호출)
**주의**: 현재 toolbar 리스너는 module top-level이 아니라 인라인 코드. `wireToolbar()`로 함수화해 `main`에서 호출.
**popstate**도 `wireRouter()` 같은 함수로 묶어 `main`에서 호출.

**완료 기준**
- 정렬/검색/타입 toolbar 동작.
- URL ↔ widget 양방향 동기화.
- 뒤로가기/앞으로가기 동작.
- 기본값 (`name:asc`/빈/`all`) URL에서 생략 확인.

### FM-3: tree.js
**출처**: app.js 2057–2251 (tree + sidebar toggle)
**export**: `loadTree`, `highlightTreeCurrent`, `syncSidebarSticky`, `setSidebarOpen`, `wireTree` (init용)
**의존**: state (currentPath, TREE_INIT_DEPTH), dom, util (esc, iconFor), browse (toggleNode가 browse 호출)

**완료 기준**
- 초기 depth 2 트리 표시.
- chevron lazy 확장.
- 현재 경로 highlight.
- 모바일 햄버거 토글.

### FM-4: settings.js
**출처**: app.js 2274–2397
**export**: `openSettingsModal`, `wireSettings` (cancel/confirm 리스너 등록)
**의존**: state (없음), dom, util (esc)
**주의**: 가장 자족적. 다른 모듈이 settings를 import하지 않음 (settings는 `/api/settings` 호출만).

**완료 기준**
- 모달 오픈, 값 표시, 저장 → 후속 import에 반영.
- 검증 에러 표시.

### FM-5: convert.js
**출처**: app.js 1583–1778
**export**: `openConvertModal`, `wireConvert` (init용 — confirm/cancel 리스너)
**의존**: state, dom, util, browse (작업 후 reload)
**주의**: SSE consumer는 이 모듈이 자체 `consumeSSE`를 가질지, urlImport 추출 시 공통화할지 판단. 현재는 두 곳에 별도 `consumeSSE`/`handleConvertSSEEvent`가 있음. **이번 단계에선 그대로 둠**(공통화는 spec 범위 밖 — follow-up).

**완료 기준**
- "모든 TS 변환" 버튼 + 다중 선택 변환 모달.
- SSE 진행 표시.
- 원본 삭제 옵션.

---

## 6. Phase 3 — URL import 클러스터

### FM-6: urlImport.js + urlImportJobs.js (+ 필요 시 urlImportRow.js)
**출처**: app.js 717–1273 + 1275–1581
**왜 함께?** 두 섹션이 양방향 함수 호출. 순차로 추출하면 import 그래프가 일시적으로 깨진다.

**작업 순서**
1. 두 섹션 모두 `app.js`에 남긴 채 `urlImport.js`를 만들어 라인 717–1273 이동.
2. `urlImportJobs.js`를 만들어 1275–1581 이동.
3. 컴파일·로드 테스트. 순환 import 발생하면:
   - `urlImportRow.js` 신규 생성 → `setRowStatus`, `ensureURLRow`, `applyURLStateToRow`, `URL_ERROR_LABELS` 이동.
   - 두 모듈 모두 `urlImportRow`만 import → 순환 해소.

**export 후보**
- `urlImport.js`: `openURLModal`, `submitURLImport`, `wireURLImport` (badge 클릭/모달 버튼 리스너)
- `urlImportJobs.js`: `bootstrapURLJobs`, `subscribeToJob`, `cancelURLAt`, `cancelBatchAll`, `dismissBatch`, `dismissAllFinishedBatches`, `updateURLBadge`
- (분리 시) `urlImportRow.js`: `URL_ERROR_LABELS`, `ensureURLRow`, `setRowStatus`, `applyURLStateToRow`, `applyJobSnapshotToBatch`

**완료 기준**
- 새로고침 후 진행 중 잡 복원 (`bootstrapURLJobs`).
- 모달 닫아도 다운로드 지속, 배지 표시.
- 개별/배치/전체 cancel·dismiss.
- HLS playlist 가져오기.
- 진행 throttle (1 MiB / 250 ms) 변경 없음 — 코드 grep으로 임계값 상수 미변경 확인.

---

## 7. Phase 4 — 핵심 도메인

### FM-7: fileOps.js
**출처**: app.js 596–715 (upload + folder create), 1780–1801 (delete), 1802–1907 (dnd), 1908–2017 (rename)
**왜 합치나?** 모두 "파일 mutation" 도메인 + 서로의 헬퍼 호출 (rename → loadBrowse, dnd → loadBrowse, delete → loadBrowse). 분리 시 4개 작은 모듈 + 동일한 import 그래프.
**export**: `uploadFiles`, `openFolderModal`, `wireFolderCreate`, `deleteFile`, `deleteFolder`, `attachDragHandlers`, `attachDropHandlers`, `canDropMoveTo`, `moveFile`, `moveFiles`, `openRenameModal`, `wireRename`, `wireUpload`, `isExternalFileDrag`
**의존**: state (currentPath, selectedPaths, dragSrcPath/s, DND_MIME), dom, util (splitExtension, parentDir, esc, rewritePathAfterFolderRename), browse (loadBrowse 재호출)

**완료 기준**
- 업로드 (드래그 + 클릭 + 진행률).
- 폴더 생성, validateName 동등 동작.
- rename: 확장자 고정, dotfile carveout, case-only.
- 삭제: 파일/폴더 (재귀).
- D&D: 다중 선택 → 사이드바/breadcrumb 폴더 이동.
- 외부 OS 파일 vs 내부 D&D 구분 정상.

**리스크**
- ~330 LoC로 큰 편. spec 한도 내지만 추후 분할 고려.

### FM-8: browse.js
**출처**: app.js 132–595 (browse + render + lightbox + audio)
**왜 합치나?** lightbox/audio 모두 `handleClick(entry)`에서 분기 호출. 추출하면 작은 모듈 2개 + 동일 import 그래프. 460 LoC로 단일 모듈 OK.
**export**: `browse` (= 기존 함수), `renderView`, `applyView`, `isClip`, `visibleTSPaths`, `openLightboxImage`, `openLightboxVideo`, `playAudio`, `wireBrowse` (lightbox 닫기/prev/next + audio 자동 다음 곡 등 리스너 등록)
**의존**: state, dom, util, fileOps (attachDragHandlers, attachDropHandlers), tree (highlightTreeCurrent), router (이때 router는 이미 추출됨)

**중요**: `loadBrowse` 재호출 패턴. fileOps/urlImport/convert가 모두 import. `browse(currentPath, false)`로 동일.

**완료 기준**
- spec §5.2 **전체** 체크리스트 — 이 단계가 사실상 마지막 큰 추출.

---

## 8. Phase 5 — 마무리

### FM-9: app.js → main.js + 검증
**작업**
1. `app.js`에 남은 init 코드만 `main.js`로 이름 변경 (`git mv`).
2. `index.html`의 `<script type="module" src="/app.js?v=29">` → `<script type="module" src="/main.js?v=29">`.
3. main.js 내부에서 모든 wiring 함수 호출:
   ```js
   wireToolbar();
   wireRouter();          // popstate
   wireTree();
   wireFolderCreate();
   wireUpload();
   wireRename();
   wireURLImport();
   wireConvert();
   wireSettings();
   wireBrowse();          // lightbox/audio listeners
   readViewFromURL();
   syncToolbarUI();
   browse(initPath, false);
   loadTree();
   bootstrapURLJobs();
   ```
4. 모든 모듈 상단에 한 줄 요약 주석 추가:
   ```js
   // browse.js — 디렉토리 조회·렌더·lightbox·오디오 플레이어
   ```
5. spec §5.2 회귀 체크리스트 **전체** + 두 브라우저 (Chrome, Firefox).
6. `git diff --stat`이 `app.js` 삭제 + 12개 신규 파일로 정리되었는지 확인.

**완료 기준 (spec §7과 동일)**
- [ ] `web/`에 12개 .js 파일 (`main.js` 포함, `app.js` 삭제됨).
- [ ] index.html `<script type="module" src="/main.js?v=29">`.
- [ ] 각 모듈 ≤ 600 LoC (`wc -l web/*.js`).
- [ ] 브라우저 콘솔 에러 0건.
- [ ] `go test ./...` `go vet ./...` 통과.
- [ ] §5.2 회귀 체크리스트 전체 통과.
- [ ] 의존 그래프 순환 없음 (수동 또는 `npx madge --circular web/main.js` 1회 — 도구 사용은 일회성, package.json 추가 안 함).
- [ ] 모든 신규 파일 상단 한 줄 요약 주석.

---

## 9. 체크포인트

각 phase 끝에 다음 검증을 실행하고, 통과 후에만 다음 phase 진입:

| Phase | 체크포인트 |
|---|---|
| Phase 1 (FM-1) | spec §5.2 **전체** — 모든 모듈의 토대가 바뀌므로 가장 신중. |
| Phase 2 각 task | 해당 도메인 회귀 + smoke 5개. |
| Phase 3 (FM-6) | URL import 전체 회귀 (모달, 배지, 새로고침 복원, HLS, cancel/dismiss). |
| Phase 4 FM-7 | upload/delete/rename/dnd 전체. |
| Phase 4 FM-8 | spec §5.2 **전체** + Chrome+Firefox. |
| Phase 5 (FM-9) | spec §7 acceptance criteria 전부. |

체크포인트 실패 시 **그 phase 안에서 fix**. 그 다음 phase로 넘어가지 않음.

---

## 10. 위험 / 우회

| 위험 | 영향 | 우회 |
|---|---|---|
| FM-1에서 setter 누락 | implicit global 또는 ReferenceError | 모듈 모드는 자동 strict, 콘솔에서 즉시 발견 |
| FM-6 순환 import | export `undefined` (런타임만 발견) | `urlImportRow.js` 분리 (12 → 13 모듈) |
| 모듈 캐싱 (브라우저) | 변경 안 반영 | 단계마다 hard reload (Ctrl+F5), 최종 FM-9에서 `?v=29` bump |
| `<script type="module">`은 자동 defer | DOM 준비 시점 변동 | 기존 코드도 `</body>` 직전 `<script>` — 사실상 동일 시점 |
| 큰 PR 리뷰 부담 | 머지 지연 | 단계별 커밋, 커밋 메시지에 단계 명시 (`refactor(web): FM-2 router.js 추출`) |
| 모듈 분리로 dead code 노출 | export 안 한 함수 = 사실상 미사용 표시 | 각 추출 시 호출 위치 grep 확인 |

---

## 11. 추정

대략 1–2일 작업 (단일 사용자, 검증 포함). 가장 큰 비중은 **수동 회귀 체크리스트 반복** — 자동화 없으니 어쩔 수 없음.
