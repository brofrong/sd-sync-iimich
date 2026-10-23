//go:build darwin

package volume

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"howett.net/plist"
)

// withEraseSeams saves the erase-related seams and restores them after the
// test, so a failure partway through a test never leaks a fake into another
// test (or, worse, into a real run against the attached SD card).
func withEraseSeams(t *testing.T) {
	t.Helper()
	origRun := runDiskutil
	origWait := waitForMount
	origGetInfo := getInfo
	t.Cleanup(func() {
		runDiskutil = origRun
		waitForMount = origWait
		getInfo = origGetInfo
	})
}

func fakeDiskutilInfoPlist(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	b, err := plist.Marshal(fields, plist.XMLFormat)
	if err != nil {
		t.Fatalf("plist.Marshal: %v", err)
	}
	return b
}

// fakeRunDiskutil returns a runDiskutil replacement that answers an "info
// -plist" call with infoFields and records every call's args in gotArgs.
// Anything else (the eraseVolume call) is handled by erase.
func fakeRunDiskutil(t *testing.T, gotArgs *[][]string, infoFields map[string]any, erase func(args []string) ([]byte, error)) func(args ...string) ([]byte, error) {
	t.Helper()
	return func(args ...string) ([]byte, error) {
		*gotArgs = append(*gotArgs, args)
		if len(args) > 0 && args[0] == "info" {
			return fakeDiskutilInfoPlist(t, infoFields), nil
		}
		if erase != nil {
			return erase(args)
		}
		return nil, nil
	}
}

func TestEraseExFATKeepsNameAndFormat(t *testing.T) {
	withEraseSeams(t)

	var gotArgs [][]string
	runDiskutil = fakeRunDiskutil(t, &gotArgs, map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "BROFRONG",
		"Removable":        true,
	}, nil)

	var waitedMount string
	waitForMount = func(mountPoint string, timeout time.Duration) error {
		waitedMount = mountPoint
		if timeout != 60*time.Second {
			t.Fatalf("waitForMount timeout = %v, want 60s", timeout)
		}
		return nil
	}

	want := Info{Name: "BROFRONG", MountPoint: "/Volumes/BROFRONG", UUID: "NEW-UUID", Removable: true}
	getInfo = func(mountPoint string) (*Info, error) {
		if mountPoint != "/Volumes/BROFRONG" {
			t.Fatalf("getInfo mountPoint = %q, want %q", mountPoint, "/Volumes/BROFRONG")
		}
		return &want, nil
	}

	got, err := Erase("/Volumes/BROFRONG")
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Erase() = %+v, want %+v", got, want)
	}
	if waitedMount != "/Volumes/BROFRONG" {
		t.Fatalf("waitForMount called with %q, want %q", waitedMount, "/Volumes/BROFRONG")
	}

	if len(gotArgs) != 2 {
		t.Fatalf("runDiskutil calls = %v, want 2 calls", gotArgs)
	}
	wantInfoArgs := []string{"info", "-plist", "/Volumes/BROFRONG"}
	if !reflect.DeepEqual(gotArgs[0], wantInfoArgs) {
		t.Fatalf("info args = %v, want %v", gotArgs[0], wantInfoArgs)
	}
	wantEraseArgs := []string{"eraseVolume", "ExFAT", "BROFRONG", "disk4s1"}
	if !reflect.DeepEqual(gotArgs[1], wantEraseArgs) {
		t.Fatalf("erase args = %v, want %v", gotArgs[1], wantEraseArgs)
	}
}

func TestEraseFAT32(t *testing.T) {
	withEraseSeams(t)

	var gotArgs [][]string
	runDiskutil = fakeRunDiskutil(t, &gotArgs, map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "msdos",
		"FilesystemName":   "MS-DOS (FAT32)",
		"VolumeName":       "CANON_EOS",
		"Removable":        true,
	}, nil)
	waitForMount = func(mountPoint string, timeout time.Duration) error { return nil }
	getInfo = func(mountPoint string) (*Info, error) {
		return &Info{Name: "CANON_EOS", MountPoint: mountPoint, UUID: "UUID-FAT32", Removable: true}, nil
	}

	if _, err := Erase("/Volumes/CANON_EOS"); err != nil {
		t.Fatalf("Erase() error = %v", err)
	}

	wantEraseArgs := []string{"eraseVolume", "MS-DOS FAT32", "CANON_EOS", "disk4s1"}
	if !reflect.DeepEqual(gotArgs[1], wantEraseArgs) {
		t.Fatalf("erase args = %v, want %v", gotArgs[1], wantEraseArgs)
	}
}

func TestEraseFAT16(t *testing.T) {
	withEraseSeams(t)

	var gotArgs [][]string
	runDiskutil = fakeRunDiskutil(t, &gotArgs, map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "msdos",
		"FilesystemName":   "MS-DOS (FAT16)",
		"VolumeName":       "OLD_CARD",
		"Removable":        true,
	}, nil)
	waitForMount = func(mountPoint string, timeout time.Duration) error { return nil }
	getInfo = func(mountPoint string) (*Info, error) {
		return &Info{Name: "OLD_CARD", MountPoint: mountPoint, UUID: "UUID-FAT16", Removable: true}, nil
	}

	if _, err := Erase("/Volumes/OLD_CARD"); err != nil {
		t.Fatalf("Erase() error = %v", err)
	}

	wantEraseArgs := []string{"eraseVolume", "MS-DOS FAT16", "OLD_CARD", "disk4s1"}
	if !reflect.DeepEqual(gotArgs[1], wantEraseArgs) {
		t.Fatalf("erase args = %v, want %v", gotArgs[1], wantEraseArgs)
	}
}

func TestEraseRefusesNonRemovable(t *testing.T) {
	withEraseSeams(t)

	var gotArgs [][]string
	runDiskutil = fakeRunDiskutil(t, &gotArgs, map[string]any{
		"DeviceIdentifier": "disk1s1",
		"FilesystemType":   "apfs",
		"FilesystemName":   "APFS",
		"VolumeName":       "Macintosh HD",
		"Removable":        false,
		"Ejectable":        false,
		"External":         false,
	}, func(args []string) ([]byte, error) {
		t.Fatalf("eraseVolume must not be called for a non-removable volume, got args %v", args)
		return nil, nil
	})
	waitForMount = func(mountPoint string, timeout time.Duration) error {
		t.Fatal("waitForMount must not be called when erase is refused")
		return nil
	}
	getInfo = func(mountPoint string) (*Info, error) {
		t.Fatal("getInfo must not be called when erase is refused")
		return nil, nil
	}

	_, err := Erase("/")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if len(gotArgs) != 1 {
		t.Fatalf("runDiskutil calls = %v, want only the info call", gotArgs)
	}
}

func TestEraseRefusesAPFS(t *testing.T) {
	withEraseSeams(t)

	var gotArgs [][]string
	runDiskutil = fakeRunDiskutil(t, &gotArgs, map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "apfs",
		"FilesystemName":   "APFS",
		"VolumeName":       "SOMEDISK",
		"Removable":        true,
	}, func(args []string) ([]byte, error) {
		t.Fatalf("eraseVolume must not be called for an APFS volume, got args %v", args)
		return nil, nil
	})
	waitForMount = func(mountPoint string, timeout time.Duration) error {
		t.Fatal("waitForMount must not be called when erase is refused")
		return nil
	}
	getInfo = func(mountPoint string) (*Info, error) {
		t.Fatal("getInfo must not be called when erase is refused")
		return nil, nil
	}

	_, err := Erase("/Volumes/SOMEDISK")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if len(gotArgs) != 1 {
		t.Fatalf("runDiskutil calls = %v, want only the info call", gotArgs)
	}
}

func TestEraseRefusesWholeDiskIdentifier(t *testing.T) {
	withEraseSeams(t)

	var gotArgs [][]string
	runDiskutil = fakeRunDiskutil(t, &gotArgs, map[string]any{
		"DeviceIdentifier": "disk4",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "WHOLEDISK",
		"Removable":        true,
	}, func(args []string) ([]byte, error) {
		t.Fatalf("eraseVolume must not be called for a whole-disk identifier, got args %v", args)
		return nil, nil
	})
	waitForMount = func(mountPoint string, timeout time.Duration) error {
		t.Fatal("waitForMount must not be called when erase is refused")
		return nil
	}
	getInfo = func(mountPoint string) (*Info, error) {
		t.Fatal("getInfo must not be called when erase is refused")
		return nil, nil
	}

	_, err := Erase("/Volumes/WHOLEDISK")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
}

func TestEraseReportsDiskutilOutput(t *testing.T) {
	withEraseSeams(t)

	var gotArgs [][]string
	runDiskutil = fakeRunDiskutil(t, &gotArgs, map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "BROFRONG",
		"Removable":        true,
	}, func(args []string) ([]byte, error) {
		return []byte("Resource busy"), errors.New("exit status 1")
	})
	waitForMount = func(mountPoint string, timeout time.Duration) error {
		t.Fatal("waitForMount must not be called when eraseVolume fails")
		return nil
	}
	getInfo = func(mountPoint string) (*Info, error) {
		t.Fatal("getInfo must not be called when eraseVolume fails")
		return nil, nil
	}

	_, err := Erase("/Volumes/BROFRONG")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "Resource busy") {
		t.Fatalf("Erase() error = %q, want it to contain %q", err.Error(), "Resource busy")
	}
}
