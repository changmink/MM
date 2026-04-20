package handler

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chang/file_server/internal/media"
	"github.com/chang/file_server/internal/thumb"
)

type entry struct {
	Name           string         `json:"name"`
	Path           string         `json:"path"`
	Type           media.FileType `json:"type"`
	Mime           string         `json:"mime"`
	Size           int64          `json:"size"`
	ModTime        time.Time      `json:"mod_time"`
	IsDir          bool           `json:"is_dir"`
	ThumbAvailable bool           `json:"thumb_available"`
	DurationSec    *float64       `json:"duration_sec"`
}

type browseResponse struct {
	Path    string  `json:"path"`
	Entries []entry `json:"entries"`
}

func (h *Handler) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, r, http.StatusMethodNotAllowed, "method not allowed", nil)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, err := media.SafePath(h.dataDir, rel)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid path", err)
		return
	}

	infos, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, r, http.StatusNotFound, "not found", nil)
			return
		}
		writeError(w, r, http.StatusInternalServerError, "read dir failed", err)
		return
	}

	entries := make([]entry, 0, len(infos))
	for _, info := range infos {
		name := info.Name()
		// hide .thumb directories
		if strings.HasPrefix(name, ".") {
			continue
		}

		fi, err := info.Info()
		if err != nil {
			continue
		}

		relPath := filepath.ToSlash(filepath.Join(rel, name))
		if !strings.HasPrefix(relPath, "/") {
			relPath = "/" + relPath
		}

		var ft media.FileType
		if info.IsDir() {
			ft = media.TypeDir
		} else {
			ft = media.DetectType(name)
		}

		thumbAvail := false
		var durSec *float64
		if ft == media.TypeImage || ft == media.TypeVideo {
			thumbPath := filepath.Join(abs, ".thumb", name+".jpg")
			if _, err := os.Stat(thumbPath); err == nil {
				thumbAvail = true
				if ft == media.TypeVideo {
					durSec = readOrBackfillDuration(thumbPath, filepath.Join(abs, name))
				}
			}
		}

		entries = append(entries, entry{
			Name:           name,
			Path:           relPath,
			Type:           ft,
			Mime:           media.MIMEType(name),
			Size:           fi.Size(),
			ModTime:        fi.ModTime(),
			IsDir:          info.IsDir(),
			ThumbAvailable: thumbAvail,
			DurationSec:    durSec,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(browseResponse{
		Path:    rel,
		Entries: entries,
	})
}

// readOrBackfillDuration returns the video's duration from the thumbnail
// sidecar, probing with ffprobe and caching the result when the sidecar is
// missing but the thumbnail exists. Returns nil on any failure so browse
// never 5xxs over a metadata read.
func readOrBackfillDuration(thumbPath, videoPath string) *float64 {
	if sec, ok := thumb.ReadDurationSidecar(thumbPath); ok {
		return &sec
	}
	sec, err := thumb.ProbeDuration(videoPath)
	if err != nil || sec <= 0 {
		return nil
	}
	_ = thumb.WriteDurationSidecar(thumbPath, sec)
	return &sec
}
