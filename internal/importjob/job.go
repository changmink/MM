// Package importjob는 URL 가져오기 작업들의 인메모리 레지스트리를 보관한다.
//
// Job 하나는 handleImportURL이 다운로드 중인 URL 배치 하나를 나타낸다.
// Job의 수명은 그것을 만든 HTTP 요청과 분리되어 있다 — 클라이언트가 탭을
// 닫거나 새로 고침하면 요청 고루틴은 반환되지만 Job은 자연 종료되거나
// 사용자가 취소할 때까지 계속된다. 새 SSE 구독자(다른 탭이거나 새로 고침
// 이후)는 진행 중에 붙어 즉시 현재 상태 스냅샷을 받고, 이후의 라이브
// progress를 이어받는다.
package importjob

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// defaultSubBuffer는 구독자별 채널 용량이다. 버퍼가 꽉 찬 구독자에게는
// Publish가 이벤트를 떨어뜨려, 한 명의 느린 소비자(예: JS 이벤트 루프가
// 막힌 탭)가 다른 모두를 위해 이벤트를 만드는 워커 고루틴을 멈추지 않도록
// 한다. 이벤트를 놓친 구독자는 재접속해 스냅샷을 다시 읽어 복구한다.
const defaultSubBuffer = 64

// Status는 Job의 라이프사이클 상태다.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// IsTerminal은 상태가 더 이상 이벤트를 발행하지 않는 종료된 Job인지
// 보고한다.
func (s Status) IsTerminal() bool {
	return s == StatusCompleted || s == StatusFailed || s == StatusCancelled
}

// URLState는 Job 안에 있는 단일 URL의 스냅샷 뷰다. 새 SSE 구독자는
// JobSnapshot의 일부로 이 값을 받아 모든 progress 이벤트를 재생하지
// 않고도 행별 UI를 재구성할 수 있다.
type URLState struct {
	URL      string   `json:"url"`
	Name     string   `json:"name,omitempty"`
	Type     string   `json:"type,omitempty"`
	Status   string   `json:"status"` // pending | running | done | error | cancelled
	Received int64    `json:"received"`
	Total    int64    `json:"total,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// Summary는 배치별 종료 카운터 묶음으로, Job 종료 시 마지막 SSE 이벤트로
// 발행된다.
type Summary struct {
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Cancelled int `json:"cancelled"`
}

// Event는 `data: <Data>\n\n` 형태로 wire에 그대로 쓸 수 있게 마샬된 SSE
// 페이로드다. Phase는 JSON의 `phase` 필드를 미러링해 Data를 다시 파싱하지
// 않고도 라우팅할 수 있게 한다.
type Event struct {
	Phase string
	Data  json.RawMessage
}

// JobSnapshot은 새 구독자에게 보내고 GET /api/import-url/jobs의 본문으로
// 사용되는, JSON 직렬화 가능한 Job 뷰다.
type JobSnapshot struct {
	ID        string     `json:"id"`
	DestPath  string     `json:"destPath"`
	Status    Status     `json:"status"`
	CreatedAt time.Time  `json:"createdAt"`
	URLs      []URLState `json:"urls"`
	Summary   *Summary   `json:"summary,omitempty"`
}

// Job은 URL 가져오기 배치 하나다. 외부에 노출된 상태 변형 메서드들은 모두
// 고루틴 안전하다 — reader는 내부를 직접 건드리지 말고 Snapshot을 사용한다.
type Job struct {
	ID        string
	DestPath  string
	CreatedAt time.Time

	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	status     Status
	urls       []URLState
	summary    *Summary
	subs       map[uint64]chan Event
	nextSubID  uint64
	urlCancels map[int]context.CancelFunc
	done       chan struct{} // SetStatus가 terminal 상태로 전이할 때 close 된다
	doneClosed bool
}

// Ctx는 Job의 컨텍스트를 반환한다 — 워커는 HTTP 요청 컨텍스트 대신 이것을
// urlfetch에 넘겨야 한다. 이를 취소하면(Cancel을 통하거나 부모 서버 컨텍스트
// 종료를 통해) 배치 전체가 종료된다.
func (j *Job) Ctx() context.Context {
	return j.ctx
}

// Status는 현재 라이프사이클 상태를 반환한다.
func (j *Job) Status() Status {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.status
}

// IsActive는 Job이 아직 queued 또는 running 상태인지 보고한다.
func (j *Job) IsActive() bool {
	return !j.Status().IsTerminal()
}

// SetStatus는 Job을 새 상태로 전이시킨다. 호출자가 해당 SSE 이벤트
// (예: summary)를 별도로 발행해야 Status 필드와 브로드캐스트가 동기화
// 상태로 유지된다. terminal 상태로 전이하면 (a) Done 채널을 close해
// graceful shutdown / WaitAll이 블록을 풀고, (b) 모든 구독자 채널을 close해
// 마지막 summary 프레임이 가득 찬 구독자 버퍼에서 떨어졌더라도 이벤트
// 스트림 읽기를 블록하던 HTTP 핸들러가 즉시 반환하게 한다. 순서가 중요하다 —
// 구독자에게 최종 summary를 보이고 싶다면 SetStatus(terminal) 전에 Publish
// 해야 한다.
func (j *Job) SetStatus(s Status) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.status = s
	if s.IsTerminal() && !j.doneClosed {
		close(j.done)
		j.doneClosed = true
		for id, ch := range j.subs {
			delete(j.subs, id)
			close(ch)
		}
	}
}

// Done은 Job이 terminal 상태에 도달하면 close되는 채널을 반환한다.
// graceful shutdown(Registry.WaitAll)과 워커 완료를 결정적으로 기다리려는
// 테스트가 사용한다.
func (j *Job) Done() <-chan struct{} {
	return j.done
}

// SetSummary는 종료 카운터를 기록한다. Status에는 영향을 주지 않는다 —
// 호출자가 SetStatus를 별도로 부른다.
func (j *Job) SetSummary(s Summary) {
	j.mu.Lock()
	defer j.mu.Unlock()
	cp := s
	j.summary = &cp
}

// UpdateURL은 Job 뮤텍스 아래에서 idx의 URLState에 fn을 적용한다. fn은
// 배타적 접근으로 실행되며, 짧아야 하고 데드락을 유발할 Job 메서드를 다시
// 호출해서는 안 된다.
func (j *Job) UpdateURL(idx int, fn func(*URLState)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if idx < 0 || idx >= len(j.urls) {
		return
	}
	fn(&j.urls[idx])
}

// URLStatus는 idx의 URL 단위 status 문자열을 반환한다 — 범위를 벗어난
// 인덱스에는 빈 문자열을 반환한다. 워커 루프가 매 반복마다 Snapshot()의
// 깊은 복사 비용 없이 사전 취소를 검사할 수 있도록 만든 가벼운 접근자다.
func (j *Job) URLStatus(idx int) string {
	j.mu.Lock()
	defer j.mu.Unlock()
	if idx < 0 || idx >= len(j.urls) {
		return ""
	}
	return j.urls[idx].Status
}

// URLCount는 Job 안의 URL 개수를 반환한다 — Create 시점에 고정된다.
// Snapshot 없이 `index` 쿼리 파라미터를 검증해야 하는 핸들러를 위한 가벼운
// 접근자다.
func (j *Job) URLCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.urls)
}

// Snapshot은 Job의 외부 노출 상태를 깊은 복사로 반환한다 — JSON 인코더나
// 다른 고루틴에 전달해도 안전하다. 슬라이스 길이와 인덱스별 `URL` 필드는
// Create 이후 불변이며, Status / Received / Total / Warnings / Error /
// Name / Type 만 변할 수 있다. 따라서 워커가 UpdateURL로 URLState를 동시
// 변형하더라도, 인덱스 기반 작업(예: cancel-marker 루프)을 위해 스냅샷을
// 순회하는 것은 옳다.
func (j *Job) Snapshot() JobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.snapshotLocked()
}

// snapshotLocked은 j.mu를 잡지 않고 깊은 복사를 만든다 — 호출자가 이미
// 잡고 있어야 한다. SubscribeWithSnapshot이 한 번의 잠금 안에서 스냅샷
// 캡처와 구독자 등록을 원자적으로 수행할 수 있도록 분리된 함수다.
func (j *Job) snapshotLocked() JobSnapshot {
	urlsCopy := make([]URLState, len(j.urls))
	for i, u := range j.urls {
		urlsCopy[i] = u
		if u.Warnings != nil {
			urlsCopy[i].Warnings = append([]string(nil), u.Warnings...)
		}
	}
	var sum *Summary
	if j.summary != nil {
		s := *j.summary
		sum = &s
	}
	return JobSnapshot{
		ID:        j.ID,
		DestPath:  j.DestPath,
		Status:    j.status,
		CreatedAt: j.CreatedAt,
		URLs:      urlsCopy,
		Summary:   sum,
	}
}

// Subscribe는 새 라이브 이벤트 리스너를 등록한다. 반환된 채널은
// unsubscribe 함수에 의해 close되거나, Job이 terminal 상태에 도달하면
// 자동으로 close된다 — 조기 반환 시 채널 누수를 막기 위해 호출자는 항상
// unsubscribe를 defer 해야 한다. 구독 시점에 Job이 이미 terminal이면 반환된
// 채널은 미리 close되어 호출자의 읽기 루프가 즉시 종료된다.
//
// 호출자가 초기 스냅샷도 필요로 하면 SubscribeWithSnapshot을 쓴다 —
// 이 변종은 한 번의 뮤텍스 잠금 안에서 스냅샷을 캡처하고 구독자를 등록하므로,
// 그 사이에 발행된 이벤트가 양쪽 모두에 들어가는 일이 없다.
func (j *Job) Subscribe() (<-chan Event, func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.subscribeLocked()
}

// SubscribeWithSnapshot은 단일 j.mu 잠금 안에서 JobSnapshot을 원자적으로
// 캡처하고 새 구독자를 등록한다. 이 호출이 반환된 뒤 발행되는 이벤트는
// 채널로만 전달된다 — 스냅샷과 채널 모두에 중복되는 일이 없다. Subscribe
// 처럼 unsubscribe를 defer로 짝지어야 한다.
func (j *Job) SubscribeWithSnapshot() (JobSnapshot, <-chan Event, func()) {
	j.mu.Lock()
	defer j.mu.Unlock()
	snap := j.snapshotLocked()
	ch, unsub := j.subscribeLocked()
	return snap, ch, unsub
}

// subscribeLocked는 잠금이 잡힌 상태의 Subscribe 본체다. 호출자가 j.mu를
// 잡고 있어야 한다. 새 버퍼 채널의 receive end와, 호출 시 j.mu를 다시 잡는
// unsubscribe 함수를 반환한다.
func (j *Job) subscribeLocked() (<-chan Event, func()) {
	ch := make(chan Event, defaultSubBuffer)
	if j.status.IsTerminal() {
		close(ch)
		return ch, func() {}
	}
	if j.subs == nil {
		j.subs = make(map[uint64]chan Event)
	}
	id := j.nextSubID
	j.nextSubID++
	j.subs[id] = ch
	return ch, func() {
		j.mu.Lock()
		defer j.mu.Unlock()
		if c, ok := j.subs[id]; ok {
			delete(j.subs, id)
			close(c)
		}
	}
}

// Publish는 ev를 모든 활성 구독자에게 브로드캐스트한다. 송신은
// non-blocking이라 — 버퍼가 꽉 찬 구독자는 이 이벤트를 잃고, 재접속 시
// 스냅샷으로 복구한다.
func (j *Job) Publish(ev Event) {
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, ch := range j.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Cancel은 Job 전체 컨텍스트를 트리거한다. 워커는 이 컨텍스트를 urlfetch에
// 넘긴다. Status는 여기서 바꾸지 않는다 — 워커가 취소를 관측하고 정리가
// 끝난 뒤에 SetStatus(StatusCancelled)를 호출한다.
func (j *Job) Cancel() {
	j.cancel()
}

// RegisterURLCancel은 URL 단위 cancel 함수를 등록한다. 외부 호출자
// (cancel HTTP 핸들러)가 배치 전체를 중단하지 않고 fetch 하나만 멈출 수
// 있게 한다. 워커는 defer로 UnregisterURLCancel을 호출해 맵을 깨끗이 유지한다.
func (j *Job) RegisterURLCancel(idx int, cancel context.CancelFunc) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.urlCancels == nil {
		j.urlCancels = make(map[int]context.CancelFunc)
	}
	j.urlCancels[idx] = cancel
}

// UnregisterURLCancel은 URL 단위 cancel 등록을 제거한다. 등록 항목이
// 없어도 호출 안전하다.
func (j *Job) UnregisterURLCancel(idx int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	delete(j.urlCancels, idx)
}

// CancelKind는 URL 단위 CancelOne 시도의 결과를 분류한다.
type CancelKind int

const (
	// CancelKindNone — URL이 알 수 없거나(범위 밖) 이미 terminal. 호출자는
	// 409로 응답해야 한다.
	CancelKindNone CancelKind = iota
	// CancelKindRunning — URL 단위 cancel 함수가 등록되어 있었고 방금
	// 트리거됐다. 워커는 ctx.Done()을 관측하고 urlfetch는 cancellation
	// 에러를 반환하며, fetchOneJob이 라이프사이클 error("cancelled")
	// 프레임을 발행한다. 호출자는 또 다른 error 이벤트를 발행해서는 안 된다.
	CancelKindRunning
	// CancelKindPending — URL이 아직 pending이었고(URL 단위 cancel이 등록
	// 안 됨, status == "pending") 원자적으로 cancelled로 표시됐다. 워커의
	// fetch 직전 URLStatus 검사가 이 인덱스를 스킵한다. 워커의 라이프사이클
	// 경로가 이 인덱스에 대해 우회되므로, 호출자는 반환된 URL과 함께
	// error("cancelled")를 반드시 발행해야 한다.
	CancelKindPending
)

// CancelOne은 URL idx의 취소를 원자적으로 시도한다. registered-cancel 분기와
// pending-mark 분기 모두 단일 뮤텍스 잠금 안에서 실행되어, 두 핸들러 호출
// (CancelURL과 MarkPendingCancelled) 사이에 워커가 RegisterURLCancel을
// 끼워넣을 수 있던 기존 race 창을 닫는다.
//
// fetchOneJob은 RegisterURLCancel 직후 URLStatus를 다시 확인하므로, 워커가
// fetchOneJob에 진입하기 직전 결정된 CancelKindPending 판단이 어떤 HTTP
// 요청보다 먼저 관측된다.
func (j *Job) CancelOne(idx int) (url string, kind CancelKind) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if idx < 0 || idx >= len(j.urls) {
		return "", CancelKindNone
	}
	if cancel, registered := j.urlCancels[idx]; registered {
		cancel()
		return j.urls[idx].URL, CancelKindRunning
	}
	s := &j.urls[idx]
	if s.Status != "pending" {
		return "", CancelKindNone
	}
	s.Status = "cancelled"
	s.Error = "cancelled"
	return s.URL, CancelKindPending
}
