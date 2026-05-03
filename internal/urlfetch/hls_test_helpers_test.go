package urlfetch

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found in PATH")
	}
}

func makeHLSFixture(t *testing.T, dir string, durationSec int) string {
	t.Helper()
	requireFFmpeg(t)
	cmd := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "color=black:size=64x64:rate=1",
		"-f", "lavfi", "-i", "anullsrc=r=44100:cl=mono",
		"-t", fmt.Sprintf("%d", durationSec),
		"-c:v", "libx264", "-preset", "ultrafast", "-g", "1",
		"-c:a", "aac",
		"-hls_time", "1",
		"-hls_segment_type", "mpegts",
		"-hls_list_size", "0",
		"-hls_playlist_type", "vod",
		"-f", "hls",
		filepath.Join(dir, "playlist.m3u8"),
	)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("makeHLSFixture: %v\nstderr: %s", err, stderr.String())
	}
	return "playlist.m3u8"
}
