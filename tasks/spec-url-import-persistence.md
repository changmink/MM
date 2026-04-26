# Spec: URL Import — 새로고침/탭 재오픈 시 잡 유지 (서버 잡 레지스트리)

> 부모 SPEC: [`/SPEC.md`](../SPEC.md). 이 문서는 그중 **§2.6 URL Import**의 영속성 모델을 확장한다.
>
> 선행: [`spec-url-import-background.md`](spec-url-import-background.md) (status: merged) — "모달 닫아도 백그라운드 진행"까지 다룸. 본 문서는 그 §1 Non-goals 첫 항목("탭/브라우저 닫힘 후에도 서버가 끝까지 다운로드")을 별도 spec으로 분리한 것.
>
> **Status: merged** — 구현 완료 후 `SPEC.md §2.6` / `§5.1` 본문에 반영됨 (Phase 20 J6). 이후 변경은 `SPEC.md`를 단일 출처로 삼고 이 문서는 결정 이력 보존용으로 그대로 둔다.

## 1. Objective

현재(2026-04-25 develop): 모달 닫기는 다운로드를 유지하지만 **탭 새로고침/탭 닫기 → 서버 `r.Context()` cancel → 진행 중 fetch 중단 + 임시파일 정리**된다 (`internal/handler/import_url.go:172`, `spec-url-import-background.md §3.5`). 대용량 HLS/원본 다운로드 중 실수로 새로고침해도 처음부터 받아야 한다.

**목표:**
- 클라이언트 세션이 끊겨도(새로고침/탭 닫기/탭 재오픈) **서버 잡은 진행 중 유지**.
- 페이지 로드 직후 헤더 배지에 진행 상황 자동 복원, 클릭으로 모달 + row 복원.
- 같은 사용자가 탭 두 개 열면 양쪽에 동일 진행 상황을 실시간 fan-out.
- 사용자가 명시적으로 배치 취소 가능 (현재는 모달 닫으면 멈출 방법 없음).
- 완료/실패/취소 이력은 사용자가 dismiss할 때까지 보임. 서버 재시작 시 자동 정리.

**Target user:** 단일 사용자(개인).

**Non-goals:**
- 서버 프로세스 재시작 후 진행 중 잡 자동 재개 (디스크 영속 큐 필요 — 별도 spec).
- HTTP Range로 끊긴 바이트 이어받기.
- 인증/멀티 사용자 격리.
- 잡 history 자동 TTL — 단일 사용자 가정상 unbounded growth 위험 낮음. 사용자 dismiss 또는 재시작이 정리.

---

## 2. Scope

### In scope
- **서버:**
  - 인메모리 잡 레지스트리 (`internal/importjob/`).
  - SSE broadcaster (잡 ID당 N subscriber fan-out).
  - 신규 API: 활성/이력 snapshot, SSE 구독, 배치 취소, 이력 dismiss, 일괄 정리.
  - 기존 `POST /api/import-url`은 잡 등록 후 첫 subscriber로 동작 (응답 SSE 그대로).
  - graceful shutdown 시 모든 잡 cancel + 임시파일 정리.
- **클라이언트:**
  - 페이지 로드 시 잡 snapshot 받아 row + 배지 복원.
  - 두 번째 탭/새로고침 후에는 GET subscribe로 진행 stream 수신.
  - 모달 row 취소 버튼, 종료 row dismiss 버튼, "모두 지우기".

### Out of scope
- 디스크 영속 / 재시작 복구.
- HTTP Range resume.
- 동시 잡 수 늘리기 — 기존 `importSem`(size 1)으로 직렬 처리 그대로.
- 인증/세션.

---

## 3. UX Spec

### 3.1 새로고침/탭 재오픈 흐름
1. 사용자가 URL 다운로드 중 새로고침
2. 페이지 로드 직후 클라이언트가 `GET /api/import-url/jobs` 호출
3. 활성 잡이 1개 이상 → 헤더 배지 표시 (`URL ↓ 3/5`, 기존 §3.2 형식 그대로)
4. 활성 잡마다 `GET /api/import-url/jobs/{id}/events` SSE 구독 (snapshot replay 후 라이브)
5. 모달은 자동으로 열리지 않음 — 배지 클릭 시 열림 + row 복원

### 3.2 다중 탭 fan-out
- 잡 ID당 subscriber slice (`map[jobID][]chan Event`).
- 한 탭에서 잡 시작 → 다른 탭이 새로고침/`/jobs` polling 없이도 페이지 로드 시 자동 발견.
- 탭 두 개 동시 띄워두는 경우는 polling이 아니라 **페이지 로드 시점에 한 번 GET /jobs 후 SSE 구독**이라, 새 잡 발견은 새로고침 또는 같은 세션 내 POST 응답 시점에만 일어난다. (실시간 push는 V2 — 비용 대비 가치 낮음.)
- 한 잡의 진행은 모든 활성 subscriber에게 즉시 broadcast.

### 3.3 취소

**개별 URL 취소** (주된 동작):
- 모달의 각 진행 중/대기 중 URL row 우측에 "취소" 버튼.
- `POST /api/import-url/jobs/{id}/cancel?index=N` → 서버는 해당 URL의 per-URL ctx cancel → 진행 중이면 fetch 중단 + 부분파일 정리(기존 `urlfetch.Fetch` defer), 미시작이면 즉시 skip → `error:"cancelled"` 1회 emit → broadcast.
- 잡 진행은 다음 URL부터 계속.

**배치 전체 취소** (편의):
- 모달 활성 배치 헤더에 "전체 취소" 버튼.
- `POST /api/import-url/jobs/{id}/cancel` (index 없음) → 잡 ctx cancel + 모든 미종료 URL을 cancelled로 표기 → summary emit → broadcast.

**잡 status 결정:**
- 개별 cancel만 일부 → 잡 status는 그대로(`running` 유지). 종료 시 summary에 `cancelled: N` 카운트만 표시.
- 모든 URL이 종료되었고 그중 1개 이상 cancelled면 → 잡 status = `cancelled` (succeeded=0이고 cancelled>0인 경우만; 1개라도 succeeded면 `completed`).
- 배치 전체 취소 → 잡 status = `cancelled`.

취소된 잡/URL도 history에 남음 (사용자가 dismiss할 때까지).

### 3.4 이력 dismiss
- 종료 상태(`completed`/`failed`/`cancelled`) 배치 row 우측에 X 버튼.
- `DELETE /api/import-url/jobs/{id}` → 서버 history 제거 + 모든 subscriber에게 `{phase:"removed"}` broadcast.
- 모달 footer에 "완료 항목 모두 지우기" → `DELETE /api/import-url/jobs?status=finished`.

### 3.5 서버 재시작
- 인메모리 레지스트리 → 모두 손실.
- graceful shutdown 시 모든 잡 ctx cancel, 진행 중 fetch는 자연스레 종료, 임시파일은 기존 `urlfetch.Fetch` 정리 흐름 유지.
- 사용자가 새로고침하면 `/jobs` = 빈 응답 → 배지 없음 → 통상 흐름.

### 3.6 잡 상태 머신

```
queued ──► running ──► completed
                  ├──► failed     (모든 URL 실패)
                  └──► cancelled  (사용자 cancel)
```

- `queued`: 등록되었으나 `importSem` 대기 중.
- `running`: 적어도 1개 URL 처리 시작.
- 종료 상태 진입 후에는 history에서 dismiss될 때까지 보임.

---

## 4. API

### 4.1 잡 ID 모델
- 형식: `imp_<base32lower 8자>` (예: `imp_a3f8k2lm`). `crypto/rand` 8 byte → base32 encode → 첫 8자.
- 단일 사용자라 충돌/추측 우려 낮음. URL-safe.

### 4.2 `POST /api/import-url?path=<rel>` (개정)
- 기존 동작 유지 + **응답 첫 SSE 이벤트로 `register` 추가**:
  ```json
  {"phase":"register","jobId":"imp_xxx"}
  ```
- 이후 기존 `queued / start / progress / done / error / summary` 그대로 emit.
- 기존 클라이언트 호환: phase별 switch에서 unknown은 무시 (구현 시 `web/app.js` 확인 필요 — Open Q1).
- 클라이언트는 jobId를 받아 batch 객체에 저장 (sessionStorage 불필요 — 새로고침 시 `/jobs`로 다시 발견).

### 4.3 `GET /api/import-url/jobs` (신규)
- 응답 200 JSON:
  ```json
  {
    "active":   [Job, ...],
    "finished": [Job, ...]
  }
  ```
- `Job`:
  ```json
  {
    "id":        "imp_xxx",
    "destPath":  "movies/2026",
    "status":    "running",
    "createdAt": "2026-04-25T12:00:00Z",
    "urls": [
      {
        "url":      "https://...",
        "name":     "foo.mp4",
        "type":     "video",
        "status":   "done",
        "received": 12345,
        "total":    67890,
        "warnings": []
      }
    ],
    "summary": { "succeeded": 1, "failed": 0, "cancelled": 0 }
  }
  ```
  - `summary`는 종료 상태일 때만 포함.
  - `active`/`finished`는 createdAt asc 정렬.

### 4.4 `GET /api/import-url/jobs/{id}/events` (신규)
- SSE 응답.
- 첫 이벤트: `{phase:"snapshot", job: <Job>}` — 위 모양 그대로.
- 이후 진행 이벤트 stream (스키마는 기존 `start/progress/done/error/summary/queued`와 동일, 단 `register`는 이미 등록된 잡이라 emit 안 함).
- 잡이 이미 종료 상태면 snapshot emit 후 connection close.
- 미존재 ID → 404.

### 4.5 `POST /api/import-url/jobs/{id}/cancel` (신규)
- 쿼리 파라미터:
  - `index=N` (선택): 해당 URL만 취소.
  - 없으면: 잡 전체 취소.
- 응답: 204 No Content.
- 동작:
  - **개별**: per-URL ctx cancel → in-flight면 fetch 중단 + 부분파일 정리, 미시작이면 skip 마킹. `error:"cancelled"` emit. 잡 진행은 계속.
  - **전체**: 잡 ctx cancel → 모든 미종료 URL을 cancelled로 emit → summary emit.
- 이미 종료된 URL/잡 → 409.
- 미존재 잡 또는 잘못된 index → 404 / 400.

### 4.6 `DELETE /api/import-url/jobs/{id}` (신규)
- 응답: 204.
- 활성 잡(`queued`/`running`) → 409 (먼저 cancel 필요).
- 종료 잡 → history에서 제거, broadcast `{phase:"removed", jobId:"imp_xxx"}`.
- 미존재 → 404.

### 4.7 `DELETE /api/import-url/jobs?status=finished` (신규)
- 응답 200: `{"removed": <int>}`.
- 종료 상태 모든 잡 일괄 제거. 각각에 대해 `removed` broadcast.

---

## 5. 서버 구현

### 5.1 `internal/importjob/registry.go` (신규)

```go
package importjob

type Status string
const (
    StatusQueued    Status = "queued"
    StatusRunning   Status = "running"
    StatusCompleted Status = "completed"
    StatusFailed    Status = "failed"
    StatusCancelled Status = "cancelled"
)

type Event struct {
    Phase string          // register / queued / start / progress / done / error / summary / removed
    Data  json.RawMessage // 기존 SSE payload 그대로
}

type URLState struct {
    URL      string
    Name     string
    Type     string
    Status   string  // pending / running / done / error / cancelled
    Received int64
    Total    int64
    Warnings []string
    Error    string
}

type Job struct {
    ID        string
    DestPath  string
    CreatedAt time.Time
    Ctx       context.Context
    Cancel    context.CancelFunc

    mu       sync.Mutex
    status   Status
    urls     []URLState
    summary  *Summary
    history  []Event       // for snapshot replay
    subs     []chan Event
}

type Registry struct {
    mu   sync.RWMutex
    jobs map[string]*Job
}

func New() *Registry
func (r *Registry) Create(parentCtx context.Context, destPath string, urls []string) *Job
func (r *Registry) Get(id string) (*Job, bool)
func (r *Registry) List() (active, finished []*Job)
func (r *Registry) Remove(id string) error
func (r *Registry) RemoveFinished() int
func (r *Registry) CancelAll()  // graceful shutdown
```

`Job` 메서드:
```go
func (j *Job) Subscribe() (events <-chan Event, unsubscribe func())
func (j *Job) Publish(ev Event)            // history append + broadcast
func (j *Job) Snapshot() JobSnapshot
func (j *Job) UpdateURL(idx int, fn func(*URLState))
func (j *Job) SetStatus(s Status)
```

- `Publish`는 `mu.Lock` 안에서 history append + 모든 sub에 non-blocking send (slow subscriber는 drop 또는 detach — 정책 결정 필요, 일단 buffer 64 + drop).
- `Subscribe`는 unsubscribe까지 항상 페어. defer로 cleanup 보장.

### 5.2 `Job.Ctx` ≠ request context
- 핵심 변경: 기존 코드는 `r.Context()` 사용 → 클라이언트 disconnect 시 cancel.
- 신규: `Job.Ctx`는 **server lifetime** 또는 **사용자 cancel**까지만 살아있음 (`context.WithCancel(serverCtx)`).
- `serverCtx`는 main.go의 root context. graceful shutdown 시 cancel.

### 5.3 `Handler.handleImportURL` 개정 (`internal/handler/import_url.go`)
- request 파싱 후 → `job := h.registry.Create(h.serverCtx, rel, urls)` → `emit(register{jobId})`.
- 기존 emit 함수가 두 가지 일을 함:
  1. response writer로 SSE 직접 write (현 동작 — 첫 subscriber).
  2. `job.Publish(ev)` 호출해서 다른 subscriber에게도 broadcast + history 누적.
- 또는 더 깔끔하게: handler 자체가 그냥 첫 subscriber로 등록하고, 모든 emit은 `job.Publish` → handler goroutine은 `job.Subscribe()` 채널을 drain해서 SSE write. fetch goroutine은 `job.Publish`만 호출.
  - **이쪽 디자인을 권장** — broadcast 로직 단일화.
- handler context disconnect → handler return → handler subscriber만 cleanup. **잡은 계속**.
- importSem 직렬화는 그대로 — fetch loop 진입 직전 acquire.
- **per-URL ctx**: `fetchOneSSE`에 진입할 때 `Job`에 해당 index의 `cancel` func을 등록 (`job.RegisterURLCancel(idx, cancel)`), defer로 unregister. registry의 cancel 핸들러가 이 함수를 호출.
- `Job.Status` 전이: Create 직후 `queued` → importSem acquire 후 `running` → fetch loop 끝나면 succeeded/failed/cancelled 카운트로 종료 상태 결정 (succeeded≥1 → completed, 그 외 cancelled가 있으면 cancelled, 아니면 failed).

### 5.4 `internal/handler/import_url_jobs.go` (신규)
- `handleListJobs(w, r)` — `GET /api/import-url/jobs`
- `handleSubscribeJob(w, r)` — `GET /api/import-url/jobs/{id}/events`
- `handleCancelJob(w, r)` — `POST /api/import-url/jobs/{id}/cancel`
- `handleDeleteJob(w, r)` — `DELETE /api/import-url/jobs/{id}` 또는 `DELETE /api/import-url/jobs?status=finished` 분기
- 라우팅: `mux.HandleFunc("/api/import-url/jobs", ...)` + `mux.HandleFunc("/api/import-url/jobs/", ...)` — path suffix 파싱 (id, /events, /cancel).

### 5.5 graceful shutdown
- `cmd/server/main.go`: SIGTERM/SIGINT signal handler에서:
  1. `httpServer.Shutdown(ctx5s)` — 진행 중 응답은 5s까지 기다림.
  2. `registry.CancelAll()` — 진행 중 잡 ctx cancel.
  3. `handler.Close()` — thumbPool shutdown.
- 진행 중 fetch는 ctx cancel로 자연스레 정리. 임시파일은 `urlfetch.Fetch`의 cleanup defer가 처리.

### 5.6 메모리/리소스 한도
- 활성 잡 수: `importSem` size 1이라 동시 활성 1, 나머지는 queued.
- 큐잉 cap: `maxQueuedJobs = 100` — 초과 시 `POST /api/import-url`이 429 응답.
- history 이벤트 cap: `maxHistoryEvents = 1000` per job — 초과 시 oldest progress부터 drop. start/done/error/summary는 항상 보존. (대부분의 progress는 새 subscriber에게는 마지막 누적값만 의미 있음 → snapshot에서 `URLState.Received`로 이미 표현됨, 그래서 history의 progress는 사실 안 보내도 됨.)
- subscriber buffer: 64. 가득 차면 해당 sub은 drop + close. 클라이언트는 SSE 끊김 → 재연결.

---

## 6. 클라이언트 구현 (`web/app.js`)

### 6.1 상태 모델
- 기존 `urlBatches` (현 코드 — `spec-url-import-background §5.1`)는 그대로 유지하되 **jobId를 키로**.
- `urlBatches[i].jobId`, `urlBatches[i].subscription` (SSE 객체) 추가.

### 6.2 `bootstrapURLJobs()` (신규, 페이지 로드 시 1회)
1. `fetch('/api/import-url/jobs')` → active/finished
2. 각 잡 → row DOM 복원 (snapshot.urls 기반), `urlBatches`에 push
3. active 잡마다 `subscribeToJob(jobId)` (EventSource로 GET /events) — snapshot 무시(이미 받았으니), live event만 사용
4. `updateURLBadge()` 호출

### 6.3 `subscribeToJob(jobId)` (신규)
- `new EventSource('/api/import-url/jobs/' + jobId + '/events')`.
- 이벤트 핸들러는 기존 POST 응답 파서와 **동일 함수** 재사용 (스키마 호환).
- snapshot 이벤트 수신 시: bootstrap에서 이미 처리했으면 무시, 아니면 row 갱신.
- removed 이벤트 수신 시: row 제거.

### 6.4 `submitURLImport()` 개정
- 기존대로 `POST /api/import-url`.
- 응답 첫 이벤트 `register`에서 jobId 추출 → batch 객체에 저장.
- 이후 같은 SSE stream으로 진행 받음 (POST 응답이 첫 subscriber 역할).

### 6.5 취소 / dismiss UI
- 모달 row hover 또는 always-visible:
  - 활성 row(개별 URL): "취소" 버튼 → `POST /api/import-url/jobs/{id}/cancel?index=N`. 응답 안 기다림 (서버가 broadcast로 상태 갱신).
  - 종료 row(잡 단위): "X" 버튼 → `DELETE /api/import-url/jobs/{id}`. broadcast로 row 제거.
- 활성 배치 헤더: "전체 취소" 버튼 → `POST /api/import-url/jobs/{id}/cancel` (index 없음).
- 모달 footer: "완료 항목 모두 지우기" → `DELETE /api/import-url/jobs?status=finished`.

### 6.6 sessionStorage 미사용
- 서버가 single source of truth. 새로고침 시 항상 `/jobs` GET.

### 6.7 SSE 끊김 재연결
- EventSource는 자동 재연결 (기본). 서버는 동일 jobId에 대해 다시 snapshot replay 후 stream — 멱등.
- POST 응답 SSE (첫 subscriber)는 자동 재연결 안 됨 — 끊기면 클라이언트가 GET subscribe로 자발적 전환.

---

## 7. Acceptance Criteria

### 서버
- [ ] `internal/importjob` 모듈: Registry, Job, Subscribe/Publish/Cancel/Remove
- [ ] `Handler.handleImportURL`이 Registry에 잡 등록, handler subscriber 1개로 동작
- [ ] **클라이언트 disconnect 후에도 잡 진행** (regression: 기존 `import_url.go:172` 동작 변경)
- [ ] `GET /api/import-url/jobs` active/finished snapshot 응답
- [ ] `GET /api/import-url/jobs/{id}/events` snapshot replay + live stream
- [ ] `POST /api/import-url/jobs/{id}/cancel` 잡 전체 cancel + summary broadcast
- [ ] `POST /api/import-url/jobs/{id}/cancel?index=N` 개별 URL cancel (잡 진행은 계속)
- [ ] per-URL ctx 등록/해제 (`Job.RegisterURLCancel`/`Unregister`)
- [ ] `DELETE /api/import-url/jobs/{id}` 종료 잡 dismiss + broadcast
- [ ] `DELETE /api/import-url/jobs?status=finished` 일괄 정리
- [ ] graceful shutdown 시 모든 잡 cancel + 임시파일 정리 (검증)
- [ ] `maxQueuedJobs=100` 초과 시 429
- [ ] importSem 직렬화 유지 (queued → running 전이 검증)

### 클라이언트
- [ ] 페이지 로드 시 `bootstrapURLJobs()` 호출 + 배지/row 복원
- [ ] 새로고침 후 진행 바가 끊김 없이 갱신 (수동 시나리오 1)
- [ ] 두 번째 탭 새로고침 시 같은 잡 진행 표시
- [ ] 모달 row 개별 "취소" 버튼 동작 + broadcast로 양쪽 탭 갱신
- [ ] 배치 헤더 "전체 취소" 버튼 동작
- [ ] 종료 row "X" + "모두 지우기" 동작
- [ ] sessionStorage 사용 안 함
- [ ] 기존 `urlBatches` 흐름과 호환 — `register`/`snapshot`/`removed` 추가 핸들

### 회귀 방지
- 기존 단일/다중 배치, queued, summary, HLS, 설정 스냅샷, 썸네일 사이드카, importSem 직렬화 모두 유지
- 기존 SSE 이벤트 스키마 (`start / progress / done / error / summary / queued`) 변경 없음
- `register / snapshot / removed`만 신규
- 모달 닫기 시 abort 호출 안 하는 동작 (`spec-url-import-background §5.2`) 그대로

---

## 8. Testing Strategy

### 단위 (`internal/importjob/registry_test.go` 신규)
- Subscribe → Publish → 모든 subscriber 수신
- Slow subscriber (buffer full) → drop + detach, 다른 sub은 정상
- Cancel → Job.Ctx done + status = cancelled + Publish summary
- Remove on running job → error
- RemoveFinished → 종료 잡만 제거, 활성은 유지
- Snapshot에서 history replay 후 live event 도착 시 중복 없이 순서대로

### 핸들러 (`internal/handler/import_url_jobs_test.go` 신규)
- POST → 잡 등록 응답에 register 이벤트 포함, jobId 형식 검증
- POST → handler context cancel (클라이언트 disconnect 시뮬레이션) → 잡은 계속 진행 (registry.Get으로 확인)
- 두 번째 GET subscribe → snapshot + 후속 progress 수신
- POST cancel (batch) → 진행 중 잡 status=cancelled, summary broadcast 검증
- POST cancel?index=N → 해당 URL만 cancelled, 잡은 다음 URL로 진행 (status running 유지)
- DELETE on running → 409
- DELETE on finished → 204 + 후속 GET /jobs에서 사라짐
- 429: maxQueuedJobs 초과
- 미존재 jobId → 404

### 기존 테스트 (`internal/handler/import_url_test.go`)
- 기존 케이스 모두 통과 — 단일 배치 흐름 변경 없음
- `register` 이벤트가 첫 SSE 응답에 포함되도록 assertion 추가 (호환 깨지면 안 됨)

### 수동 시나리오
1. 큰 URL 다운로드 시작 → 새로고침 → 배지 복원 + 진행 바 끊김 없이 갱신
2. 탭 두 개 → A에서 시작 → B 새로고침 → 진행 표시
3. URL 5개 배치 진행 중 2번째 URL 개별 취소 → 2번만 cancelled, 3~5는 정상 진행 + 부분파일 정리
4. 다운로드 중 "전체 취소" → 모든 URL 즉시 중단, summary 표시, 부분파일 정리
5. 종료 배치 dismiss → 양쪽 탭에서 row 제거
6. 서버 재시작 → 새로고침 → 배지 없음, 임시파일 정리됨
7. HLS 배치 동일 흐름
8. 큐잉 100개 가득 → 101번째 POST → 429 + 사용자에게 에러 메시지

---

## 9. 구현 순서 (제안)

1. **서버 잡 레지스트리 모듈** + 단위 테스트 (`internal/importjob/`)
2. **graceful shutdown**: main.go에 serverCtx + signal handler (잡 cancel 흐름 먼저 확립)
3. **`Handler.handleImportURL` 개정**: registry 사용, handler subscriber 패턴, `r.Context()` → `Job.Ctx` 분리
4. **회귀 테스트**: 기존 import_url_test.go 모두 통과 + handler disconnect → 잡 계속 진행 신규 케이스
5. **신규 엔드포인트** (jobs/cancel/dismiss) + 핸들러 테스트
6. **클라이언트 bootstrap + subscribe** 분리, register/snapshot/removed 핸들러
7. **취소/dismiss UI** + "모두 지우기"
8. **수동 시나리오 검증**
9. `SPEC.md §2.6 / §5.1` 본문 갱신

---

## 10. Open Questions / Risks

- **Q1: `register` 이벤트가 기존 클라이언트 호환을 깨지 않는가?** — 구현 시 `web/app.js`의 SSE phase switch 확인. unknown phase 무시하면 OK. 깨지면 `register`는 SSE가 아니라 별도 응답 헤더(`X-Job-ID`)로 전달하는 대안.
- **Q2: history에 progress 이벤트를 보존할 것인가?** — 새 subscriber는 snapshot의 `URLState.Received`로 누적값을 받으므로 progress history는 사실상 무용. 보존 안 하는 게 메모리 절약. 단, snapshot 후 첫 progress가 늦으면 UI가 stale로 보일 수 있음 → 그래도 무시 가능.
- **Q3: subscriber buffer 가득 찰 때 drop vs detach**? drop이면 progress 한두 개 놓치는 정도라 안전. summary/done/error 같은 lifecycle 이벤트는 절대 drop되면 안 됨 → buffer 크기를 충분히 (64+) + 그래도 가득 차면 detach 후 클라이언트 재연결에 위임.
- **Q4: HLS 작업의 graceful shutdown** — ffmpeg 자식 프로세스 SIGTERM 시 부분 .ts → .mp4 임시파일이 남는 경우. urlfetch/hls의 cleanup defer가 모든 경로에서 도달하는지 검증 필요.
- **Q5: maxQueuedJobs = 100** — 사용자 결정. 단일 사용자 + 직렬 처리 가정상 사실상 도달 불가능한 수준의 안전장치.
- **Q6: 잡 ID URL exposure** — 단일 사용자/loopback 가정이라 보안 우려 적음. 외부 노출 시 ID 추측 공격은 base32 8자 = 40bit라 충분.
