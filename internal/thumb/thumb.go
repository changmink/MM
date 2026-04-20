package thumb

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/disintegration/imaging"
)

const (
	thumbWidth  = 200
	thumbHeight = 200
)

// Generate creates a 200x200 JPEG thumbnail at dst from the image at src.
func Generate(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	ext := strings.ToLower(filepath.Ext(src))

	var img image.Image
	var err error

	if ext == ".gif" {
		img, err = decodeGIFFirstFrame(src)
	} else {
		img, err = imaging.Open(src, imaging.AutoOrientation(true))
	}
	if err != nil {
		return err
	}

	thumb := imaging.Fit(img, thumbWidth, thumbHeight, imaging.Lanczos)
	return imaging.Save(thumb, dst, imaging.JPEGQuality(85))
}

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

// WriteDurationSidecar writes sec to the sidecar file next to thumbPath.
func WriteDurationSidecar(thumbPath string, sec float64) error {
	data := []byte(strconv.FormatFloat(sec, 'f', 3, 64))
	return os.WriteFile(DurationSidecarPath(thumbPath), data, 0644)
}

// ReadDurationSidecar reads the duration sidecar next to thumbPath.
// Returns (0, false) if the sidecar is missing or its contents cannot be parsed.
func ReadDurationSidecar(thumbPath string) (float64, bool) {
	data, err := os.ReadFile(DurationSidecarPath(thumbPath))
	if err != nil {
		return 0, false
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil {
		return 0, false
	}
	return sec, true
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
	out, err := exec.Command("ffprobe",
		"-v", "quiet",
		"-show_entries", "format=duration",
		"-of", "csv=p=0",
		src,
	).Output()
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

	cmd := exec.Command("ffmpeg",
		"-y",
		"-loglevel", "error",
		"-ss", strconv.FormatFloat(offsetSec, 'f', 3, 64),
		"-i", src,
		"-vframes", "1",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease,pad=%d:%d:(ow-iw)/2:(oh-ih)/2",
			thumbWidth, thumbHeight, thumbWidth, thumbHeight),
		tmpPath,
	)
	if err := cmd.Run(); err != nil {
		os.Remove(tmpPath)
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
