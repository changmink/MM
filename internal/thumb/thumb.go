package thumb

import (
	"errors"
	"image"
	"image/gif"
	"os"
	"path/filepath"
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
