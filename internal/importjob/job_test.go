package importjob

import (
	"context"
	"encoding/json"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

// newTestJob is a thin shortcut around Registry.Create so individual job
// tests don't have to keep a Registry alive just for a constructor.
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

// TestJob_Subscribe_SlowConsumerDropped: a subscriber that never drains its
// channel must not stall a fast subscriber. The fast consumer drains in real
// time and the publisher yields between sends so the cooperative scheduler
// has a chance to run it; the slow consumer never reads, so its buffer fills
// up and the rest of its sends are dropped — independently of fast.
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

	// Let fast finish draining anything still in its buffer, then close so
	// the goroutine exits.
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

	// Status is the worker's responsibility — Cancel only fires the context.
	if got := job.Status(); got != StatusQueued {
		t.Errorf("status changed by Cancel alone: got %q, want unchanged", got)
	}
	job.SetStatus(StatusCancelled)
	if got := job.Status(); got != StatusCancelled {
		t.Errorf("status after SetStatus: got %q, want %q", got, StatusCancelled)
	}
}

// TestJob_SetStatus_ClosesSubsOnTerminal: handlers blocked reading the
// events channel must wake up when the worker hits a terminal state, even
// if the final summary frame was dropped from a full per-subscriber
// buffer. Without this, a slow client + a cap-1 ResponseWriter would hang
// the request goroutine until the client disconnected.
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

// TestJob_Subscribe_AfterTerminal returns a pre-closed channel so a J4
// /jobs/{id}/events subscriber that arrives after the job finished does not
// block forever. The snapshot is the source of truth in that path.
func TestJob_Subscribe_AfterTerminal(t *testing.T) {
	job := newTestJob(t, "a")
	job.SetStatus(StatusCompleted)

	sub, unsub := job.Subscribe()
	defer unsub() // must be a no-op
	select {
	case _, ok := <-sub:
		if ok {
			t.Errorf("expected pre-closed channel for terminal job, got open one")
		}
	case <-time.After(200 * time.Millisecond):
		t.Error("Subscribe on terminal job returned an open channel")
	}
}

// TestJob_CancelOne_Running: a registered per-URL cancel fires only the
// targeted index's context and reports CancelKindRunning to the caller.
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

// TestJob_CancelOne_Pending: a still-pending URL (no per-URL cancel
// registered, status == "pending") is atomically marked cancelled and the
// caller is told it owns the lifecycle event publication.
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

// TestJob_CancelOne_Terminal: a URL that has already finished cannot be
// cancelled — caller responds 409 based on CancelKindNone.
func TestJob_CancelOne_Terminal(t *testing.T) {
	job := newTestJob(t, "a")
	job.UpdateURL(0, func(s *URLState) { s.Status = "done" })

	if _, kind := job.CancelOne(0); kind != CancelKindNone {
		t.Errorf("kind for terminal URL = %v, want CancelKindNone", kind)
	}
}

// TestJob_CancelOne_OutOfRange: defensive index bounds check returns
// CancelKindNone, never panics.
func TestJob_CancelOne_OutOfRange(t *testing.T) {
	job := newTestJob(t, "a")
	if _, kind := job.CancelOne(-1); kind != CancelKindNone {
		t.Errorf("kind for -1 = %v, want CancelKindNone", kind)
	}
	if _, kind := job.CancelOne(99); kind != CancelKindNone {
		t.Errorf("kind for 99 = %v, want CancelKindNone", kind)
	}
}

// TestJob_SubscribeWithSnapshot_NoDoubleDelivery: an event published
// between Snapshot() and Subscribe() must never appear in both the
// captured snapshot AND the live channel. Exercise the contract by
// hammering Publish concurrently with SubscribeWithSnapshot.
func TestJob_SubscribeWithSnapshot_NoDoubleDelivery(t *testing.T) {
	job := newTestJob(t, "https://x")

	// Pre-mutate so we have a deterministic non-zero starting state.
	job.UpdateURL(0, func(s *URLState) {
		s.Status = "running"
		s.Received = 100
	})

	// Concurrently flip the URL to done and Publish that done event. The
	// race is whether Publish lands BEFORE the snapshot capture (no
	// channel delivery, snapshot shows done) or AFTER (channel sees done,
	// snapshot shows running). Either is correct; what must NOT happen is
	// "both snapshot shows done AND channel sees done".
	pubReady := make(chan struct{})
	go func() {
		<-pubReady
		job.UpdateURL(0, func(s *URLState) { s.Status = "done"; s.Received = 200 })
		job.Publish(Event{Phase: "done", Data: []byte(`{"phase":"done","index":0}`)})
	}()
	close(pubReady)

	snap, ch, unsub := job.SubscribeWithSnapshot()
	defer unsub()

	// Drain whatever the channel surfaces (at most a handful of events).
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
	// The whole point of the atomic API: never both true.
	if snapDone && liveSawDone {
		t.Errorf("done event delivered twice: snapshot=done AND channel saw done frame")
	}
}

// TestJob_SubscribeWithSnapshot_TerminalReturnsClosedChannel ensures the
// terminal-job branch still pre-closes the channel and short-circuits the
// reader.
func TestJob_SubscribeWithSnapshot_TerminalReturnsClosedChannel(t *testing.T) {
	job := newTestJob(t, "a")
	job.SetStatus(StatusCompleted)

	snap, ch, unsub := job.SubscribeWithSnapshot()
	defer unsub() // must be a no-op
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

// TestJob_CancelOne_AtomicityCloseRace: even if a per-URL cancel is
// registered AFTER an attempted CancelOne but BEFORE a follow-up call, the
// next CancelOne sees the registered entry — confirming the registered
// branch always wins when both signals coexist.
func TestJob_CancelOne_PreferRunningOverPending(t *testing.T) {
	job := newTestJob(t, "a")
	_, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	job.RegisterURLCancel(0, cancel)
	// Status still pending, but cancel is registered → CancelKindRunning
	// must win so the worker's existing cancellation path is the single
	// source of the error event.
	url, kind := job.CancelOne(0)
	if kind != CancelKindRunning {
		t.Fatalf("kind = %v, want CancelKindRunning (registered cancel takes precedence)", kind)
	}
	if url == "" {
		t.Errorf("URL not returned")
	}
}
