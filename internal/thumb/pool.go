package thumb

import (
	"os"
	"path/filepath"
	"sync"
)

// Pool serializes thumbnail generation through a bounded set of workers so
// bulk image uploads cannot fan out into one CPU-bound goroutine per file.
type Pool struct {
	jobs chan job
	wg   sync.WaitGroup
}

type job struct {
	src, dst string
}

// NewPool starts workers goroutines that consume jobs until Shutdown is called.
// The job channel is buffered at workers*4; Submit drops jobs rather than
// blocking the caller when the queue is full.
func NewPool(workers int) *Pool {
	if workers < 1 {
		workers = 1
	}
	p := &Pool{jobs: make(chan job, workers*4)}
	for i := 0; i < workers; i++ {
		p.wg.Add(1)
		go p.worker()
	}
	return p
}

func (p *Pool) worker() {
	defer p.wg.Done()
	for j := range p.jobs {
		_ = os.MkdirAll(filepath.Dir(j.dst), 0755)
		_ = Generate(j.src, j.dst)
	}
}

// Submit enqueues a job. Returns false if the queue is full so the caller can
// decide between dropping silently and falling back to inline generation.
func (p *Pool) Submit(src, dst string) bool {
	select {
	case p.jobs <- job{src: src, dst: dst}:
		return true
	default:
		return false
	}
}

// Shutdown stops accepting jobs and waits for in-flight work to finish.
// Idempotent: calling Shutdown twice panics — guard externally.
func (p *Pool) Shutdown() {
	close(p.jobs)
	p.wg.Wait()
}
