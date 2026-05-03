package convert

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"file_server/internal/ffmpeg"
)

const (
	webpProbeTimeout = 5 * time.Second
	webpTmpPattern   = ".webpconvert-*.webp"
	// webpEncoder는 ffmpeg 인코더 이름이다. 등록된 "libwebp_anim" 별칭
	// 대신 일반 "libwebp"를 쓰는 이유: alpine apk의 ffmpeg 6.1 빌드에서
	// libwebp_anim은 단일 프레임만 출력하는 반면(인코더 등록을 직접
	// 검증함), libwebp는 다중 프레임 입력을 자동으로 애니메이션 WebP로
	// 승격해 같은 온디스크 컨테이너(RIFF + animation flag가 켜진 VP8X)와
	// 정상 파일 크기를 만든다.
	webpEncoder          = "libwebp"
	webpQuality          = "80"
	webpCompressionLevel = "4"
)

// EncodeWebP는 srcPath를 애니메이션 WebP로 인코딩해
// <dstDir>/<baseName>.webp로 저장한다. 최종 경로가 이미 존재하지 않는지는
// 호출자가 보장해야 하며, 이 함수는 검사하지 않는다. 실패 시 임시 출력
// 파일은 항상 정리된다.
//
// 인코더 인자: -c:v libwebp -loop 0 -lossless 0 -q:v 80
// -compression_level 4 -an. ffmpeg가 다중 프레임 입력을 자동으로 애니메이션
// WebP로 승격한다. fps와 해상도는 입력을 유지하고, 오디오는 항상 제거된다.
// 호출자는 ProbeStreamInfo를 바탕으로 필요할 때 audio_dropped 경고를
// 발행한다.
func EncodeWebP(ctx context.Context, srcPath, dstDir, baseName string, cb Callbacks) (*Result, error) {
	if err := ffmpeg.Require("ffmpeg"); err != nil {
		return nil, err
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
	// ffmpeg가 -y로 경로를 다시 열기 전에 핸들을 닫는다. Windows에서는
	// 남아 있는 핸들이 ffmpeg의 open이나 그 후의 os.Rename에서 sharing
	// violation 에러를 일으킨다. RemuxTSToMP4와 동일한 처리.
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

	args := []string{
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
	}

	// watch ctx를 분리해 Wait 반환 후에도 마지막 progress 샘플을 받게 한다.
	// RemuxTSToMP4 / urlfetch/hls.go의 계약과 동일하다.
	watchCtx, cancelWatch := context.WithCancel(context.Background())
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		watchTmp(watchCtx, tmpPath, cb.OnProgress)
	}()

	waitErr := ffmpeg.Run(ctx, args...)
	cancelWatch()
	<-watchDone

	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		return nil, waitErr
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

// ProbeStreamInfo는 ffprobe를 한 번 실행해 (durationSec, hasAudio, err)를
// 반환한다. convert-webp 핸들러가 클립 자격(duration)을 검증하고
// audio_dropped 경고를 발행할지 결정하는 데 사용한다. duration은
// format.duration에서 읽고, hasAudio는 codec_type이 "audio"인 스트림이
// 하나라도 있으면 true다.
//
// GIF 입력은 durationSec=0을 반환할 수 있다(ffprobe 빌드에 따라 다름) —
// 호출자는 이 경우를 "duration 미상"으로 처리하되 GIF는 SPEC §2.9에 따라
// 무조건 허용되므로 자격을 떨어뜨리지 않는다. ffprobe가 없거나 non-zero
// 종료면 non-nil 에러를 반환한다.
func ProbeStreamInfo(srcPath string) (durationSec float64, hasAudio bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), webpProbeTimeout)
	defer cancel()
	out, runErr := ffmpeg.Probe(ctx,
		"-v", "quiet",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		srcPath,
	)
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
