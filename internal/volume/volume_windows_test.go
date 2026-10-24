//go:build windows

package volume

import (
	"fmt"
	"reflect"
	"testing"

	"golang.org/x/sys/windows"
)

func TestFormatSerial(t *testing.T) {
	got := formatSerial(0x1234ABCD)
	want := "1234-ABCD"
	if got != want {
		t.Fatalf("formatSerial(0x1234ABCD) = %q, want %q", got, want)
	}
}

// withVolumeSeams saves ListRemovable/GetInfo/FindByUUID's Win32 seams and
// restores them after the test, so a failing test never leaks a fake into
// another test (or, worse, into a real run against the attached SD card).
func withVolumeSeams(t *testing.T) {
	t.Helper()
	origDrives := getLogicalDrivesFn
	origType := getDriveTypeFn
	origInfo := getVolumeInformationFn
	origErrorMode := setErrorModeFn
	t.Cleanup(func() {
		getLogicalDrivesFn = origDrives
		getDriveTypeFn = origType
		getVolumeInformationFn = origInfo
		setErrorModeFn = origErrorMode
	})
}

func TestListRemovableFiltersFixed(t *testing.T) {
	withVolumeSeams(t)

	// A: fixed system drive, E: removable card reader. Only E should come
	// back from ListRemovable.
	getLogicalDrivesFn = func() (uint32, error) {
		return 1<<0 | 1<<4, nil // bit 0 = A, bit 4 = E
	}
	getDriveTypeFn = func(root string) uint32 {
		switch root {
		case `A:\`:
			return windows.DRIVE_FIXED
		case `E:\`:
			return windows.DRIVE_REMOVABLE
		default:
			t.Fatalf("getDriveTypeFn called with unexpected root %q", root)
			return windows.DRIVE_UNKNOWN
		}
	}
	getVolumeInformationFn = func(root string) (string, uint32, error) {
		switch root {
		case `A:\`:
			return "SYSTEM", 0x11112222, nil
		case `E:\`:
			return "CARD", 0x1234ABCD, nil
		default:
			return "", 0, fmt.Errorf("no volume at %s", root)
		}
	}

	got, err := ListRemovable()
	if err != nil {
		t.Fatalf("ListRemovable() error = %v", err)
	}
	want := []Info{
		{Name: "CARD", MountPoint: `E:\`, UUID: "1234-ABCD", Removable: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListRemovable() = %+v, want %+v", got, want)
	}
}

// TestDisableCriticalErrorDialogsPreservesExistingFlags checks that
// disableCriticalErrorDialogs ORs SEM_FAILCRITICALERRORS into whatever
// error-mode flags are already set, rather than overwriting them: a drive
// with no media must stop raising "insert a disk" dialogs without turning
// off some other flag (e.g. SEM_NOOPENFILEERRORBOX) that something else in
// the process depends on.
func TestDisableCriticalErrorDialogsPreservesExistingFlags(t *testing.T) {
	withVolumeSeams(t)

	var calls []uint32
	setErrorModeFn = func(mode uint32) uint32 {
		calls = append(calls, mode)
		if len(calls) == 1 {
			// Simulate some other flag already being set.
			return windows.SEM_NOOPENFILEERRORBOX
		}
		return 0
	}

	disableCriticalErrorDialogs()

	want := []uint32{
		windows.SEM_FAILCRITICALERRORS,
		windows.SEM_NOOPENFILEERRORBOX | windows.SEM_FAILCRITICALERRORS,
	}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("setErrorModeFn calls = %#v, want %#v", calls, want)
	}
}

// TestDisableCriticalErrorDialogsSkipsRedundantCall checks that when the
// previous mode already includes SEM_FAILCRITICALERRORS (or was already
// exactly SEM_FAILCRITICALERRORS, as after a prior call), a second,
// redundant SetErrorMode call isn't made.
func TestDisableCriticalErrorDialogsSkipsRedundantCall(t *testing.T) {
	withVolumeSeams(t)

	var calls []uint32
	setErrorModeFn = func(mode uint32) uint32 {
		calls = append(calls, mode)
		return windows.SEM_FAILCRITICALERRORS
	}

	disableCriticalErrorDialogs()

	if len(calls) != 1 {
		t.Fatalf("setErrorModeFn calls = %#v, want exactly one call", calls)
	}
}

// TestListRemovableDisablesErrorDialogsBeforeQueryingVolumes checks that
// ListRemovable applies the no-dialog error mode before it calls
// GetVolumeInformation on any drive, not after or interleaved with the
// loop: a removable/optical drive with no media must never get a chance to
// raise Windows' blocking dialog.
func TestListRemovableDisablesErrorDialogsBeforeQueryingVolumes(t *testing.T) {
	withVolumeSeams(t)

	var order []string
	setErrorModeFn = func(mode uint32) uint32 {
		order = append(order, "setErrorMode")
		return windows.SEM_FAILCRITICALERRORS
	}
	getLogicalDrivesFn = func() (uint32, error) {
		order = append(order, "getLogicalDrives")
		return 1 << 4, nil // E only
	}
	getDriveTypeFn = func(root string) uint32 {
		order = append(order, "getDriveType")
		return windows.DRIVE_REMOVABLE
	}
	getVolumeInformationFn = func(root string) (string, uint32, error) {
		order = append(order, "getVolumeInformation")
		return "CARD", 0x1234ABCD, nil
	}

	if _, err := ListRemovable(); err != nil {
		t.Fatalf("ListRemovable() error = %v", err)
	}

	if len(order) == 0 || order[0] != "setErrorMode" {
		t.Fatalf("call order = %v, want setErrorMode first", order)
	}
	setIdx, volIdx := -1, -1
	for i, name := range order {
		if name == "setErrorMode" && setIdx == -1 {
			setIdx = i
		}
		if name == "getVolumeInformation" && volIdx == -1 {
			volIdx = i
		}
	}
	if setIdx == -1 || volIdx == -1 || setIdx > volIdx {
		t.Fatalf("call order = %v, want setErrorMode before getVolumeInformation", order)
	}
}

// TestGetInfoDisablesErrorDialogsBeforeQueryingVolume is
// TestListRemovableDisablesErrorDialogsBeforeQueryingVolumes's counterpart
// for GetInfo, which queries a single drive directly rather than going
// through listAllVolumes.
func TestGetInfoDisablesErrorDialogsBeforeQueryingVolume(t *testing.T) {
	withVolumeSeams(t)

	var order []string
	setErrorModeFn = func(mode uint32) uint32 {
		order = append(order, "setErrorMode")
		return windows.SEM_FAILCRITICALERRORS
	}
	getDriveTypeFn = func(root string) uint32 {
		order = append(order, "getDriveType")
		return windows.DRIVE_REMOVABLE
	}
	getVolumeInformationFn = func(root string) (string, uint32, error) {
		order = append(order, "getVolumeInformation")
		return "CARD", 0x1234ABCD, nil
	}

	if _, err := GetInfo(`E:\`); err != nil {
		t.Fatalf("GetInfo() error = %v", err)
	}

	if len(order) < 2 || order[0] != "setErrorMode" || order[1] != "getVolumeInformation" {
		t.Fatalf("call order = %v, want [setErrorMode, getVolumeInformation, ...]", order)
	}
}

func TestWatcherDiff(t *testing.T) {
	cardE := Info{Name: "OLD", MountPoint: `E:\`, UUID: "AAAA-1111", Removable: true}
	cardF := Info{Name: "NEW", MountPoint: `F:\`, UUID: "BBBB-2222", Removable: true}
	cardG := Info{Name: "OTHER", MountPoint: `G:\`, UUID: "CCCC-3333", Removable: true}

	prev := []Info{cardE, cardG}
	cur := []Info{cardF, cardG}

	got := diffVolumes(prev, cur)
	want := []Event{
		{Type: EventUnmounted, Info: cardE},
		{Type: EventMounted, Info: cardF},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diffVolumes() = %+v, want %+v", got, want)
	}

	// Run it again with the roles reversed (prev/cur swapped) to confirm the
	// ordering (unmounted before mounted, each sorted by key) is stable and
	// not an artifact of map iteration order.
	got2 := diffVolumes(cur, prev)
	want2 := []Event{
		{Type: EventUnmounted, Info: cardF},
		{Type: EventMounted, Info: cardE},
	}
	if !reflect.DeepEqual(got2, want2) {
		t.Fatalf("diffVolumes() swapped = %+v, want %+v", got2, want2)
	}
}

func TestWatcherDiffNoChange(t *testing.T) {
	cards := []Info{
		{Name: "CARD", MountPoint: `E:\`, UUID: "AAAA-1111", Removable: true},
	}
	if got := diffVolumes(cards, cards); len(got) != 0 {
		t.Fatalf("diffVolumes(unchanged) = %+v, want no events", got)
	}
}
