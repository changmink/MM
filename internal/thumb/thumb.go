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

// GenerateFromVideo extracts a representative frame from a video file and saves
// it as a 200x200 JPEG at dst. It tries 50%, 25%, and 75% of the video
// duration in order, falling back on the next offset if the frame is blank
// (all-black or all-white). Returns an error if ffmpeg/ffprobe is unavailable
// or all offsets produce blank frames.
func GenerateFromVideo(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	duration, err := videoDuration(src)
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
		return saveJPEG(img, dst)
	}
	return errors.New("all extracted frames are blank")
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
