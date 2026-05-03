package handler

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"file_server/internal/media"
	"file_server/internal/thumb"
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

	// Cap how many missing-sidecar backfills (each forks ffprobe) we run per
	// request so a directory of legacy thumbnails can't stall the handler.
	backfillBudget := 1

	// 사이드카 stat을 entry마다 부르면 1000개 디렉터리 = 1000 syscall이라
	// .thumb 디렉터리를 한 번만 ReadDir해 set으로 lookup. 디렉터리가 없으면
	// 빈 set으로 자연스럽게 모든 thumb_available=false 처리된다.
	thumbSet := make(map[string]struct{})
	if entries, err := os.ReadDir(filepath.Join(abs, ".thumb")); err == nil {
		for _, e := range entries {
			thumbSet[e.Name()] = struct{}{}
		}
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
			if _, ok := thumbSet[name+".jpg"]; ok {
				thumbAvail = true
				if ft == media.TypeVideo {
					thumbPath := filepath.Join(abs, ".thumb", name+".jpg")
					durSec = lookupVideoDuration(thumbPath, filepath.Join(abs, name), &backfillBudget)
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

	writeJSON(w, r, http.StatusOK, browseResponse{
		Path:    rel,
		Entries: entries,
	})
}

// lookupVideoDuration returns the cached duration sidecar value, or — if the
// sidecar is missing and the per-request budget allows — performs one
// ffprobe-backed backfill. Returns nil on any failure so browse never 5xxs
// over a metadata read. The budget pointer is decremented when a backfill
// is attempted (regardless of success), bounding ffprobe forks per request.
func lookupVideoDuration(thumbPath, videoPath string, budget *int) *float64 {
	if d := thumb.LookupDuration(thumbPath); d != nil {
		return d
	}
	if *budget <= 0 {
		return nil
	}
	*budget--
	return thumb.BackfillDuration(thumbPath, videoPath)
}
