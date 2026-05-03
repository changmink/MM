# Plan: web/browse.js 분해 (Phase 32)

> **Status: ready for next session.**
> 브랜치: `feature/browse-decomposition` — BD-0 commit `afb6051` (spec accepted + Phase 32 entry) 완료, 그 이상 코드 변경 없음.
> Spec: [`spec-browse-decomposition.md`](./spec-browse-decomposition.md) (accepted)
> 추적: [`todo.md` Phase 32](./todo.md)

---

## 다음 세션 작업 시작 절차

1. `git checkout feature/browse-decomposition`
2. `git log --oneline develop..HEAD` — 마지막 BD-N 커밋 위치 확인 (BD-0만 있으면 BD-1부터)
3. `todo.md` Phase 32 미체크(`- [ ]`) 항목부터 본 plan §3 절차 따라 진행
4. 각 BD-N 끝나면:
   - `node --check web/<changed>.js`로 syntax sanity
   - 해당 chromedp e2e 테스트 통과 (`go test ./internal/handler -run "<TestName>" -v`)
   - 단계별 commit (메시지 규약 §4)
5. BD-7(수동 검증) 끝나면 spec Status `merged` 갱신 + todo.md 항목 모두 체크 + 머지 진행

---

## 1. 의존 그래프 (분리 후)

```
main.js
 └─ browse.js (slim, ≤250줄)
     ├─ selection.js          ← bindEntrySelection·setSelected·refreshSelectionUI 등 7
     ├─ visiblePaths.js       ← visibleXxxPaths·selectedVisibleXxxPaths·updateConvertXxxBtn 8
     ├─ cardBuilders.js       ← sectionTitle·buildImageGrid·buildVideoGrid·buildTable
     │   └─ clipPlayback.js   ← attachClipHoverPlayback (buildImageGrid이 GIF/WebP에 호출)
     │   └─ clipPlayback.js   ← isClip (buildVideoGrid이 isClipVideo 판정)
     ├─ clipPlayback.js       ← isClip (browse.applyView이 type=clip/!clip 분류)
     └─ lightbox.js           ← openLightboxImage·openLightboxVideo·playAudio 등 7

dragSelect.js → selection.js  ← syncCardSelectionStates (BD-3 시점에 import 경로 갱신 필수)
visiblePaths.js → clipPlayback.js  ← isClipConvertable (BD-2가 BD-1 export 의존)
```

**단방향 보장**: 분리 모듈 간 사이클 없음. clipPlayback은 leaf (다른 분리 모듈을 import하지 않음).

---

## 2. 의존 순서로 본 BD-N 정렬

각 BD-N은 다음 단계의 사전 조건을 만든다 — 순서대로 진행 권장.

| 순서 | BD | 사전 조건 | export하는 신규 surface |
|---|---|---|---|
| 1 | BD-1 | (BD-0만) | `clipPlayback.js`: `isClip`, `isClipConvertable`, `attachClipHoverPlayback` |
| 2 | BD-2 | BD-1 (`isClipConvertable` import 가능) | `visiblePaths.js`: 8 함수 |
| 3 | BD-3 | (독립) | `selection.js`: 7 함수 (`syncCardSelectionStates` 포함) — **dragSelect.js import 갱신 필수** |
| 4 | BD-4 | BD-1 (`attachClipHoverPlayback`, `isClip` import 가능) | `cardBuilders.js`: 4 함수 |
| 5 | BD-5 | (독립) | `lightbox.js`: 7 함수 + 키보드/click 바인딩 |
| 6 | BD-6 | BD-1~5 모두 | `browse.js` 슬림화 + cache-bust v=42 |
| 7 | BD-7 | BD-6 | 수동 검증 통과, Status merged |

BD-3·BD-5는 다른 BD-N과 독립이라 BD-1·2 다음에 어느 순서로도 가능. BD-4는 반드시 BD-1 이후. BD-6는 모든 모듈 분리 끝난 뒤.

---

## 3. BD-N 상세 절차

### BD-1 · `web/clipPlayback.js` 분리

**영향 파일**:
- 신규: `web/clipPlayback.js`
- 수정: `web/browse.js` (정의 제거 + import 추가)

**이전할 심볼** (`web/browse.js:115-180`):
- `HOVER_CAPABLE` (const, line 119-121)
- `_clipIO` (모듈 상태, line 123)
- `clipIOInstance()` (line 124-138) — private
- `attachClipHoverPlayback(card)` (line 140-159) — export
- `isClip(e)` (line 165-173) — export (서명 보존)
- `isClipConvertable(e)` (line 178-180) — export (BD-2가 import해야)

**검증 — 호출 사이트 (확인된 것)**:
- `web/browse.js:184, 192` — `isClipConvertable` (BD-2까지 browse.js에 남음, 그동안 clipPlayback import)
- `web/browse.js:229, 231` — `applyView`가 `isClip` 사용 (browse.js에 남음)
- `web/browse.js:472` — `buildImageGrid`이 `attachClipHoverPlayback` 호출 (BD-4까지 browse.js에 남음)
- `web/browse.js:495, 529` — `buildVideoGrid`이 `isClip` 사용 (BD-4까지 browse.js)
- 외부 모듈에서 `isClip`·`attachClipHoverPlayback` import: **없음** (grep 확인 완료)

**단계**:
1. `web/clipPlayback.js` 생성 — 6 심볼 named export. 헤더 주석 (역할: SPEC §2.5.6 hover/IO 기반 GIF/WebP 자동재생 throttle).
2. `web/browse.js`에서 6 심볼 정의 삭제 + 상단 import 추가:
   ```js
   import { attachClipHoverPlayback, isClip, isClipConvertable } from './clipPlayback.js';
   ```
3. 기존 `export function isClip` → 유지하되 `export { isClip } from './clipPlayback.js'`로 re-export (외부 surface 보존). **또는** browse.js의 isClip export 제거 (외부 import 없음 확인됨). 권장: re-export 유지 (보수적).

**Acceptance**:
- AC: `node --check web/browse.js web/clipPlayback.js` syntax OK
- AC: `go test ./internal/handler -run "TestWeb_ClipHoverPlayback" -v` (4 시나리오) 통과
- AC: `web/clipPlayback.js` ≤ 80줄

**Risk**:
- 모듈 lifetime IntersectionObserver 인스턴스(`_clipIO`)가 모듈 단위로 한 번만 생성되어야 함 — 이전 시 `let _clipIO = null` 모듈 스코프 유지 필수.
- HOVER_CAPABLE 평가가 import 시점(모듈 로드 시점)에 매체 쿼리를 호출 — 동작 동일 보장.

**Commit 메시지 예시**:
```
refactor(web): clipPlayback 함수군을 web/clipPlayback.js로 분리 (BD-1)

SPEC §2.5.6 GIF/WebP 자동재생 throttle 로직을 단일 책임 모듈로 추출 —
HOVER_CAPABLE / clipIOInstance / attachClipHoverPlayback / isClip /
isClipConvertable. browse.js는 named import 경유 사용, 외부 surface 보존
(isClip re-export). 호출 사이트 변경 0.

chromedp e2e TestWeb_ClipHoverPlayback 4 시나리오 통과.
```

---

### BD-2 · `web/visiblePaths.js` 분리

**영향 파일**:
- 신규: `web/visiblePaths.js`
- 수정: `web/browse.js`

**이전할 심볼** (`web/browse.js:67-110, 182-218`):
- `visibleTSPaths(visible)` — export
- `updateConvertAllBtn(visible)` — private
- `visiblePNGPaths(visible)` — export
- `selectedVisiblePNGPaths(visible)` — export
- `updateConvertPNGAllBtn(visible)` — private
- `visibleClipPaths(visible)` — export
- `selectedVisibleClipPaths(visible)` — export
- `updateConvertWebPAllBtn(visible)` — private

**의존**:
- `clipPlayback.isClipConvertable` (BD-1에서 export됨)
- `state.js`: `selectedPaths`, `view`
- `dom.js`: `$.convertAllBtn`, `$.convertPngAllBtn`, `$.convertWebpAllBtn`

**호출 사이트**:
- `web/browse.js:applyView` 또는 `renderView`이 update*Btn 3개 호출 (line 확인 필요 — 다음 세션)
- 외부 모듈: visible*Paths를 호출하는 외부 import 가능 — grep으로 재확인 필요

**단계**:
1. `web/visiblePaths.js` 생성 — 8 심볼 + clipPlayback import
2. `web/browse.js`에서 8 정의 삭제 + import 추가
3. 외부 import 사이트 갱신 (있다면)

**Acceptance**:
- AC: `node --check`
- AC: `go test ./internal/handler -run "TestWeb_PNGSelect|TestWeb_ClipToWebp" -v` 통과
- AC: `web/visiblePaths.js` ≤ 130줄

**Risk**:
- updateConvertXxxBtn은 toolbar `dataset.paths` 갱신 — 호출 시점·순서 보존

---

### BD-3 · `web/selection.js` 분리

**영향 파일**:
- 신규: `web/selection.js`
- 수정: `web/browse.js`, `web/dragSelect.js` (import 경로 갱신)

**이전할 심볼** (`web/browse.js:335-397`):
- `syncSelectionWithVisible(entries)` — private
- `renderSelectionControls()` — private
- `updateCardSelection(path, selected)` — private
- `syncCardSelectionStates()` — **export (dragSelect.js가 import 중)**
- `refreshSelectionUI()` — private
- `setSelected(path, selected)` — private
- `bindEntrySelection(container, entry)` — private (cardBuilders가 호출 — BD-4 시점)

**`wireBrowse`에서 이전할 부분** (line 691-708):
- `$.selectAllFiles` change listener
- `$.clearSelectionBtn` click listener

**의존**:
- `state.js`: `selectedPaths`, `visibleFilePaths`
- `dom.js`: `$.selectAllFiles`, `$.clearSelectionBtn`, 카드 DOM 조회

**핵심 갱신 — dragSelect.js**:
```js
// 변경 전 (dragSelect.js:13)
import { syncCardSelectionStates } from './browse.js';

// 변경 후
import { syncCardSelectionStates } from './selection.js';
```

**단계**:
1. `web/selection.js` 생성 — 7 심볼 + `wireSelection()` (selectAll/clearSelection toolbar 바인딩)
2. `web/browse.js`에서 7 정의 + wireBrowse의 selection toolbar 부분 삭제
3. `web/browse.js wireBrowse` → `wireSelection()` 호출 추가
4. `web/dragSelect.js` import 경로 갱신
5. `web/browse.js`도 `syncCardSelectionStates`·`bindEntrySelection`·`refreshSelectionUI` 등 selection import 추가

**Acceptance**:
- AC: `node --check`
- AC: `go test ./internal/handler -run "TestWeb_DragSelect|TestWeb_PNGSelect" -v` 통과
- AC: `web/selection.js` ≤ 130줄

**Risk**:
- `wireBrowse` listener 등록 순서가 wireSelection 호출 시점에 따라 바뀌지 않게 — wireBrowse 본체에서 wireSelection을 가장 처음 호출.
- `bindEntrySelection`은 cardBuilders가 호출 — BD-4 시점에 import 경로 추가.

---

### BD-4 · `web/cardBuilders.js` 분리

**영향 파일**:
- 신규: `web/cardBuilders.js`
- 수정: `web/browse.js`

**이전할 심볼** (`web/browse.js:399-578`):
- `sectionTitle(text)` — private
- `buildImageGrid(images)` — private
- `buildVideoGrid(videos)` — private
- `buildTable(entries)` — private

**의존**:
- `clipPlayback`: `attachClipHoverPlayback`, `isClip`
- `selection`: `bindEntrySelection`
- `state.js`: `selectedPaths`
- `util.js`: `esc`, `iconFor`, `formatSize`, `formatDuration`
- `fileOps.js`: `attachDragHandlers`, `attachDropHandlers`, `openRenameModal`, `deleteFile`, `deleteFolder`
- `convert.js`·`convertImage.js`·`convertWebp.js`: 카드별 변환 버튼 모달 오픈
- `dom.js`: 컨테이너 요소

**호출 사이트**:
- `web/browse.js renderFileList`이 type별로 build* 호출

**단계**:
1. `web/cardBuilders.js` 생성 — 4 심볼 + 위 의존 import
2. `web/browse.js`에서 4 정의 삭제 + import 추가

**Acceptance**:
- AC: `node --check`
- AC: `go test ./internal/handler -run "TestWeb_Sticky" -v` 통과 (image-grid-clip 클래스 부착 등)
- AC: `web/cardBuilders.js` ≤ 250줄 (예외 한도 — 3 grid 빌더가 함께)

**Risk**:
- 카드 DOM 구성 + 이벤트 바인딩이 한 함수에 같이 있음 — 분리 시 순서 보존.
- handleClick이 카드 click listener로 부착 — handleClick은 BD-5에서 lightbox 호출 분기 포함, browse.js에 남기거나 cardBuilders로 같이 옮길지 결정.
  - **권장**: handleClick은 browse.js에 남김 (lightbox open 분기 + audio 플레이리스트 진입 분기 — 양 도메인 orchestrator). build*는 클릭 핸들러를 인자로 받음.

---

### BD-5 · `web/lightbox.js` 분리

**영향 파일**:
- 신규: `web/lightbox.js`
- 수정: `web/browse.js`

**이전할 심볼** (`web/browse.js:612-690`):
- `openLightboxImage(index)` — private
- `openLightboxVideo(entry)` — private
- `closeLightbox()` — private
- `deleteCurrentLightboxItem()` — async
- `playAudio(entry)` — private
- `loadPlaylistTrack(index)` — private
- `renderPlaylist()` — private

**`wireBrowse`에서 이전할 부분** (line 710-742):
- `$.lbClose` / `$.lbDelete` / `$.lbPrev` / `$.lbNext` click listeners
- `$.lightbox` click (backdrop)
- `document.keydown` (ESC / Arrow / Delete)
- `$.audioEl ended` (auto-advance)

**의존**:
- `state.js`: `lbIndex`, `setLbIndex`, `lbCurrentVideoPath`, `setLbCurrentVideoPath`, `playlist`, `playlistIndex`, `setPlaylistIndex`, `imageEntries`
- `dom.js`: `$.lightbox`, `$.lbImg`, `$.lbVideo`, `$.lbClose`, `$.lbDelete`, `$.lbPrev`, `$.lbNext`, `$.audioEl`, `$.audioControls` 등
- `fileOps.js`: `deleteFile`
- `browse.js`: `browse(currentPath)` (lightbox delete 후 새로고침) — 잠재적 사이클 위험! browse → lightbox → browse. 해결: lightbox.js가 `browse` 함수를 콜백으로 주입받게 (`wireLightbox({onAfterDelete})`)
  - **권장**: `browse.js`가 `wireLightbox({onAfterDelete: () => browse(currentPath, false)})` 호출

**단계**:
1. `web/lightbox.js` 생성 — 7 심볼 + `wireLightbox(deps)`
2. `web/browse.js`에서 7 정의 + wireBrowse의 lightbox/audio/keyboard 부분 삭제
3. `web/browse.js wireBrowse` → `wireLightbox({onAfterDelete: ...})` 호출

**Acceptance**:
- AC: `node --check`
- AC: `go test ./internal/handler -run "TestWeb_LightboxDelete" -v` (6 시나리오) 통과
- AC: `web/lightbox.js` ≤ 200줄

**Risk**:
- 모듈 ESM mutable bindings 함정 — `setLbIndex(...)` 같은 setter 경유 (Phase 30 FM-1 회귀 안전망 그대로).
- 키보드 핸들러는 `document.addEventListener('keydown', ...)` — wireLightbox 안에서 단일 등록.
- **사이클 회피**: browse.js → lightbox.js (단방향). browse 재호출은 콜백 주입으로 — lightbox.js가 browse.js를 import하지 않게.

---

### BD-6 · `browse.js` 슬림화 + cache-bust

**영향 파일**:
- `web/browse.js` (정리)
- `web/index.html` (`?v=41` → `?v=42`)

**남는 함수**:
- `browse(path, pushState)` — fetch + state 갱신 진입점
- `renderView()` — sort/filter 호출 + renderFileList orchestrator
- `applyView(entries)` — sort/filter 적용
- `renderBrowseSummary(entries)` — 카운터 표시
- `renderBreadcrumb(path)` — 브레드크럼 렌더
- `renderFileList(entries)` — type별 build* 호출 dispatcher
- `handleClick(entry)` — 카드 click 분기 (lightbox image/video / audio playlist)
- `wireBrowse()` — wireSelection / wireLightbox 호출 + convertAllBtn 리스너 (visiblePaths 의존)

**Acceptance**:
- AC: `web/browse.js` ≤ 250줄 (현재 752 → ≤250)
- AC: `node --check web/*.js` 모두 OK
- AC: `go test ./internal/handler -run "TestWeb_" -v` 6개 e2e 모두 통과

**Risk**:
- 슬림화 후 import 누락 발견 가능 — 빌드 에러는 즉시. node --check 또는 브라우저 콘솔로.

---

### BD-7 · 수동 12 시나리오 + Status `merged`

**검증 절차**:
1. `docker compose up -d --build`
2. 브라우저(http://localhost:8080)로 spec §6.2 12 시나리오 수행
3. 12 모두 PASS 확인
4. `tasks/spec-browse-decomposition.md` Status `accepted` → `merged`
5. `tasks/todo.md` Phase 32 항목 모두 `[x]`로 갱신
6. 최종 commit

**Acceptance**:
- 12 시나리오 모두 PASS
- spec/todo 상태 갱신 완료

---

## 4. Commit 메시지 규약

CLAUDE.md 규약 (`type(scope): 메시지`) 준수:

| BD | type(scope) | 예시 |
|---|---|---|
| BD-1 | `refactor(web)` | `refactor(web): clipPlayback 함수군을 web/clipPlayback.js로 분리 (BD-1)` |
| BD-2 | `refactor(web)` | `refactor(web): visibleXxxPaths를 web/visiblePaths.js로 분리 (BD-2)` |
| BD-3 | `refactor(web)` | `refactor(web): selection 함수군을 web/selection.js로 분리 + dragSelect import 갱신 (BD-3)` |
| BD-4 | `refactor(web)` | `refactor(web): card builders를 web/cardBuilders.js로 분리 (BD-4)` |
| BD-5 | `refactor(web)` | `refactor(web): lightbox + audio 플레이리스트를 web/lightbox.js로 분리 (BD-5)` |
| BD-6 | `refactor(web)` | `refactor(web): browse.js 슬림화 + cache-bust v=42 (BD-6)` |
| BD-7 | `docs(tasks)` | `docs(tasks): browse 분해 spec merged + Phase 32 완료 (BD-7)` |

각 commit 본문에 acceptance(node check + 해당 e2e 통과) 한 줄 명시.

---

## 5. 체크포인트

### CP-1: BD-3 종료 시점
- 분리 완료: clipPlayback, visiblePaths, selection (3 모듈)
- browse.js: 여전히 카드 빌더 + 라이트박스 + 오디오 (~500줄)
- 검증: `web_clip_hover_playback`, `web_png_select`, `web_clip_to_webp`, `web_drag_select` e2e 통과
- 만약 이 시점에서 시간이 부족하면 머지 후 BD-4~7을 다음 세션으로 — clipPlayback/visiblePaths/selection은 자체 완결.

### CP-2: BD-5 종료 시점
- 분리 완료: 5 모듈 모두
- browse.js: 슬림화 직전 (orchestrator 함수만)
- 검증: 모든 e2e 통과
- BD-6은 정리 + cache-bust만이라 빠름.

### CP-3: BD-6 종료 시점
- AC-1 (≤250줄) 검증
- 자동 검증 끝, 수동 BD-7만 남음

---

## 6. Open issues / 잠재적 trip-up

다음 세션에서 BD-N 진행 중 마주칠 수 있는 함정:

1. **`isClip` 외부 import**: 본 plan 작성 시 grep 결과 외부 import 없음 확인됨. 그러나 BD-1 시점에 다시 한 번 grep으로 확인 (코드가 변경됐을 수 있음).
   ```bash
   grep -rn "isClip" web/ --include='*.js' | grep -v browse.js | grep -v clipPlayback.js
   ```
2. **`syncCardSelectionStates`의 dragSelect import**: BD-3에서 selection.js로 옮길 때 `web/dragSelect.js:13` import 경로 갱신 필수. 빠뜨리면 dragSelect e2e 4개 즉시 실패.
3. **`wireBrowse` listener 등록 순서**: BD-3·BD-5에서 wire* 호출 순서 보존. 권장: wireBrowse 본체에서 wireSelection → wireLightbox → convertAllBtn 순.
4. **lightbox → browse 사이클 회피**: BD-5에서 deleteCurrentLightboxItem이 폴더 새로고침 필요 — `browse(currentPath, false)`를 직접 호출하지 말고 콜백 주입 (`wireLightbox({onAfterDelete: () => browse(currentPath, false)})`).
5. **module mutable bindings**: ESM은 import된 `let` 변수를 직접 재할당할 수 없다. setter (`setLbIndex`, `setPlaylistIndex` 등) 경유 — Phase 30 FM-1 회귀 안전망과 동일.
6. **cache-bust**: BD-6에서 `web/index.html`의 단일 entry `<script type="module" src="/main.js?v=N">`만 bump. 내부 모듈 파일에 `?v=` 부착 금지.
7. **node --check만으로 ESM 검증되지 않을 수 있음**: `node --check`는 syntax만 본다. import 경로 오타·존재하지 않는 export는 브라우저에서야 잡힘. chromedp e2e가 final acceptance.
8. **browse.js 외부 export surface**: `main.js`가 import하는 `browse`, `renderView`, `wireBrowse`만 보존. 나머지 export(`isClip`, `applyView`, `visibleTSPaths`, `visiblePNGPaths`, `selectedVisiblePNGPaths`, `visibleClipPaths`, `selectedVisibleClipPaths`, `syncCardSelectionStates`)는 외부 import 없음 확인됨 — drop 가능하지만 보수적으로 re-export 유지 권장.

---

## 7. 단일 PR 머지 절차 (BD-7 이후)

```bash
git checkout develop
git merge --ff-only feature/browse-decomposition  # 8 commits ff
git push origin develop
git branch -d feature/browse-decomposition
docker compose up -d --build  # 컨테이너 재기동 검증
```
