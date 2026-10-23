package config

import (
	"os"
	"path/filepath"
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
	UI     UIConfig     `yaml:"ui"`
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
	AutoPaths       bool     `yaml:"auto_paths"`
}

type UploadConfig struct {
	Concurrency        int `yaml:"concurrency"`         // 0 = auto (see AutoConcurrency)
	CheckpointInterval int `yaml:"checkpoint_interval"` // flush WAL every N files (100–200, default 150)
}

type StateConfig struct {
	DBPath string `yaml:"db_path"`
}

// UIConfig holds settings for the GUI.
type UIConfig struct {
	// Language is a BCP-47-ish tag from i18n.Supported (e.g. "ru", "pt-BR").
	// Empty means "follow the system language".
	Language string `yaml:"language"`
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
			Paths:           []string{"DCIM"},
			Extensions:      []string{"jpg", "jpeg", "arw", "mp4"},
			ExcludePatterns: []string{"._*"},
			AutoPaths:       true,
		},
		Upload: UploadConfig{
			Concurrency:        0,
			CheckpointInterval: 0,
		},
		State: StateConfig{
			DBPath: "~/.config/sd-sync-immich/state.db",
		},
		UI: UIConfig{
			Language: "",
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

// DefaultConcurrency is the number of parallel uploads. Uploads are bound by the
// network and the SD card, not the CPU: in measurements 4 HTTP/1.1 connections
// beat 10, and more parallel reads slow the card down.
const DefaultConcurrency = 4

// MaxConcurrency is the most parallel uploads, set by hand or picked
// automatically.
const MaxConcurrency = 16

// AutoConcurrency reports whether each sync picks the number of parallel
// uploads itself (concurrency 0, the default); see the uploader.
func (c Config) AutoConcurrency() bool {
	return c.Upload.Concurrency <= 0
}

// EffectiveConcurrency returns upload parallelism (DefaultConcurrency unless
// set): the fixed count, or where the automatic mode starts.
func (c Config) EffectiveConcurrency() int {
	if c.Upload.Concurrency > 0 {
		return c.Upload.Concurrency
	}
	return DefaultConcurrency
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

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

func (s *Store) Update(fn func(*Config)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := cloneConfig(s.cfg)
	fn(&s.cfg)
	s.applyDefaults()
	if err := s.saveLocked(); err != nil {
		s.cfg = previous
		return err
	}
	return nil
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

// ServerConfigured reports whether the Immich URL and API key are both set,
// regardless of whether a card has been selected yet.
func (c Config) ServerConfigured() bool {
	return strings.TrimSpace(c.Immich.URL) != "" && strings.TrimSpace(c.Immich.APIKey) != ""
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
	extensions := c.Scan.Extensions
	if c.Scan.AutoPaths {
		extensions = appendCopy(extensions, knownCameraExtensions...)
	}
	set := make(map[string]struct{}, len(extensions))
	for _, ext := range extensions {
		set[strings.ToLower(strings.TrimPrefix(ext, "."))] = struct{}{}
	}
	return set
}

var knownCameraPaths = []string{
	"DCIM",
	"PRIVATE/M4ROOT/CLIP",
	"PRIVATE/AVCHD/BDMV/STREAM",
	"MP_ROOT",
	"PRIVATE/XDROOT/Clip",
	"XDROOT/Clip",
}

var knownCameraExtensions = []string{
	// Common still-image formats.
	"avif", "bmp", "gif", "heic", "heif", "hif", "insp", "jpeg", "jpg", "jpe",
	"jp2", "jxl", "mpo", "png", "tif", "tiff", "webp",
	// RAW formats supported by Immich.
	"3fr", "ari", "arw", "cap", "cin", "cr2", "cr3", "crw", "dcr", "dng",
	"erf", "fff", "iiq", "k25", "kdc", "mrw", "nef", "nrw", "orf", "ori",
	"pef", "raf", "raw", "rw2", "rwl", "sr2", "srf", "srw", "x3f",
	// Video containers supported by Immich.
	"3gp", "3gpp", "avi", "flv", "insv", "m2t", "m2ts", "m4v", "mkv",
	"mov", "mp4", "mpe", "mpeg", "mpg", "mts", "mxf", "ts", "vob", "webm", "wmv",
}

// ScanPaths returns custom scan roots plus known camera layouts when automatic
// media discovery is enabled. Parent paths replace nested paths to avoid
// scanning the same file more than once.
func (c Config) ScanPaths() []string {
	paths := appendCopy([]string(nil), c.Scan.Paths...)
	if c.Scan.AutoPaths {
		paths = append(paths, knownCameraPaths...)
	}

	unique := make([]string, 0, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
		if path == "" {
			continue
		}

		skip := false
		for _, existing := range unique {
			if sameOrNestedPath(existing, path) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		filtered := unique[:0]
		for _, existing := range unique {
			if !sameOrNestedPath(path, existing) {
				filtered = append(filtered, existing)
			}
		}
		unique = append(filtered, path)
	}
	return unique
}

func sameOrNestedPath(parent, child string) bool {
	parent = strings.ToLower(strings.TrimSuffix(parent, "/"))
	child = strings.ToLower(strings.TrimSuffix(child, "/"))
	return parent == "." || child == parent || strings.HasPrefix(child, parent+"/")
}

func appendCopy[T any](dst []T, values ...T) []T {
	out := make([]T, len(dst), len(dst)+len(values))
	copy(out, dst)
	return append(out, values...)
}

func cloneConfig(cfg Config) Config {
	cloned := cfg
	cloned.Scan.Paths = appendCopy([]string(nil), cfg.Scan.Paths...)
	cloned.Scan.Extensions = appendCopy([]string(nil), cfg.Scan.Extensions...)
	cloned.Scan.ExcludePatterns = appendCopy([]string(nil), cfg.Scan.ExcludePatterns...)
	return cloned
}
