//go:build linux

package volume

import (
	"strings"
	"testing"

	"github.com/godbus/dbus/v5"
)

// fakeUDisksConn is a fake udisksConn for exercising ejectDevice's
// unmount/power-off/eject decision logic without a real system bus or
// UDisks2 daemon.
type fakeUDisksConn struct {
	calls []string

	// block is what blockState reports; newFakeUDisksConn fills in a
	// removable card's block on /dev/sdb1.
	block          udisksBlockState
	blockStateErr  error
	unmountErr     error
	canPowerOffVal bool
	canPowerOffErr error
	powerOffErr    error
	ejectErr       error
}

// fakeCardDrive is the drive of the fake card reader.
const fakeCardDrive dbus.ObjectPath = "/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader"

// newFakeUDisksConn returns a fake bus whose /dev/sdb1 is a removable
// card's partition on fakeCardDrive.
func newFakeUDisksConn() *fakeUDisksConn {
	return &fakeUDisksConn{block: udisksBlockState{
		Device: "/dev/sdb1",
		Drive:  fakeCardDrive,
	}}
}

func (f *fakeUDisksConn) blockState(path dbus.ObjectPath) (udisksBlockState, error) {
	f.calls = append(f.calls, "blockState:"+string(path))
	return f.block, f.blockStateErr
}

func (f *fakeUDisksConn) unmount(path dbus.ObjectPath) error {
	f.calls = append(f.calls, "unmount:"+string(path))
	return f.unmountErr
}

func (f *fakeUDisksConn) canPowerOff(drive dbus.ObjectPath) (bool, error) {
	f.calls = append(f.calls, "canPowerOff:"+string(drive))
	return f.canPowerOffVal, f.canPowerOffErr
}

func (f *fakeUDisksConn) powerOff(drive dbus.ObjectPath) error {
	f.calls = append(f.calls, "powerOff:"+string(drive))
	return f.powerOffErr
}

func (f *fakeUDisksConn) eject(drive dbus.ObjectPath) error {
	f.calls = append(f.calls, "eject:"+string(drive))
	return f.ejectErr
}

const (
	fakeBlockPath = "/org/freedesktop/UDisks2/block_devices/sdb1"
	fakeDrivePath = string(fakeCardDrive)
)

func TestEjectDevicePowersOffWhenSupported(t *testing.T) {
	f := newFakeUDisksConn()
	f.canPowerOffVal = true

	if err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1"); err != nil {
		t.Fatalf("ejectDevice() error = %v", err)
	}

	want := []string{
		"blockState:" + fakeBlockPath,
		"unmount:" + fakeBlockPath,
		"canPowerOff:" + fakeDrivePath,
		"powerOff:" + fakeDrivePath,
	}
	assertCalls(t, f.calls, want)
}

func TestEjectDeviceFallsBackToEjectWhenPowerOffUnsupported(t *testing.T) {
	f := newFakeUDisksConn()

	if err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1"); err != nil {
		t.Fatalf("ejectDevice() error = %v", err)
	}

	want := []string{
		"blockState:" + fakeBlockPath,
		"unmount:" + fakeBlockPath,
		"canPowerOff:" + fakeDrivePath,
		"eject:" + fakeDrivePath,
	}
	assertCalls(t, f.calls, want)
}

func TestEjectDeviceFallsBackToEjectWhenPowerOffCallFails(t *testing.T) {
	f := newFakeUDisksConn()
	f.canPowerOffVal = true
	f.powerOffErr = dbus.NewError("org.freedesktop.UDisks2.Error.NotSupported", nil)

	if err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1"); err != nil {
		t.Fatalf("ejectDevice() error = %v", err)
	}

	want := []string{
		"blockState:" + fakeBlockPath,
		"unmount:" + fakeBlockPath,
		"canPowerOff:" + fakeDrivePath,
		"powerOff:" + fakeDrivePath,
		"eject:" + fakeDrivePath,
	}
	assertCalls(t, f.calls, want)
}

func TestEjectDeviceBusyFilesystemMentionsCardInUse(t *testing.T) {
	f := newFakeUDisksConn()
	f.unmountErr = dbus.NewError(udisksBusyErrorName, nil)

	err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1")
	if err == nil {
		t.Fatal("ejectDevice() error = nil, want error for a busy filesystem")
	}
	if !strings.Contains(err.Error(), "in use") {
		t.Fatalf("ejectDevice() error = %q, want it to mention the card being in use", err.Error())
	}
	// Busy at unmount must not proceed to powerOff/eject.
	want := []string{"blockState:" + fakeBlockPath, "unmount:" + fakeBlockPath}
	assertCalls(t, f.calls, want)
}

// TestEjectDeviceBusyFilesystemValueFormMentionsCardInUse is the
// regression test for the real-dispatch bug: godbus's actual error
// dispatch (conn.go's handleDBusError -> finalizeWithError) constructs a
// VALUE dbus.Error, not a pointer — dbus.NewError (used by every other
// test in this file, matching how a caller might construct one locally)
// returns *dbus.Error, which is a different concrete type. This test
// builds the error exactly the way a real UDisks2 "DeviceBusy" reply
// would arrive on Call.Err, to prove isDeviceBusyErr (and so ejectDevice's
// "card is in use" message) actually fires against it.
func TestEjectDeviceBusyFilesystemValueFormMentionsCardInUse(t *testing.T) {
	f := newFakeUDisksConn()
	f.unmountErr = dbus.Error{Name: udisksBusyErrorName, Body: []any{"target is busy"}}

	err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1")
	if err == nil {
		t.Fatal("ejectDevice() error = nil, want error for a busy filesystem")
	}
	if !strings.Contains(err.Error(), "in use") {
		t.Fatalf("ejectDevice() error = %q, want it to mention the card being in use (value dbus.Error, as real godbus dispatch produces it)", err.Error())
	}
}

// TestEjectDeviceCanPowerOffErrorFallsThroughToEject checks that a failed
// CanPowerOff property read (e.g. the drive doesn't expose it, or UDisks2
// is slow to respond) doesn't abort the eject or leak out of ejectDevice:
// it's logged and treated the same as CanPowerOff being false, falling
// through to a plain Drive.Eject.
func TestEjectDeviceCanPowerOffErrorFallsThroughToEject(t *testing.T) {
	f := newFakeUDisksConn()
	f.canPowerOffErr = dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil)

	if err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1"); err != nil {
		t.Fatalf("ejectDevice() error = %v, want the CanPowerOff failure to be non-fatal", err)
	}

	want := []string{
		"blockState:" + fakeBlockPath,
		"unmount:" + fakeBlockPath,
		"canPowerOff:" + fakeDrivePath,
		"eject:" + fakeDrivePath,
	}
	assertCalls(t, f.calls, want)
}

func TestEjectDeviceOtherUnmountFailureIsReported(t *testing.T) {
	f := newFakeUDisksConn()
	f.unmountErr = dbus.NewError("org.freedesktop.UDisks2.Error.NotAuthorized", nil)

	err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1")
	if err == nil {
		t.Fatal("ejectDevice() error = nil, want error")
	}
	if strings.Contains(err.Error(), "in use") {
		t.Fatalf("ejectDevice() error = %q, should not claim the card is in use for an unrelated failure", err.Error())
	}
}

// TestEjectDeviceRefusesWithoutUnmounting: a block that is not a removable
// card's (a system device, also when UDisks2 does not say; another device
// node; no drive) or that can't be read is refused before anything is
// unmounted, powered off or ejected.
func TestEjectDeviceRefusesWithoutUnmounting(t *testing.T) {
	tests := []struct {
		name  string
		setup func(f *fakeUDisksConn)
		want  string
	}{
		{"system device", func(f *fakeUDisksConn) { f.block.HintSystem = true }, "system device"},
		{"hint missing", func(f *fakeUDisksConn) {
			// What blockStateFromProps makes of a block without HintSystem.
			f.block = blockStateFromProps(map[string]dbus.Variant{
				"Device": dbus.MakeVariant([]byte("/dev/sdb1\x00")),
				"Drive":  dbus.MakeVariant(fakeCardDrive),
			}, nil, nil)
		}, "system device"},
		{"other device", func(f *fakeUDisksConn) { f.block.Device = "/dev/sda1" }, "/dev/sda1"},
		{"no drive", func(f *fakeUDisksConn) { f.block.Drive = "" }, "without a drive"},
		{"root drive path", func(f *fakeUDisksConn) { f.block.Drive = "/" }, "without a drive"},
		{"block unreadable", func(f *fakeUDisksConn) {
			f.blockStateErr = dbus.NewError("org.freedesktop.DBus.Error.UnknownObject", nil)
		}, "read UDisks2 block"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeUDisksConn()
			f.canPowerOffVal = true
			tt.setup(f)

			err := ejectDevice(f, "/media/u/Data", "/dev/sdb1")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ejectDevice() error = %v, want a refusal mentioning %q", err, tt.want)
			}
			assertCalls(t, f.calls, []string{"blockState:" + fakeBlockPath})
		})
	}
}

func TestBlockObjectPathEscapesSpecialCharacters(t *testing.T) {
	got := blockObjectPath("/dev/weird name")
	want := dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/weird_20name")
	if got != want {
		t.Fatalf("blockObjectPath() = %q, want %q", got, want)
	}
}

func TestIsDeviceBusyErr(t *testing.T) {
	// The pointer form, as dbus.NewError (and this package's own tests
	// that construct an error locally) returns it.
	ptrBusy := dbus.NewError(udisksBusyErrorName, nil)
	if !isDeviceBusyErr(ptrBusy) {
		t.Error("isDeviceBusyErr(pointer DeviceBusy) = false, want true")
	}

	// The value form, exactly as godbus's real dispatch path (conn.go's
	// handleDBusError -> finalizeWithError(..., Error{name, msg.Body}))
	// constructs it for an error received off the wire — see
	// TestEjectDeviceBusyFilesystemValueFormMentionsCardInUse for why this
	// distinction matters.
	valBusy := dbus.Error{Name: udisksBusyErrorName, Body: []any{"target is busy"}}
	if !isDeviceBusyErr(valBusy) {
		t.Error("isDeviceBusyErr(value DeviceBusy) = false, want true")
	}

	other := dbus.NewError("org.freedesktop.UDisks2.Error.NotAuthorized", nil)
	if isDeviceBusyErr(other) {
		t.Error("isDeviceBusyErr(NotAuthorized) = true, want false")
	}
	otherVal := dbus.Error{Name: "org.freedesktop.UDisks2.Error.NotAuthorized"}
	if isDeviceBusyErr(otherVal) {
		t.Error("isDeviceBusyErr(value NotAuthorized) = true, want false")
	}

	if isDeviceBusyErr(nil) {
		t.Error("isDeviceBusyErr(nil) = true, want false")
	}
}

func assertCalls(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("calls = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("calls = %v, want %v", got, want)
		}
	}
}
