// Package ffmpeg centralizes ffmpeg/ffprobe process execution.
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

// ErrMissing is returned when the requested ffmpeg-family binary is not on PATH.
var ErrMissing = errors.New("ffmpeg not found in PATH")

// Require verifies that binary is available on PATH.
func Require(binary string) error {
	if _, err := exec.LookPath(binary); err != nil {
		return ErrMissing
	}
	return nil
}

// ExitError wraps a non-zero process exit with captured stderr.
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

// Run executes ffmpeg with stderr captured.
func Run(ctx context.Context, args ...string) error {
	var stderr bytes.Buffer
	return RunWithStderr(ctx, &stderr, args...)
}

// RunWithStderr executes ffmpeg and writes process stderr to stderr.
func RunWithStderr(ctx context.Context, stderr io.Writer, args ...string) error {
	return runBinary(ctx, "ffmpeg", nil, stderr, args...)
}

// Probe executes ffprobe and returns stdout.
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
