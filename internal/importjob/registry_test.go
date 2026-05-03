package importjob

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"
)

var jobIDPattern = regexp.MustCompile(`^imp_[a-z2-7]{8}$`)

func TestRegistry_Create_AssignsID(t *testing.T) {
	reg := New(context.Background())
	job, err := reg.Create("dest", []string{"https://example/a"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !jobIDPattern.MatchString(job.ID) {
		t.Errorf("ID %q does not match %s", job.ID, jobIDPattern)
	}
	if job.DestPath != "dest" {
		t.Errorf("DestPath = %q, want %q", job.DestPath, "dest")
	}
	if job.Status() != StatusQueued {
		t.Errorf("initial status = %q, want %q", job.Status(), StatusQueued)
	}
	if got, ok := reg.Get(job.ID); !ok || got != job {
		t.Errorf("Get returned %v, ok=%v; want the created job", got, ok)
	}
	snap := job.Snapshot()
	if len(snap.URLs) != 1 || snap.URLs[0].URL != "https://example/a" || snap.URLs[0].Status != "pending" {
		t.Errorf("snapshot URLs = %#v", snap.URLs)
	}
}

func TestRegistry_Create_RejectsWhenFull(t *testing.T) {
	reg := New(context.Background())
	reg.maxQueued = 2

	for i := 0; i < 2; i++ {
		if _, err := reg.Create("dest", []string{"u"}); err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
	}
	_, err := reg.Create("dest", []string{"u"})
	if !errors.Is(err, ErrTooManyJobs) {
		t.Fatalf("Create over cap: got %v, want %v", err, ErrTooManyJobs)
	}

	// 끝난 Job은 상한 카운트에 포함되지 않아야 한다 — 하나 만들어 끝낸 뒤
	// 새 Create가 성공하는지 확인한다.
	active, _ := reg.List()
	active[0].SetStatus(StatusCompleted)
	if _, err := reg.Create("dest", []string{"u"}); err != nil {
		t.Fatalf("Create after finishing one: %v", err)
	}
}

func TestRegistry_Remove_RejectsActive(t *testing.T) {
	reg := New(context.Background())
	job, err := reg.Create("dest", []string{"u"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := reg.Remove(job.ID); !errors.Is(err, ErrJobActive) {
		t.Errorf("Remove on active: got %v, want %v", err, ErrJobActive)
	}

	job.SetStatus(StatusCompleted)
	if err := reg.Remove(job.ID); err != nil {
		t.Errorf("Remove after finish: %v", err)
	}
	if _, ok := reg.Get(job.ID); ok {
		t.Errorf("job still present after Remove")
	}
	if err := reg.Remove(job.ID); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("Remove unknown: got %v, want %v", err, ErrJobNotFound)
	}
}

func TestRegistry_RemoveFinished_LeavesActive(t *testing.T) {
	reg := New(context.Background())

	finished := make([]*Job, 0, 3)
	for i := 0; i < 3; i++ {
		j, err := reg.Create("dest", []string{"u"})
		if err != nil {
			t.Fatalf("Create finished #%d: %v", i, err)
		}
		j.SetStatus(StatusCompleted)
		finished = append(finished, j)
	}
	active, err := reg.Create("dest", []string{"u"})
	if err != nil {
		t.Fatalf("Create active: %v", err)
	}

	n := reg.RemoveFinished()
	if n != 3 {
		t.Errorf("RemoveFinished returned %d, want 3", n)
	}
	for _, j := range finished {
		if _, ok := reg.Get(j.ID); ok {
			t.Errorf("finished job %s still present", j.ID)
		}
	}
	if _, ok := reg.Get(active.ID); !ok {
		t.Errorf("active job removed")
	}
}

func TestRegistry_CancelAll_AffectsAllActive(t *testing.T) {
	reg := New(context.Background())

	jobs := make([]*Job, 3)
	for i := range jobs {
		j, err := reg.Create("dest", []string{"u"})
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		jobs[i] = j
	}

	reg.CancelAll()

	for i, j := range jobs {
		select {
		case <-j.Ctx().Done():
		case <-time.After(200 * time.Millisecond):
			t.Errorf("job %d (%s) ctx not cancelled by CancelAll", i, j.ID)
		}
	}
}

// TestRegistry_WaitAll_ParallelDeadline: deadline 예산은 풀 전체에 적용되며
// 각 Job에 순차로 적용되지 않는다. 막힌 Job 5개와 100ms 예산이면 호출은
// 약 100ms(500ms가 아님)에 반환되어야 한다 — 여기 회귀가 생기면 graceful
// shutdown 시간이 조용히 길어진다.
func TestRegistry_WaitAll_ParallelDeadline(t *testing.T) {
	reg := New(context.Background())
	for i := 0; i < 5; i++ {
		if _, err := reg.Create("dest", []string{"u"}); err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
	}

	start := time.Now()
	reg.WaitAll(100 * time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > 250*time.Millisecond {
		t.Errorf("WaitAll took %v, want ≤ 250ms (budget should apply to the pool, not per-job)", elapsed)
	}
}

// TestRegistry_WaitAll_ReturnsEarlyWhenAllDone: 모든 active Job이 deadline
// 이전에 끝나면 WaitAll은 즉시 반환해야 한다(쓸데없는 deadline 대기 금지).
func TestRegistry_WaitAll_ReturnsEarlyWhenAllDone(t *testing.T) {
	reg := New(context.Background())
	jobs := make([]*Job, 3)
	for i := range jobs {
		j, err := reg.Create("dest", []string{"u"})
		if err != nil {
			t.Fatalf("Create #%d: %v", i, err)
		}
		jobs[i] = j
	}
	for _, j := range jobs {
		j.SetStatus(StatusCompleted)
	}

	start := time.Now()
	reg.WaitAll(5 * time.Second)
	elapsed := time.Since(start)

	if elapsed > 200*time.Millisecond {
		t.Errorf("WaitAll took %v with all jobs done, want fast return", elapsed)
	}
}

func TestRegistry_List_SortedByCreated(t *testing.T) {
	// List가 보장하는 정렬에 대한 sanity check — API 핸들러는 안정적인
	// 클라이언트 행 순서를 위해 이 보장에 의존한다.
	reg := New(context.Background())
	first, _ := reg.Create("dest", []string{"u"})
	time.Sleep(2 * time.Millisecond)
	second, _ := reg.Create("dest", []string{"u"})
	time.Sleep(2 * time.Millisecond)
	third, _ := reg.Create("dest", []string{"u"})
	third.SetStatus(StatusCompleted)

	active, finished := reg.List()
	if len(active) != 2 || active[0].ID != first.ID || active[1].ID != second.ID {
		t.Errorf("active = %v, want [%s %s]", listIDs(active), first.ID, second.ID)
	}
	if len(finished) != 1 || finished[0].ID != third.ID {
		t.Errorf("finished = %v, want [%s]", listIDs(finished), third.ID)
	}
}

func listIDs(jobs []*Job) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.ID
	}
	return out
}
