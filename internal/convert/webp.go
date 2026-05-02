package convert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	webpProbeTimeout     = 5 * time.Second
	webpTmpPattern       = ".webpconvert-*.webp"
	// webpEncoder is the ffmpeg encoder name. We use plain "libwebp" rather
	// than the registered "libwebp_anim" alias because alpine apk's ffmpeg
	// 6.1 build emits single-frame output for libwebp_anim (verified
	// empirically against the encoder's registration), whereas libwebp
	// auto-promotes multi-frame input to animated WebP — same on-disk
	// container (RIFF + VP8X with animation flag) plus a working file size.
	webpEncoder          = "libwebp"
	webpQuality          = "80"
	webpCompressionLevel = "4"
)

// EncodeWebP encodes srcPath as an animated WebP into <dstDir>/<baseName>.webp.
// The caller must ensure the final path does not already exist — this function
// does not check. The temporary output file is always cleaned up on failure.
//
// Encoder args: -c:v libwebp -loop 0 -lossless 0 -q:v 80 -compression_level 4
// -an. ffmpeg auto-promotes multi-frame input to animated WebP. fps and
// resolution preserve the input. Audio is always dropped; callers emit the
// audio_dropped warning based on ProbeStreamInfo when applicable.
func EncodeWebP(ctx context.Context, srcPath, dstDir, baseName string, cb Callbacks) (*Result, error) {
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

	tmpFile, err := os.CreateTemp(dstDir, webpTmpPattern)
	if err != nil {
		return nil, err
	}
	tmpPath := tmpFile.Name()
	// Close the handle before ffmpeg reopens the path with -y. On Windows a
	// lingering handle makes ffmpeg's open or the subsequent os.Rename fail
	// with sharing-violation errors. Mirrors RemuxTSToMP4.
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
		"-c:v", webpEncoder,
		"-loop", "0",
		"-lossless", "0",
		"-q:v", webpQuality,
		"-compression_level", webpCompressionLevel,
		"-an",
		tmpPath,
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Decoupled watch ctx so the final progress sample can land after Wait
	// returns, mirroring RemuxTSToMP4 / urlfetch/hls.go's contract.
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

	finalPath := filepath.Join(dstDir, baseName+".webp")
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

// ProbeStreamInfo runs ffprobe once and returns (durationSec, hasAudio, err).
// Used by the convert-webp handler to gate clip eligibility (duration) and
// to decide whether to emit the audio_dropped warning. Duration is read from
// format.duration; hasAudio is true if any stream has codec_type "audio".
//
// GIF inputs may return durationSec=0 (ffprobe build-dependent) — caller
// treats that as "duration unknown" but does not fail GIF eligibility, since
// SPEC §2.9 admits GIFs unconditionally. ffprobe missing or non-zero exit
// returns a non-nil error.
func ProbeStreamInfo(srcPath string) (durationSec float64, hasAudio bool, err error) {
	if _, lerr := exec.LookPath("ffprobe"); lerr != nil {
		return 0, false, fmt.Errorf("ffprobe not found in PATH: %w", lerr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), webpProbeTimeout)
	defer cancel()
	out, runErr := exec.CommandContext(ctx, "ffprobe",
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		srcPath,
	).Output()
	if runErr != nil {
		return 0, false, runErr
	}
	var data struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
	}
	if jerr := json.Unmarshal(out, &data); jerr != nil {
		return 0, false, jerr
	}
	if data.Format.Duration != "" {
		if d, perr := strconv.ParseFloat(data.Format.Duration, 64); perr == nil {
			durationSec = d
		}
	}
	for _, s := range data.Streams {
		if s.CodecType == "audio" {
			hasAudio = true
			break
		}
	}
	return durationSec, hasAudio, nil
}
