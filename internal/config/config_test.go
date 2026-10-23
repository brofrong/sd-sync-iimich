package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaultEnablesAutomaticCameraMediaDiscovery(t *testing.T) {
	cfg := Default()

	if !cfg.Scan.AutoPaths {
		t.Fatal("automatic camera media discovery should be enabled by default")
	}
}

func TestLegacyYAMLWithoutAutoPathsKeepsAutomaticDiscoveryEnabled(t *testing.T) {
	cfg := Default()
	data := []byte("scan:\n  paths: [DCIM]\n  extensions: [jpg]\n")

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.Scan.AutoPaths {
		t.Fatal("legacy configuration should inherit auto_paths: true")
	}
}

func TestYAMLCanExplicitlyDisableAutomaticDiscovery(t *testing.T) {
	cfg := Default()
	data := []byte("scan:\n  auto_paths: false\n  paths: [DCIM]\n")

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Scan.AutoPaths {
		t.Fatal("explicit auto_paths: false should be preserved")
	}
}

func TestUpdateRollsBackInMemoryConfigWhenSaveFails(t *testing.T) {
	store := &Store{
		path: t.TempDir(), // Writing a file over this directory must fail.
		cfg:  Default(),
	}

	err := store.Update(func(cfg *Config) {
		cfg.Scan.AutoPaths = false
	})

	if err == nil {
		t.Fatal("Update() error = nil, want save error")
	}
	if !store.Get().Scan.AutoPaths {
		t.Fatal("Update() retained an unsaved in-memory value")
	}
}

func TestScanPathsMergesKnownCameraLayoutsWithCustomPaths(t *testing.T) {
	cfg := Default()
	cfg.Scan.Paths = []string{"CUSTOM/MEDIA"}

	got := cfg.ScanPaths()
	want := []string{
		"CUSTOM/MEDIA",
		"DCIM",
		"PRIVATE/M4ROOT/CLIP",
		"PRIVATE/AVCHD/BDMV/STREAM",
		"MP_ROOT",
		"PRIVATE/XDROOT/Clip",
		"XDROOT/Clip",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanPaths() = %#v, want %#v", got, want)
	}
}

func TestScanPathsRemovesNestedAndCaseInsensitiveDuplicates(t *testing.T) {
	cfg := Default()
	cfg.Scan.Paths = []string{"private", "DCIM", "dcim"}

	got := cfg.ScanPaths()
	want := []string{"private", "DCIM", "MP_ROOT", "XDROOT/Clip"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanPaths() = %#v, want %#v", got, want)
	}
}

func TestAutoPathsCanBeDisabled(t *testing.T) {
	cfg := Default()
	cfg.Scan.AutoPaths = false
	cfg.Scan.Paths = []string{"CUSTOM/MEDIA"}
	cfg.Scan.Extensions = []string{"jpg"}

	if got, want := cfg.ScanPaths(), []string{"CUSTOM/MEDIA"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("ScanPaths() = %#v, want %#v", got, want)
	}
	if got := cfg.ExtensionSet(); len(got) != 1 {
		t.Fatalf("ExtensionSet() contains %d extensions, want 1", len(got))
	}
}

func TestAutomaticExtensionsCoverCommonCameraMedia(t *testing.T) {
	cfg := Default()
	required := []string{
		"jpg", "heic", "insp", "arw", "cr3", "nef", "raf", "rw2", "orf", "dng",
		"mp4", "mov", "mts", "m2ts", "mxf",
	}

	extensions := cfg.ExtensionSet()
	for _, ext := range required {
		if _, ok := extensions[ext]; !ok {
			t.Errorf("ExtensionSet() does not contain %q", ext)
		}
	}
}

func TestUILanguageRoundTrips(t *testing.T) {
	cfg := Default()
	data := []byte("ui:\n  language: ja\n")

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Language != "ja" {
		t.Fatalf("cfg.UI.Language = %q, want %q", cfg.UI.Language, "ja")
	}
	if got := Default().UI.Language; got != "" {
		t.Fatalf("Default().UI.Language = %q, want empty", got)
	}
}

func TestEffectiveConcurrencyDefaultsToFourIndependentOfCPUCount(t *testing.T) {
	if got := Default().EffectiveConcurrency(); got != 4 {
		t.Fatalf("EffectiveConcurrency() = %d, want 4", got)
	}

	cfg := Default()
	cfg.Upload.Concurrency = 7
	if got := cfg.EffectiveConcurrency(); got != 7 {
		t.Fatalf("EffectiveConcurrency() with explicit value = %d, want 7", got)
	}
}

func TestLoadOldConfigGetsCardDefaults(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	// Old config.yaml predates the card/notifications sections.
	data := []byte("immich:\n  url: http://example.com\n  api_key: key\nwatch:\n  volume_uuid: VOL\n  volume_name: CARD\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	store, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg := store.Get()

	if !cfg.Card.AutoEject {
		t.Error("Card.AutoEject = false, want true")
	}
	if cfg.Card.AutoFormat {
		t.Error("Card.AutoFormat = true, want false")
	}
	n := cfg.Notifications
	if !n.Enabled || !n.Done || !n.Errors || !n.OtherCard || !n.Server || !n.Card {
		t.Errorf("Notifications = %+v, want all true", n)
	}
}

func TestKnownMediaExtensions(t *testing.T) {
	extensions := KnownMediaExtensions()

	for _, want := range []string{"arw", "hif", "mxf"} {
		if _, ok := extensions[want]; !ok {
			t.Errorf("KnownMediaExtensions() missing %q", want)
		}
	}
	for _, notWant := range []string{"xml", "xmp"} {
		if _, ok := extensions[notWant]; ok {
			t.Errorf("KnownMediaExtensions() contains %q, want absent", notWant)
		}
	}
}

func TestInScanPaths(t *testing.T) {
	cfg := Default()
	cfg.Scan.AutoPaths = false
	cfg.Scan.Paths = []string{"DCIM"}

	tests := []struct {
		name string
		rel  string
		want bool
	}{
		{"nested", "DCIM/100SONY/DSC00001.JPG", true},
		{"case-insensitive", "dcim/100sony/dsc00001.jpg", true},
		{"leading slash tolerated", "/DCIM/100SONY/DSC00001.JPG", true},
		{"sibling prefix is not nested", "DCIMX/DSC00001.JPG", false},
		{"unrelated path", "PRIVATE/M4ROOT/CLIP/C0001.MP4", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cfg.InScanPaths(tt.rel); got != tt.want {
				t.Errorf("InScanPaths(%q) = %v, want %v", tt.rel, got, tt.want)
			}
		})
	}
}

func TestInScanPathsRootScanPathMatchesEverything(t *testing.T) {
	for _, root := range []string{".", "/"} {
		cfg := Default()
		cfg.Scan.AutoPaths = false
		cfg.Scan.Paths = []string{root}

		if !cfg.InScanPaths("ANYTHING/HERE.JPG") {
			t.Errorf("InScanPaths() with root scan path %q = false, want true", root)
		}
	}
}

func TestAutoConcurrencyIsTheDefault(t *testing.T) {
	cfg := Default()
	if !cfg.AutoConcurrency() {
		t.Fatal("AutoConcurrency() = false by default, want true")
	}
	cfg.Upload.Concurrency = 6
	if cfg.AutoConcurrency() {
		t.Fatal("AutoConcurrency() = true with an explicit 6, want false")
	}
}
