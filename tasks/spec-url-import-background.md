# Spec: URL Import — 모달 닫아도 백그라운드 진행

> 부모 SPEC: [`/SPEC.md`](../SPEC.md). 이 문서는 그중 **2.6 URL Import**의 UX/동시성 모델을 개정한다.
>
> 구현 완료 시 `SPEC.md §2.6` 본문을 이 문서 기준으로 갱신한다.

## 1. Objective

현재는 URL Import 모달을 닫으면 `AbortController.abort()`로 fetch를 끊어 서버 측 다운로드가 즉시 중단된다 (커밋 `cb1ca17`). 대용량 HLS/원본을 받는 중 실수로 모달을 닫으면 처음부터 다시 받아야 해서 불편하다.

**목표:**
- 모달 닫기는 **뷰 숨김**이지 **작업 취소**가 아니다. 닫아도 현재 탭이 살아있는 한 다운로드는 계속된다.
- 진행 중 상태는 헤더 미니 배지로 항상 확인 가능.
- 모달을 다시 열면 진행 중 배치가 그대로 보인다.
- 진행 중에 새 배치를 추가 제출할 수 있고, 서버가 배치 단위로 직렬 처리한다.

**Target user:** 단일 사용자(개인).

**Non-goals:**
- 탭/브라우저 닫힘 후에도 서버가 끝까지 다운로드 (이는 서버 측 잡 레지스트리가 필요 — 별도 spec).
- 중단된 다운로드의 HTTP Range resume.
- 개별 URL 취소 버튼.

---

## 2. Scope

### In scope
- 클라이언트: 모달 close 시 abort 제거, 미니 진행 배지, 모달 재오픈 시 현재 배치 상태 복원, 다중 배치 동시 추적.
- 서버: 배치 단위 직렬화를 위한 전역 `sync.Mutex` 추가. 큐잉 단계 SSE 이벤트 1종 추가.

### Out of scope
- 서버 측 영속 잡 큐.
- 탭 닫힘 복구.
- 우선순위/스킵/개별 취소.
- HLS/일반 URL 분기 로직 자체 변경.

---

## 3. UX Spec

### 3.1 동작 흐름

**단일 배치 시나리오:**
1. 사용자가 ⚙ 옆 "URL 가져오기" 버튼 클릭 → 모달 오픈
2. URL 입력 → "가져오기" 클릭 → SSE 스트림 시작 → row별 진행 바 갱신
3. 사용자가 X/ESC/배경 클릭 → 모달만 숨김 (`urlAbort.abort()` **호출하지 않음**)
4. 헤더 우측에 미니 배지 표시 "URL ↓ 2/5"
5. 다운로드 계속 진행. 완료 시 배지 사라지고, 성공이 1건 이상이면 `browse()` 자동 재조회
6. 사용자가 배지 클릭 또는 다시 "URL 가져오기" 버튼 클릭 → 모달 재오픈, 기존 row 그대로 복원

**다중 배치 시나리오:**
1. 배치 A 진행 중 사용자가 모달 재오픈
2. confirm 버튼 라벨이 **"새 배치 추가"** 로 바뀌어 있음. textarea는 빈 상태.
3. 새 URL 입력 → "새 배치 추가" 클릭 → POST 발송
4. 새 row들이 기존 row 아래 append, "대기 중" 상태
5. 서버는 배치 A의 mutex가 release될 때까지 배치 B의 핸들러를 블록. 블록 진입 직전에 `queued` SSE 1회 방출 → 클라이언트가 row 상태를 "대기 중(큐잉)"으로 유지
6. 배치 A summary 수신 → 서버 mutex release → 배치 B `start` 이벤트 방출 시작

### 3.2 미니 배지

- 위치: 헤더 우측 ⚙ 버튼 근처
- 표시 조건: `urlSubmitting === true` AND 모달 숨김 상태
- 내용:
  - 진행 중: `URL ↓ 3/5` (완료/전체 URL 카운트 aggregate)
  - 일부 에러: `URL ↓ 3/5 · ⚠` (전체 중 실패가 있을 때 보조 아이콘)
- 클릭 시: 모달 재오픈
- 완료 시: 자동 사라짐. 에러만 있는 경우도 summary 수신 후 3초 후 사라짐.

### 3.3 모달 상태별 라벨

| 상태 | confirm 버튼 | textarea | 기존 row |
|---|---|---|---|
| 초기 (진행 중 배치 없음) | "가져오기" | 편집 가능 | 없음 |
| 진행 중 배치 있음 (재오픈) | "새 배치 추가" | 편집 가능 (빈 상태) | 기존 진행 상황 그대로 |
| textarea 비어있음 | disabled | — | — |
| 모든 배치 완료 직후 | "가져오기" | 편집 가능 | 기존 row 유지 (다음 열기까지는 참고용) |

### 3.4 배치 간 시각 구분

- 각 배치는 연속된 row 그룹. 배치 경계에 얇은 divider 또는 `"배치 2"` 라벨 삽입.
- row의 `data-batch` 속성으로 관리.

### 3.5 탭 닫기/새로고침

- 브라우저가 fetch를 abort → 서버 context cancel → 기존 동작(진행 중 URL 중단, 임시 파일 정리) 유지.
- 이는 복구하지 않는다. 미니 배지 상태는 sessionStorage에 저장하지 않음.

---

## 4. API / 서버 변경

### 4.1 `Handler.importMu sync.Mutex`

- `internal/handler/handler.go`의 `Handler` 구조체에 `importMu sync.Mutex` 추가.
- `handleImportURL`에서 **응답 헤더를 쓴 후, SSE 시작 직전에 acquire**.
- 블록 진입 전 `queued` 이벤트 1회 emit.
- 함수 리턴 시 release (defer).

```go
// pseudo-code
w.Header().Set("Content-Type", "text/event-stream")
w.WriteHeader(http.StatusOK)

emit(sseQueued{Phase: "queued"}) // 새 이벤트 타입, index 없음
h.importMu.Lock()
defer h.importMu.Unlock()

// 기존 for 루프
```

- mutex 대기 중 `r.Context().Done()`도 함께 select — client disconnect 시 대기 포기하고 리턴. Lock은 취소 불가하므로 `TryLock` + 재시도 루프 또는 대기 채널 기법이 필요. 구현 시 선택.

### 4.2 `queued` SSE 이벤트 (신규)

```json
{"phase": "queued"}
```

- 배치당 최대 1회. mutex 획득 후에는 발송하지 않음.
- 기존 스키마 `start / progress / done / error / summary`에 1종 추가.
- 클라이언트에서 이 이벤트 수신 시 해당 배치의 모든 row를 "대기 중(큐잉)"으로 표시.
- `SPEC.md §5.1` API 문서에도 반영.

### 4.3 기존 이벤트 스키마

- `start / progress / done / error / summary` 모두 **변경 없음**.
- `index`, `url`, `name`, `total`, `received`, `warnings` 필드도 그대로.

---

## 5. 클라이언트 변경

### 5.1 상태 모델 (app.js)

기존:
```js
let urlSubmitting = false;
let urlAnySucceeded = false;
let urlAbort = null;
```

개정:
```js
// 배치별 상태. 동시에 여러 배치가 살아있을 수 있음.
const urlBatches = [];   // { id, abort, urls[], rowEls[], summary, done }
let urlBatchSeq = 0;
```

- `urlSubmitting`은 "활성 배치가 1개 이상 있는가" 파생 상태로 대체.
- 모든 배치 `done = true` + summary 수신 완료 시 → browse 재조회 → 배지 숨김.

### 5.2 `closeURLModal()` 개정

```js
function closeURLModal() {
  urlModal.classList.add('hidden');
  updateURLBadge();    // 진행 중이면 배지 표시
  // abort() 호출 제거!
}
```

### 5.3 `submitURLImport()` 개정

- 호출 시 새 `batch` 객체 생성, `urlBatches.push(batch)`.
- SSE 이벤트는 `batch` 스코프로 라우팅 (각 배치의 `rowEls[]`에만 업데이트).
- 배치의 summary 수신 시 `batch.done = true`, 성공 카운트 누적.

### 5.4 미니 배지

- `updateURLBadge()`: 모달 숨김 && `urlBatches.some(b => !b.done)` → 배지 보이기.
- 배지 텍스트: `완료 URL 수 / 전체 URL 수` aggregate across all active batches.
- 배지 클릭 핸들러: `openURLModal()` 재호출.

### 5.5 `openURLModal()` 개정

- 진행 중 배치가 있으면 confirm 버튼 라벨을 "새 배치 추가"로. textarea는 항상 빈 상태로.
- 기존 row 영역은 **초기화하지 않는다** — 진행 중 배치의 row를 그대로 유지.
- "초기" 상태(모든 배치 완료 + 모달 재오픈)는 row를 비우고 라벨을 "가져오기"로.

---

## 6. Acceptance Criteria

### 클라이언트
- [ ] `closeURLModal()`에서 `urlAbort.abort()` 호출 제거
- [ ] 헤더 미니 배지 컴포넌트 추가 (HTML/CSS/JS)
- [ ] `urlBatches` 배열 기반 다중 배치 관리
- [ ] 진행 중 재오픈 시 confirm 버튼 라벨 전환
- [ ] 새 배치 추가 시 기존 row 유지하고 아래에 append
- [ ] 모든 배치 완료 시 배지 제거 + `browse()` 재조회
- [ ] 탭 새로고침/닫기 시 기존 동작 유지 (브라우저 abort → 서버 cancel)
- [ ] `queued` 이벤트 수신 시 해당 배치 row를 "대기 중(큐잉)"으로 표시

### 서버
- [ ] `Handler.importMu sync.Mutex` 추가
- [ ] `handleImportURL`에서 `queued` 이벤트 emit 후 mutex acquire, defer release
- [ ] mutex 대기 중 `r.Context()` cancel 시 바로 리턴 (대기 포기)
- [ ] `sseQueued` 타입 추가, `writeSSEEvent`로 기존 스키마에 맞춰 emit

### 회귀 방지
- [ ] 단일 배치 전체 흐름 (열기 → 입력 → 가져오기 → 진행 → 닫기 → 완료 → browse 재조회)
- [ ] 기존 에러 코드(`too_large`, `download_timeout`, `ffmpeg_error` 등) 모두 그대로
- [ ] HLS 배치 흐름 그대로
- [ ] 설정 스냅샷 (§2.7) 규칙 유지 — 각 배치는 해당 POST 시점의 설정으로 고정
- [ ] 썸네일/duration 사이드카 생성 흐름 유지

---

## 7. Testing Strategy

### 단위 테스트 (`internal/handler/import_url_test.go`)
- 기존 테스트 그대로 통과해야 함 (단일 배치).
- 신규: 동시 2개 POST 요청이 mutex로 직렬화되는지. 첫 번째 요청의 SSE가 끝나기 전까지 두 번째는 `queued` 이벤트 후 블록됨을 확인.
- 신규: 두 번째 요청의 context를 먼저 취소하면 mutex acquire 없이 리턴됨.

### 수동 시나리오
1. URL 2개 배치 시작 → 모달 닫기 → 배지 확인 → 모달 재오픈 → 진행 확인 → 완료 → browse 재조회
2. 1개 배치 진행 중 새 배치 추가 → 첫 배치 완료 후 두 번째 시작 확인 → 각 batch 별 row 분리 표시
3. 진행 중 탭 새로고침 → 서버 로그에서 context cancel 확인 → 재접속 후 배지 없음
4. HLS 배치 같은 흐름으로 확인
5. 배치 중 설정 PATCH (`url_import_max_bytes` 하향) → 진행 중 배치는 원래 값 유지 / 새 배치부터 새 값 적용

---

## 8. 구현 순서 (제안)

1. 서버: `importMu` + `queued` 이벤트 + 단위 테스트 (회귀 방지 우선)
2. 클라이언트: `urlBatches` 배열로 상태 모델 교체 (기존 동작 동일하게 유지)
3. 클라이언트: `closeURLModal()` abort 제거
4. 클라이언트: 미니 배지 추가
5. 클라이언트: 진행 중 재오픈 + 새 배치 추가 UX
6. 수동 시나리오 검증
7. `SPEC.md §2.6` 본문 갱신 (개발 메모 → 정식 스펙 병합)
8. `tasks/todo.md`에 완료 체크

---

## 9. Open Questions / Risks

- **미니 배지 디자인**: 기존 헤더 공간이 빠듯하면 아이콘+숫자 조합만으로 축약. 구현 시 화면 보고 결정.
- **`sync.Mutex`의 context-aware 취소**: Go 표준 `Mutex`는 cancel 불가. TryLock 루프 대신 semaphore 채널 (`chan struct{}` size 1) 패턴이 더 깔끔할 수 있음. 구현 시 선택 — 외부 행동 동일.
- **다중 배치 HTTP 연결 수**: 배치 N개 동시 → 브라우저는 호스트당 동시 연결 ~6개 제한. 단일 사용자 + 순차 제출 가정이면 문제 없음. 한 번에 7개 이상 쌓으면 나중 것이 큐잉됨 (브라우저 레벨) — 서버 mutex와 무관하게 정상 작동.
