// Package imageconv는 PNG 이미지를 JPEG로 변환한다. JPEG는 알파 채널이
// 없으므로 인코딩 전에 투명 픽셀을 불투명 흰 배경 위에 합성한다(SPEC §2.8).
// 이 패키지는 경로 검증·확장자 검사·사이드카 처리를 하지 않는다 — 모두
// 호출자의 책임이다.
package imageconv

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // image.DecodeConfig가 사용할 PNG 디코더 등록
	"os"
	"path/filepath"

	"github.com/disintegration/imaging"
)

// MaxPixels는 디코딩을 허용하는 총 픽셀 수(width × height)의 상한이다.
// 64M 픽셀 ≈ 8K×8K. RGBA로 펼치면 ~256 MiB이고 합성 버퍼까지 합치면 동시
// 요청당 약 512 MiB까지 치솟는다. 악성 PNG 헤더는 65535×65535 ≈ 16 GiB를
// 주장할 수 있다 — 아래 게이트는 픽셀 메모리를 할당하기 전에 IHDR chunk를
// 읽어 그런 입력을 거부한다.
//
// const가 아닌 var인 이유는 테스트에서 작은 값으로 오버라이드해 거대한
// 더미 픽스처 없이도 거부 분기를 검증하기 위함이다.
var MaxPixels = 64_000_000

// ErrImageTooLarge는 소스 PNG의 선언된 크기가 MaxPixels를 초과할 때 반환된다.
// 호출자는 이를 안정된 wire 코드로 매핑해 클라이언트가 "디코딩 실패"와
// "시도 자체를 거부"를 구분할 수 있게 한다.
var ErrImageTooLarge = errors.New("imageconv: image too large")

// ConvertPNGToJPG는 srcPath를 PNG로 디코딩하고 알파 픽셀을 흰 배경에
// 합성한 뒤 destPath에 JPEG로 쓴다. 쓰기는 원자적이다 — destPath와 같은
// 디렉터리에 temp 파일을 만들어 인코딩한 뒤 rename 한다. 어떤 실패라도
// temp 파일은 제거된다. quality는 [0, 100] 범위여야 하며, 프로젝트 표준은
// 90이다.
func ConvertPNGToJPG(srcPath, destPath string, quality int) error {
	if quality < 0 || quality > 100 {
		return fmt.Errorf("imageconv: quality out of range: %d", quality)
	}

	// 먼저 헤더만 읽는다 — image.DecodeConfig는 IHDR chunk만 파싱하므로
	// width*height*4 할당이 일어나기 전에 픽셀 상한 검사를 통과시킨다.
	// 정상적으로 거대한 이미지뿐 아니라 IDAT가 수 GiB로 풀리는 압축 폭탄
	// 입력도 함께 차단한다.
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
