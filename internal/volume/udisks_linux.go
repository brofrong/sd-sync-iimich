//go:build linux

package volume

import (
	"fmt"

	"github.com/godbus/dbus/v5"
)

// UDisks2 bus name and interfaces used by Eject (eject_linux.go).
const (
	udisksBusName         = "org.freedesktop.UDisks2"
	udisksBlockIface      = "org.freedesktop.UDisks2.Block"
	udisksFilesystemIface = "org.freedesktop.UDisks2.Filesystem"
	udisksDriveIface      = "org.freedesktop.UDisks2.Drive"

	// udisksBlockDevicesPath is where UDisks2 exposes one object per block
	// device, named after the device's basename (e.g. "sdb1").
	udisksBlockDevicesPath = "/org/freedesktop/UDisks2/block_devices/"

	// udisksBusyErrorName is the D-Bus error UDisks2's Filesystem.Unmount
	// returns when something still has the filesystem open.
	udisksBusyErrorName = "org.freedesktop.UDisks2.Error.DeviceBusy"
)

// noInteractionOptions is the options argument UDisks2 methods take, set so
// they never fall back to a polkit authentication prompt: the app never
// asks for administrator/root rights for a normal eject.
func noInteractionOptions() map[string]dbus.Variant {
	return map[string]dbus.Variant{"auth.no_user_interaction": dbus.MakeVariant(true)}
}

// connectUDisksFn opens the real system-bus connection Eject talks to
// UDisks2 over. A package var so Eject's unmount/power-off/eject decision
// logic (ejectDevice, in eject_linux.go) can be exercised in tests against
// a fake udisksConn instead: there may be no system bus at all in the
// sandboxed Docker container these tests run in, and even on a real Linux
// desktop a test must never unmount or eject whatever card happens to be
// plugged in.
var connectUDisksFn = func() (udisksConn, func(), error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, func() {}, err
	}
	return &realUDisksConn{conn: conn}, func() { conn.Close() }, nil
}

// realUDisksConn implements udisksConn (eject_linux.go) over a real D-Bus
// system-bus connection.
type realUDisksConn struct {
	conn *dbus.Conn
}

func (c *realUDisksConn) object(path dbus.ObjectPath) dbus.BusObject {
	return c.conn.Object(udisksBusName, path)
}

func (c *realUDisksConn) unmount(path dbus.ObjectPath) error {
	return c.object(path).Call(udisksFilesystemIface+".Unmount", 0, noInteractionOptions()).Err
}

func (c *realUDisksConn) drivePath(path dbus.ObjectPath) (dbus.ObjectPath, error) {
	v, err := c.object(path).GetProperty(udisksBlockIface + ".Drive")
	if err != nil {
		return "", err
	}
	drive, ok := v.Value().(dbus.ObjectPath)
	if !ok {
		return "", fmt.Errorf("unexpected type for Drive property: %T", v.Value())
	}
	return drive, nil
}

func (c *realUDisksConn) canPowerOff(drive dbus.ObjectPath) (bool, error) {
	v, err := c.object(drive).GetProperty(udisksDriveIface + ".CanPowerOff")
	if err != nil {
		return false, err
	}
	can, ok := v.Value().(bool)
	if !ok {
		return false, fmt.Errorf("unexpected type for CanPowerOff property: %T", v.Value())
	}
	return can, nil
}

func (c *realUDisksConn) powerOff(drive dbus.ObjectPath) error {
	return c.object(drive).Call(udisksDriveIface+".PowerOff", 0, noInteractionOptions()).Err
}

func (c *realUDisksConn) eject(drive dbus.ObjectPath) error {
	return c.object(drive).Call(udisksDriveIface+".Eject", 0, noInteractionOptions()).Err
}
