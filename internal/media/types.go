package media

import "strings"

type FileType string

const (
	TypeImage FileType = "image"
	TypeVideo FileType = "video"
	TypeAudio FileType = "audio"
	TypeDir   FileType = "dir"
	TypeOther FileType = "other"
)

var mimeTypes = map[string]string{
	".mp4":  "video/mp4",
	".mkv":  "video/x-matroska",
	".avi":  "video/x-msvideo",
	".ts":   "video/mp2t",
	".mp3":  "audio/mpeg",
	".flac": "audio/flac",
	".aac":  "audio/aac",
	".ogg":  "audio/ogg",
	".wav":  "audio/wav",
	".m4a":  "audio/mp4",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
	".gif":  "image/gif",
}

var imageExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true,
}

var videoExts = map[string]bool{
	".mp4": true, ".mkv": true, ".avi": true, ".ts": true,
}

var audioExts = map[string]bool{
	".mp3": true, ".flac": true, ".aac": true, ".ogg": true, ".wav": true, ".m4a": true,
}

func DetectType(name string) FileType {
	ext := strings.ToLower(extOf(name))
	switch {
	case imageExts[ext]:
		return TypeImage
	case videoExts[ext]:
		return TypeVideo
	case audioExts[ext]:
		return TypeAudio
	default:
		return TypeOther
	}
}

func MIMEType(name string) string {
	ext := strings.ToLower(extOf(name))
	if m, ok := mimeTypes[ext]; ok {
		return m
	}
	return "application/octet-stream"
}

func IsImage(name string) bool {
	return imageExts[strings.ToLower(extOf(name))]
}

func IsTS(name string) bool {
	return strings.ToLower(extOf(name)) == ".ts"
}

func extOf(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '.' {
			return name[i:]
		}
		if name[i] == '/' || name[i] == '\\' {
			break
		}
	}
	return ""
}
