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

func TestScanVolumeDoesNotAttachSonyXMLAsSidecar(t *testing.T) {
	mount := t.TempDir()
	writeTestFile(t, mount, "PRIVATE/M4ROOT/CLIP/C0001.MP4")
	writeTestFile(t, mount, "PRIVATE/M4ROOT/CLIP/C0001M01.XML")

	files, err := ScanVolume(mount, config.Default())
	if err != nil {
		t.Fatalf("ScanVolume() error = %v", err)
	}
	// Immich rejects the whole upload ("Unsupported file type") for non-XMP sidecars.
	if len(files) != 1 || files[0].SidecarPath != "" {
		t.Fatalf("ScanVolume() returned %#v, want the MP4 without a sidecar", files)
	}
}

func TestScanVolumeAttachesXMPSidecars(t *testing.T) {
	mount := t.TempDir()
	writeTestFile(t, mount, "DCIM/100SONY/DSC00001.ARW")
	writeTestFile(t, mount, "DCIM/100SONY/DSC00001.xmp")
	writeTestFile(t, mount, "DCIM/100SONY/DSC00002.JPG")
	writeTestFile(t, mount, "DCIM/100SONY/DSC00002.JPG.xmp")
	writeTestFile(t, mount, "DCIM/100SONY/DSC00003.JPG")

	files, err := ScanVolume(mount, config.Default())
	if err != nil {
		t.Fatalf("ScanVolume() error = %v", err)
	}
	want := map[string]string{
		"DCIM/100SONY/DSC00001.ARW": "DSC00001.xmp",
		"DCIM/100SONY/DSC00002.JPG": "DSC00002.JPG.xmp",
		"DCIM/100SONY/DSC00003.JPG": "",
	}
	if len(files) != len(want) {
		t.Fatalf("ScanVolume() returned %d files, want %d: %#v", len(files), len(want), files)
	}
	for _, f := range files {
		got := ""
		if f.SidecarPath != "" {
			got = filepath.Base(f.SidecarPath)
		}
		if got != want[f.RelativePath] {
			t.Errorf("%s sidecar = %q, want %q", f.RelativePath, got, want[f.RelativePath])
		}
	}
}

// TestScanRelativePathsUseSlash checks that relative paths, which the
// database and history store, use "/" on every system, though Windows
// walks the card with "\".
func TestScanRelativePathsUseSlash(t *testing.T) {
	mount := t.TempDir()
	writeTestFile(t, mount, "DCIM/100X/A.JPG")

	files, err := ScanVolume(mount, config.Default())
	if err != nil {
		t.Fatalf("ScanVolume() error = %v", err)
	}
	if len(files) != 1 || files[0].RelativePath != "DCIM/100X/A.JPG" {
		t.Fatalf("ScanVolume() returned %#v, want one file at \"DCIM/100X/A.JPG\"", files)
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
