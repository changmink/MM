// Package convert는 영구 TS → MP4 remux를 담당한다. ffmpeg를 stream-copy
// 인자(재인코딩 없음)로 실행하고 결과를 원자적으로 rename 한다. ffmpeg
// 인자는 handler/stream.go:streamTS와 동일해, 실시간 TS 스트림 캐시 결과와
// 디스크 산출물이 같다.
package convert

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"file_server/internal/ffmpeg"
)

// ErrFFmpegMissing은 PATH에서 ffmpeg 바이너리를 찾을 수 없을 때 반환된다.
// 호출자는 이를 별도 SSE 에러 코드(`ffmpeg_missing`)로 매핑해, 입력 문제가
// 아닌 서버 설치 문제임을 운영자가 구분할 수 있게 한다.
var ErrFFmpegMissing = ffmpeg.ErrMissing

// FFmpegExitError는 non-zero로 종료된 ffmpeg와 캡처된 stderr를 함께 감싼다.
// stderr 텍스트는 서버 로그용이며, 호출자는 이를 최종 사용자에게 노출해선
// 안 된다.
type FFmpegExitError = ffmpeg.ExitError

// Callbacks는 선택적 라이프사이클 훅을 담는다. 어떤 필드든 nil이 가능하다.
// Progress는 urlfetch의 SSE 페이스에 맞춰 runner가 throttle 한다(1 MiB
// 또는 250 ms 중 먼저 도달한 쪽).
type Callbacks struct {
	OnStart    func(totalBytes int64)
	OnProgress func(outputBytes int64)
}

// Result는 성공한 remux의 결과를 담는다.
type Result struct {
	Path string // 최종 MP4의 절대 경로
	Size int64  // 최종 파일 크기 (바이트)
}

const (
	watchInterval         = 500 * time.Millisecond
	progressByteThreshold = 1 << 20 // 1 MiB
	progressTimeThreshold = 250 * time.Millisecond
	tmpPattern            = ".convert-*.mp4"
)

// RemuxTSToMP4는 ffmpeg를 실행해 srcPath를 <dstDir>/<baseName>.mp4로
// remux 한다. 최종 경로가 이미 존재하지 않는지 확인하는 책임은 호출자에게
// 있다 — 이 함수는 검사하지 않는다. 실패 시 임시 출력 파일은 항상 정리된다.
//
// ffmpeg 인자는 handler/stream.go:streamTS와 동일하다: -c:v copy / -c:a copy,
// 오디오에는 aac_adtstoasc bitstream filter, 그리고 rename 직후 seek 가능한
// 출력을 위해 +faststart movflags를 사용한다.
func RemuxTSToMP4(ctx context.Context, srcPath, dstDir, baseName string, cb Callbacks) (*Result, error) {
	if err := ffmpeg.Require("ffmpeg"); err != nil {
		return nil, err
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return nil, err
	}
	if cb.OnStart != nil {
		cb.OnStart(srcInfo.Size())
	}

	tmpFile, err := os.CreateTemp(dstDir, tmpPattern)
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	// ffmpeg가 -y로 경로를 다시 열기 전에 핸들을 닫는다. Windows에서는
	// 남아 있는 핸들이 ffmpeg의 open이나 그 후의 os.Rename에서 sharing
	// violation 에러를 일으키기 때문이다.
	if closeErr := tmpFile.Close(); closeErr != nil {
		_ = os.Remove(tmpPath)
		return nil, closeErr
	}
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	args := []string{
		"-y",
		"-hide_banner", "-loglevel", "error",
		"-i", srcPath,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c:v", "copy",
		"-c:a", "copy",
		"-bsf:a", "aac_adtstoasc",
		"-movflags", "+faststart",
		tmpPath,
	}

	// watchCtx를 부모 ctx와 분리해 cmd.Wait()가 반환된 뒤에도 마지막 progress
	// 샘플을 받아낼 수 있게 한다. urlfetch/hls.go의 watchOutputFile 계약과
	// 동일한 동작이다. 이 고루틴이 ffmpeg를 종료시키지는 않는다 — ctx
	// 취소는 exec.CommandContext를 통해 전파된다.
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watchTmp(watchCtx, tmpPath, cb.OnProgress)
	}()

	waitErr := ffmpeg.Run(ctx, args...)
	cancelWatch()
	<-watchDone

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		return nil, waitErr
	}

	finalPath := filepath.Join(dstDir, baseName+".mp4")
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return nil, err
	}
	renamed = true

	fi, err := os.Stat(finalPath)
	if err != nil {
		return nil, err
	}
	return &Result{Path: finalPath, Size: fi.Size()}, nil
}

// watchTmp는 ctx가 취소될 때까지 tmpPath의 크기 증가를 폴링하며, byte/time
// throttling을 적용해 onProgress에 전달한다. syscall이 가벼우며 호출자가
// 취소할 때까지 계속 실행된다.
func watchTmp(ctx context.Context, tmpPath string, onProgress func(int64)) {
	if onProgress == nil {
		// 라이프사이클을 단순하게 유지하기 위해 ctx를 계속 존중하되,
		// 듣는 쪽이 없으면 stat syscall은 생략한다.
		<-ctx.Done()
		return
	}
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()
	var lastReported atomic.Int64
	lastEmit := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fi, err := os.Stat(tmpPath)
			if err != nil {
				continue
			}
			size := fi.Size()
			prev := lastReported.Load()
			if size == prev {
				continue
			}
			now := time.Now()
			delta := size - prev
			if delta >= progressByteThreshold || now.Sub(lastEmit) >= progressTimeThreshold {
				onProgress(size)
				lastReported.Store(size)
				lastEmit = now
			}
		}
	}
}
