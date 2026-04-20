package media

import "testing"

func TestDetectType(t *testing.T) {
	cases := []struct {
		name string
		want FileType
	}{
		{"photo.jpg", TypeImage},
		{"photo.JPEG", TypeImage},
		{"photo.png", TypeImage},
		{"photo.webp", TypeImage},
		{"photo.gif", TypeImage},
		{"film.mp4", TypeVideo},
		{"film.mkv", TypeVideo},
		{"film.avi", TypeVideo},
		{"film.ts", TypeVideo},
		{"song.mp3", TypeAudio},
		{"song.flac", TypeAudio},
		{"song.aac", TypeAudio},
		{"song.ogg", TypeAudio},
		{"song.wav", TypeAudio},
		{"song.m4a", TypeAudio},
		{"doc.pdf", TypeOther},
		{"noext", TypeOther},
	}
	for _, c := range cases {
		if got := DetectType(c.name); got != c.want {
			t.Errorf("DetectType(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestIsVideo(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"film.mp4", true},
		{"film.MP4", true},
		{"film.mkv", true},
		{"film.avi", true},
		{"film.ts", true},
		{"photo.jpg", false},
		{"song.mp3", false},
		{"noext", false},
	}
	for _, c := range cases {
		if got := IsVideo(c.name); got != c.want {
			t.Errorf("IsVideo(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestIsTS(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"video.ts", true},
		{"video.TS", true},
		{"video.mp4", false},
		{"video.mkv", false},
		{"noext", false},
	}
	for _, c := range cases {
		if got := IsTS(c.name); got != c.want {
			t.Errorf("IsTS(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMIMEType(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"video.mp4", "video/mp4"},
		{"video.mkv", "video/x-matroska"},
		{"audio.mp3", "audio/mpeg"},
		{"image.jpg", "image/jpeg"},
		{"image.PNG", "image/png"},
		{"unknown.xyz", "application/octet-stream"},
	}
	for _, c := range cases {
		if got := MIMEType(c.name); got != c.want {
			t.Errorf("MIMEType(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
