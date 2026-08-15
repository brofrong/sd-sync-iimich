package config

import (
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
