// Package settings stores user-adjustable knobs that affect URL import and
// HLS import: the maximum per-download byte cap and per-URL timeout (SPEC
// §2.7). Values live in <dataDir>/.config/settings.json and are cached in
// memory so request-path reads are lock-bounded but cheap. Both the getter
// (Snapshot) and the setter (Update) return by value, so in-flight requests
// that captured the snapshot at entry keep their original values even if a
// concurrent PATCH lands mid-download.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

const (
	// DefaultMaxBytes is the fresh-install URL import cap (10 GiB).
	DefaultMaxBytes = int64(10) * 1024 * 1024 * 1024
	// DefaultTimeoutSeconds is the fresh-install per-URL timeout (30 min).
	DefaultTimeoutSeconds = 1800

	// MinMaxBytes is the smallest cap a user may set (1 MiB).
	MinMaxBytes = int64(1) << 20
	// MaxMaxBytes is the largest cap a user may set (1 TiB).
	MaxMaxBytes = int64(1) << 40

	// MinTimeoutSeconds / MaxTimeoutSeconds bound the timeout input.
	MinTimeoutSeconds = 60
	MaxTimeoutSeconds = 14400

	// configSubdir and settingsFile anchor the on-disk location under dataDir.
	configSubdir = ".config"
	settingsFile = "settings.json"
)

// Settings is the wire and in-memory value object. JSON tags match SPEC §2.7.
type Settings struct {
	URLImportMaxBytes       int64 `json:"url_import_max_bytes"`
	URLImportTimeoutSeconds int   `json:"url_import_timeout_seconds"`
}

// Default returns fresh-install values. Callers should never mutate the
// returned value — Settings is a value type so callers get their own copy.
func Default() Settings {
	return Settings{
		URLImportMaxBytes:       DefaultMaxBytes,
		URLImportTimeoutSeconds: DefaultTimeoutSeconds,
	}
}

// Validate rejects values outside the documented bounds. The field name in
// RangeError matches the JSON key so the handler can surface it to the client
// without a second mapping step.
func Validate(s Settings) error {
	if s.URLImportMaxBytes < MinMaxBytes || s.URLImportMaxBytes > MaxMaxBytes {
		return &RangeError{Field: "url_import_max_bytes"}
	}
	if s.URLImportTimeoutSeconds < MinTimeoutSeconds || s.URLImportTimeoutSeconds > MaxTimeoutSeconds {
		return &RangeError{Field: "url_import_timeout_seconds"}
	}
	return nil
}

// RangeError is returned by Validate (and by Store.Update) when a field is
// outside its allowed range. The handler inspects Field to build the
// out_of_range error response per SPEC §5 PATCH /api/settings.
type RangeError struct {
	Field string
}

func (e *RangeError) Error() string { return "out_of_range: " + e.Field }

// Store holds the current settings value plus the disk path it was loaded
// from. All access goes through Snapshot / Update, which take the mutex.
type Store struct {
	mu      sync.RWMutex
	current Settings
	path    string
}

// New loads settings from dataDir/.config/settings.json. Any failure (missing
// file, parse error, out-of-bounds value) is logged and the returned Store
// holds Default() values. The bad file on disk is left untouched so a user's
// later PATCH overwrites it atomically. Returns an error only for problems
// that would prevent future writes (e.g. could not create the config dir).
func New(dataDir string) (*Store, error) {
	configDir := filepath.Join(dataDir, configSubdir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return nil, fmt.Errorf("settings: create config dir: %w", err)
	}
	path := filepath.Join(configDir, settingsFile)

	s := &Store{path: path, current: Default()}
	loaded, err := loadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			slog.Warn("settings: using defaults — load failed",
				"path", path, "err", err)
		}
		return s, nil
	}
	if err := Validate(loaded); err != nil {
		slog.Warn("settings: using defaults — out-of-range value on disk",
			"path", path, "err", err)
		return s, nil
	}
	s.current = loaded
	return s, nil
}

// Snapshot returns the current settings by value. Safe to call concurrently
// with Update.
func (s *Store) Snapshot() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Update validates the new settings, atomically writes them to disk, and
// swaps the in-memory cache. On disk-write failure the cache is NOT updated
// — callers can assume (cache == disk) after a successful Update.
func (s *Store) Update(next Settings) error {
	if err := Validate(next); err != nil {
		return err
	}
	if err := writeFile(s.path, next); err != nil {
		return err
	}
	s.mu.Lock()
	s.current = next
	s.mu.Unlock()
	return nil
}

func loadFile(path string) (Settings, error) {
	var zero Settings
	data, err := os.ReadFile(path)
	if err != nil {
		return zero, err
	}
	var s Settings
	if err := json.Unmarshal(data, &s); err != nil {
		return zero, err
	}
	return s, nil
}

// writeFile performs an atomic JSON write: marshal → temp file in the same
// directory → fsync → rename. Renaming within a single directory is atomic on
// POSIX and on NTFS, so a reader (or a next New call) never sees a partial
// JSON object.
func writeFile(path string, s Settings) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("settings: marshal: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return fmt.Errorf("settings: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("settings: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("settings: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("settings: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("settings: rename temp: %w", err)
	}
	return nil
}
