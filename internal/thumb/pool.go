package thumb

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"file_server/internal/media"
)

// Pool은 제한된 수의 워커를 통해 썸네일 생성을 직렬화한다. 대량 업로드가
// 파일당 CPU bound 고루틴 하나씩 펼쳐지는 상황을 막기 위함이다.
type Pool struct {
	jobs chan job
	wg   sync.WaitGroup
}

type job struct {
	src, dst string
}

// NewPool은 Shutdown이 호출될 때까지 잡을 소비하는 워커 고루틴을 띄운다.
// jobs 채널은 workers*4 크기로 버퍼링되어 있고, 큐가 가득 차면 Submit이
// 호출자를 막지 않고 잡을 떨어뜨린다.
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
		// 영상은 ffmpeg 경로, 이미지는 이미지 디코더 경로로 분기한다. 에러는
		// best-effort로 로그만 남긴다(handleThumb가 첫 조회 시 lazy 재생성
		// 한다). 이렇게 해야 만성 ffmpeg/libwebp 실패 원인을 운영자가 파악할
		// 수 있다.
		var err error
		switch {
		case media.IsVideo(j.src):
			err = GenerateFromVideo(j.src, j.dst)
		case media.IsImage(j.src):
			err = Generate(j.src, j.dst)
		}
		if err != nil {
			slog.Warn("thumbnail generation failed", "src", j.src, "err", err)
		}
	}
}

// Submit은 잡을 큐에 넣는다. 큐가 가득 차면 false를 반환하므로 호출자가
// 조용히 떨어뜨릴지, 인라인 생성으로 폴백할지 선택할 수 있다.
func (p *Pool) Submit(src, dst string) bool {
	select {
	case p.jobs <- job{src: src, dst: dst}:
		return true
	default:
		return false
	}
}

// Shutdown은 잡 수신을 멈추고 진행 중인 작업이 끝나기를 기다린다.
// 멱등하지 않다: Shutdown을 두 번 호출하면 panic이 발생하므로 외부에서
// 호출 횟수를 보장해야 한다.
func (p *Pool) Shutdown() {
	close(p.jobs)
	p.wg.Wait()
}
