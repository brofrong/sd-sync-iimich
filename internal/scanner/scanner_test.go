package scanner

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
)

func TestScanVolumeFindsKnownCameraLayoutsWithoutDuplicates(t *testing.T) {
	mount := t.TempDir()
	writeTestFile(t, mount, "DCIM/100SONY/DSC00001.ARW")
	writeTestFile(t, mount, "PRIVATE/M4ROOT/CLIP/C0001.MP4")
	writeTestFile(t, mount, "PRIVATE/AVCHD/BDMV/STREAM/00000.MTS")

	cfg := config.Default()
	cfg.Scan.Paths = []string{"PRIVATE"}

	files, err := ScanVolume(mount, cfg)
	if err != nil {
		t.Fatalf("ScanVolume() error = %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("ScanVolume() returned %d files, want 3: %#v", len(files), files)
	}
}

func TestScanVolumeRespectsDisabledAutomaticDiscovery(t *testing.T) {
	mount := t.TempDir()
	writeTestFile(t, mount, "DCIM/100SONY/DSC00001.ARW")
	writeTestFile(t, mount, "PRIVATE/M4ROOT/CLIP/C0001.MP4")

	cfg := config.Default()
	cfg.Scan.AutoPaths = false
	cfg.Scan.Paths = []string{"DCIM"}
	cfg.Scan.Extensions = []string{"arw"}

	files, err := ScanVolume(mount, cfg)
	if err != nil {
		t.Fatalf("ScanVolume() error = %v", err)
	}
	if len(files) != 1 || files[0].RelativePath != "DCIM/100SONY/DSC00001.ARW" {
		t.Fatalf("ScanVolume() returned %#v, want only the configured ARW file", files)
	}
}

func writeTestFile(t *testing.T, root, relativePath string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(relativePath), 0o644); err != nil {
		t.Fatal(err)
	}
}
