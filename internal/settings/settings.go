// Package settings는 URL import와 HLS import에 영향을 주는 사용자 조정
// 가능 값을 저장한다. 다운로드당 바이트 상한과 URL당 타임아웃이 그것이다
// (SPEC §2.7). 값은 <dataDir>/.config/settings.json에 영속화되며 메모리에
// 캐시되므로 요청 경로의 읽기는 잠금에 묶여 있어도 저렴하다. 게터(Snapshot)
// 와 세터(Update) 모두 값을 복사로 넘겨주므로, 진행 중인 요청이 진입
// 시점에 잡아둔 스냅샷은 도중에 PATCH가 들어와도 원래 값을 그대로 유지한다.
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
	// DefaultMaxBytes는 fresh-install 시점의 URL import 상한이다 (10 GiB).
	DefaultMaxBytes = int64(10) * 1024 * 1024 * 1024
	// DefaultTimeoutSeconds는 fresh-install 시점의 URL당 타임아웃이다 (30분).
	DefaultTimeoutSeconds = 1800

	// MinMaxBytes는 사용자가 설정 가능한 최소 상한이다 (1 MiB).
	MinMaxBytes = int64(1) << 20
	// MaxMaxBytes는 사용자가 설정 가능한 최대 상한이다 (1 TiB).
	MaxMaxBytes = int64(1) << 40

	// MinTimeoutSeconds / MaxTimeoutSeconds는 타임아웃 입력 범위를 제한한다.
	MinTimeoutSeconds = 60
	MaxTimeoutSeconds = 14400

	// configSubdir와 settingsFile은 dataDir 아래의 디스크 경로를 정의한다.
	configSubdir = ".config"
	settingsFile = "settings.json"
)

// Settings는 wire(JSON) 및 메모리상의 값 객체다. JSON 태그는 SPEC §2.7과 일치한다.
type Settings struct {
	URLImportMaxBytes       int64 `json:"url_import_max_bytes"`
	URLImportTimeoutSeconds int   `json:"url_import_timeout_seconds"`
	AutoConvertPNGToJPG     bool  `json:"auto_convert_png_to_jpg"`
}

// Default은 fresh-install 시점의 값을 반환한다. 호출자는 반환된 값을 변형해선
// 안 된다 — Settings는 값 타입이라 호출자에게 별도 사본이 전달된다.
func Default() Settings {
	return Settings{
		URLImportMaxBytes:       DefaultMaxBytes,
		URLImportTimeoutSeconds: DefaultTimeoutSeconds,
		AutoConvertPNGToJPG:     true,
	}
}

// Validate는 문서화된 범위를 벗어난 값을 거부한다. RangeError의 필드명은
// JSON 키와 동일해서, 핸들러가 별도 매핑 없이 그대로 클라이언트에 노출할
// 수 있다.
func Validate(s Settings) error {
	if s.URLImportMaxBytes < MinMaxBytes || s.URLImportMaxBytes > MaxMaxBytes {
		return &RangeError{Field: "url_import_max_bytes"}
	}
	if s.URLImportTimeoutSeconds < MinTimeoutSeconds || s.URLImportTimeoutSeconds > MaxTimeoutSeconds {
		return &RangeError{Field: "url_import_timeout_seconds"}
	}
	return nil
}

// RangeError는 필드가 허용 범위를 벗어났을 때 Validate(및 Store.Update)가
// 반환한다. SPEC §5 PATCH /api/settings에 따라 핸들러는 Field를 보고
// out_of_range 에러 응답을 구성한다.
type RangeError struct {
	Field string
}

func (e *RangeError) Error() string { return "out_of_range: " + e.Field }

// Store는 현재 settings 값과 그 값을 로드한 디스크 경로를 함께 보관한다.
// 모든 접근은 뮤텍스를 사용하는 Snapshot / Update를 통해 이뤄진다.
type Store struct {
	mu      sync.RWMutex
	current Settings
	path    string
}

// New는 dataDir/.config/settings.json에서 settings를 로드한다. 어떤
// 실패(파일 없음, 파싱 에러, 범위 벗어남)이든 로그만 남기고 반환된 Store는
// Default() 값을 보유한다. 디스크의 잘못된 파일은 그대로 두어 이후 PATCH가
// 원자적으로 덮어쓰게 한다. 이후 쓰기를 막을 수준의 문제(예: 설정 디렉터리
// 생성 실패)가 아니면 에러를 반환하지 않는다.
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

// Snapshot은 현재 settings를 값으로 반환한다. Update와 동시에 호출해도
// 안전하다.
func (s *Store) Snapshot() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Update는 새 settings를 검증하고 디스크에 원자적으로 쓴 다음 메모리 캐시를
// 교체한다. 디스크 쓰기에 실패하면 캐시는 갱신되지 않으므로, Update가
// 성공한 뒤에는 (캐시 == 디스크) 가정을 유지할 수 있다.
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
	// 레거시 마이그레이션: Phase-25 이전 settings.json에는
	// auto_convert_png_to_jpg 키가 없다. JSON 디코드는 조용히 boolean zero
	// (false)로 채우지만, SPEC §2.7에는 기본값이 true로 명시되어 있다. 원시
	// 맵에서 키 존재 여부를 검사해 키가 없을 때 문서상 기본값을 적용하면,
	// 레거시 사용자도 첫 실행 시 새 동작을 얻고 이후 PATCH가 그 값을
	// 명시적으로 영속화한다.
	var raw map[string]json.RawMessage
	if json.Unmarshal(data, &raw) == nil {
		if _, ok := raw["auto_convert_png_to_jpg"]; !ok {
			s.AutoConvertPNGToJPG = true
		}
	}
	return s, nil
}

// writeFile은 원자적 JSON 쓰기를 수행한다: marshal → 같은 디렉터리의
// temp 파일 → fsync → rename. 같은 디렉터리 내 rename은 POSIX와 NTFS 모두
// 원자적이므로, reader(또는 다음 New 호출)가 부분적으로 쓰인 JSON 객체를
// 보는 일이 없다.
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
