//go:build linux

package volume

import (
	"reflect"
	"testing"
)

// withVolumeSeams saves ListRemovable/GetInfo/FindByUUID's IO seams and
// restores them after the test, so a failing test never leaks a fixture
// into another test (or, worse, into a real run against this machine's
// actual /proc, /sys or /dev).
func withVolumeSeams(t *testing.T) {
	t.Helper()
	origMountinfo := readMountinfoFn
	origSysRemovable := isSysRemovableFn
	origByUUID := listByUUIDFn
	t.Cleanup(func() {
		readMountinfoFn = origMountinfo
		isSysRemovableFn = origSysRemovable
		listByUUIDFn = origByUUID
	})
}

func TestParseMountinfo(t *testing.T) {
	// A mount point containing a space, octal-escaped as the kernel writes
	// it ("\040"), alongside a normal line with no escaping, to confirm
	// parseMountinfo decodes the escape without disturbing ordinary lines.
	data := "15 1 253:0 / / rw,relatime shared:1 - ext4 /dev/mapper/vgubuntu-root rw,errors=remount-ro\n" +
		"61 25 8:18 / /media/user/My\\040Card rw,nosuid,nodev,relatime shared:35 - vfat /dev/sdc1 rw,fmask=0022\n"

	got, err := parseMountinfo(data)
	if err != nil {
		t.Fatalf("parseMountinfo() error = %v", err)
	}

	want := []mountEntry{
		{MountPoint: "/", FSType: "ext4", Source: "/dev/mapper/vgubuntu-root"},
		{MountPoint: "/media/user/My Card", FSType: "vfat", Source: "/dev/sdc1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMountinfo() = %+v, want %+v", got, want)
	}
}

func TestParseMountinfoRejectsMalformedLine(t *testing.T) {
	if _, err := parseMountinfo("too few fields\n"); err == nil {
		t.Fatal("parseMountinfo() error = nil, want error for a malformed line")
	}
}

// TestLinuxRemovableByMountPoint is the Review Focus 1 test: a USB SD-card
// reader whose block device reports removable=0 at the sysfs level (common
// for some readers) must still show up as removable once its card is
// mounted under /media/<user>/..., the path desktop Linux auto-mounts
// removable media under.
func TestLinuxRemovableByMountPoint(t *testing.T) {
	withVolumeSeams(t)

	readMountinfoFn = func() (string, error) {
		return "60 25 8:17 / /media/u/CARD rw,relatime shared:34 - vfat /dev/sdb1 rw\n", nil
	}
	isSysRemovableFn = func(disk string) bool {
		if disk != "sdb" {
			t.Fatalf("isSysRemovableFn called with unexpected disk %q", disk)
		}
		return false // the reader reports removable=0 despite the card being removable.
	}
	listByUUIDFn = func() ([]uuidLink, error) {
		return []uuidLink{{UUID: "ABCD-1234", Device: "/dev/sdb1"}}, nil
	}

	got, err := ListRemovable()
	if err != nil {
		t.Fatalf("ListRemovable() error = %v", err)
	}
	want := []Info{
		{Name: "CARD", MountPoint: "/media/u/CARD", UUID: "ABCD-1234", Removable: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListRemovable() = %+v, want %+v", got, want)
	}
}

// TestLinuxIgnoresSystemMounts checks that ordinary system mounts ("/",
// "/boot", a snap's squashfs loop mount) never show up as removable, even
// though they're real entries in mountinfo, alongside one genuine
// removable card to confirm the filtering is selective rather than
// accidentally excluding everything.
func TestLinuxIgnoresSystemMounts(t *testing.T) {
	withVolumeSeams(t)

	readMountinfoFn = func() (string, error) {
		return "" +
			"15 1 253:0 / / rw,relatime shared:1 - ext4 /dev/mapper/vgubuntu-root rw,errors=remount-ro\n" +
			"16 15 259:1 / /boot rw,relatime shared:2 - ext4 /dev/nvme0n1p2 rw\n" +
			"17 1 7:0 / /snap/core20/1234 ro,nodev,relatime shared:3 - squashfs /dev/loop0 ro\n" +
			"60 25 8:17 / /media/u/CARD rw,relatime shared:34 - vfat /dev/sdb1 rw\n", nil
	}
	isSysRemovableFn = func(disk string) bool { return false }
	listByUUIDFn = func() ([]uuidLink, error) { return nil, nil }

	got, err := ListRemovable()
	if err != nil {
		t.Fatalf("ListRemovable() error = %v", err)
	}
	want := []Info{
		{Name: "CARD", MountPoint: "/media/u/CARD", UUID: "", Removable: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListRemovable() = %+v, want only the card at /media/u/CARD", got)
	}
}

func TestUUIDFromByUUID(t *testing.T) {
	links := []uuidLink{
		{UUID: "1234-5678", Device: "/dev/sdb1"},
		{UUID: "abcdefab-1234-5678-9abc-def012345678", Device: "/dev/sdc1"},
	}

	if got, want := uuidForDevice("/dev/sdb1", links), "1234-5678"; got != want {
		t.Errorf("uuidForDevice(/dev/sdb1) = %q, want %q", got, want)
	}
	if got, want := uuidForDevice("/dev/sdc1", links), "ABCDEFAB-1234-5678-9ABC-DEF012345678"; got != want {
		t.Errorf("uuidForDevice(/dev/sdc1) = %q, want %q (upper-cased)", got, want)
	}
	if got := uuidForDevice("/dev/sdz9", links); got != "" {
		t.Errorf("uuidForDevice(no match) = %q, want \"\"", got)
	}
}

func TestDiskName(t *testing.T) {
	cases := map[string]string{
		"/dev/sdb1":      "sdb",
		"/dev/sda":       "sda",
		"/dev/mmcblk0p1": "mmcblk0",
		"/dev/nvme0n1p1": "nvme0n1",
		"/dev/nvme0n1":   "nvme0n1",
	}
	for device, want := range cases {
		if got := diskName(device); got != want {
			t.Errorf("diskName(%q) = %q, want %q", device, got, want)
		}
	}
}

func TestGetInfoNotMounted(t *testing.T) {
	withVolumeSeams(t)

	readMountinfoFn = func() (string, error) { return "", nil }
	listByUUIDFn = func() ([]uuidLink, error) { return nil, nil }

	if _, err := GetInfo("/media/u/MISSING"); err == nil {
		t.Fatal("GetInfo() error = nil, want error for a mount point that isn't mounted")
	}
}

func TestFindByUUIDNotMounted(t *testing.T) {
	withVolumeSeams(t)

	readMountinfoFn = func() (string, error) {
		return "60 25 8:17 / /media/u/CARD rw,relatime shared:34 - vfat /dev/sdb1 rw\n", nil
	}
	isSysRemovableFn = func(disk string) bool { return false }
	listByUUIDFn = func() ([]uuidLink, error) {
		return []uuidLink{{UUID: "ABCD-1234", Device: "/dev/sdb1"}}, nil
	}

	got, err := FindByUUID("0000-0000")
	if err != nil {
		t.Fatalf("FindByUUID() error = %v", err)
	}
	if got != nil {
		t.Fatalf("FindByUUID(not mounted) = %+v, want nil, nil (same as the other OSes)", got)
	}
}

func TestFindByUUIDCaseInsensitive(t *testing.T) {
	withVolumeSeams(t)

	readMountinfoFn = func() (string, error) {
		return "60 25 8:17 / /media/u/CARD rw,relatime shared:34 - vfat /dev/sdb1 rw\n", nil
	}
	isSysRemovableFn = func(disk string) bool { return false }
	listByUUIDFn = func() ([]uuidLink, error) {
		return []uuidLink{{UUID: "abcd-1234", Device: "/dev/sdb1"}}, nil
	}

	got, err := FindByUUID("AbCd-1234")
	if err != nil {
		t.Fatalf("FindByUUID() error = %v", err)
	}
	if got == nil || got.UUID != "ABCD-1234" {
		t.Fatalf("FindByUUID(case-insensitive) = %+v, want the CARD volume", got)
	}
}

func TestWatcherDiffSharedWithLinux(t *testing.T) {
	// diffVolumes/volumeKey/sortedKeys live in watcher_diff.go (no build
	// tag) and are already exercised by TestWatcherDiff in
	// volume_windows_test.go; this just confirms the Linux build sees the
	// same shared symbol rather than a per-OS duplicate.
	cardA := Info{Name: "A", MountPoint: "/media/u/A", UUID: "AAAA", Removable: true}
	cardB := Info{Name: "B", MountPoint: "/media/u/B", UUID: "BBBB", Removable: true}

	got := diffVolumes([]Info{cardA}, []Info{cardB})
	want := []Event{
		{Type: EventUnmounted, Info: cardA},
		{Type: EventMounted, Info: cardB},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("diffVolumes() = %+v, want %+v", got, want)
	}
}
