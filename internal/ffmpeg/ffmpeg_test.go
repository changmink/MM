package ffmpeg

import (
	"context"
	"errors"
	"testing"
)

func TestRunMissingBinary(t *testing.T) {
	t.Setenv("PATH", "")

	err := Run(context.Background(), "-version")
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
}

func TestProbeMissingBinary(t *testing.T) {
	t.Setenv("PATH", "")

	_, err := Probe(context.Background(), "-version")
	if !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
}
