// Package imageconv converts PNG images to JPEG. JPEG has no alpha channel
// so any transparent pixels are composited over an opaque white background
// before encoding (SPEC §2.8). The package performs no path validation,
// extension checks, or sidecar handling — those are the caller's
// responsibility.
package imageconv

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // register PNG decoder for image.DecodeConfig
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"
)

// MaxPixels caps the total pixel count (width × height) we will decode.
// 64M pixels ≈ 8K×8K, which decodes to ~256 MiB of RGBA + a same-sized
// composite buffer (~512 MiB peak per concurrent request). A pathological
// PNG header can claim 65535×65535 ≈ 16 GiB; the gate below rejects such
// input by reading the IHDR chunk before any pixel allocation.
//
// Variable, not const, so tests can override with a small value to exercise
// the rejection branch without allocating a real oversize fixture.
var MaxPixels = 64_000_000

// ErrImageTooLarge is returned when the source PNG's declared dimensions
// would exceed MaxPixels. Callers map this to a stable wire code so
// clients can distinguish "decode failed" from "we refused to try".
var ErrImageTooLarge = errors.New("imageconv: image too large")

// ConvertPNGToJPG decodes srcPath as a PNG, composites alpha pixels onto a
// white background, and writes a JPEG to destPath. The write is atomic — a
// temp file in destPath's directory is created, encoded, then renamed; on
// any failure the temp file is removed. quality must be in [0, 100]; 90 is
// the project standard.
func ConvertPNGToJPG(srcPath, destPath string, quality int) error {
	if quality < 0 || quality > 100 {
		return fmt.Errorf("imageconv: quality out of range: %d", quality)
	}

	// Header-only inspection first — image.DecodeConfig reads just the IHDR
	// chunk so the pixel cap fires before any width*height*4 allocation.
	// This blocks both legitimately huge images and crafted decompression
	// bombs whose IDAT decompresses to many GiB.
	cfgFile, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("imageconv: decode: %w", err)
	}
	cfg, _, err := image.DecodeConfig(cfgFile)
	cfgFile.Close()
	if err != nil {
		return fmt.Errorf("imageconv: decode: %w", err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > int64(MaxPixels) {
		return fmt.Errorf("%w: %dx%d (limit %d pixels)", ErrImageTooLarge, cfg.Width, cfg.Height, MaxPixels)
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
