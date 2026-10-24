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

	unmountErr     error
	drive          dbus.ObjectPath
	drivePathErr   error
	canPowerOffVal bool
	canPowerOffErr error
	powerOffErr    error
	ejectErr       error
}

func (f *fakeUDisksConn) unmount(path dbus.ObjectPath) error {
	f.calls = append(f.calls, "unmount:"+string(path))
	return f.unmountErr
}

func (f *fakeUDisksConn) drivePath(path dbus.ObjectPath) (dbus.ObjectPath, error) {
	f.calls = append(f.calls, "drivePath:"+string(path))
	return f.drive, f.drivePathErr
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

func TestEjectDevicePowersOffWhenSupported(t *testing.T) {
	f := &fakeUDisksConn{
		drive:          "/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader",
		canPowerOffVal: true,
	}

	if err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1"); err != nil {
		t.Fatalf("ejectDevice() error = %v", err)
	}

	want := []string{
		"unmount:/org/freedesktop/UDisks2/block_devices/sdb1",
		"drivePath:/org/freedesktop/UDisks2/block_devices/sdb1",
		"canPowerOff:/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader",
		"powerOff:/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader",
	}
	assertCalls(t, f.calls, want)
}

func TestEjectDeviceFallsBackToEjectWhenPowerOffUnsupported(t *testing.T) {
	f := &fakeUDisksConn{
		drive:          "/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader",
		canPowerOffVal: false,
	}

	if err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1"); err != nil {
		t.Fatalf("ejectDevice() error = %v", err)
	}

	want := []string{
		"unmount:/org/freedesktop/UDisks2/block_devices/sdb1",
		"drivePath:/org/freedesktop/UDisks2/block_devices/sdb1",
		"canPowerOff:/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader",
		"eject:/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader",
	}
	assertCalls(t, f.calls, want)
}

func TestEjectDeviceFallsBackToEjectWhenPowerOffCallFails(t *testing.T) {
	f := &fakeUDisksConn{
		drive:          "/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader",
		canPowerOffVal: true,
		powerOffErr:    dbus.NewError("org.freedesktop.UDisks2.Error.NotSupported", nil),
	}

	if err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1"); err != nil {
		t.Fatalf("ejectDevice() error = %v", err)
	}

	want := []string{
		"unmount:/org/freedesktop/UDisks2/block_devices/sdb1",
		"drivePath:/org/freedesktop/UDisks2/block_devices/sdb1",
		"canPowerOff:/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader",
		"powerOff:/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader",
		"eject:/org/freedesktop/UDisks2/drives/Generic_SD_Card_Reader",
	}
	assertCalls(t, f.calls, want)
}

func TestEjectDeviceBusyFilesystemMentionsCardInUse(t *testing.T) {
	f := &fakeUDisksConn{
		unmountErr: dbus.NewError(udisksBusyErrorName, nil),
	}

	err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1")
	if err == nil {
		t.Fatal("ejectDevice() error = nil, want error for a busy filesystem")
	}
	if !strings.Contains(err.Error(), "in use") {
		t.Fatalf("ejectDevice() error = %q, want it to mention the card being in use", err.Error())
	}
	// Busy at unmount must not proceed to drivePath/powerOff/eject.
	want := []string{"unmount:/org/freedesktop/UDisks2/block_devices/sdb1"}
	assertCalls(t, f.calls, want)
}

func TestEjectDeviceOtherUnmountFailureIsReported(t *testing.T) {
	f := &fakeUDisksConn{
		unmountErr: dbus.NewError("org.freedesktop.UDisks2.Error.NotAuthorized", nil),
	}

	err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1")
	if err == nil {
		t.Fatal("ejectDevice() error = nil, want error")
	}
	if strings.Contains(err.Error(), "in use") {
		t.Fatalf("ejectDevice() error = %q, should not claim the card is in use for an unrelated failure", err.Error())
	}
}

func TestEjectDeviceDrivePathFailureIsReported(t *testing.T) {
	f := &fakeUDisksConn{
		drivePathErr: dbus.NewError("org.freedesktop.DBus.Error.UnknownProperty", nil),
	}

	err := ejectDevice(f, "/media/u/CARD", "/dev/sdb1")
	if err == nil {
		t.Fatal("ejectDevice() error = nil, want error when the drive can't be resolved")
	}
	// Must not attempt canPowerOff/powerOff/eject without a resolved drive.
	want := []string{
		"unmount:/org/freedesktop/UDisks2/block_devices/sdb1",
		"drivePath:/org/freedesktop/UDisks2/block_devices/sdb1",
	}
	assertCalls(t, f.calls, want)
}

func TestBlockObjectPathEscapesSpecialCharacters(t *testing.T) {
	got := blockObjectPath("/dev/weird name")
	want := dbus.ObjectPath("/org/freedesktop/UDisks2/block_devices/weird_20name")
	if got != want {
		t.Fatalf("blockObjectPath() = %q, want %q", got, want)
	}
}

func TestIsDeviceBusyErr(t *testing.T) {
	busy := dbus.NewError(udisksBusyErrorName, nil)
	if !isDeviceBusyErr(busy) {
		t.Error("isDeviceBusyErr(DeviceBusy) = false, want true")
	}

	other := dbus.NewError("org.freedesktop.UDisks2.Error.NotAuthorized", nil)
	if isDeviceBusyErr(other) {
		t.Error("isDeviceBusyErr(NotAuthorized) = true, want false")
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
