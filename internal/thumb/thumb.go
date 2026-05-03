package thumb

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"file_server/internal/ffmpeg"
	"github.com/disintegration/imaging"
)

const probeTimeout = 5 * time.Second

const (
	thumbWidth  = 200
	thumbHeight = 200
)

// Generate creates a 200x200 JPEG thumbnail at dst from the image at src.
// GIF and animated WebP are special-cased to extract the first frame —
// imaging.Open's WebP decoder (golang.org/x/image/webp) handles only static
// frames, so animated WebPs fall back to webpmux from libwebp-tools.
func Generate(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	ext := strings.ToLower(filepath.Ext(src))

	var img image.Image
	var err error

	switch ext {
	case ".gif":
		img, err = decodeGIFFirstFrame(src)
	case ".webp":
		img, err = imaging.Open(src, imaging.AutoOrientation(true))
		if err != nil {
			// Likely an animated WebP — imaging's static decoder can't
			// parse VP8X+ANIM chunks. Try the webpmux fallback before
			// surfacing the error.
			img, err = decodeAnimatedWebPFirstFrame(src)
		}
	default:
		img, err = imaging.Open(src, imaging.AutoOrientation(true))
	}
	if err != nil {
		return err
	}

	thumb := imaging.Fit(img, thumbWidth, thumbHeight, imaging.Lanczos)
	return imaging.Save(thumb, dst, imaging.JPEGQuality(85))
}

// decodeAnimatedWebPFirstFrame extracts frame 1 from an animated WebP via
// libwebp-tools — webpmux pulls the single ANMF chunk into a static WebP,
// then dwebp transcodes it to PNG which imaging can read reliably. The
// imaging package's golang.org/x/image/webp decoder rejected the static
// webpmux output in alpine 3.19's libwebp build, so the dwebp hop is the
// pragmatic fix. Returns ErrWebPMuxMissing when either tool is absent so
// callers distinguish deployment misconfig from input corruption.
func decodeAnimatedWebPFirstFrame(src string) (image.Image, error) {
	if _, err := exec.LookPath("webpmux"); err != nil {
		return nil, ErrWebPMuxMissing
	}
	if _, err := exec.LookPath("dwebp"); err != nil {
		return nil, ErrWebPMuxMissing
	}

	tmpWebp, err := os.CreateTemp("", "webp-frame-*.webp")
	if err != nil {
		return nil, err
	}
	tmpWebpPath := tmpWebp.Name()
	tmpWebp.Close()
	defer os.Remove(tmpWebpPath)

	tmpPng, err := os.CreateTemp("", "webp-frame-*.png")
	if err != nil {
		return nil, err
	}
	tmpPngPath := tmpPng.Name()
	tmpPng.Close()
	defer os.Remove(tmpPngPath)

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	var muxErr strings.Builder
	muxCmd := exec.CommandContext(ctx, "webpmux",
		"-get", "frame", "1",
		src, "-o", tmpWebpPath,
	)
	muxCmd.Stderr = &muxErr
	if err := muxCmd.Run(); err != nil {
		return nil, fmt.Errorf("webpmux extract: %w%s", err, stderrSuffix(muxErr.String()))
	}
	var dwebpErr strings.Builder
	dwebpCmd := exec.CommandContext(ctx, "dwebp",
		tmpWebpPath, "-o", tmpPngPath,
	)
	dwebpCmd.Stderr = &dwebpErr
	if err := dwebpCmd.Run(); err != nil {
		return nil, fmt.Errorf("dwebp decode: %w%s", err, stderrSuffix(dwebpErr.String()))
	}
	return imaging.Open(tmpPngPath, imaging.AutoOrientation(true))
}

// stderrSuffix appends "(stderr: ...)" when a captured stderr is non-empty,
// otherwise returns "". 운영자가 만성 libwebp 실패 원인을 파악할 수 있도록
// 에러 wrap에 stderr를 첨부하는 공유 헬퍼.
func stderrSuffix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return " (stderr: " + s + ")"
}

// ErrWebPMuxMissing signals that webpmux from libwebp-tools is unavailable.
// Surfaced by decodeAnimatedWebPFirstFrame so handler code can distinguish
// "deployment misconfigured" from "input file is broken".
var ErrWebPMuxMissing = errors.New("webpmux not found in PATH (libwebp-tools)")

// GenerateFromVideo extracts a representative frame from a video file and
// saves it as a 200x200 JPEG at dst. It tries 50%, 25%, and 75% of the video
// duration in order, falling back on the next offset if the frame is blank
// (all-black or all-white). On success it also writes a duration sidecar at
// dst+".dur" so browse can serve the value without reprobing. Returns an
// error if ffmpeg/ffprobe is unavailable or all offsets produce blank frames.
func GenerateFromVideo(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	duration, err := ProbeDuration(src)
	if err != nil {
		return fmt.Errorf("probe duration: %w", err)
	}
	if duration <= 0 {
		return errors.New("video has zero duration")
	}

	for _, pct := range []float64{0.5, 0.25, 0.75} {
		offset := duration * pct
		tmp, err := extractFrame(src, offset)
		if err != nil {
			continue
		}
		img, err := decodeJPEG(tmp)
		os.Remove(tmp)
		if err != nil {
			continue
		}
		if IsBlankFrame(img) {
			continue
		}
		if err := saveJPEG(img, dst); err != nil {
			return err
		}
		// Best-effort: a missing sidecar will be backfilled on next browse.
		_ = WriteDurationSidecar(dst, duration)
		return nil
	}
	return errors.New("all extracted frames are blank")
}

// ProbeDuration returns the duration of a video file in seconds, using ffprobe.
func ProbeDuration(src string) (float64, error) {
	return videoDuration(src)
}

// DurationSidecarPath returns the sidecar file path for a thumbnail JPEG.
// The sidecar lives next to the thumbnail and stores the source video's
// duration as a plaintext float (seconds).
func DurationSidecarPath(thumbPath string) string {
	return thumbPath + ".dur"
}

// WriteDurationSidecar atomically writes sec to the sidecar file next to
// thumbPath. Rejects non-finite or non-positive values to avoid poisoning
// the cache with garbage that would deserialize as NaN/Inf.
func WriteDurationSidecar(thumbPath string, sec float64) error {
	if !isValidDuration(sec) {
		return fmt.Errorf("invalid duration: %v", sec)
	}
	dst := DurationSidecarPath(thumbPath)
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	data := []byte(strconv.FormatFloat(sec, 'f', 3, 64))
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// ReadDurationSidecar reads the duration sidecar next to thumbPath.
// Returns (0, false) if the sidecar is missing, malformed, or contains a
// non-finite or non-positive value (a corrupted half-write or stray "NaN").
func ReadDurationSidecar(thumbPath string) (float64, bool) {
	data, err := os.ReadFile(DurationSidecarPath(thumbPath))
	if err != nil {
		return 0, false
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, false
	}
	if !isValidDuration(sec) {
		return 0, false
	}
	return sec, true
}

// LookupDuration returns the cached duration if a sidecar exists.
// Returns nil for any failure; never probes or writes.
func LookupDuration(thumbPath string) *float64 {
	sec, ok := ReadDurationSidecar(thumbPath)
	if !ok {
		return nil
	}
	return &sec
}

// BackfillDuration probes videoPath, writes the duration sidecar next to
// thumbPath, and returns the duration. Returns nil on probe failure or
// invalid duration. Used to migrate thumbnails generated before duration
// caching was added.
func BackfillDuration(thumbPath, videoPath string) *float64 {
	sec, err := ProbeDuration(videoPath)
	if err != nil || !isValidDuration(sec) {
		return nil
	}
	_ = WriteDurationSidecar(thumbPath, sec)
	return &sec
}

func isValidDuration(sec float64) bool {
	return !math.IsNaN(sec) && !math.IsInf(sec, 0) && sec > 0
}

// IsBlankFrame returns true if every pixel in img has R+G+B < 10 (near-black)
// or R+G+B > 745 (near-white). A blank frame provides no useful thumbnail.
func IsBlankFrame(img image.Image) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := color.NRGBAModel.Convert(img.At(x, y)).RGBA()
			// RGBA() returns 16-bit values; scale to 8-bit for threshold math
			sum := (r >> 8) + (g >> 8) + (b >> 8)
			if sum >= 10 && sum <= 745 {
				return false
			}
		}
	}
	return true
}

func videoDuration(src string) (float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := ffmpeg.Probe(ctx,
		"-v", "quiet",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		src,
	)
	if err != nil {
		return 0, err
	}
	s := strings.TrimSpace(string(out))
	return strconv.ParseFloat(s, 64)
}

func extractFrame(src string, offsetSec float64) (string, error) {
	tmp, err := os.CreateTemp("", "thumb_frame_*.jpg")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	tmp.Close()

	// 손상된 입력에 ffmpeg가 영구 hang하면 thumb.Pool 워커가 고갈된다.
	// videoDuration의 probeTimeout과 동일 상한으로 방어.
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()

	var stderr strings.Builder
	args := []string{
		"-y",
		"-loglevel", "error",
		"-ss", strconv.FormatFloat(offsetSec, 'f', 3, 64),
		"-i", src,
		"-vframes", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2",
			thumbWidth, thumbHeight, thumbWidth, thumbHeight),
		tmpPath,
	}
	if err := ffmpeg.RunWithStderr(ctx, &stderr, args...); err != nil {
		os.Remove(tmpPath)
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("ffmpeg extractFrame timeout after %v: %w", probeTimeout, err)
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("ffmpeg extractFrame: %w (stderr: %s)", err, msg)
		}
		return "", err
	}
	return tmpPath, nil
}

func decodeJPEG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return jpeg.Decode(f)
}

func saveJPEG(img image.Image, dst string) error {
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()
	return jpeg.Encode(f, img, &jpeg.Options{Quality: 85})
}

func decodeGIFFirstFrame(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	g, err := gif.DecodeAll(f)
	if err != nil {
		return nil, err
	}
	if len(g.Image) == 0 {
		return nil, errors.New("gif has no frames")
	}
	return g.Image[0], nil
}
