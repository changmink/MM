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

// Generate은 src 이미지에서 200x200 JPEG 썸네일을 만들어 dst에 저장한다.
// GIF와 애니메이션 WebP는 별도 분기로 첫 프레임을 추출한다 — imaging.Open
// 의 WebP 디코더(golang.org/x/image/webp)는 정적 프레임만 처리하므로,
// 애니메이션 WebP는 libwebp-tools의 webpmux로 폴백한다.
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
			// 애니메이션 WebP일 가능성이 높다 — imaging의 정적 디코더는
			// VP8X+ANIM chunk를 해석하지 못한다. 에러를 그대로 올리기 전에
			// webpmux 폴백을 시도한다.
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

// decodeAnimatedWebPFirstFrame은 libwebp-tools를 거쳐 애니메이션 WebP에서
// 첫 프레임을 뽑아낸다 — webpmux가 단일 ANMF chunk를 정적 WebP로 분리하면
// dwebp가 PNG로 변환해 imaging이 안정적으로 읽을 수 있게 만든다. alpine
// 3.19의 libwebp 빌드에서 imaging(golang.org/x/image/webp) 디코더가
// webpmux 정적 출력을 거부했기 때문에 dwebp 한 단계를 거치는 게 현실적인
// 해법이다. 두 도구 중 하나라도 없으면 ErrWebPMuxMissing을 반환해 호출자가
// 배포 설정 문제와 입력 손상을 구분할 수 있게 한다.
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

// stderrSuffix는 캡처된 stderr가 비어 있지 않으면 "(stderr: ...)"를
// 덧붙이고, 비어 있으면 빈 문자열을 반환한다. 운영자가 만성 libwebp
// 실패 원인을 파악할 수 있도록 에러 wrap에 stderr를 첨부하는 공유 헬퍼다.
func stderrSuffix(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return " (stderr: " + s + ")"
}

// ErrWebPMuxMissing은 libwebp-tools의 webpmux가 사용 불가함을 알린다.
// decodeAnimatedWebPFirstFrame이 이 에러를 노출해 핸들러가 "배포 설정
// 문제"와 "입력 파일 손상"을 구분할 수 있게 한다.
var ErrWebPMuxMissing = errors.New("webpmux not found in PATH (libwebp-tools)")

// GenerateFromVideo는 영상 파일에서 대표 프레임을 추출해 dst에 200x200
// JPEG로 저장한다. duration의 50%, 25%, 75% 위치를 순서대로 시도하고,
// 프레임이 거의 검정 또는 거의 흰색이면 다음 오프셋으로 폴백한다. 성공 시
// browse가 ffprobe 재호출 없이 값을 돌려줄 수 있도록 dst+".dur" 사이드카에
// duration도 같이 기록한다. ffmpeg/ffprobe가 없거나 모든 오프셋의 프레임이
// 비어 있으면 에러를 반환한다.
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
		// best-effort 처리. 사이드카가 없으면 다음 browse 때 backfill 된다.
		_ = WriteDurationSidecar(dst, duration)
		return nil
	}
	return errors.New("all extracted frames are blank")
}

// ProbeDuration은 ffprobe를 사용해 영상 파일의 duration을 초 단위로 반환한다.
func ProbeDuration(src string) (float64, error) {
	return videoDuration(src)
}

// DurationSidecarPath는 썸네일 JPEG에 대응하는 사이드카 파일 경로를
// 반환한다. 사이드카는 썸네일 옆에 위치하며, 원본 영상의 duration을 평문
// float(초 단위)로 저장한다.
func DurationSidecarPath(thumbPath string) string {
	return thumbPath + ".dur"
}

// WriteDurationSidecar는 thumbPath 옆 사이드카 파일에 sec을 원자적으로
// 기록한다. 캐시가 NaN/Inf로 역직렬화되는 쓰레기를 만들지 않도록 비유한
// 값이나 0 이하 값은 거부한다.
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

// ReadDurationSidecar는 thumbPath 옆의 duration 사이드카를 읽는다.
// 사이드카가 없거나, 형식이 잘못됐거나, 비유한·0 이하 값(반쯤 쓰다 만 손상,
// 떠도는 "NaN" 등)을 담고 있으면 (0, false)를 반환한다.
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

// LookupDuration은 사이드카가 있으면 캐시된 duration을 반환한다.
// 어떤 실패라도 nil을 반환하며, probe나 쓰기는 절대 수행하지 않는다.
func LookupDuration(thumbPath string) *float64 {
	sec, ok := ReadDurationSidecar(thumbPath)
	if !ok {
		return nil
	}
	return &sec
}

// BackfillDuration은 videoPath를 probe해 thumbPath 옆에 duration 사이드카를
// 기록하고 그 값을 반환한다. probe 실패나 잘못된 duration이면 nil을
// 반환한다. duration 캐싱 도입 이전에 만들어진 썸네일을 마이그레이션할 때
// 사용한다.
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

// IsBlankFrame은 img의 모든 픽셀이 R+G+B < 10(거의 검정) 또는
// R+G+B > 745(거의 흰색)일 때 true를 반환한다. 이런 프레임은 쓸만한 썸네일이
// 되지 못한다.
func IsBlankFrame(img image.Image) bool {
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := color.NRGBAModel.Convert(img.At(x, y)).RGBA()
			// RGBA()는 16비트 값을 반환하므로 임계값 계산을 위해 8비트로 축소한다.
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
