// Package convert handles persistent TS → MP4 remuxing. It runs ffmpeg with
// stream-copy arguments (no re-encode) and atomically renames the output into
// place. The ffmpeg arg set mirrors handler/stream.go:streamTS so the on-disk
// result is identical to the existing real-time TS stream cache.
package convert

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

// ErrFFmpegMissing is returned when the ffmpeg binary cannot be found on PATH.
// Callers map this to a distinct SSE error code (`ffmpeg_missing`) so operators
// know this is a server-side setup issue rather than a bad input.
var ErrFFmpegMissing = errors.New("ffmpeg not found in PATH")

// FFmpegExitError wraps a non-zero ffmpeg termination with captured stderr.
// The stderr text is for server logs only; callers should not surface it to
// end users.
type FFmpegExitError struct {
	ExitCode int
	Stderr   string
}

func (e *FFmpegExitError) Error() string {
	return fmt.Sprintf("ffmpeg exited %d: %s", e.ExitCode, e.Stderr)
}

// Callbacks carries optional lifecycle hooks. Any field may be nil. Progress
// is throttled by the runner (1 MiB or 250 ms, whichever comes first) to
// match urlfetch's SSE pacing.
type Callbacks struct {
	OnStart    func(totalBytes int64)
	OnProgress func(outputBytes int64)
}

// Result describes a successful remux.
type Result struct {
	Path string // absolute path to the final MP4
	Size int64  // final file size in bytes
}

const (
	watchInterval         = 500 * time.Millisecond
	progressByteThreshold = 1 << 20 // 1 MiB
	progressTimeThreshold = 250 * time.Millisecond
	tmpPattern            = ".convert-*.mp4"
)

// RemuxTSToMP4 runs ffmpeg to remux srcPath into <dstDir>/<baseName>.mp4.
// The caller is responsible for ensuring the final path does not already
// exist — this function does not check. The temporary output file is always
// cleaned up on failure.
//
// ffmpeg args match handler/stream.go:streamTS: -c:v copy / -c:a copy with
// the aac_adtstoasc bitstream filter for audio and +faststart movflags so
// the output is seekable immediately after rename.
func RemuxTSToMP4(ctx context.Context, srcPath, dstDir, baseName string, cb Callbacks) (*Result, error) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return nil, ErrFFmpegMissing
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
	// Close the handle before ffmpeg reopens the path with -y. On Windows a
	// lingering handle makes ffmpeg's open or the subsequent os.Rename fail
	// with sharing-violation errors.
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

	cmd := exec.CommandContext(ctx, "ffmpeg",
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
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// watchCtx is decoupled from parent ctx so the final progress sample can
	// still land after cmd.Wait() returns, matching urlfetch/hls.go's
	// watchOutputFile contract. The goroutine does not kill ffmpeg itself;
	// ctx cancel flows through exec.CommandContext.
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watchTmp(watchCtx, tmpPath, cb.OnProgress)
	}()

	waitErr := cmd.Wait()
	cancelWatch()
	<-watchDone

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		exitCode := -1
		var ee *exec.ExitError
		if errors.As(waitErr, &ee) {
			exitCode = ee.ExitCode()
		}
		return nil, &FFmpegExitError{
			ExitCode: exitCode,
			Stderr:   strings.TrimSpace(stderr.String()),
		}
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

// watchTmp polls tmpPath for size growth until ctx cancels, forwarding size
// changes to onProgress with byte/time throttling. Cheap syscalls; runs until
// the caller cancels.
func watchTmp(ctx context.Context, tmpPath string, onProgress func(int64)) {
	if onProgress == nil {
		// Still consume ticks (and respect ctx) to keep lifecycle simple,
		// but avoid the stat syscall when nobody is listening.
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
