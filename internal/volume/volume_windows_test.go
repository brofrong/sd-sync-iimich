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
	t.Cleanup(func() {
		getLogicalDrivesFn = origDrives
		getDriveTypeFn = origType
		getVolumeInformationFn = origInfo
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
