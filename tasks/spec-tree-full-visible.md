# Spec: 폴더 트리 전체 가시성 + 업로드 존 sticky

> **Status: merged** — `web/tree.js`·`web/style.css`로 이전 후 머지 완료. 본 spec은 historical record로 보존.
>
> 부모 SPEC: [`/SPEC.md`](../SPEC.md). **§2.5 프론트엔드 UI**의 사이드바 동작을 보강한다.
> 관련 선행 spec: [`spec-sidebar-dnd.md`](spec-sidebar-dnd.md) (사이드바 도입 자체).

## 1. Objective

현재 좌측 폴더 트리 사이드바는 페이지를 아래로 스크롤하면 윗부분이 함께 가려져 트리 탐색이 끊긴다(사용자 보고). 동시에 업로드 존(`#upload-zone`)도 본문과 함께 위로 사라져, 스크롤 후 파일을 추가하려면 다시 위로 올라와야 한다.

**목표:** 트리 길이·뷰포트 높이와 무관하게 **트리 전체에 도달 가능**하면서, 업로드 존은 **헤더 바로 아래에 항상 고정**되어 보이도록 한다.

**Target user:** 단일 사용자(개인). 데스크톱 브라우저(>600px) 기준. 모바일 드로어는 이번 변경 대상 아님.

**Success criteria:**
1. 트리 노드가 뷰포트보다 길든 짧든, **페이지를 끝까지 스크롤하면 트리의 마지막 노드까지 시야에 들어온다**.
2. 트리가 짧을 때(뷰포트 이내)는 헤더 바로 아래에 sticky로 고정된다(현재와 동일 체감).
3. 업로드 존은 헤더 바로 아래(좌측 사이드바 옆 메인 컬럼 최상단)에 sticky로 고정되어 본문 스크롤 중에도 항상 보인다.
4. 사이드바 내부 `overflow-y: auto`로 인한 자체 스크롤은 제거된다(트리는 자연 스크롤로 노출).
5. chromedp 기반 자동화 테스트가 위 1~3을 검증한다.

---

## 2. Scope

### In scope
- `web/style.css`의 `.sidebar`·`#upload-zone` 영역 sticky 정책 변경
- `web/app.js`에 사이드바 sticky `top` 동기화 로직 추가 (window resize / 트리 로드·확장/접기·rename·delete 시 재계산) *(머지 후 ES module 분할로 `web/tree.js`의 `syncSidebarSticky`로 이전됨)*
- `internal/handler` 또는 `web/` 하위에 chromedp 통합 테스트 1종 추가
- `go.mod`에 `github.com/chromedp/chromedp` 추가
- 본 변경사항을 SPEC.md §2.5에 한 줄 반영

### Out of scope
- 모바일 드로어(<600px) 동작 — 그대로 둔다
- 트리 활성 노드로의 자동 스크롤(`scrollIntoView`)
- 업로드 존 외 다른 본문 요소(`#browse-toolbar` 등)의 sticky 처리
- 트리 가상 스크롤·접기 정책 변경

---

## 3. UX 동작 명세

### 3.1 사이드바 — Sticky-until-bottom (③ 패턴)

레이아웃 모델:

- 사이드바는 자연 높이(콘텐츠 만큼)로 자란다. 자체 `overflow-y` 없음.
- `position: sticky` 의 `top` 값은 다음 식으로 **JS가 계산**:
  - `headerH = 57px` (`--header-h`)
  - `sidebarH = sidebar.offsetHeight` (콘텐츠 높이)
  - `viewportH = window.innerHeight`
  - `overflow = max(0, sidebarH - (viewportH - headerH))`
  - `top = headerH - overflow` ← 음수가 될 수 있음
- 결과:
  - 트리 ≤ 뷰포트 가용 영역: `overflow = 0` → `top = headerH` → 헤더 바로 아래 고정.
  - 트리 > 뷰포트 가용 영역: `top` 이 음수 → 페이지를 그만큼 내리는 동안 사이드바는 같이 위로 올라가다가, 사이드바 **하단이 뷰포트 바닥에 닿는 순간 pin**. 이후 더 스크롤해도 사이드바는 그 위치(트리 끝이 보이는 상태)에 고정.

언제 재계산하는가:
- 페이지 로드(`loadTree` 완료 시)
- 트리 노드 펼침/접힘(`buildTreeNode` 콜백 끝)
- 트리 노드 추가/삭제/rename(폴더 모달 confirm, drag-drop 완료, delete confirm 후)
- `window.resize`
- (옵션) `ResizeObserver` 로 사이드바 콘텐츠 변경 감지 — 위 콜백을 빠뜨리는 케이스 방어

### 3.2 업로드 존 — 헤더 바로 아래 sticky

- `#upload-zone` 을 `position: sticky; top: var(--header-h);` 로 고정.
- z-index 는 헤더(10) 보다 작게(예: 5) 두어 헤더가 항상 위.
- 배경이 투명하면 본문이 뒤로 비치므로 `background: var(--bg);` 명시.
- 기존 `margin: 16px 24px;`은 sticky 영역의 시작 좌표를 어긋나게 하므로 **세로 마진 제거**(`margin: 0 24px;`)하고 본문 첫 자식과의 간격은 `padding-top` 또는 다음 형제의 `margin-top`으로 처리.
- drag-over 스타일은 그대로.

### 3.3 시각적 검증 시나리오

1. **짧은 트리(폴더 5개 이하)**: 페이지를 끝까지 스크롤해도 사이드바 전체가 헤더 아래에 그대로. 업로드 존도 헤더 아래에 그대로.
2. **딱 들어맞는 트리(뷰포트와 동일 높이)**: 1과 동일.
3. **긴 트리(뷰포트의 1.5~2배)**: 페이지를 내릴수록 사이드바 위쪽이 자연스럽게 화면 밖으로 빠지고, 페이지를 충분히 내린 시점에 사이드바 **마지막 노드가 뷰포트 바닥 안에 들어온다**. 그 이후 더 스크롤해도 사이드바 마지막 노드는 보이는 상태 유지(본문만 계속 스크롤).
4. **업로드 존**: 시나리오 1~3 모두에서 `#upload-zone` 의 `getBoundingClientRect().top` 이 `headerH` 와 일치(±1px).

---

## 4. 기술 설계

### 4.1 CSS 변경 (`web/style.css`)

```css
.sidebar {
  background: var(--surface);
  border-right: 1px solid var(--border);
  padding: 12px 8px;
  position: sticky;
  /* top 은 JS 가 인라인 스타일로 주입 (기본값은 헤더 바로 아래) */
  top: var(--header-h);
  align-self: start;          /* grid item 에서 sticky 가 동작하려면 필요 */
  /* height/overflow-y 제거 — 자연 높이 + 페이지 스크롤로 노출 */
  font-size: 0.95rem;
}

.upload-zone {
  position: sticky;
  top: var(--header-h);
  z-index: 5;
  background: var(--bg);
  margin: 0 24px;             /* 세로 마진 제거 */
  /* 기존 border / padding / transition 유지 */
}
/* 다음 형제(#browse-toolbar)와의 간격은 .upload-zone 에 padding-bottom 또는 형제의 margin-top 으로 부여 */
```

모바일(<600px) 미디어 쿼리 안의 `.sidebar` 블록은 기존 그대로(드로어).

### 4.2 JS 변경 (`web/app.js`)

새 함수:

```js
function syncSidebarSticky() {
  if (!sidebar || window.matchMedia('(max-width: 600px)').matches) return;
  const headerH = parseInt(
    getComputedStyle(document.documentElement).getPropertyValue('--header-h'),
    10
  ) || 57;
  // 콘텐츠 자체 높이를 얻기 위해 sticky top 영향 배제
  const sidebarH = sidebar.scrollHeight;
  const viewportH = window.innerHeight;
  const overflow = Math.max(0, sidebarH - (viewportH - headerH));
  sidebar.style.top = (headerH - overflow) + 'px';
}
```

호출 지점:
- `loadTree()` 마지막 줄
- `toggleNode()`(펼침/접힘) 끝
- 폴더 생성/삭제/rename 콜백 끝
- 드래그 드롭 mutation 후
- `window.addEventListener('resize', syncSidebarSticky)` (앱 init 1회)
- `new ResizeObserver(syncSidebarSticky).observe(sidebar)` (콜백 누락 방어)

### 4.3 Go 의존성

`go.mod` 에 `github.com/chromedp/chromedp` 추가. 시스템에 설치된 Chrome 사용(브라우저 다운로드 없음).

### 4.4 테스트 (`internal/handler/web_sticky_e2e_test.go`)

- `httptest.NewServer` 로 `Handler` + 정적 `/web` 서빙 인스턴스 기동.
- 임시 데이터 디렉터리에 **트리가 짧은 / 긴** 두 종류 폴더 트리를 생성.
- `chromedp.NewContext` 로 헤드리스 Chrome 실행.
- 시나리오:
  1. **짧은 트리**: `window.scrollTo(0, document.body.scrollHeight)` → `#sidebar` 의 `getBoundingClientRect().top` 이 `--header-h` 와 일치, `#upload-zone` 도 동일.
  2. **긴 트리**: 동일하게 페이지 끝까지 스크롤 → `#sidebar` 의 `getBoundingClientRect().bottom` 이 `window.innerHeight` 와 일치(±2px). `#tree-root` 의 마지막 자식이 시야 안(`top < window.innerHeight`).
- Chrome 부재 시 `t.Skip` (CI/타 OS 친화).

테스트는 `go test ./internal/handler -run TestSidebarSticky -v` 로만 실행되도록 자동 마커(빌드 태그 또는 `LookPath("chrome")` 가드) 사용.

### 4.5 SPEC.md 업데이트

§2.5(사이드바) 항목에 한 줄 추가:

> 사이드바는 콘텐츠 자연 높이로 자라며 sticky pin 위치를 JS가 동적으로 계산해, 페이지 스크롤만으로 트리 전체에 도달할 수 있게 한다. 업로드 존은 헤더 바로 아래 sticky.

---

## 5. Acceptance Criteria

| # | 기준 | 검증 |
|---|---|---|
| AC1 | 트리 ≤ 뷰포트 가용 영역에서 페이지 끝까지 스크롤해도 사이드바·업로드 존 전체가 헤더 바로 아래에 sticky | chromedp 시나리오 1 |
| AC2 | 트리 > 뷰포트일 때 페이지 끝까지 스크롤하면 트리 마지막 노드가 뷰포트 안에 들어옴 | chromedp 시나리오 2 |
| AC3 | 업로드 존 `getBoundingClientRect().top` 이 `--header-h` 와 ±1px 일치 (스크롤 위치 무관) | chromedp 양 시나리오 |
| AC4 | 사이드바 내부 자체 `overflow-y` 스크롤바가 등장하지 않음 | CSS diff 리뷰 + 수동 |
| AC5 | 트리 노드 펼침/접힘·rename·delete 후 sticky `top` 이 즉시 갱신 | 수동 1회 |
| AC6 | 모바일(<600px) 햄버거 드로어 동작 회귀 없음 | 수동 1회 |

---

## 6. 구현 순서 (incremental slice)

1. **slice 1**: CSS 변경 + JS `syncSidebarSticky` 골격(`loadTree` + resize 만 호출) → 짧은 트리 수동 확인
2. **slice 2**: 모든 mutation 콜백 연결 + `ResizeObserver` → 펼침/접힘·rename 동작 확인
3. **slice 3**: chromedp 의존성 추가 + AC1·AC2·AC3 자동 테스트
4. **slice 4**: SPEC.md 업데이트 + 커밋

각 slice 끝에 `go build ./cmd/server`, slice 3 끝에 `go test ./...`.

---

## 7. Boundaries

**Always do**
- sticky `top` 계산 시 mobile (`max-width: 600px`) 분기 — 드로어 모드에서는 인라인 `top` 주입 금지
- `ResizeObserver` 로 콜백 누락을 방어
- chromedp 테스트는 Chrome 부재 시 `t.Skip`

**Ask first**
- `#browse-toolbar` 도 sticky 화 (이번 spec 범위 밖이지만 사용자 추가 요청 가능)
- 활성 폴더 자동 `scrollIntoView`

**Never do**
- 사이드바 내부에 `overflow-y: auto` 재도입
- 모바일 드로어 동작에 영향 주는 변경
- chromedp 테스트가 시스템 Chrome 외 별도 브라우저 다운로드 트리거하도록 두는 것

---

## 8. 위험 요소

- **사이드바 height 가 뷰포트와 거의 같을 때 떨림**: `overflow` 가 0~수 px 사이에서 진동할 수 있음 → `Math.max(0, ...)` 로 음수 방지, 1px 미만은 0 으로 라운딩.
- **트리 lazy 확장 직후 콘텐츠 높이 변동**: `ResizeObserver` 가 잡아주지만, 애니메이션 중간 프레임에서 1~2회 흔들 수 있음. UX상 무시 가능 수준이면 그대로 진행.
- **chromedp 헤드리스에서 `position: sticky` 가 시뮬레이션과 차이**: 헤드리스 Chrome 도 sticky 정상 지원. 실패 시 `--headless=new` 플래그로 전환.
- **헤더 높이 변경 시(반응형 등) `--header-h` 와 실제 헤더 높이 어긋남**: 본 spec 범위에서는 57px 고정 가정. 향후 동적 헤더가 도입되면 `header.offsetHeight` 로 측정 변경.
