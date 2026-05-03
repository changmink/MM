package importjob

import (
	"context"
	"encoding/json"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// newTestJob은 Registry.Create를 감싼 얇은 단축이다 — 개별 Job 테스트가
// 단지 생성자를 위해 Registry를 살려둘 필요 없게 만든다.
func newTestJob(t *testing.T, urls ...string) *Job {
	t.Helper()
	reg := New(context.Background())
	job, err := reg.Create("dest", urls)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return job
}

func TestJob_Subscribe_BroadcastsToAll(t *testing.T) {
	job := newTestJob(t, "a")

	subs := make([]<-chan Event, 3)
	unsubs := make([]func(), 3)
	for i := range subs {
		subs[i], unsubs[i] = job.Subscribe()
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	job.Publish(Event{Phase: "start", Data: json.RawMessage(`{"phase":"start"}`)})

	for i, sub := range subs {
		select {
		case ev := <-sub:
			if ev.Phase != "start" {
				t.Errorf("sub %d: got phase %q, want %q", i, ev.Phase, "start")
			}
		case <-time.After(200 * time.Millisecond):
			t.Errorf("sub %d: timed out waiting for event", i)
		}
	}
}

// TestJob_Subscribe_SlowConsumerDropped: 채널을 전혀 비우지 않는 구독자가
// 빠른 구독자를 멈춰서는 안 된다. 빠른 소비자는 실시간으로 드레인하고
// 발행자는 전송 사이에 yield해 협력적 스케줄러가 그것을 돌릴 기회를 갖는다.
// 느린 소비자는 읽지 않으므로 버퍼가 가득 차고 그 뒤 전송은 떨어진다 —
// fast와는 독립적으로.
func TestJob_Subscribe_SlowConsumerDropped(t *testing.T) {
	job := newTestJob(t, "a")

	slow, slowUnsub := job.Subscribe()
	defer slowUnsub()
	fast, fastUnsub := job.Subscribe()
	defer fastUnsub()

	const total = defaultSubBuffer * 3

	var fastReceived atomic.Int32
	fastDone := make(chan struct{})
	go func() {
		for range fast {
			fastReceived.Add(1)
		}
		close(fastDone)
	}()

	for i := 0; i < total; i++ {
		job.Publish(Event{Phase: "progress", Data: json.RawMessage(`{}`)})
		runtime.Gosched()
	}

	// 버퍼에 남아 있는 것을 fast가 드레인할 시간을 주고, goroutine이 종료되도록 close 한다.
	time.Sleep(50 * time.Millisecond)
	fastUnsub()
	<-fastDone

	if got := int(fastReceived.Load()); got != total {
		t.Errorf("fast subscriber received %d events, want %d (publisher must not stall fast)", got, total)
	}

	slowCount := 0
drain:
	for {
		select {
		case <-slow:
			slowCount++
		default:
			break drain
		}
	}
	if slowCount != defaultSubBuffer {
		t.Errorf("slow subscriber received %d events, want %d (buffer cap; rest must drop)", slowCount, defaultSubBuffer)
	}
}

func TestJob_Unsubscribe_StopsDelivery(t *testing.T) {
	job := newTestJob(t, "a")
	sub, unsub := job.Subscribe()

	job.Publish(Event{Phase: "first"})
	select {
	case ev := <-sub:
		if ev.Phase != "first" {
			t.Fatalf("got phase %q, want first", ev.Phase)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timed out waiting for first event")
	}

	unsub()
	job.Publish(Event{Phase: "second"})

	for ev := range sub {
		if ev.Phase == "second" {
			t.Errorf("received event %q after unsubscribe", ev.Phase)
		}
	}
}

func TestJob_Cancel_PropagatesContext(t *testing.T) {
	job := newTestJob(t, "a", "b")

	if err := job.Ctx().Err(); err != nil {
		t.Fatalf("ctx already done before Cancel: %v", err)
	}
	job.Cancel()

	select {
	case <-job.Ctx().Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ctx not done after Cancel")
	}

	// Status는 워커의 책임이다 — Cancel은 컨텍스트만 트리거한다.
	if got := job.Status(); got != StatusQueued {
		t.Errorf("status changed by Cancel alone: got %q, want unchanged", got)
	}
	job.SetStatus(StatusCancelled)
	if got := job.Status(); got != StatusCancelled {
		t.Errorf("status after SetStatus: got %q, want %q", got, StatusCancelled)
	}
}

// TestJob_SetStatus_ClosesSubsOnTerminal: 이벤트 채널 읽기에서 블록된
// 핸들러는 — 마지막 summary 프레임이 가득 찬 구독자 버퍼에서 떨어졌더라도
// — 워커가 terminal 상태에 도달할 때 깨어나야 한다. 이게 없으면 느린
// 클라이언트 + cap-1 ResponseWriter가 클라이언트가 끊을 때까지 요청
// 고루틴을 멈춰 세운다.
func TestJob_SetStatus_ClosesSubsOnTerminal(t *testing.T) {
	for _, terminal := range []Status{StatusCompleted, StatusFailed, StatusCancelled} {
		t.Run(string(terminal), func(t *testing.T) {
			job := newTestJob(t, "a")
			sub, _ := job.Subscribe()

			job.SetStatus(terminal)

			select {
			case _, ok := <-sub:
				if ok {
					t.Errorf("channel still open after SetStatus(%q)", terminal)
				}
			case <-time.After(200 * time.Millisecond):
				t.Errorf("channel not closed within 200ms after SetStatus(%q)", terminal)
			}
		})
	}
}

// TestJob_Subscribe_AfterTerminal은 미리 close된 채널을 반환한다 — Job이
// 끝난 뒤에 도착한 J4 /jobs/{id}/events 구독자가 영원히 블록되지 않도록.
// 그 경로에서는 스냅샷이 진실의 출처다.
func TestJob_Subscribe_AfterTerminal(t *testing.T) {
	job := newTestJob(t, "a")
	job.SetStatus(StatusCompleted)

	sub, unsub := job.Subscribe()
	defer unsub() // no-op이어야 한다
	select {
	case _, ok := <-sub:
		if ok {
			t.Errorf("expected pre-closed channel for terminal job, got open one")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("Subscribe on terminal job returned an open channel")
	}
}

// TestJob_CancelOne_Running: 등록된 URL 단위 cancel은 대상 인덱스의
// 컨텍스트만 트리거하고 호출자에게 CancelKindRunning을 보고한다.
func TestJob_CancelOne_Running(t *testing.T) {
	job := newTestJob(t, "a", "b", "c")

	ctxs := make([]context.Context, 3)
	for i := range ctxs {
		c, cancel := context.WithCancel(context.Background())
		ctxs[i] = c
		job.RegisterURLCancel(i, cancel)
		t.Cleanup(cancel)
	}

	url, kind := job.CancelOne(1)
	if kind != CancelKindRunning {
		t.Fatalf("kind = %v, want CancelKindRunning", kind)
	}
	if url == "" {
		t.Errorf("URL not returned for running cancel")
	}

	select {
	case <-ctxs[1].Done():
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ctx 1 not cancelled after CancelOne(1)")
	}
	for _, idx := range []int{0, 2} {
		if err := ctxs[idx].Err(); err != nil {
			t.Errorf("ctx %d cancelled unexpectedly: %v", idx, err)
		}
	}
}

// TestJob_CancelOne_Pending: 아직 pending인 URL(URL 단위 cancel 등록 없음,
// status == "pending")은 원자적으로 cancelled로 표시되며, 호출자는 라이프
// 사이클 이벤트 발행 책임을 자신이 갖는다는 것을 통보받는다.
func TestJob_CancelOne_Pending(t *testing.T) {
	job := newTestJob(t, "https://a", "https://b")

	url, kind := job.CancelOne(0)
	if kind != CancelKindPending {
		t.Fatalf("kind = %v, want CancelKindPending", kind)
	}
	if url != "https://a" {
		t.Errorf("URL = %q, want https://a", url)
	}
	snap := job.Snapshot()
	if snap.URLs[0].Status != "cancelled" || snap.URLs[0].Error != "cancelled" {
		t.Errorf("urls[0] = %+v, want status+error cancelled", snap.URLs[0])
	}
	if snap.URLs[1].Status != "pending" {
		t.Errorf("urls[1] status = %q, want pending (untouched)", snap.URLs[1].Status)
	}
}

// TestJob_CancelOne_Terminal: 이미 끝난 URL은 취소할 수 없다 — 호출자는
// CancelKindNone을 근거로 409를 응답한다.
func TestJob_CancelOne_Terminal(t *testing.T) {
	job := newTestJob(t, "a")
	job.UpdateURL(0, func(s *URLState) { s.Status = "done" })

	if _, kind := job.CancelOne(0); kind != CancelKindNone {
		t.Errorf("kind for terminal URL = %v, want CancelKindNone", kind)
	}
}

// TestJob_CancelOne_OutOfRange: 방어적 인덱스 경계 검사는 panic 없이
// CancelKindNone을 반환한다.
func TestJob_CancelOne_OutOfRange(t *testing.T) {
	job := newTestJob(t, "a")
	if _, kind := job.CancelOne(-1); kind != CancelKindNone {
		t.Errorf("kind for -1 = %v, want CancelKindNone", kind)
	}
	if _, kind := job.CancelOne(99); kind != CancelKindNone {
		t.Errorf("kind for 99 = %v, want CancelKindNone", kind)
	}
}

// TestJob_SubscribeWithSnapshot_NoDoubleDelivery: Snapshot()과 Subscribe()
// 사이에 발행된 이벤트는 캡처된 스냅샷과 라이브 채널 모두에 나타나서는
// 절대 안 된다. SubscribeWithSnapshot과 동시에 Publish를 두드려 계약을
// 검증한다.
//
// 단서: 이 테스트는 API 측 보장(한 번의 mu 잠금 안의 Snapshot + Subscribe)
// 만 단언하며, end-to-end exactly-once 전달은 아니다. 생산자의 UpdateURL
// → Publish 쌍은 두 번의 mu 획득이라, 적대적 스케줄(UpdateURL →
// SubscribeWithSnapshot → Publish)에서는 소비자가 snap=done이면서 동시에
// channel=done을 받을 수 있다. 그것까지 닫으려면 Job에 원자적
// UpdateURLAndPublish 헬퍼가 필요하다. pubReady 배리어로 스케줄을
// "Subscribe가 락을 먼저 잡는" 쪽으로 편향시켜, 이 테스트는 주로 API 계약
// 분기를 검증한다.
func TestJob_SubscribeWithSnapshot_NoDoubleDelivery(t *testing.T) {
	job := newTestJob(t, "https://x")

	// 결정적 비-zero 시작 상태를 갖도록 미리 변형해 둔다.
	job.UpdateURL(0, func(s *URLState) {
		s.Status = "running"
		s.Received = 100
	})

	// 동시에 URL을 done으로 뒤집고 그 done 이벤트를 Publish한다. race는
	// Publish가 스냅샷 캡처 이전에 일어나는지(채널 전달 없음, 스냅샷이 done)
	// 이후인지(채널은 done, 스냅샷은 running)이다. 둘 중 어느 쪽이든 옳다 —
	// 절대 일어나서는 안 되는 건 "스냅샷이 done이면서 동시에 채널도 done"이다.
	pubReady := make(chan struct{})
	go func() {
		<-pubReady
		job.UpdateURL(0, func(s *URLState) { s.Status = "done"; s.Received = 200 })
		job.Publish(Event{Phase: "done", Data: []byte(`{"phase":"done","index":0}`)})
	}()
	close(pubReady)

	snap, ch, unsub := job.SubscribeWithSnapshot()
	defer unsub()

	// 채널이 내보낸 것을 모두 drain한다(많아야 몇 개).
	var live []Event
	deadline := time.After(100 * time.Millisecond)
drain:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break drain
			}
			live = append(live, ev)
		case <-deadline:
			break drain
		}
	}

	snapDone := snap.URLs[0].Status == "done"
	liveSawDone := false
	for _, ev := range live {
		if ev.Phase == "done" {
			liveSawDone = true
		}
	}
	// 원자적 API의 핵심: 둘 다 true가 되어선 안 된다.
	if snapDone && liveSawDone {
		t.Errorf("done event delivered twice: snapshot=done AND channel saw done frame")
	}
}

// TestJob_SubscribeWithSnapshot_TerminalReturnsClosedChannel은 terminal-Job
// 분기가 여전히 채널을 미리 close해 reader를 short-circuit 시키는지 보장한다.
func TestJob_SubscribeWithSnapshot_TerminalReturnsClosedChannel(t *testing.T) {
	job := newTestJob(t, "a")
	job.SetStatus(StatusCompleted)

	snap, ch, unsub := job.SubscribeWithSnapshot()
	defer unsub() // no-op이어야 한다
	if snap.Status != StatusCompleted {
		t.Errorf("snapshot status = %q, want completed", snap.Status)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Errorf("channel should be pre-closed for terminal job")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("terminal-job channel did not close")
	}
}

// TestJob_CancelOne_AtomicityCloseRace: CancelOne 시도 이후, 이어지는
// 호출 이전에 URL 단위 cancel이 등록되더라도, 다음 CancelOne은 등록된
// 항목을 본다 — 두 신호가 공존할 때 등록된 분기가 항상 이기는지 확인한다.
func TestJob_CancelOne_PreferRunningOverPending(t *testing.T) {
	job := newTestJob(t, "a")
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	job.RegisterURLCancel(0, cancel)
	// Status는 여전히 pending이지만 cancel이 등록돼 있으므로
	// CancelKindRunning이 이겨야 한다 — 워커의 기존 취소 경로가 error
	// 이벤트의 단일 출처가 되어야 한다.
	url, kind := job.CancelOne(0)
	if kind != CancelKindRunning {
		t.Fatalf("kind = %v, want CancelKindRunning (registered cancel takes precedence)", kind)
	}
	if url == "" {
		t.Errorf("URL not returned")
	}
}
