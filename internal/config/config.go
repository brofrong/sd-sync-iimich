package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

const (
	DefaultConfigDir  = ".config/sd-sync-immich"
	DefaultConfigName = "config.yaml"
)

type Config struct {
	Immich ImmichConfig `yaml:"immich"`
	Watch  WatchConfig  `yaml:"watch"`
	Scan   ScanConfig   `yaml:"scan"`
	Upload UploadConfig `yaml:"upload"`
	State  StateConfig  `yaml:"state"`
}

type ImmichConfig struct {
	URL    string `yaml:"url"`
	APIKey string `yaml:"api_key"`
}

type WatchConfig struct {
	VolumeUUID string `yaml:"volume_uuid"`
	VolumeName string `yaml:"volume_name"`
}

type ScanConfig struct {
	Paths           []string `yaml:"paths"`
	Extensions      []string `yaml:"extensions"`
	ExcludePatterns []string `yaml:"exclude_patterns"`
}

type UploadConfig struct {
	Concurrency         int `yaml:"concurrency"`          // 0 = auto (NumCPU, max 16)
	CheckpointInterval  int `yaml:"checkpoint_interval"`  // flush WAL every N files (100–200, default 150)
}

type StateConfig struct {
	DBPath string `yaml:"db_path"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

func Default() Config {
	return Config{
		Immich: ImmichConfig{URL: "", APIKey: ""},
		Watch:  WatchConfig{VolumeUUID: "", VolumeName: ""},
		Scan: ScanConfig{
			Paths:           []string{"DCIM", "PRIVATE/M4ROOT/CLIP"},
			Extensions:      []string{"jpg", "jpeg", "arw", "mp4"},
			ExcludePatterns: []string{"._*"},
		},
		Upload: UploadConfig{
			Concurrency:        0,
			CheckpointInterval: 0,
		},
		State: StateConfig{
			DBPath: "~/.config/sd-sync-immich/state.db",
		},
	}
}

func ConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, DefaultConfigDir, DefaultConfigName), nil
}

func Load() (*Store, error) {
	path, err := ConfigPath()
	if err != nil {
		return nil, err
	}

	store := &Store{
		path: path,
		cfg:  Default(),
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil {
				return nil, mkErr
			}
			return store, store.Save()
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, &store.cfg); err != nil {
		return nil, err
	}
	store.applyDefaults()
	return store, nil
}

func (s *Store) applyDefaults() {
	def := Default()
	if len(s.cfg.Scan.Paths) == 0 {
		s.cfg.Scan.Paths = def.Scan.Paths
	}
	if len(s.cfg.Scan.Extensions) == 0 {
		s.cfg.Scan.Extensions = def.Scan.Extensions
	}
	if len(s.cfg.Scan.ExcludePatterns) == 0 {
		s.cfg.Scan.ExcludePatterns = def.Scan.ExcludePatterns
	}
	if s.cfg.State.DBPath == "" {
		s.cfg.State.DBPath = def.State.DBPath
	}
}

// EffectiveConcurrency returns upload parallelism (defaults to NumCPU, capped at 16).
func (c Config) EffectiveConcurrency() int {
	if c.Upload.Concurrency > 0 {
		return c.Upload.Concurrency
	}
	n := runtime.NumCPU()
	if n < 4 {
		return 4
	}
	if n > 16 {
		return 16
	}
	return n
}

// EffectiveCheckpointInterval returns how often to flush state to disk (default 150, clamped 100–200).
func (c Config) EffectiveCheckpointInterval() int {
	n := c.Upload.CheckpointInterval
	if n == 0 {
		return 150
	}
	if n < 100 {
		return 100
	}
	if n > 200 {
		return 200
	}
	return n
}

// WorkerLabel formats a worker setting for display in the menu.
func (c Config) WorkerLabel(raw int, effectiveFn func(Config) int) string {
	if raw == 0 {
		return fmt.Sprintf("авто (%d)", effectiveFn(c))
	}
	return fmt.Sprintf("%d", raw)
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.cfg)
	s.applyDefaults()
	return s.saveLocked()
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := yaml.Marshal(&s.cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

func (s *Store) IsConfigured() bool {
	cfg := s.Get()
	return strings.TrimSpace(cfg.Immich.URL) != "" &&
		strings.TrimSpace(cfg.Immich.APIKey) != "" &&
		strings.TrimSpace(cfg.Watch.VolumeUUID) != ""
}

func (s *Store) ResolvedDBPath() (string, error) {
	return ExpandPath(s.Get().State.DBPath)
}

func ExpandPath(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

func (c Config) ExtensionSet() map[string]struct{} {
	set := make(map[string]struct{}, len(c.Scan.Extensions))
	for _, ext := range c.Scan.Extensions {
		set[strings.ToLower(strings.TrimPrefix(ext, "."))] = struct{}{}
	}
	return set
}
