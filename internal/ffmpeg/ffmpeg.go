// Package ffmpeg는 ffmpeg/ffprobe 프로세스 실행을 한 곳에 모아둔다.
package ffmpeg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// ErrMissing은 ffmpeg 계열 바이너리가 PATH에서 발견되지 않을 때 반환된다.
var ErrMissing = errors.New("ffmpeg not found in PATH")

// Require는 binary가 PATH에 있는지 확인한다.
func Require(binary string) error {
	if _, err := exec.LookPath(binary); err != nil {
		return ErrMissing
	}
	return nil
}

// ExitError는 non-zero 프로세스 종료 코드와 캡처된 stderr를 함께 감싼다.
type ExitError struct {
	Binary   string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *ExitError) Error() string {
	if e.Binary == "" {
		e.Binary = "ffmpeg"
	}
	return fmt.Sprintf("%s exited %d: %s", e.Binary, e.ExitCode, e.Stderr)
}

func (e *ExitError) Unwrap() error {
	return e.Err
}

// Run은 ffmpeg를 실행하면서 stderr를 캡처한다.
func Run(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	return RunWithStderr(ctx, &stderr, args...)
}

// RunWithStderr는 ffmpeg를 실행하면서 프로세스 stderr를 stderr 인자로 전달한다.
func RunWithStderr(ctx context.Context, stderr io.Writer, args ...string) error {
	return runBinary(ctx, "ffmpeg", nil, stderr, args...)
}

// Probe는 ffprobe를 실행하고 stdout을 반환한다.
func Probe(ctx context.Context, args ...string) ([]byte, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	err := runBinary(ctx, "ffprobe", &stdout, &stderr, args...)
	if err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

func runBinary(ctx context.Context, binary string, stdout, stderr io.Writer, args ...string) error {
	if err := Require(binary); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	if stdout != nil {
		cmd.Stdout = stdout
	}
	var capture bytes.Buffer
	if stderr != nil {
		cmd.Stderr = io.MultiWriter(stderr, &capture)
	} else {
		cmd.Stderr = &capture
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		exitCode := -1
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		}
		return &ExitError{
			Binary:   binary,
			ExitCode: exitCode,
			Stderr:   strings.TrimSpace(capture.String()),
			Err:      err,
		}
	}
	return nil
}
