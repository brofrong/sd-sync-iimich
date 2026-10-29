package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// DefaultConfigName is the config file's name inside Dir().
const DefaultConfigName = "config.yaml"

// appDirName names the app's directories under the system's per-user ones.
const appDirName = "sd-sync-immich"

// defaultDBName is the database's name inside Dir() when db_path is empty.
const defaultDBName = "state.db"

type Config struct {
	Immich        ImmichConfig    `yaml:"immich"`
	Watch         WatchConfig     `yaml:"watch"`
	Scan          ScanConfig      `yaml:"scan"`
	Upload        UploadConfig    `yaml:"upload"`
	State         StateConfig     `yaml:"state"`
	UI            UIConfig        `yaml:"ui"`
	Card          CardConfig      `yaml:"card"`
	Notifications NotifyConfig    `yaml:"notifications"`
	Telemetry     TelemetryConfig `yaml:"telemetry"`
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
	// StackRawJPEG stacks a RAW and the JPEG or HEIF shot with it (same
	// name, same folder) in Immich, the JPEG on top.
	StackRawJPEG bool `yaml:"stack_raw_jpeg"`
}

type StateConfig struct {
	// DBPath is the database file; "~/" starts it at the user's home.
	// Empty (the default) means state.db in Dir(). Installs made before
	// that store "~/.config/sd-sync-immich/state.db", the same file on
	// macOS.
	DBPath string `yaml:"db_path"`
}

// UIConfig holds settings for the GUI.
type UIConfig struct {
	// Language is a BCP-47-ish tag from i18n.Supported (e.g. "ru", "pt-BR").
	// Empty means "follow the system language".
	Language string `yaml:"language"`
}

// CardConfig controls what happens to the SD card after a sync.
type CardConfig struct {
	AutoEject  bool `yaml:"auto_eject"`
	AutoFormat bool `yaml:"auto_format"`
}

// NotifyConfig controls which desktop notifications are shown.
type NotifyConfig struct {
	Enabled   bool `yaml:"enabled"`
	Done      bool `yaml:"done"`
	Errors    bool `yaml:"errors"`
	OtherCard bool `yaml:"other_card"`
	Server    bool `yaml:"server"`
	Card      bool `yaml:"card"`
}

// TelemetryConfig is the user's choice about anonymous statistics (see
// package telemetry): Enabled sends them, Asked is set once the user has
// answered the question in the window, and ID is the random install id
// made when they were turned on ("" while off).
type TelemetryConfig struct {
	Enabled bool   `yaml:"enabled"`
	Asked   bool   `yaml:"asked"`
	ID      string `yaml:"id,omitempty"`
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
			StackRawJPEG:       true,
		},
		State: StateConfig{
			DBPath: "",
		},
		UI: UIConfig{
			Language: "",
		},
		Card: CardConfig{
			AutoEject:  true,
			AutoFormat: false,
		},
		Notifications: NotifyConfig{
			Enabled:   true,
			Done:      true,
			Errors:    true,
			OtherCard: true,
			Server:    true,
			Card:      true,
		},
	}
}

// ConfigPath is the config file, config.yaml in Dir().
func ConfigPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DefaultConfigName), nil
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

// ResolvedDBPath is the database file: db_path with "~" expanded, or
// state.db in Dir() when db_path is empty.
func (s *Store) ResolvedDBPath() (string, error) {
	p := s.Get().State.DBPath
	if p == "" {
		dir, err := Dir()
		if err != nil {
			return "", err
		}
		return filepath.Join(dir, defaultDBName), nil
	}
	return ExpandPath(p)
}

// ExpandPath replaces a leading "~/" (or "~\" on Windows) with the user's
// home directory (os.UserHomeDir: $HOME, or the profile on Windows).
func ExpandPath(p string) (string, error) {
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(filepath.Separator)) {
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

// KnownMediaExtensions returns the lowercase, dot-less file extensions
// recognized as camera media (stills, RAW and video), independent of any
// user configuration.
func KnownMediaExtensions() map[string]struct{} {
	set := make(map[string]struct{}, len(knownCameraExtensions))
	for _, ext := range knownCameraExtensions {
		set[ext] = struct{}{}
	}
	return set
}

// IsRawExtension reports whether ext (lowercase, without the dot) is a RAW
// format Immich supports.
func IsRawExtension(ext string) bool {
	return slices.Contains(rawExtensions, ext)
}

// rawExtensions are the RAW formats supported by Immich.
var rawExtensions = []string{
	"3fr", "ari", "arw", "cap", "cin", "cr2", "cr3", "crw", "dcr", "dng",
	"erf", "fff", "iiq", "k25", "kdc", "mrw", "nef", "nrw", "orf", "ori",
	"pef", "raf", "raw", "rw2", "rwl", "sr2", "srf", "srw", "x3f",
}

var knownCameraExtensions = slices.Concat(
	// Common still-image formats.
	[]string{
		"avif", "bmp", "gif", "heic", "heif", "hif", "insp", "jpeg", "jpg", "jpe",
		"jp2", "jxl", "mpo", "png", "tif", "tiff", "webp",
	},
	rawExtensions,
	// Video containers supported by Immich.
	[]string{
		"3gp", "3gpp", "avi", "flv", "insv", "m2t", "m2ts", "m4v", "mkv",
		"mov", "mp4", "mpe", "mpeg", "mpg", "mts", "mxf", "ts", "vob", "webm", "wmv",
	},
)

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
		path = normalizeScanPath(path)
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

// normalizeScanPath cleans p into the canonical, slash-separated,
// mount-point-relative form both ScanPaths (for configured paths) and
// InScanPaths (for a file's relative path) compare with sameOrNestedPath:
// it trims whitespace, applies filepath.Clean, converts to forward
// slashes, treats a bare "/" the same as "." (the whole mount point), and
// drops a stray leading slash — a path relative to a card's mount point is
// never actually absolute, so "/DCIM" means the same thing as "DCIM".
func normalizeScanPath(p string) string {
	p = filepath.ToSlash(filepath.Clean(strings.TrimSpace(p)))
	if p == "/" {
		return "."
	}
	return strings.TrimPrefix(p, "/")
}

// InScanPaths reports whether rel — a slash-separated path relative to a
// card's mount point, as produced by filepath.Rel + filepath.ToSlash while
// walking it — equals one of ScanPaths() or is nested under one. It uses
// exactly the normalization and case-insensitive, nested-path comparison
// ScanPaths itself uses to merge configured paths, so a caller outside this
// package (card verification, before a format) classifies a file exactly as
// ScanVolume would, without re-implementing the path-nesting rules.
func (c Config) InScanPaths(rel string) bool {
	rel = normalizeScanPath(rel)
	for _, sp := range c.ScanPaths() {
		if sameOrNestedPath(sp, rel) {
			return true
		}
	}
	return false
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
