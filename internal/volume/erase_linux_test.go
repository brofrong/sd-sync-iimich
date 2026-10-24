//go:build linux

package volume

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	testLinuxMount  = "/media/user/EOS_DIGITAL"
	testLinuxDevice = "/dev/sdb1"
	testLinuxBlock  = dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb1")
	testLinuxDrive  = dbus.ObjectPath("/org/freedesktop/UDisks2/drives/Generic_SD_Reader_1234")
)

// withLinuxEraseSeams replaces every seam Erase touches and restores the
// originals afterwards: a test never reads this machine's mount table or
// talks to its UDisks2, let alone formats anything. It also shrinks the
// post-format polling so the loop runs in milliseconds.
func withLinuxEraseSeams(t *testing.T) {
	t.Helper()
	origMountinfo := readMountinfoFn
	origRemovable := isSysRemovableFn
	origConnect := connectUDisksEraseFn
	origInterval := erasePollInterval
	origTimeout := eraseRemountTimeout
	t.Cleanup(func() {
		readMountinfoFn = origMountinfo
		isSysRemovableFn = origRemovable
		connectUDisksEraseFn = origConnect
		erasePollInterval = origInterval
		eraseRemountTimeout = origTimeout
	})
	erasePollInterval = time.Millisecond
	eraseRemountTimeout = 5 * time.Millisecond
	readMountinfoFn = func() (string, error) {
		t.Fatal("readMountinfoFn reached without a fake")
		return "", nil
	}
	isSysRemovableFn = func(disk string) bool { return false }
	connectUDisksEraseFn = func() (udisksEraseConn, func(), error) {
		t.Fatal("connectUDisksEraseFn reached without a fake")
		return nil, func() {}, nil
	}
}

// fakeEraseConn is a fake UDisks2 for Erase: one partition, sdb1, holding
// card until format is called and then after (mounted on the first Mount).
type fakeEraseConn struct {
	t     *testing.T
	calls []string

	card        udisksBlockState
	after       udisksBlockState
	formatted   bool
	unmounted   bool
	mountedPath string

	unmountErr error
	formatErr  error
	mountErr   error
	mountPath  string
	// states, if set, answers blockState after the format in turn (the
	// last one repeats) instead of after.
	states []udisksBlockState
	looks  int
	// swapped, if set, is what the block reports once unmounted (and not
	// yet formatted): another card was put in, or UDisks2 changed its mind.
	swapped *udisksBlockState
}

func newFakeEraseConn(t *testing.T) *fakeEraseConn {
	t.Helper()
	withLinuxEraseSeams(t)
	f := &fakeEraseConn{
		t: t,
		card: udisksBlockState{
			Device:        testLinuxDevice,
			IdUUID:        "1234-ABCD",
			IdType:        "exfat",
			IdVersion:     "1.0",
			IdLabel:       "EOS_DIGITAL",
			HintSystem:    false,
			Drive:         testLinuxDrive,
			IsPartition:   true,
			HasFilesystem: true,
			MountPoints:   []string{testLinuxMount},
		},
		mountPath: testLinuxMount,
	}
	f.after = f.card
	f.after.IdUUID = "5A6B-7C8D"
	f.after.MountPoints = nil

	readMountinfoFn = func() (string, error) {
		return "25 1 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw\n" +
			"98 25 8:17 / /media/user/EOS_DIGITAL rw,nosuid,nodev,relatime shared:50 - exfat /dev/sdb1 rw\n", nil
	}
	connectUDisksEraseFn = func() (udisksEraseConn, func(), error) {
		return f, func() {}, nil
	}
	return f
}

func (f *fakeEraseConn) blockState(path dbus.ObjectPath) (udisksBlockState, error) {
	f.calls = append(f.calls, "blockState:"+string(path))
	if path != testLinuxBlock {
		return udisksBlockState{}, errors.New("no such object")
	}
	if !f.formatted {
		st := f.card
		if f.unmounted {
			if f.swapped != nil {
				st = *f.swapped
			}
			st.MountPoints = nil
			if f.mountedPath != "" {
				st.MountPoints = []string{f.mountedPath}
			}
		}
		return st, nil
	}
	st := f.after
	if len(f.states) > 0 {
		i := f.looks
		if i >= len(f.states) {
			i = len(f.states) - 1
		}
		f.looks++
		st = f.states[i]
	}
	if f.mountedPath != "" {
		st.MountPoints = []string{f.mountedPath}
	}
	return st, nil
}

func (f *fakeEraseConn) unmount(path dbus.ObjectPath) error {
	f.calls = append(f.calls, "unmount:"+string(path))
	if f.unmountErr == nil {
		f.unmounted = true
	}
	return f.unmountErr
}

func (f *fakeEraseConn) format(path dbus.ObjectPath, fsType, label string) error {
	f.calls = append(f.calls, "format:"+string(path)+":"+fsType+":"+label)
	if f.formatErr == nil {
		f.formatted = true
	}
	return f.formatErr
}

func (f *fakeEraseConn) mount(path dbus.ObjectPath) (string, error) {
	f.calls = append(f.calls, "mount:"+string(path))
	if f.mountErr != nil {
		return "", f.mountErr
	}
	f.mountedPath = f.mountPath
	return f.mountPath, nil
}

func (f *fakeEraseConn) formatCalls() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "format:") {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeEraseConn) assertUntouched() {
	f.t.Helper()
	for _, c := range f.calls {
		if !strings.HasPrefix(c, "blockState:") {
			f.t.Fatalf("calls = %v, want only reads: nothing unmounted or formatted", f.calls)
		}
	}
}

func TestLinuxEraseKeepsLabelAndFormat(t *testing.T) {
	for _, tt := range []struct {
		idType, idVersion, wantType string
	}{
		{"exfat", "1.0", "exfat"},
		{"vfat", "FAT32", "vfat"},
	} {
		t.Run(tt.idType+"/"+tt.idVersion, func(t *testing.T) {
			f := newFakeEraseConn(t)
			f.card.IdType, f.card.IdVersion = tt.idType, tt.idVersion
			f.after.IdType, f.after.IdVersion = tt.idType, tt.idVersion

			got, err := Erase(testLinuxMount, "1234-abcd")
			if err != nil {
				t.Fatalf("Erase() error = %v", err)
			}
			want := []string{
				"blockState:" + string(testLinuxBlock),
				"unmount:" + string(testLinuxBlock),
				"blockState:" + string(testLinuxBlock), // re-checked once unmounted
				"format:" + string(testLinuxBlock) + ":" + tt.wantType + ":EOS_DIGITAL",
				"blockState:" + string(testLinuxBlock),
				"mount:" + string(testLinuxBlock),
			}
			if !reflect.DeepEqual(f.calls, want) {
				t.Fatalf("calls = %v\nwant %v", f.calls, want)
			}
			wantInfo := Info{Name: "EOS_DIGITAL", MountPoint: testLinuxMount, UUID: "5A6B-7C8D", Removable: true}
			if got != wantInfo {
				t.Fatalf("Erase() = %+v, want %+v", got, wantInfo)
			}
		})
	}
}

func TestLinuxEraseRefusesOtherUUID(t *testing.T) {
	f := newFakeEraseConn(t)

	_, err := Erase(testLinuxMount, "1234-ABCE")
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("Erase() error = %v, want an identity mismatch", err)
	}
	f.assertUntouched()
}

func TestLinuxEraseRefusesEmptyUUIDs(t *testing.T) {
	f := newFakeEraseConn(t)
	if _, err := Erase(testLinuxMount, ""); err == nil {
		t.Fatal("Erase() with an empty wantUUID succeeded, want a refusal")
	}
	f.assertUntouched()

	f = newFakeEraseConn(t)
	f.card.IdUUID = ""
	if _, err := Erase(testLinuxMount, "1234-ABCD"); err == nil {
		t.Fatal("Erase() of a filesystem without a UUID succeeded, want a refusal")
	}
	f.assertUntouched()
}

func TestLinuxEraseRefusesHintSystem(t *testing.T) {
	f := newFakeEraseConn(t)
	f.card.HintSystem = true

	_, err := Erase(testLinuxMount, "1234-ABCD")
	if err == nil || !strings.Contains(err.Error(), "system") {
		t.Fatalf("Erase() error = %v, want a system device refusal", err)
	}
	f.assertUntouched()
}

func TestLinuxEraseRefusesRootFilesystem(t *testing.T) {
	f := newFakeEraseConn(t)
	f.card.MountPoints = []string{"/"}

	if _, err := Erase("/", "1234-ABCD"); err == nil {
		t.Fatal("Erase(/) succeeded, want a refusal")
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls = %v, want none for /", f.calls)
	}
}

func TestLinuxEraseRefusesNonRemovable(t *testing.T) {
	f := newFakeEraseConn(t)
	readMountinfoFn = func() (string, error) {
		return "98 25 8:17 / /mnt/data rw,relatime shared:50 - exfat /dev/sdb1 rw\n", nil
	}
	f.card.MountPoints = []string{"/mnt/data"}

	if _, err := Erase("/mnt/data", "1234-ABCD"); err == nil {
		t.Fatal("Erase() of a non-removable mount succeeded, want a refusal")
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls = %v, want UDisks2 never reached", f.calls)
	}
}

func TestLinuxEraseRefusesWholeDisk(t *testing.T) {
	f := newFakeEraseConn(t)
	f.card.IsPartition = false

	if _, err := Erase(testLinuxMount, "1234-ABCD"); err == nil {
		t.Fatal("Erase() of a whole disk succeeded, want a refusal")
	}
	f.assertUntouched()
}

func TestLinuxEraseRefusesInconsistentBlock(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(st *udisksBlockState)
	}{
		{"other device", func(st *udisksBlockState) { st.Device = "/dev/sdc1" }},
		{"no drive", func(st *udisksBlockState) { st.Drive = "" }},
		{"root drive path", func(st *udisksBlockState) { st.Drive = "/" }},
		{"not mounted there", func(st *udisksBlockState) { st.MountPoints = []string{"/media/user/OTHER"} }},
		{"no filesystem", func(st *udisksBlockState) { st.HasFilesystem = false }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeEraseConn(t)
			tt.setup(&f.card)

			if _, err := Erase(testLinuxMount, "1234-ABCD"); err == nil {
				t.Fatal("Erase() succeeded, want a refusal")
			}
			f.assertUntouched()
		})
	}
}

func TestLinuxEraseRefusesInvalidLabel(t *testing.T) {
	for _, label := range []string{"", "A/B", ".."} {
		f := newFakeEraseConn(t)
		f.card.IdLabel = label

		if _, err := Erase(testLinuxMount, "1234-ABCD"); err == nil {
			t.Fatalf("Erase() of a filesystem labelled %q succeeded, want a refusal", label)
		}
		f.assertUntouched()
	}
}

func TestLinuxEraseRefusesOtherFilesystems(t *testing.T) {
	for _, tt := range []struct{ idType, idVersion string }{
		{"ext4", "1.0"}, {"ntfs", ""}, {"vfat", "FAT12"}, {"vfat", "FAT16"}, {"vfat", ""}, {"btrfs", ""}, {"", ""}, {"crypto_LUKS", "2"},
	} {
		f := newFakeEraseConn(t)
		f.card.IdType, f.card.IdVersion = tt.idType, tt.idVersion

		if _, err := Erase(testLinuxMount, "1234-ABCD"); err == nil {
			t.Fatalf("Erase() of %s/%s succeeded, want a refusal", tt.idType, tt.idVersion)
		}
		f.assertUntouched()
	}
}

func TestLinuxEraseNotAuthorizedIsNeedsAdmin(t *testing.T) {
	for _, name := range []string{
		"org.freedesktop.UDisks2.Error.NotAuthorized",
		"org.freedesktop.UDisks2.Error.NotAuthorizedCanObtain",
		"org.freedesktop.UDisks2.Error.NotAuthorizedDismissed",
		"org.freedesktop.PolicyKit1.Error.NotAuthorized",
		"org.freedesktop.DBus.Error.AccessDenied",
	} {
		t.Run(name, func(t *testing.T) {
			// As godbus delivers a real reply: a value dbus.Error.
			f := newFakeEraseConn(t)
			f.formatErr = dbus.Error{Name: name, Body: []any{"Not authorized to perform operation"}}

			_, err := Erase(testLinuxMount, "1234-ABCD")
			if !errors.Is(err, ErrNeedsAdmin) {
				t.Fatalf("Erase() error = %v, want ErrNeedsAdmin", err)
			}
			if errors.Is(err, ErrErasedNotRemounted) {
				t.Fatalf("Erase() error = %v, want the card reported as not erased", err)
			}
			// The card was unmounted for the format: it is mounted again.
			if last := f.calls[len(f.calls)-1]; last != "mount:"+string(testLinuxBlock) {
				t.Fatalf("calls = %v, want the card mounted again", f.calls)
			}
		})
	}

	t.Run("unmount refused, pointer form", func(t *testing.T) {
		f := newFakeEraseConn(t)
		f.unmountErr = dbus.NewError("org.freedesktop.UDisks2.Error.NotAuthorizedCanObtain", nil)

		_, err := Erase(testLinuxMount, "1234-ABCD")
		if !errors.Is(err, ErrNeedsAdmin) {
			t.Fatalf("Erase() error = %v, want ErrNeedsAdmin", err)
		}
		if len(f.formatCalls()) != 0 {
			t.Fatalf("calls = %v, want no format", f.calls)
		}
	})
}

func TestLinuxEraseBusyCard(t *testing.T) {
	f := newFakeEraseConn(t)
	f.unmountErr = dbus.Error{Name: udisksBusyErrorName, Body: []any{"target is busy"}}

	_, err := Erase(testLinuxMount, "1234-ABCD")
	if err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("Erase() error = %v, want the card reported in use", err)
	}
	if errors.Is(err, ErrNeedsAdmin) || len(f.formatCalls()) != 0 {
		t.Fatalf("Erase() error = %v, calls %v; want a plain failure and no format", err, f.calls)
	}
}

func TestLinuxEraseFormatFailure(t *testing.T) {
	f := newFakeEraseConn(t)
	f.formatErr = dbus.Error{Name: "org.freedesktop.UDisks2.Error.Failed", Body: []any{"Error creating filesystem"}}

	_, err := Erase(testLinuxMount, "1234-ABCD")
	if err == nil || errors.Is(err, ErrNeedsAdmin) || errors.Is(err, ErrErasedNotRemounted) {
		t.Fatalf("Erase() error = %v, want a plain failure", err)
	}
}

func TestLinuxEraseFindsNewVolumeByDevice(t *testing.T) {
	f := newFakeEraseConn(t)
	eraseRemountTimeout = time.Second
	stale := f.card
	stale.MountPoints = nil
	pending := f.after
	pending.IdUUID = ""
	pending.HasFilesystem = false
	moved := f.after
	moved.Device = "/dev/sdc1" // never trusted: another device's state
	f.states = []udisksBlockState{stale, pending, moved, f.after}
	f.mountPath = "/media/user/EOS_DIGITAL1"

	got, err := Erase(testLinuxMount, "1234-ABCD")
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	want := Info{Name: "EOS_DIGITAL", MountPoint: "/media/user/EOS_DIGITAL1", UUID: "5A6B-7C8D", Removable: true}
	if got != want {
		t.Fatalf("Erase() = %+v, want %+v", got, want)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "blockState:") && c != "blockState:"+string(testLinuxBlock) {
			t.Fatalf("calls = %v, want only the formatted block looked at", f.calls)
		}
	}
}

func TestLinuxEraseUsesAutoMount(t *testing.T) {
	f := newFakeEraseConn(t)
	// The desktop mounted the new filesystem itself.
	f.after.MountPoints = []string{"/media/user/EOS_DIGITAL"}

	got, err := Erase(testLinuxMount, "1234-ABCD")
	if err != nil {
		t.Fatalf("Erase() error = %v", err)
	}
	if got.MountPoint != "/media/user/EOS_DIGITAL" {
		t.Fatalf("Erase() = %+v", got)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "mount:") {
			t.Fatalf("calls = %v, want no Mount for an already mounted filesystem", f.calls)
		}
	}
}

func TestLinuxEraseNotRemounted(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(f *fakeEraseConn)
	}{
		{"same uuid", func(f *fakeEraseConn) { f.after.IdUUID = f.card.IdUUID }},
		{"mount refused", func(f *fakeEraseConn) {
			f.mountErr = dbus.Error{Name: "org.freedesktop.UDisks2.Error.NotAuthorized"}
		}},
		{"other device", func(f *fakeEraseConn) { f.after.Device = "/dev/sdc1" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeEraseConn(t)
			tt.setup(f)

			_, err := Erase(testLinuxMount, "1234-ABCD")
			if !errors.Is(err, ErrErasedNotRemounted) {
				t.Fatalf("Erase() error = %v, want ErrErasedNotRemounted", err)
			}
			if errors.Is(err, ErrNeedsAdmin) {
				t.Fatalf("Erase() error = %v: the card was erased, not refused", err)
			}
		})
	}
}

func TestIsNotAuthorizedErr(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want bool
	}{
		{dbus.Error{Name: "org.freedesktop.UDisks2.Error.NotAuthorized"}, true},
		{dbus.NewError("org.freedesktop.UDisks2.Error.NotAuthorizedCanObtain", nil), true},
		{dbus.Error{Name: "org.freedesktop.DBus.Error.InteractiveAuthorizationRequired"}, true},
		{dbus.Error{Name: udisksBusyErrorName}, false},
		{dbus.Error{Name: "org.freedesktop.UDisks2.Error.Failed"}, false},
		{errors.New("NotAuthorized"), false},
		{nil, false},
	} {
		if got := isNotAuthorizedErr(tt.err); got != tt.want {
			t.Errorf("isNotAuthorizedErr(%v) = %v, want %v", tt.err, got, tt.want)
		}
	}
}

func TestFormatOptions(t *testing.T) {
	opts := formatOptions("EOS_DIGITAL")
	if v, ok := opts["label"].Value().(string); !ok || v != "EOS_DIGITAL" {
		t.Errorf("label = %v, want EOS_DIGITAL", opts["label"])
	}
	if v, ok := opts["auth.no_user_interaction"].Value().(bool); !ok || !v {
		t.Errorf("auth.no_user_interaction = %v, want true", opts["auth.no_user_interaction"])
	}
	if v, ok := opts["update-partition-type"].Value().(bool); !ok || v {
		t.Errorf("update-partition-type = %v, want false", opts["update-partition-type"])
	}
	for k := range opts {
		switch k {
		case "label", "auth.no_user_interaction", "update-partition-type":
		default:
			t.Errorf("unexpected format option %q", k)
		}
	}
}

func TestBlockStateFromProps(t *testing.T) {
	block := map[string]dbus.Variant{
		"Device":     dbus.MakeVariant([]byte("/dev/sdb1\x00")),
		"IdUUID":     dbus.MakeVariant("1234-ABCD"),
		"IdType":     dbus.MakeVariant("exfat"),
		"IdVersion":  dbus.MakeVariant("1.0"),
		"IdLabel":    dbus.MakeVariant("EOS_DIGITAL"),
		"HintSystem": dbus.MakeVariant(false),
		"Drive":      dbus.MakeVariant(testLinuxDrive),
	}
	part := map[string]dbus.Variant{
		"Number": dbus.MakeVariant(uint32(1)),
		"Table":  dbus.MakeVariant(dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/sdb")),
	}
	fs := map[string]dbus.Variant{
		"MountPoints": dbus.MakeVariant([][]byte{[]byte("/media/user/EOS_DIGITAL\x00")}),
	}
	got := blockStateFromProps(block, part, fs)
	want := udisksBlockState{
		Device: "/dev/sdb1", IdUUID: "1234-ABCD", IdType: "exfat", IdVersion: "1.0", IdLabel: "EOS_DIGITAL",
		HintSystem: false, Drive: testLinuxDrive, IsPartition: true, HasFilesystem: true,
		MountPoints: []string{"/media/user/EOS_DIGITAL"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blockStateFromProps() = %+v\nwant %+v", got, want)
	}

	// Anything missing or of the wrong type fails closed: a system device,
	// not a partition, no filesystem.
	empty := blockStateFromProps(map[string]dbus.Variant{"HintSystem": dbus.MakeVariant("no")}, map[string]dbus.Variant{}, nil)
	if !empty.HintSystem || empty.IsPartition || empty.HasFilesystem {
		t.Fatalf("blockStateFromProps(empty) = %+v, want HintSystem, no partition, no filesystem", empty)
	}
}

// TestLinuxEraseReverifiesAfterUnmount: the block is read again once
// unmounted, right before Format. If it is no longer the verified card on
// the same partition and drive, nothing is formatted and it is mounted
// back.
func TestLinuxEraseReverifiesAfterUnmount(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(st *udisksBlockState)
	}{
		{"other uuid", func(st *udisksBlockState) { st.IdUUID = "9999-0000" }},
		{"no uuid", func(st *udisksBlockState) { st.IdUUID = "" }},
		{"other device", func(st *udisksBlockState) { st.Device = "/dev/sdc1" }},
		{"system device", func(st *udisksBlockState) { st.HintSystem = true }},
		{"whole disk", func(st *udisksBlockState) { st.IsPartition = false }},
		{"other drive", func(st *udisksBlockState) { st.Drive = "/org/freedesktop/UDisks2/drives/Other" }},
		{"other filesystem", func(st *udisksBlockState) { st.IdType = "ext4" }},
		{"other label", func(st *udisksBlockState) { st.IdLabel = "OTHER" }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeEraseConn(t)
			swapped := f.card
			tt.setup(&swapped)
			f.swapped = &swapped

			_, err := Erase(testLinuxMount, "1234-ABCD")
			if err == nil || !strings.Contains(err.Error(), "changed") {
				t.Fatalf("Erase() error = %v, want a refusal: the card changed", err)
			}
			if errors.Is(err, ErrNeedsAdmin) || errors.Is(err, ErrErasedNotRemounted) {
				t.Fatalf("Erase() error = %v, want a plain refusal (not erased)", err)
			}
			if got := f.formatCalls(); len(got) != 0 {
				t.Fatalf("calls = %v, want no format", f.calls)
			}
			if last := f.calls[len(f.calls)-1]; last != "mount:"+string(testLinuxBlock) {
				t.Fatalf("calls = %v, want the card mounted back", f.calls)
			}
		})
	}
}

func (f *fakeEraseConn) mountCalls() int {
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, "mount:") {
			n++
		}
	}
	return n
}

// TestLinuxEraseMountRefusedNotRepeated: after the format, a Mount that is
// not authorized is not asked again on every poll (the state is still
// re-read, in case the desktop mounts it); other Mount failures are retried
// with a growing gap.
func TestLinuxEraseMountRefusedNotRepeated(t *testing.T) {
	t.Run("not authorized", func(t *testing.T) {
		f := newFakeEraseConn(t)
		eraseRemountTimeout = 20 * time.Millisecond // 20 polls
		f.mountErr = dbus.Error{Name: "org.freedesktop.UDisks2.Error.NotAuthorizedCanObtain"}

		_, err := Erase(testLinuxMount, "1234-ABCD")
		if !errors.Is(err, ErrErasedNotRemounted) {
			t.Fatalf("Erase() error = %v, want ErrErasedNotRemounted", err)
		}
		if n := f.mountCalls(); n != 1 {
			t.Fatalf("Mount called %d times, want once", n)
		}
	})
	t.Run("other failure", func(t *testing.T) {
		f := newFakeEraseConn(t)
		eraseRemountTimeout = 20 * time.Millisecond // 20 polls
		f.mountErr = dbus.Error{Name: "org.freedesktop.UDisks2.Error.Failed"}

		if _, err := Erase(testLinuxMount, "1234-ABCD"); !errors.Is(err, ErrErasedNotRemounted) {
			t.Fatalf("Erase() error = %v, want ErrErasedNotRemounted", err)
		}
		// Polls 0, 1, 3, 7 and 15.
		if n := f.mountCalls(); n != 5 {
			t.Fatalf("Mount called %d times in 20 polls, want 5 (backing off)", n)
		}
	})
	t.Run("desktop mounts it later", func(t *testing.T) {
		f := newFakeEraseConn(t)
		eraseRemountTimeout = time.Second
		f.mountErr = dbus.Error{Name: "org.freedesktop.UDisks2.Error.NotAuthorized"}
		mounted := f.after
		mounted.MountPoints = []string{"/media/user/EOS_DIGITAL"}
		f.states = []udisksBlockState{f.after, f.after, f.after, mounted}

		got, err := Erase(testLinuxMount, "1234-ABCD")
		if err != nil {
			t.Fatalf("Erase() error = %v", err)
		}
		if got.MountPoint != "/media/user/EOS_DIGITAL" || f.mountCalls() != 1 {
			t.Fatalf("Erase() = %+v with %d Mount calls, want the desktop's mount after one refused Mount", got, f.mountCalls())
		}
	})
}
