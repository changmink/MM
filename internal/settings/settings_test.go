package settings

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	d := Default()
	if d.URLImportMaxBytes != DefaultMaxBytes {
		t.Errorf("URLImportMaxBytes = %d, want %d", d.URLImportMaxBytes, DefaultMaxBytes)
	}
	if d.URLImportTimeoutSeconds != DefaultTimeoutSeconds {
		t.Errorf("URLImportTimeoutSeconds = %d, want %d", d.URLImportTimeoutSeconds, DefaultTimeoutSeconds)
	}
	if !d.AutoConvertPNGToJPG {
		t.Errorf("AutoConvertPNGToJPG = false, want true (SPEC §2.7 default)")
	}
	if err := Validate(d); err != nil {
		t.Fatalf("Default() produced invalid settings: %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		s       Settings
		wantErr bool
		field   string
	}{
		{"valid default", Default(), false, ""},
		{"minimum allowed", Settings{URLImportMaxBytes: MinMaxBytes, URLImportTimeoutSeconds: MinTimeoutSeconds}, false, ""},
		{"maximum allowed", Settings{URLImportMaxBytes: MaxMaxBytes, URLImportTimeoutSeconds: MaxTimeoutSeconds}, false, ""},
		{"zero max bytes", Settings{URLImportMaxBytes: 0, URLImportTimeoutSeconds: DefaultTimeoutSeconds}, true, "url_import_max_bytes"},
		{"max bytes below minimum", Settings{URLImportMaxBytes: MinMaxBytes - 1, URLImportTimeoutSeconds: DefaultTimeoutSeconds}, true, "url_import_max_bytes"},
		{"max bytes above maximum", Settings{URLImportMaxBytes: MaxMaxBytes + 1, URLImportTimeoutSeconds: DefaultTimeoutSeconds}, true, "url_import_max_bytes"},
		{"timeout below minimum", Settings{URLImportMaxBytes: DefaultMaxBytes, URLImportTimeoutSeconds: MinTimeoutSeconds - 1}, true, "url_import_timeout_seconds"},
		{"timeout above maximum", Settings{URLImportMaxBytes: DefaultMaxBytes, URLImportTimeoutSeconds: MaxTimeoutSeconds + 1}, true, "url_import_timeout_seconds"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.s)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				var re *RangeError
				if !errors.As(err, &re) {
					t.Fatalf("want *RangeError, got %T: %v", err, err)
				}
				if re.Field != tc.field {
					t.Errorf("Field = %q, want %q", re.Field, tc.field)
				}
			} else if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

func TestNew_MissingFileUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Snapshot() != Default() {
		t.Errorf("snapshot = %+v, want Default", s.Snapshot())
	}
	// File must not have been written on a pure read — a readable snapshot
	// should not mutate disk state.
	if _, err := os.Stat(filepath.Join(dir, configSubdir, settingsFile)); !os.IsNotExist(err) {
		t.Errorf("settings.json should not exist yet, stat err=%v", err)
	}
}

func TestNew_CorruptJSONFallsBack(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, configSubdir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(configDir, settingsFile)
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Snapshot() != Default() {
		t.Errorf("corrupt JSON should fall back to defaults, got %+v", s.Snapshot())
	}
	// File should remain untouched so the user can inspect or PATCH it.
	data, _ := os.ReadFile(path)
	if string(data) != "{ this is not json" {
		t.Errorf("corrupt file was modified, now=%q", data)
	}
}

func TestNew_OutOfRangeOnDiskFallsBack(t *testing.T) {
	dir := t.TempDir()
	configDir := filepath.Join(dir, configSubdir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := Settings{URLImportMaxBytes: 0, URLImportTimeoutSeconds: 0}
	data, _ := json.Marshal(bad)
	path := filepath.Join(configDir, settingsFile)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if s.Snapshot() != Default() {
		t.Errorf("out-of-range on disk should fall back to defaults, got %+v", s.Snapshot())
	}
}

func TestUpdate_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}

	next := Settings{URLImportMaxBytes: 5 * 1024 * 1024 * 1024, URLImportTimeoutSeconds: 600}
	if err := s.Update(next); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if s.Snapshot() != next {
		t.Errorf("snapshot after Update = %+v, want %+v", s.Snapshot(), next)
	}

	// Reload from disk — value must persist.
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Snapshot() != next {
		t.Errorf("after reload = %+v, want %+v", s2.Snapshot(), next)
	}
}

func TestUpdate_RejectsOutOfRange(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	bad := Settings{URLImportMaxBytes: -1, URLImportTimeoutSeconds: 0}
	if err := s.Update(bad); err == nil {
		t.Fatal("Update: want error, got nil")
	}
	// Rejected Update must NOT mutate cache or write anything to disk.
	if s.Snapshot() != Default() {
		t.Errorf("rejected update leaked into cache: %+v", s.Snapshot())
	}
	path := filepath.Join(dir, configSubdir, settingsFile)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("rejected update wrote settings.json: err=%v", err)
	}
}

func TestNew_LegacyMissingAutoConvertKey(t *testing.T) {
	// Pre-Phase-25 settings.json files only contain the two URL fields.
	// New() must treat the missing auto_convert_png_to_jpg key as the default
	// (true) so existing users get the documented behavior on first run.
	dir := t.TempDir()
	configDir := filepath.Join(dir, configSubdir)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"url_import_max_bytes": 10737418240, "url_import_timeout_seconds": 1800}`)
	if err := os.WriteFile(filepath.Join(configDir, settingsFile), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Snapshot().AutoConvertPNGToJPG {
		t.Errorf("legacy file: AutoConvertPNGToJPG = false, want true (default migration)")
	}
}

func TestUpdate_AutoConvertToggle(t *testing.T) {
	// Verify true→false→true round-trip including disk persistence.
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Snapshot().AutoConvertPNGToJPG {
		t.Fatal("fresh Store should snapshot AutoConvertPNGToJPG=true")
	}

	off := s.Snapshot()
	off.AutoConvertPNGToJPG = false
	if err := s.Update(off); err != nil {
		t.Fatalf("Update(false): %v", err)
	}
	if s.Snapshot().AutoConvertPNGToJPG {
		t.Fatal("after Update(false), Snapshot still true")
	}

	// Reload from disk — false must persist.
	s2, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Snapshot().AutoConvertPNGToJPG {
		t.Fatal("disk reload: AutoConvertPNGToJPG = true, want false (persisted)")
	}

	on := s2.Snapshot()
	on.AutoConvertPNGToJPG = true
	if err := s2.Update(on); err != nil {
		t.Fatalf("Update(true): %v", err)
	}
	if !s2.Snapshot().AutoConvertPNGToJPG {
		t.Fatal("after Update(true), Snapshot still false")
	}
}

func TestUpdate_AtomicWriteLeavesNoTmp(t *testing.T) {
	dir := t.TempDir()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(Settings{URLImportMaxBytes: MinMaxBytes, URLImportTimeoutSeconds: MinTimeoutSeconds}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(dir, configSubdir))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" && e.Name() != settingsFile {
			t.Errorf("stray temp file left behind: %s", e.Name())
		}
	}
}
