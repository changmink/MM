// Package imageconv converts PNG images to JPEG. JPEG has no alpha channel
// so any transparent pixels are composited over an opaque white background
// before encoding (SPEC §2.8). The package performs no path validation,
// extension checks, or sidecar handling — those are the caller's
// responsibility.
package imageconv

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"
)

// ConvertPNGToJPG decodes srcPath as a PNG, composites alpha pixels onto a
// white background, and writes a JPEG to destPath. The write is atomic — a
// temp file in destPath's directory is created, encoded, then renamed; on
// any failure the temp file is removed. quality must be in [0, 100]; 90 is
// the project standard.
func ConvertPNGToJPG(srcPath, destPath string, quality int) error {
	if quality < 0 || quality > 100 {
		return fmt.Errorf("imageconv: quality out of range: %d", quality)
	}

	src, err := imaging.Open(srcPath)
	if err != nil {
		return fmt.Errorf("imageconv: decode: %w", err)
	}

	bounds := src.Bounds()
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, bounds, src, bounds.Min, draw.Over)

	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".imageconv-*.jpg")
	if err != nil {
		return fmt.Errorf("imageconv: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := jpeg.Encode(tmp, dst, &jpeg.Options{Quality: quality}); err != nil {
		tmp.Close()
		return fmt.Errorf("imageconv: encode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("imageconv: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("imageconv: rename: %w", err)
	}
	renamed = true
	return nil
}
