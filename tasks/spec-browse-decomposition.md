# Spec: web/browse.js 분해 (B 후속)

> 부모 SPEC: [`/SPEC.md`](../SPEC.md). 본 spec은 **프론트엔드 구현 구조**만 다룬다 — 제품 행동·API·SSE 스키마 변경 없음.
>
> Status: **merged** — Phase 32 완료. BD-1~7 7 commit, browse.js 752→248, 5 신규 모듈(clipPlayback / visiblePaths / selection / cardBuilders / lightbox). chromedp e2e 6개 + 수동 12 시나리오 통과.
>
> 선행 spec: [`spec-frontend-modularization.md`](./spec-frontend-modularization.md) (Phase 30 머지 완료, 17 모듈 분할). 본 spec은 그 후속으로 가장 큰 단일 모듈인 `web/browse.js`(752줄)만 한 단계 더 쪼갠다.

## 1. Objective

`web/app.js` 분해 결과 17 모듈 중 `web/browse.js`만 752 라인으로 비대화 — 디렉토리 조회·필터·렌더·lightbox·오디오 플레이어·selection·hover playback이 한 파일에 응집되어 신규 view type / selection 동작 추가 시 비용이 가파르게 오른다.

이미 자연스러운 도메인 경계가 그어져 있어 (`renderFileList` / `buildImageGrid` / `attachClipHoverPlayback` / lightbox 함수군 / selection 함수군) **번들러 없이 브라우저 네이티브 ES Modules**로 추가 분리한다.

**목표**

- `web/browse.js`를 ≤250 라인으로 축소 — orchestrator + 진입점 역할만 유지.
- 분리된 모듈은 각 단일 책임 (한 줄로 설명 가능).
- 신규 기여자가 "이 동작을 고치려면 어디 파일?" 질문에 5초 안에 답할 수 있다.

**Non-goals**

- 동작 변경. 발견된 버그는 별도 follow-up 이슈로 분리 (Phase 30 spec과 동일 정책).
- 신규 기능 추가, 리팩토링 김에 끼워넣는 청소.
- CSS/HTML 구조 변경 (모듈 경계와 무관).
- selection / lightbox / clip playback 동작 자체는 변경 없음.
- 새 자동 테스트 인프라 도입 (vanilla JS, 기존 chromedp e2e 활용).

**Target user:** 코드베이스 유지보수자(본인).

---

## 2. Strategy

### 2.1 분리 원칙

Phase 30 spec과 동일:

- 브라우저 네이티브 ES Modules, `web/` 평면 배치, 서브디렉토리 없음.
- named export only. default export 금지.
- 상대 경로 + 확장자 명시 (`import './selection.js'`).
- mutable 공유 상태는 `state.js` 단일 출처 — 새 entry 추가 없음 (이미 있는 것만 import).
- 단방향 의존: `main.js → browse.js → {selection, lightbox, clipPlayback, visiblePaths, cardBuilders}`. 신규 사이클 금지.

### 2.2 cache busting

`web/index.html`의 `?v=N` 단일 bump (현재 `v=41` → `v=42`). 내부 import는 상대 경로 그대로.

### 2.3 분리 후보

현재 `browse.js`의 함수 ~40개를 도메인별로 그룹화한 결과:

| 그룹 | 함수 / 라인 | 새 모듈 |
|---|---|---|
| **데이터 fetch + 정렬·필터 + 진입점** | `browse`, `applyView`, `renderView`, `renderBrowseSummary`, `renderBreadcrumb`, `renderFileList`, `handleClick`, `wireBrowse` (slim) | `web/browse.js` (slim) |
| **clip hover 재생** | `clipIOInstance`, `attachClipHoverPlayback`, `isClip`, `isClipConvertable` (~50줄) | `web/clipPlayback.js` |
| **visible paths + 일괄 변환 toolbar 갱신** | `visibleTSPaths` / `visiblePNGPaths` / `selectedVisiblePNGPaths` / `visibleClipPaths` / `selectedVisibleClipPaths` + `updateConvertAllBtn` / `updateConvertPNGAllBtn` / `updateConvertWebPAllBtn` (~110줄) | `web/visiblePaths.js` |
| **selection 상태·UI** | `syncSelectionWithVisible` / `renderSelectionControls` / `updateCardSelection` / `syncCardSelectionStates` / `refreshSelectionUI` / `setSelected` / `bindEntrySelection` (~80줄) | `web/selection.js` |
| **카드 빌더** | `sectionTitle`, `buildImageGrid`, `buildVideoGrid`, `buildTable` (~200줄) | `web/cardBuilders.js` |
| **lightbox + audio 플레이리스트** | `openLightboxImage` / `openLightboxVideo` / `closeLightbox` / `deleteCurrentLightboxItem` / `playAudio` / `loadPlaylistTrack` / `renderPlaylist` + lightbox·audio 키바인딩 (~120줄) | `web/lightbox.js` |

**가정 (사용자 확인 필요):**

- A1: 6 모듈 분리 (browse 슬림 + 5 신규). 더 적게(예: cardBuilders + lightbox 통합) / 더 많게(per-card-type cardBuilders.js → imageGrid.js / videoGrid.js / table.js) 옵션도 가능 — 5초 답변 기준으로 6가 적정.
- A2: cardBuilders는 단일 파일. `buildImageGrid` / `buildVideoGrid` / `buildTable`은 응집도 높은 카드 DOM 구성 헬퍼라 한 파일에 두는 게 자연스럽다 (총 ~200줄 단일 책임).
- A3: lightbox 모듈에 audio 플레이리스트(`playAudio`/`loadPlaylistTrack`/`renderPlaylist`)도 같이 둔다. 두 도메인 모두 "현재 항목 재생 + 다음/이전" 패턴이라 응집도 있고, 분리 시 카드 클릭 분기(`handleClick`)가 두 모듈 import해야 해서 비용이 늘어난다.
- A4: 공유 상태(`lbIndex`, `playlistIndex`, `imageEntries`, `videoEntries`, `playlist` 등)는 모두 이미 `state.js`에 있어 추가 작업 불요.

### 2.4 모듈 의존 그래프 (분리 후)

```
main.js
 ├─ browse.js (slim)
 │   ├─ selection.js          ← bindEntrySelection·setSelected 등
 │   ├─ visiblePaths.js       ← visibleXxxPaths·updateXxxBtn
 │   ├─ cardBuilders.js       ← buildImageGrid·buildVideoGrid·buildTable
 │   ├─ clipPlayback.js       ← attachClipHoverPlayback (cardBuilders가 호출)
 │   └─ lightbox.js           ← openLightboxImage·openLightboxVideo·playAudio
 ├─ tree.js
 ├─ fileOps.js
 ├─ urlImport*.js
 └─ convert*.js
```

`browse.js`가 5 모듈을 import하지만 5 모듈은 서로 import하지 않는다 — `cardBuilders.js`만 `clipPlayback.attachClipHoverPlayback`을 호출 (단방향).

---

## 3. 행위 보존 규칙

순수 refactor — 다음 모두 동일해야 한다:

- **Export 시그니처**: `main.js`와 다른 17 모듈이 import하는 함수 시그니처 (`browse`, `renderView`, `applyView`, `visibleTSPaths`, `isClip`, `visibleClipPaths`, `selectedVisibleClipPaths`, `visiblePNGPaths`, `selectedVisiblePNGPaths`, `wireBrowse`, `syncCardSelectionStates`)는 그대로. 호출 사이트 변경 0.
- **DOM mutation 순서**: 카드 빌드 → 이벤트 리스너 부착 → toolbar 갱신 순서 보존.
- **이벤트 리스너 등록 순서**: `wireBrowse` 안의 listener 등록 순서 (selection toolbar → lightbox → audio → convertAll). 키보드 핸들러(`document.keydown`) 단일 등록.
- **state.js mutable bindings**: `setLbIndex`/`setPlaylistIndex`/`setLbCurrentVideoPath` setter 경유. 분리된 모듈 내부에서 직접 재할당 금지 (Phase 30 FM-1 회귀 방지).
- **Selection 동작**: `syncCardSelectionStates`가 외부(`fileOps.js`)에서 호출되므로 export 유지. 분리 후 `selection.js`에서 export.

---

## 4. Acceptance Criteria

- **AC-1**: `web/browse.js` ≤ 250 라인 (현재 752).
- **AC-2**: 분리된 5 신규 모듈 각 ≤ 250 라인. `cardBuilders.js`만 예외적으로 ≤ 250 (image/video/table 셋이 함께 있어).
- **AC-3**: 외부 import 사이트 변경 0 — `main.js`, `fileOps.js`, `convert.js`, `convertImage.js`, `convertWebp.js`, `urlImport.js` 등이 기존 함수명을 그대로 호출.
- **AC-4**: 기존 chromedp e2e 테스트 모두 통과:
  - `web_clip_hover_playback_e2e_test.go`
  - `web_clip_to_webp_e2e_test.go`
  - `web_drag_select_e2e_test.go`
  - `web_lightbox_delete_e2e_test.go`
  - `web_png_select_e2e_test.go`
  - `web_sticky_e2e_test.go`
- **AC-5**: 수동 브라우저 검증 통과 (§6 수동 시나리오).
- **AC-6**: `web/index.html` cache-bust 단일 bump (`v=41` → `v=42`). 내부 모듈은 `?v=` 미부착.
- **AC-7**: ESLint·tsc 없음 (vanilla JS) — `node --check web/*.js`로 syntax sanity만 확인.
- **AC-8**: 모듈 의존 그래프 사이클 없음 (§2.4 단방향).

---

## 5. 작업 순서 (Plan)

각 단계는 독립 commit. 단계 사이에 chromedp e2e 통과 확인.

| # | Task | 산출물 |
|---|---|---|
| BD-1 | `web/clipPlayback.js` 분리 | clipIOInstance / attachClipHoverPlayback / isClip / isClipConvertable + 호출 사이트 import 갱신 |
| BD-2 | `web/visiblePaths.js` 분리 | visibleXxxPaths · selectedVisibleXxxPaths · updateConvertXxxBtn 5+3개 |
| BD-3 | `web/selection.js` 분리 | bindEntrySelection / setSelected / refreshSelectionUI 등 7 함수 + `wireBrowse`의 selection toolbar 부분 이전 |
| BD-4 | `web/cardBuilders.js` 분리 | sectionTitle / buildImageGrid / buildVideoGrid / buildTable |
| BD-5 | `web/lightbox.js` 분리 | openLightboxImage / openLightboxVideo / closeLightbox / deleteCurrentLightboxItem / playAudio / loadPlaylistTrack / renderPlaylist + `wireBrowse`의 lightbox·audio 부분 이전 |
| BD-6 | `web/browse.js` 슬림화 + cache-bust bump | 잔여 함수만 유지, `wireBrowse`는 분리 모듈의 `wireXxx` 호출 delegate |
| BD-7 | 수동 브라우저 검증 (12 시나리오, §6) | 통과 확인 + spec Status `merged` 갱신 |

각 BD-N 단계는 chromedp e2e 통과를 acceptance로 한다 (개별 회귀 안전망).

---

## 6. 검증 (Verification)

### 6.1 자동

```bash
go test ./internal/handler -run "TestWeb_" -v
node --check web/browse.js web/clipPlayback.js web/visiblePaths.js web/selection.js web/cardBuilders.js web/lightbox.js
```

### 6.2 수동 브라우저 (Phase 30 패턴 답습)

12 시나리오:

1. 폴더 진입 → URL 쿼리(`path`/`sort`/`q`/`type`) 동기화 정상
2. 정렬 변경 → URL replaceState만, history 추가 없음
3. 검색 / 타입 필터 → visible 카드만 갱신
4. 이미지 카드 클릭 → 라이트박스 오픈, prev/next/close 동작
5. 동영상 카드 클릭 → 라이트박스 video 재생, close 시 폴더 새로고침 없음
6. 오디오 카드 클릭 → 플레이리스트 + auto-advance
7. 라이트박스 Delete 키 → 다음 항목으로 이동, 마지막 항목이면 닫힘
8. 움짤 탭(`type=clip`) hover/IO 재생 토글 (`/api/thumb` 정적 → `/api/stream` 토글)
9. 카드 단일 selection (체크박스) → toolbar 갱신
10. drag-select rubber-band → visible 카드 선택
11. selection 후 일괄 변환 버튼 (PNG / TS / WebP) — selection-aware 라벨 갱신
12. 회귀: rename / delete / drag-and-drop / 새 폴더 / 업로드 — Phase 28~30 기능 모두 정상

### 6.3 회귀 risk 매트릭스

| 위험 | 등급 | 완화 |
|---|---|---|
| `wireBrowse` 안 listener 등록 순서 변경 | 높음 | 분리 후에도 동일 순서 유지, 코드 review에서 확인 |
| `state.js` setter 미경유 → silent TypeError | 중 | 분리 시 `setLbIndex(...)` 같은 setter 호출 그대로 carry-over. eslint 없음 — 코드 review로 |
| chromedp e2e가 selector 의존 | 중 | DOM 구조는 유지 — 모듈만 옮김. selector 영향 없음 |
| cache-bust 누락으로 stale module | 낮음 | `index.html`에서 `v=` 단일 bump. 수동 hard reload 안내 |

---

## 7. Boundaries (이 spec이 명시하는 한계)

**항상 할 것:**

- `node --check`로 분리한 모든 모듈의 syntax 통과 확인.
- 분리 후 chromedp e2e 6개 모두 통과.
- mutable 공유 상태는 `state.js` 단일 출처 (setter 경유).
- 단방향 의존 — `browse.js`가 `selection.js`를 import하지만 그 반대 금지.

**먼저 물어볼 것:**

- §2.3 분리 단위 (5 모듈 vs 다른 granularity).
- A2 cardBuilders 단일 파일 vs per-card-type 분할.
- A3 lightbox + audio 통합 vs 분리.
- AC-1/AC-2 라인 한도 적정성.

**절대 안 할 것:**

- 동작 변경 (pure refactor 외 어떤 것도).
- 빌드 도구·번들러·트랜스파일러·타입 시스템 도입.
- CSS/HTML/SPEC.md 수정 (모듈 경계와 무관).
- `state.js`에 새 entry 추가 (이미 있는 것만 사용).
- 신규 사이클 — `selection.js → cardBuilders.js → selection.js` 같은 순환.

---

## 8. Open Questions — 확정

5 항목 모두 권장값으로 확정:

1. ✅ **분리 granularity**: 5 신규 모듈 + browse 슬림화.
2. ✅ **lightbox + audio**: 통합 (`web/lightbox.js`).
3. ✅ **AC-1 한도**: `web/browse.js` ≤ 250 라인.
4. ✅ **수동 검증 시나리오 수**: 12개.
5. ✅ **머지 단위**: 한 PR (`feature/browse-decomposition`) + 단계별 commit (BD-1 ~ BD-7).

Phase 32 진행 — `tasks/todo.md` 추적.
