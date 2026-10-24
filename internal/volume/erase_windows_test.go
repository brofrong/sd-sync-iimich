//go:build windows

package volume

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

const (
	testCardRoot = `E:\`
	testCardGUID = `\\?\Volume{0f3c1a2b-4d5e-11ef-9a7b-806e6f6e6963}\`
)

// withWindowsEraseSeams replaces every seam Erase touches with one that
// fails the test if it is reached unconfigured, and restores the originals
// afterwards: a test must never format, or even query, a real drive. It
// also shrinks the post-format polling so the loop runs in milliseconds.
func withWindowsEraseSeams(t *testing.T) {
	t.Helper()
	withVolumeSeams(t)
	origDetails := getVolumeDetailsFn
	origGUID := getVolumeGUIDPathFn
	origSystem := systemDrivesFn
	origElevated := isElevatedFn
	origFormat := runFormatFn
	origInterval := erasePollInterval
	origTimeout := eraseRemountTimeout
	t.Cleanup(func() {
		getVolumeDetailsFn = origDetails
		getVolumeGUIDPathFn = origGUID
		systemDrivesFn = origSystem
		isElevatedFn = origElevated
		runFormatFn = origFormat
		erasePollInterval = origInterval
		eraseRemountTimeout = origTimeout
	})
	erasePollInterval = time.Millisecond
	eraseRemountTimeout = 5 * time.Millisecond

	setErrorModeFn = func(mode uint32) uint32 { return 0 }
	getLogicalDrivesFn = func() (uint32, error) {
		t.Fatal("Erase must not enumerate drives")
		return 0, nil
	}
	getVolumeInformationFn = func(root string) (string, uint32, error) {
		t.Fatal("Erase must read the volume through getVolumeDetailsFn")
		return "", 0, nil
	}
	runFormatFn = func(req formatRequest) ([]byte, error) {
		t.Fatal("runFormatFn reached without a fake")
		return nil, nil
	}
}

// winEraseFake is a fake Windows machine for Erase: C: is the fixed system
// drive, E: (testCardRoot) a removable card reader holding card before the
// format and after once runFormatFn was called.
type winEraseFake struct {
	t *testing.T

	driveTypes map[string]uint32
	guids      map[string]string
	card       volumeDetails
	after      volumeDetails
	afterType  uint32
	afterGUID  string
	system     []string
	elevated   bool

	formatOut []byte
	formatErr error
	formats   []formatRequest
}

func newWinEraseFake(t *testing.T) *winEraseFake {
	t.Helper()
	withWindowsEraseSeams(t)
	f := &winEraseFake{
		t: t,
		driveTypes: map[string]uint32{
			`C:\`:        windows.DRIVE_FIXED,
			testCardRoot: windows.DRIVE_REMOVABLE,
		},
		guids: map[string]string{
			`C:\`:        `\\?\Volume{11111111-2222-3333-4444-555555555555}\`,
			testCardRoot: testCardGUID,
		},
		card:      volumeDetails{Name: "EOS_DIGITAL", Serial: 0x1234ABCD, FileSystem: "exFAT"},
		after:     volumeDetails{Name: "EOS_DIGITAL", Serial: 0x9876FEDC, FileSystem: "exFAT"},
		afterType: windows.DRIVE_REMOVABLE,
		afterGUID: testCardGUID,
		system:    []string{`C:\`},
		elevated:  true,
	}
	getDriveTypeFn = func(root string) uint32 {
		if len(f.formats) > 0 && root == testCardRoot {
			return f.afterType
		}
		if dt, ok := f.driveTypes[root]; ok {
			return dt
		}
		return windows.DRIVE_NO_ROOT_DIR
	}
	getVolumeGUIDPathFn = func(root string) (string, error) {
		if len(f.formats) > 0 && root == testCardRoot {
			return f.afterGUID, nil
		}
		if g, ok := f.guids[root]; ok {
			return g, nil
		}
		return "", errors.New("no volume")
	}
	getVolumeDetailsFn = func(root string) (volumeDetails, error) {
		switch {
		case root == `C:\`:
			return volumeDetails{Name: "Windows", Serial: 0x11112222, FileSystem: "NTFS"}, nil
		case root != testCardRoot:
			return volumeDetails{}, errors.New("no volume")
		case len(f.formats) > 0:
			return f.after, nil
		default:
			return f.card, nil
		}
	}
	systemDrivesFn = func() []string { return f.system }
	isElevatedFn = func() bool { return f.elevated }
	runFormatFn = func(req formatRequest) ([]byte, error) {
		f.formats = append(f.formats, req)
		return f.formatOut, f.formatErr
	}
	return f
}

func (f *winEraseFake) assertNotFormatted() {
	f.t.Helper()
	if len(f.formats) != 0 {
		f.t.Fatalf("format ran %d times (%+v), want never", len(f.formats), f.formats)
	}
}

func TestWindowsEraseKeepsLabelAndFormat(t *testing.T) {
	for _, tt := range []struct {
		fs, wantFS string
	}{
		{"exFAT", "exFAT"},
		{"FAT32", "FAT32"},
		{"FAT", "FAT"},
	} {
		t.Run(tt.fs, func(t *testing.T) {
			f := newWinEraseFake(t)
			f.card.FileSystem = tt.fs
			f.after.FileSystem = tt.fs

			got, err := Erase(`E:\`, "1234-abcd")
			if err != nil {
				t.Fatalf("Erase() error = %v", err)
			}
			want := []formatRequest{{Root: `E:\`, FileSystem: tt.wantFS, Label: "EOS_DIGITAL"}}
			if !reflect.DeepEqual(f.formats, want) {
				t.Fatalf("format requests = %+v, want %+v", f.formats, want)
			}
			wantInfo := Info{Name: "EOS_DIGITAL", MountPoint: `E:\`, UUID: "9876-FEDC", Removable: true}
			if got != wantInfo {
				t.Fatalf("Erase() = %+v, want %+v", got, wantInfo)
			}
		})
	}
}

// TestWindowsFormatCommandLine pins the exact format.com invocation: the
// drive letter only, the same filesystem and label, quick, no prompts.
func TestWindowsFormatCommandLine(t *testing.T) {
	got := formatArgs(formatRequest{Root: `E:\`, FileSystem: "FAT32", Label: "MY CARD"})
	want := []string{"E:", "/FS:FAT32", "/V:MY CARD", "/Q", "/Y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("formatArgs() = %q, want %q", got, want)
	}
	// Whatever format.com asks ("Insert new disk ... press ENTER", "Enter
	// current volume label"), the answer is Enter or the label Erase
	// verified, and then end of input: never a "Y".
	in := formatStdin(formatRequest{Root: `E:\`, FileSystem: "FAT32", Label: "MY CARD"})
	if in != "MY CARD\r\nMY CARD\r\n" {
		t.Fatalf("formatStdin() = %q", in)
	}
}

func TestWindowsEraseRefusesOtherSerial(t *testing.T) {
	f := newWinEraseFake(t)

	_, err := Erase(`E:\`, "1234-ABCE")
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("Erase() error = %v, want an identity mismatch", err)
	}
	f.assertNotFormatted()
}

func TestWindowsEraseRefusesEmptyWantUUID(t *testing.T) {
	f := newWinEraseFake(t)

	if _, err := Erase(`E:\`, ""); err == nil {
		t.Fatal("Erase() with an empty wantUUID succeeded, want a refusal")
	}
	f.assertNotFormatted()
}

func TestWindowsEraseRefusesZeroSerial(t *testing.T) {
	f := newWinEraseFake(t)
	f.card.Serial = 0

	if _, err := Erase(`E:\`, "0000-0000"); err == nil {
		t.Fatal("Erase() of a volume without a serial succeeded, want a refusal")
	}
	f.assertNotFormatted()
}

func TestWindowsEraseRefusesSystemDrive(t *testing.T) {
	f := newWinEraseFake(t)
	// Even a "removable" system drive whose serial matches is refused.
	f.system = []string{`E:`}

	_, err := Erase(`E:\`, "1234-ABCD")
	if err == nil || !strings.Contains(err.Error(), "system drive") {
		t.Fatalf("Erase() error = %v, want a system drive refusal", err)
	}
	f.assertNotFormatted()
}

func TestWindowsEraseRefusesUnknownSystemDrive(t *testing.T) {
	f := newWinEraseFake(t)
	f.system = nil

	if _, err := Erase(`E:\`, "1234-ABCD"); err == nil {
		t.Fatal("Erase() without a known system drive succeeded, want a refusal")
	}
	f.assertNotFormatted()
}

func TestWindowsEraseRefusesNonRemovable(t *testing.T) {
	for _, dt := range []uint32{windows.DRIVE_FIXED, windows.DRIVE_REMOTE, windows.DRIVE_CDROM, windows.DRIVE_RAMDISK, windows.DRIVE_UNKNOWN} {
		f := newWinEraseFake(t)
		f.driveTypes[testCardRoot] = dt

		if _, err := Erase(`E:\`, "1234-ABCD"); err == nil {
			t.Fatalf("Erase() of drive type %d succeeded, want a refusal", dt)
		}
		f.assertNotFormatted()
	}
}

func TestWindowsEraseRefusesNonDriveLetter(t *testing.T) {
	for _, mp := range []string{"", `E:\DCIM`, `\\server\share\`, `E`, `EE:\`, `/Volumes/CARD`, `1:\`} {
		f := newWinEraseFake(t)

		if _, err := Erase(mp, "1234-ABCD"); err == nil {
			t.Fatalf("Erase(%q) succeeded, want a refusal", mp)
		}
		f.assertNotFormatted()
	}
}

func TestWindowsEraseRefusesWithoutVolumeGUID(t *testing.T) {
	for _, guid := range []string{"", `\\.\PhysicalDrive1`, `\\?\Volume{bad}\`} {
		f := newWinEraseFake(t)
		f.guids[testCardRoot] = guid

		if _, err := Erase(`E:\`, "1234-ABCD"); err == nil {
			t.Fatalf("Erase() with volume name %q succeeded, want a refusal", guid)
		}
		f.assertNotFormatted()
	}
}

func TestWindowsEraseRefusesInvalidLabel(t *testing.T) {
	for _, label := range []string{"", "A/B", "..", `A"B`, `A\B`, "A\nB"} {
		f := newWinEraseFake(t)
		f.card.Name = label

		if _, err := Erase(`E:\`, "1234-ABCD"); err == nil {
			t.Fatalf("Erase() of a volume labelled %q succeeded, want a refusal", label)
		}
		f.assertNotFormatted()
	}
}

func TestWindowsEraseRefusesOtherFilesystems(t *testing.T) {
	for _, fs := range []string{"NTFS", "ReFS", "FAT12", "CDFS", "UDF", ""} {
		f := newWinEraseFake(t)
		f.card.FileSystem = fs

		if _, err := Erase(`E:\`, "1234-ABCD"); err == nil {
			t.Fatalf("Erase() of a %q volume succeeded, want a refusal", fs)
		}
		f.assertNotFormatted()
	}
}

func TestWindowsEraseVolumeInfoError(t *testing.T) {
	f := newWinEraseFake(t)
	getVolumeDetailsFn = func(root string) (volumeDetails, error) {
		return volumeDetails{}, errors.New("the device is not ready")
	}

	if _, err := Erase(`E:\`, "1234-ABCD"); err == nil {
		t.Fatal("Erase() with an unreadable volume succeeded, want an error")
	}
	f.assertNotFormatted()
}

// TestWindowsEraseDisablesErrorDialogsBeforeQueryingVolume: an empty
// reader must not raise "There is no disk in the drive" during an erase.
func TestWindowsEraseDisablesErrorDialogsBeforeQueryingVolume(t *testing.T) {
	f := newWinEraseFake(t)
	var calls []string
	setErrorModeFn = func(mode uint32) uint32 {
		calls = append(calls, "seterrormode")
		return 0
	}
	inner := getVolumeDetailsFn
	getVolumeDetailsFn = func(root string) (volumeDetails, error) {
		calls = append(calls, "details")
		return inner(root)
	}

	if _, err := Erase(`E:\`, "1234-ABCD"); err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	if len(calls) < 2 || calls[0] != "seterrormode" || calls[1] != "details" {
		t.Fatalf("calls = %v, want SetErrorMode before the first volume query", calls)
	}
	_ = f
}

func TestWindowsEraseAccessDeniedIsNeedsAdmin(t *testing.T) {
	t.Run("not elevated", func(t *testing.T) {
		f := newWinEraseFake(t)
		f.elevated = false

		_, err := Erase(`E:\`, "1234-ABCD")
		if !errors.Is(err, ErrNeedsAdmin) {
			t.Fatalf("Erase() error = %v, want ErrNeedsAdmin", err)
		}
		// format.com would refuse anyway: it is never started.
		f.assertNotFormatted()
	})
	for _, tt := range []struct {
		name string
		out  string
		err  error
	}{
		{"access denied output", "Access Denied as you do not have sufficient privileges or the disk may be locked by another process.\r\nYou have to invoke this utility running in elevated mode and make sure the disk is unlocked.\r\n", errors.New("exit status 4")},
		{"elevation required", "", windows.ERROR_ELEVATION_REQUIRED},
		{"access denied error", "", windows.ERROR_ACCESS_DENIED},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newWinEraseFake(t)
			f.formatOut, f.formatErr = []byte(tt.out), tt.err

			_, err := Erase(`E:\`, "1234-ABCD")
			if !errors.Is(err, ErrNeedsAdmin) {
				t.Fatalf("Erase() error = %v, want ErrNeedsAdmin", err)
			}
			if errors.Is(err, ErrErasedNotRemounted) {
				t.Fatalf("Erase() error = %v, want the card reported as not erased", err)
			}
		})
	}
}

func TestWindowsEraseReportsFormatOutput(t *testing.T) {
	f := newWinEraseFake(t)
	f.formatOut = []byte("The volume is in use by another process.")
	f.formatErr = errors.New("exit status 4")

	_, err := Erase(`E:\`, "1234-ABCD")
	if err == nil || !strings.Contains(err.Error(), "in use by another process") {
		t.Fatalf("Erase() error = %v, want format.com's output in it", err)
	}
	if errors.Is(err, ErrNeedsAdmin) || errors.Is(err, ErrErasedNotRemounted) {
		t.Fatalf("Erase() error = %v, want a plain failure", err)
	}
}

func TestWindowsEraseFindsNewVolumeByDevice(t *testing.T) {
	f := newWinEraseFake(t)
	eraseRemountTimeout = time.Second
	// The new serial shows up only on the third look.
	looks := 0
	getVolumeDetailsFn = func(root string) (volumeDetails, error) {
		if root != testCardRoot {
			t.Fatalf("volume queried at %q, want only %q", root, testCardRoot)
		}
		if len(f.formats) == 0 {
			return f.card, nil
		}
		looks++
		switch looks {
		case 1:
			return volumeDetails{}, errors.New("the device is not ready")
		case 2:
			return f.card, nil // stale: the old serial
		default:
			return volumeDetails{Name: "EOS_DIGITAL", Serial: 0xCAFE0001, FileSystem: "exFAT"}, nil
		}
	}

	got, err := Erase(`E:\`, "1234-ABCD")
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	if got.UUID != "CAFE-0001" || got.MountPoint != `E:\` {
		t.Fatalf("Erase() = %+v, want the new serial at E:", got)
	}
	if looks != 3 {
		t.Fatalf("looked %d times, want 3", looks)
	}
}

func TestWindowsEraseNewVolumeMustBeSameDevice(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(f *winEraseFake)
	}{
		{"same serial", func(f *winEraseFake) { f.after = f.card }},
		{"zero serial", func(f *winEraseFake) { f.after.Serial = 0 }},
		{"other volume at the letter", func(f *winEraseFake) {
			f.afterGUID = `\\?\Volume{99999999-8888-7777-6666-555555555555}\`
		}},
		{"no longer removable", func(f *winEraseFake) { f.afterType = windows.DRIVE_FIXED }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newWinEraseFake(t)
			tt.setup(f)

			_, err := Erase(`E:\`, "1234-ABCD")
			if !errors.Is(err, ErrErasedNotRemounted) {
				t.Fatalf("Erase() error = %v, want ErrErasedNotRemounted", err)
			}
			if len(f.formats) != 1 {
				t.Fatalf("format ran %d times, want once", len(f.formats))
			}
		})
	}
}
