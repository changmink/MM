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

	// A finished job should not count against the cap; create one, finish it,
	// and verify a new Create succeeds.
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

func TestRegistry_List_SortedByCreated(t *testing.T) {
	// Sanity check on the sort guarantee List makes — it is what the API
	// handler depends on for stable client-side row ordering.
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
