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

// MaxQueuedJobs는 레지스트리가 수용하는 동시 활성(queued + running) Job
// 개수의 상한이다. handleImportURL이 프로세스 단위 직렬화를 하므로 실제로는
// 한 번에 하나의 Job만 돌지만, 폭주하는 클라이언트에 대한 안전벨트 역할을
// 한다. Create가 ErrTooManyJobs를 반환하면 handleImportURL은 429를 응답한다.
const MaxQueuedJobs = 100

// idLength는 "imp_" 접두사 뒤 Job ID에 있는 base32 인코딩 문자 개수다.
// 원시 5바이트는 정확히 8개의 base32 문자(패딩 없음)로 인코딩된다.
const idLength = 8

// idMaxAttempts는 새 ID 생성 시 충돌 재시도 횟수의 상한이다.
// crypto/rand 덕분에 활성 Job 100개 사이에서의 충돌은 사실상 0에 수렴한다
// (엔트로피 40비트). 이 루프는 순전히 방어적이다.
const idMaxAttempts = 5

var (
	// ErrTooManyJobs는 활성 Job 수가 설정된 상한을 초과할 때 Create가 반환한다.
	ErrTooManyJobs = errors.New("too many queued jobs")
	// ErrJobNotFound는 알 수 없는 id로 Remove를 호출했을 때 반환된다.
	ErrJobNotFound = errors.New("job not found")
	// ErrJobActive는 terminal 상태에 도달하지 않은 Job에 Remove를 호출했을
	// 때 반환된다 — 클라이언트는 삭제 전에 Cancel하고 기다려야 한다.
	ErrJobActive = errors.New("job is still active")
)

// Registry는 서버 프로세스의 수명 동안 모든 Job을 소유한다. 디스크 영속화는
// 없다 — 서버 재시작은 모든 활성·완료 Job을 잃으며, 이는 의도된 동작이다
// (spec-url-import-persistence §2 Out of scope 참조).
type Registry struct {
	mu        sync.RWMutex
	jobs      map[string]*Job
	parentCtx context.Context

	// maxQueued는 활성 Job 상한이다. New가 MaxQueuedJobs로 기본값을 설정한다.
	// 테스트는 100개의 실제 Job을 만들지 않고 거부 경로를 검증하기 위해
	// 이 필드를 직접 오버라이드한다.
	maxQueued int
}

// New는 빈 레지스트리를 만들고, 그 안의 Job들은 parentCtx에서 컨텍스트를
// 파생한다 — 보통 cmd/server/main.go의 signal-aware 컨텍스트를 넘겨,
// graceful shutdown 시 모든 활성 Job에 취소가 전파되도록 한다.
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

// Create는 주어진 목적지 디렉터리와 URL 목록에 대해 새 queued Job을
// 등록한다. 반환된 Job은 레지스트리의 부모 컨텍스트에서 파생된 컨텍스트를
// 갖는다. 활성 개수가 이미 상한에 도달했으면 ErrTooManyJobs를 반환한다.
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

// Get은 주어진 id의 Job을 반환한다. 제거됐거나 존재한 적 없으면 false를 반환한다.
func (r *Registry) Get(id string) (*Job, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[id]
	return j, ok
}

// List는 active와 finished Job을 각각 생성 시각 오름차순으로 반환한다.
// 반환된 슬라이스는 새로 할당된 것이라 잠금 없이 순회해도 안전하다.
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

// Remove는 완료된 Job을 레지스트리에서 삭제한다. Active Job은 ErrJobActive를
// 반환한다 — 호출자는 먼저 Cancel하고 워커가 terminal 상태로 전이할 때까지
// 기다려야 한다.
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

// RemoveFinished는 terminal 상태의 모든 Job을 삭제하고 제거된 개수를 반환한다.
// active Job은 건드리지 않는다.
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

// CancelAll은 모든 active Job의 컨텍스트를 트리거한다. 서버가 SIGINT/SIGTERM
// 을 받았을 때 진행 중 fetch를 즉시 멈추도록 graceful shutdown이 사용한다.
// Create/Remove와 동시 호출해도 안전하다.
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

// WaitAll은 현재 active인 모든 Job이 terminal 상태에 도달하거나 d가 지날
// 때까지 블록한다. graceful shutdown 시 CancelAll과 짝지어 사용하면
// 프로세스 종료(또는 테스트 cleanup) 전에 워커 고루틴과 열린 파일 핸들이
// 풀린다. Job은 병렬로 대기하므로, 한 Job이 막혀도 다른 Job들의 대기 예산을
// 빼앗기지 않는다.
func (r *Registry) WaitAll(d time.Duration) {
	r.mu.RLock()
	jobs := make([]*Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		if j.IsActive() {
			jobs = append(jobs, j)
		}
	}
	r.mu.RUnlock()
	if len(jobs) == 0 {
		return
	}
	allDone := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		wg.Add(len(jobs))
		for _, j := range jobs {
			go func(j *Job) {
				defer wg.Done()
				<-j.Done()
			}(j)
		}
		wg.Wait()
		close(allDone)
	}()
	select {
	case <-allDone:
	case <-time.After(d):
	}
}

// activeCountLocked는 queued+running Job 수를 센다. 호출자가 r.mu를 잡고 있어야 한다.
func (r *Registry) activeCountLocked() int {
	n := 0
	for _, j := range r.jobs {
		if j.IsActive() {
			n++
		}
	}
	return n
}

// uniqueIDLocked는 새 Job ID를 생성한다. 드문 충돌 시 재시도한다.
// 호출자가 r.mu를 잡고 있어야 한다.
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

// generateJobID는 "imp_" + 소문자 base32 8자(40비트) 형식의 ID를 반환한다.
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

// SetMaxQueuedForTesting은 MaxQueuedJobs 슬롯을 채우지 않고 ErrTooManyJobs
// 경로를 검증하려는 테스트를 위해 활성 Job 상한을 오버라이드한다. 프로덕션
// 코드는 절대 호출하지 말아야 한다 — 생성자가 설정한 기본값(MaxQueuedJobs)을
// 그대로 둬야 한다.
func SetMaxQueuedForTesting(r *Registry, cap int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxQueued = cap
}
