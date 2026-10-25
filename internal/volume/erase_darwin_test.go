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
// test (or, worse, into a real run against the attached SD card). It also
// shrinks the polling interval/timeout so tests that exercise the
// post-erase polling loop run in milliseconds instead of up to a minute.
func withEraseSeams(t *testing.T) {
	t.Helper()
	origRun := runDiskutil
	origInterval := erasePollInterval
	origTimeout := eraseRemountTimeout
	t.Cleanup(func() {
		runDiskutil = origRun
		erasePollInterval = origInterval
		eraseRemountTimeout = origTimeout
	})
	erasePollInterval = time.Millisecond
	eraseRemountTimeout = 5 * time.Millisecond
}

func fakeDiskutilInfoPlist(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	b, err := plist.Marshal(fields, plist.XMLFormat)
	if err != nil {
		t.Fatalf("plist.Marshal: %v", err)
	}
	return b
}

// eraseFake is a fake runDiskutil for Erase. It answers the initial
// `info -plist <mountPoint>` call with card, every `eraseVolume` call with
// (eraseOut, eraseErr), and every `info -plist <device>` poll call (any
// "info" call whose target isn't mountPoint) with the next entry from
// polls. Once polls is exhausted, the last entry repeats, so a test can
// represent "the new volume never comes back" with a single entry.
type eraseFake struct {
	t          *testing.T
	mountPoint string
	card       map[string]any
	eraseOut   []byte
	eraseErr   error
	polls      []map[string]any
	pollIndex  int
	calls      [][]string
}

func newEraseFake(t *testing.T, mountPoint string, card map[string]any) *eraseFake {
	t.Helper()
	return &eraseFake{t: t, mountPoint: mountPoint, card: card}
}

func (f *eraseFake) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if len(args) == 0 {
		f.t.Fatalf("runDiskutil called with no args")
		return nil, nil
	}
	switch args[0] {
	case "info":
		if len(args) < 3 {
			f.t.Fatalf("diskutil info call missing target: %v", args)
			return nil, nil
		}
		if args[2] == f.mountPoint {
			return fakeDiskutilInfoPlist(f.t, f.card), nil
		}
		if len(f.polls) == 0 {
			f.t.Fatalf("no poll response configured for device call: %v", args)
			return nil, nil
		}
		idx := f.pollIndex
		if idx >= len(f.polls) {
			idx = len(f.polls) - 1
		} else {
			f.pollIndex++
		}
		return fakeDiskutilInfoPlist(f.t, f.polls[idx]), nil
	case "eraseVolume":
		return f.eraseOut, f.eraseErr
	default:
		f.t.Fatalf("unexpected diskutil verb: %v", args)
		return nil, nil
	}
}

// eraseCalled reports whether `diskutil eraseVolume` was ever invoked.
func (f *eraseFake) eraseCalled() bool {
	for _, c := range f.calls {
		if len(c) > 0 && c[0] == "eraseVolume" {
			return true
		}
	}
	return false
}

// pollCalls returns every `info -plist <device>` poll call (excluding the
// initial `info -plist <mountPoint>` call).
func (f *eraseFake) pollCalls() [][]string {
	var result [][]string
	for _, c := range f.calls {
		if len(c) > 2 && c[0] == "info" && c[2] != f.mountPoint {
			result = append(result, c)
		}
	}
	return result
}

func TestEraseExFATKeepsNameAndFormat(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/BROFRONG", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "BROFRONG",
		"VolumeUUID":       "old-uuid-0001",
		"Removable":        true,
	})
	fake.polls = []map[string]any{{
		"DeviceIdentifier": "disk4s1",
		"VolumeName":       "BROFRONG",
		"VolumeUUID":       "new-uuid-5678",
		"MountPoint":       "/Volumes/BROFRONG",
	}}
	runDiskutil = fake.run

	// wantUUID deliberately differs in case from the card's VolumeUUID to
	// exercise the case-insensitive identity check.
	got, err := Erase("/Volumes/BROFRONG", "OLD-UUID-0001")
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	want := Info{Name: "BROFRONG", MountPoint: "/Volumes/BROFRONG", UUID: "NEW-UUID-5678", Removable: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Erase() = %+v, want %+v", got, want)
	}

	if len(fake.calls) < 2 {
		t.Fatalf("runDiskutil calls = %v, want at least an info call and an eraseVolume call", fake.calls)
	}
	wantInfoArgs := []string{"info", "-plist", "/Volumes/BROFRONG"}
	if !reflect.DeepEqual(fake.calls[0], wantInfoArgs) {
		t.Fatalf("info args = %v, want %v", fake.calls[0], wantInfoArgs)
	}
	wantEraseArgs := []string{"eraseVolume", "ExFAT", "BROFRONG", "disk4s1"}
	if !reflect.DeepEqual(fake.calls[1], wantEraseArgs) {
		t.Fatalf("erase args = %v, want %v", fake.calls[1], wantEraseArgs)
	}
}

func TestEraseFAT32(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/CANON_EOS", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "msdos",
		"FilesystemName":   "MS-DOS (FAT32)",
		"VolumeName":       "CANON_EOS",
		"VolumeUUID":       "OLD-FAT32",
		"Removable":        true,
	})
	fake.polls = []map[string]any{{
		"DeviceIdentifier": "disk4s1",
		"VolumeName":       "CANON_EOS",
		"VolumeUUID":       "NEW-FAT32",
		"MountPoint":       "/Volumes/CANON_EOS",
	}}
	runDiskutil = fake.run

	if _, err := Erase("/Volumes/CANON_EOS", "OLD-FAT32"); err != nil {
		t.Fatalf("Erase() error = %v", err)
	}

	if len(fake.calls) < 2 {
		t.Fatalf("runDiskutil calls = %v, want at least an info call and an eraseVolume call", fake.calls)
	}
	wantEraseArgs := []string{"eraseVolume", "MS-DOS FAT32", "CANON_EOS", "disk4s1"}
	if !reflect.DeepEqual(fake.calls[1], wantEraseArgs) {
		t.Fatalf("erase args = %v, want %v", fake.calls[1], wantEraseArgs)
	}
}

func TestEraseFAT16(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/OLD_CARD", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "msdos",
		"FilesystemName":   "MS-DOS (FAT16)",
		"VolumeName":       "OLD_CARD",
		"VolumeUUID":       "OLD-FAT16",
		"Removable":        true,
	})
	fake.polls = []map[string]any{{
		"DeviceIdentifier": "disk4s1",
		"VolumeName":       "OLD_CARD",
		"VolumeUUID":       "NEW-FAT16",
		"MountPoint":       "/Volumes/OLD_CARD",
	}}
	runDiskutil = fake.run

	if _, err := Erase("/Volumes/OLD_CARD", "OLD-FAT16"); err != nil {
		t.Fatalf("Erase() error = %v", err)
	}

	if len(fake.calls) < 2 {
		t.Fatalf("runDiskutil calls = %v, want at least an info call and an eraseVolume call", fake.calls)
	}
	wantEraseArgs := []string{"eraseVolume", "MS-DOS FAT16", "OLD_CARD", "disk4s1"}
	if !reflect.DeepEqual(fake.calls[1], wantEraseArgs) {
		t.Fatalf("erase args = %v, want %v", fake.calls[1], wantEraseArgs)
	}
}

func TestEraseRefusesFAT12(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/OLDCAM", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "msdos",
		"FilesystemName":   "MS-DOS (FAT12)",
		"VolumeName":       "OLDCAM",
		"VolumeUUID":       "FAT12-UUID",
		"Removable":        true,
	})
	runDiskutil = fake.run

	_, err := Erase("/Volumes/OLDCAM", "FAT12-UUID")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if fake.eraseCalled() {
		t.Fatal("eraseVolume must not be called for an unsupported FAT variant (FAT12)")
	}
}

func TestEraseRefusesNonRemovable(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/INTERNAL", map[string]any{
		"DeviceIdentifier": "disk2s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "INTERNAL",
		"VolumeUUID":       "INTERNAL-UUID",
		"Removable":        false,
		"Ejectable":        false,
		"External":         false,
	})
	runDiskutil = fake.run

	_, err := Erase("/Volumes/INTERNAL", "INTERNAL-UUID")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "non-removable") {
		t.Fatalf("Erase() error = %q, want it to identify the non-removable gate", err.Error())
	}
	if fake.eraseCalled() {
		t.Fatal("eraseVolume must not be called for a non-removable volume")
	}
}

func TestEraseRefusesBootVolume(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/", map[string]any{
		"DeviceIdentifier": "disk1s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "Macintosh HD",
		"VolumeUUID":       "BOOT-UUID",
		"Removable":        true,
	})
	runDiskutil = fake.run

	_, err := Erase("/", "BOOT-UUID")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "boot volume") {
		t.Fatalf("Erase() error = %q, want it to identify the boot-volume gate", err.Error())
	}
	if fake.eraseCalled() {
		t.Fatal("eraseVolume must not be called for the boot volume, even if diskutil reports it removable")
	}
}

func TestEraseRefusesAPFS(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/SOMEDISK", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "apfs",
		"FilesystemName":   "APFS",
		"VolumeName":       "SOMEDISK",
		"VolumeUUID":       "APFS-UUID",
		"Removable":        true,
	})
	runDiskutil = fake.run

	_, err := Erase("/Volumes/SOMEDISK", "APFS-UUID")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if fake.eraseCalled() {
		t.Fatal("eraseVolume must not be called for an APFS volume")
	}
}

func TestEraseRefusesWholeDiskIdentifier(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/WHOLEDISK", map[string]any{
		"DeviceIdentifier": "disk4",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "WHOLEDISK",
		"VolumeUUID":       "WHOLE-UUID",
		"Removable":        true,
	})
	runDiskutil = fake.run

	_, err := Erase("/Volumes/WHOLEDISK", "WHOLE-UUID")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if fake.eraseCalled() {
		t.Fatal("eraseVolume must not be called for a whole-disk identifier")
	}
}

func TestEraseRefusesEmptyDeviceIdentifier(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/NODEVICE", map[string]any{
		"DeviceIdentifier": "",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "NODEVICE",
		"VolumeUUID":       "NODEV-UUID",
		"Removable":        true,
	})
	runDiskutil = fake.run

	_, err := Erase("/Volumes/NODEVICE", "NODEV-UUID")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if fake.eraseCalled() {
		t.Fatal("eraseVolume must not be called without a device identifier")
	}
}

func TestEraseRefusesEmptyName(t *testing.T) {
	cases := []struct {
		name       string
		volumeName string
	}{
		{"empty", ""},
		{"slash", "a/b"},
		{"dotdot", "a..b"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withEraseSeams(t)

			fake := newEraseFake(t, "/Volumes/NONAME", map[string]any{
				"DeviceIdentifier": "disk4s1",
				"FilesystemType":   "exfat",
				"FilesystemName":   "ExFAT",
				"VolumeName":       tc.volumeName,
				"VolumeUUID":       "NONAME-UUID",
				"Removable":        true,
			})
			runDiskutil = fake.run

			_, err := Erase("/Volumes/NONAME", "NONAME-UUID")
			if err == nil {
				t.Fatal("Erase() error = nil, want error")
			}
			if fake.eraseCalled() {
				t.Fatal("eraseVolume must not be called for an invalid volume name")
			}
		})
	}
}

func TestEraseRefusesOtherCard(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/BROFRONG", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "BROFRONG",
		"VolumeUUID":       "ABCD-1111",
		"Removable":        true,
	})
	runDiskutil = fake.run

	// Same name and path as a legitimate card, but the UUID the caller
	// verified belongs to a different VolumeUUID than what's mounted now.
	_, err := Erase("/Volumes/BROFRONG", "ZZZZ-9999")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if fake.eraseCalled() {
		t.Fatal("eraseVolume must not be called when the mounted card's UUID differs from wantUUID")
	}
}

func TestEraseRefusesEmptyWantUUID(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/BROFRONG", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "BROFRONG",
		"VolumeUUID":       "ABCD-1111",
		"Removable":        true,
	})
	runDiskutil = fake.run

	_, err := Erase("/Volumes/BROFRONG", "")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if fake.eraseCalled() {
		t.Fatal("eraseVolume must not be called without a card identity to verify")
	}
}

func TestEraseReportsDiskutilOutput(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/BROFRONG", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "BROFRONG",
		"VolumeUUID":       "BUSY-UUID",
		"Removable":        true,
	})
	fake.eraseOut = []byte("Resource busy")
	fake.eraseErr = errors.New("exit status 1")
	runDiskutil = fake.run

	_, err := Erase("/Volumes/BROFRONG", "BUSY-UUID")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "Resource busy") {
		t.Fatalf("Erase() error = %q, want it to contain %q", err.Error(), "Resource busy")
	}
	if calls := fake.pollCalls(); len(calls) != 0 {
		t.Fatalf("polled for a remount after a failed eraseVolume: %v", calls)
	}
}

func TestEraseFindsNewVolumeByDevice(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/BROFRONG", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "BROFRONG",
		"VolumeUUID":       "OLD-UUID",
		"Removable":        true,
	})
	fake.polls = []map[string]any{
		{
			// Not remounted yet: no MountPoint at all.
			"DeviceIdentifier": "disk4s1",
			"VolumeName":       "BROFRONG",
			"VolumeUUID":       "OLD-UUID",
		},
		{
			"DeviceIdentifier": "disk4s1",
			"VolumeName":       "BROFRONG",
			"VolumeUUID":       "NEW-UUID",
			"MountPoint":       "/Volumes/BROFRONG",
		},
	}
	runDiskutil = fake.run

	got, err := Erase("/Volumes/BROFRONG", "OLD-UUID")
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	want := Info{Name: "BROFRONG", MountPoint: "/Volumes/BROFRONG", UUID: "NEW-UUID", Removable: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Erase() = %+v, want %+v", got, want)
	}
	if calls := fake.pollCalls(); len(calls) != 2 {
		t.Fatalf("poll calls = %v, want exactly 2 (first without a mount, second with one)", calls)
	}
}

func TestEraseRemountTimeout(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/BROFRONG", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "BROFRONG",
		"VolumeUUID":       "OLD-UUID",
		"Removable":        true,
	})
	fake.polls = []map[string]any{{
		// Never remounts: no MountPoint, ever.
		"DeviceIdentifier": "disk4s1",
		"VolumeName":       "BROFRONG",
		"VolumeUUID":       "OLD-UUID",
	}}
	runDiskutil = fake.run

	_, err := Erase("/Volumes/BROFRONG", "OLD-UUID")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if !errors.Is(err, ErrErasedNotRemounted) {
		t.Fatalf("Erase() error = %v, want it to wrap ErrErasedNotRemounted", err)
	}
}

func TestEraseNewVolumeSameUUIDFails(t *testing.T) {
	withEraseSeams(t)

	fake := newEraseFake(t, "/Volumes/BROFRONG", map[string]any{
		"DeviceIdentifier": "disk4s1",
		"FilesystemType":   "exfat",
		"FilesystemName":   "ExFAT",
		"VolumeName":       "BROFRONG",
		"VolumeUUID":       "OLD-UUID",
		"Removable":        true,
	})
	fake.polls = []map[string]any{{
		// Mounted again, but diskutil never gives it a new identity:
		// stale data, not the freshly erased volume.
		"DeviceIdentifier": "disk4s1",
		"VolumeName":       "BROFRONG",
		"VolumeUUID":       "OLD-UUID",
		"MountPoint":       "/Volumes/BROFRONG",
	}}
	runDiskutil = fake.run

	_, err := Erase("/Volumes/BROFRONG", "OLD-UUID")
	if err == nil {
		t.Fatal("Erase() error = nil, want error")
	}
	if !errors.Is(err, ErrErasedNotRemounted) {
		t.Fatalf("Erase() error = %v, want it to wrap ErrErasedNotRemounted", err)
	}
}

// TestEraseNeedsAdminDarwin: diskutil never needs administrator rights to
// erase a card, so the auto-format flow on macOS is unchanged.
func TestEraseNeedsAdminDarwin(t *testing.T) {
	if EraseNeedsAdmin() {
		t.Error("EraseNeedsAdmin() = true on macOS, want false")
	}
}
