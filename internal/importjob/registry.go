package importjob

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
)

// MaxQueuedJobs caps the number of simultaneously active (queued + running)
// jobs the registry will accept. Process-wide serialization in handleImportURL
// means only one job runs at a time, so this is mainly a safety belt against
// runaway clients. handleImportURL returns 429 when Create returns
// ErrTooManyJobs.
const MaxQueuedJobs = 100

// idLength is the number of base32-encoded characters in a job ID after the
// "imp_" prefix. 5 raw bytes encode to exactly 8 base32 chars (no padding).
const idLength = 8

// idMaxAttempts is the cap on collision retries when generating a new ID.
// Crypto/rand makes a collision among 100 active jobs vanishingly improbable
// (40 bits of entropy); the loop is purely defensive.
const idMaxAttempts = 5

var (
	// ErrTooManyJobs is returned by Create when the active job count would
	// exceed the configured cap.
	ErrTooManyJobs = errors.New("too many queued jobs")
	// ErrJobNotFound is returned by Remove when the id is unknown.
	ErrJobNotFound = errors.New("job not found")
	// ErrJobActive is returned by Remove for a job that has not reached a
	// terminal status — clients must Cancel and wait before deleting.
	ErrJobActive = errors.New("job is still active")
)

// Registry owns every Job for the lifetime of the server process. There is
// no on-disk persistence: a server restart loses every active and finished
// job, which is intentional (see spec-url-import-persistence §2 Out of scope).
type Registry struct {
	mu        sync.RWMutex
	jobs      map[string]*Job
	parentCtx context.Context

	// maxQueued is the active-job cap, defaulted from MaxQueuedJobs in New.
	// Tests override this directly to exercise the rejection path without
	// having to create 100 real jobs.
	maxQueued int
}

// New creates an empty registry whose jobs derive their context from
// parentCtx — typically the signal-aware context from cmd/server/main.go so
// that graceful shutdown propagates a cancel to every active job.
func New(parentCtx context.Context) *Registry {
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	return &Registry{
		jobs:      make(map[string]*Job),
		parentCtx: parentCtx,
		maxQueued: MaxQueuedJobs,
	}
}

// Create registers a new queued job for the given destination directory and
// list of URLs. The returned Job has a context derived from the registry's
// parent context. ErrTooManyJobs is returned when the active count is
// already at the cap.
func (r *Registry) Create(destPath string, urls []string) (*Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.activeCountLocked() >= r.maxQueued {
		return nil, ErrTooManyJobs
	}

	id, err := r.uniqueIDLocked()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(r.parentCtx)
	states := make([]URLState, len(urls))
	for i, u := range urls {
		states[i] = URLState{URL: u, Status: "pending"}
	}
	job := &Job{
		ID:         id,
		DestPath:   destPath,
		CreatedAt:  time.Now().UTC(),
		ctx:        ctx,
		cancel:     cancel,
		status:     StatusQueued,
		urls:       states,
		subs:       make(map[uint64]chan Event),
		urlCancels: make(map[int]context.CancelFunc),
		done:       make(chan struct{}),
	}
	r.jobs[id] = job
	return job, nil
}

// Get returns the job with the given id, or false if it has been removed or
// never existed.
func (r *Registry) Get(id string) (*Job, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[id]
	return j, ok
}

// List returns active and finished jobs, each sorted by creation time
// ascending. The returned slices are freshly allocated and safe to iterate
// without holding any lock.
func (r *Registry) List() (active, finished []*Job) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, j := range r.jobs {
		if j.IsActive() {
			active = append(active, j)
		} else {
			finished = append(finished, j)
		}
	}
	sortByCreated(active)
	sortByCreated(finished)
	return
}

// Remove deletes a finished job from the registry. Active jobs return
// ErrJobActive — the caller must Cancel first and wait for the worker to
// transition to a terminal status.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	j, ok := r.jobs[id]
	if !ok {
		return ErrJobNotFound
	}
	if j.IsActive() {
		return ErrJobActive
	}
	delete(r.jobs, id)
	return nil
}

// RemoveFinished deletes every job in a terminal state and returns the count
// removed. Active jobs are left untouched.
func (r *Registry) RemoveFinished() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for id, j := range r.jobs {
		if !j.IsActive() {
			delete(r.jobs, id)
			n++
		}
	}
	return n
}

// CancelAll fires every active job's context. Used by graceful shutdown so
// in-flight fetches stop promptly when the server receives SIGINT/SIGTERM.
// Safe to call concurrently with Create/Remove.
func (r *Registry) CancelAll() {
	r.mu.RLock()
	jobs := make([]*Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		jobs = append(jobs, j)
	}
	r.mu.RUnlock()
	for _, j := range jobs {
		j.Cancel()
	}
}

// WaitAll blocks until every currently-active job reaches a terminal state
// or until d elapses. Pair with CancelAll for graceful shutdown to ensure
// worker goroutines and their open file handles unwind before the process
// exits or the test cleanup runs.
func (r *Registry) WaitAll(d time.Duration) {
	r.mu.RLock()
	jobs := make([]*Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		if j.IsActive() {
			jobs = append(jobs, j)
		}
	}
	r.mu.RUnlock()
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	for _, j := range jobs {
		select {
		case <-j.Done():
		case <-deadline.C:
			return
		}
	}
}

// activeCountLocked counts queued+running jobs. Caller must hold r.mu.
func (r *Registry) activeCountLocked() int {
	n := 0
	for _, j := range r.jobs {
		if j.IsActive() {
			n++
		}
	}
	return n
}

// uniqueIDLocked generates a fresh job ID, retrying on the rare collision.
// Caller must hold r.mu.
func (r *Registry) uniqueIDLocked() (string, error) {
	for i := 0; i < idMaxAttempts; i++ {
		id, err := generateJobID()
		if err != nil {
			return "", err
		}
		if _, exists := r.jobs[id]; !exists {
			return id, nil
		}
	}
	return "", errors.New("could not allocate unique job id")
}

// generateJobID returns "imp_" plus 8 lowercase base32 chars (40 bits).
func generateJobID() (string, error) {
	var b [5]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])
	return "imp_" + strings.ToLower(enc), nil
}

func sortByCreated(jobs []*Job) {
	sort.Slice(jobs, func(i, k int) bool {
		return jobs[i].CreatedAt.Before(jobs[k].CreatedAt)
	})
}

// SetMaxQueuedForTesting overrides the active-job cap for tests that want to
// exercise the ErrTooManyJobs path without filling MaxQueuedJobs slots.
// Production code must never call this — leaving the field at its
// constructor-set default (MaxQueuedJobs).
func SetMaxQueuedForTesting(r *Registry, cap int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxQueued = cap
}
