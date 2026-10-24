//go:build linux

package volume

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"
)

// udisksConn is the subset of UDisks2's D-Bus API ejectDevice needs,
// behind a seam so its unmount/power-off/eject decision logic can be
// tested with a fake bus instead of a real system bus or UDisks2 daemon
// (see udisks_linux.go for the real implementation, and
// eject_linux_test.go for the fake).
type udisksConn interface {
	// unmount calls Filesystem.Unmount on the block device at path.
	unmount(path dbus.ObjectPath) error
	// drivePath reads the block device's Drive property: the path of the
	// drive object that controls ejecting/powering off its hardware.
	drivePath(path dbus.ObjectPath) (dbus.ObjectPath, error)
	// canPowerOff reads the drive's CanPowerOff property.
	canPowerOff(drive dbus.ObjectPath) (bool, error)
	// powerOff calls Drive.PowerOff.
	powerOff(drive dbus.ObjectPath) error
	// eject calls Drive.Eject.
	eject(drive dbus.ObjectPath) error
}

// Eject unmounts the volume mounted at mountPoint and then powers off (or,
// if that's not supported, ejects) the removable drive behind it, via
// UDisks2 — the same action as a desktop file manager's "Eject" button —
// without requiring root.
func Eject(mountPoint string) error {
	device, err := deviceForMountPoint(mountPoint)
	if err != nil {
		return fmt.Errorf("eject %s: %w", mountPoint, err)
	}

	conn, closeConn, err := connectUDisksFn()
	if err != nil {
		return fmt.Errorf("eject %s: connect to UDisks2: %w", mountPoint, err)
	}
	defer closeConn()

	return ejectDevice(conn, mountPoint, device)
}

// ejectDevice implements Eject's UDisks2 sequence against conn: unmount the
// filesystem, then power off the drive if it supports that, falling back
// to a plain eject when it doesn't (or when PowerOff itself fails).
func ejectDevice(conn udisksConn, mountPoint, device string) error {
	blockPath := blockObjectPath(device)

	if err := conn.unmount(blockPath); err != nil {
		if isDeviceBusyErr(err) {
			return fmt.Errorf("eject %s: card is in use: %w", mountPoint, err)
		}
		return fmt.Errorf("eject %s: unmount failed: %w", mountPoint, err)
	}

	drive, err := conn.drivePath(blockPath)
	if err != nil {
		return fmt.Errorf("eject %s: find drive: %w", mountPoint, err)
	}

	if canPowerOff, _ := conn.canPowerOff(drive); canPowerOff {
		if err := conn.powerOff(drive); err == nil {
			return nil
		}
		// CanPowerOff said yes but the call itself failed (e.g. the drive
		// stopped responding) — fall back to a plain eject below rather
		// than reporting the card merely unmounted but not ejected.
	}

	if err := conn.eject(drive); err != nil {
		return fmt.Errorf("eject %s: eject drive failed: %w", mountPoint, err)
	}
	return nil
}

// blockObjectPath returns the UDisks2 object path for device's block
// device (e.g. "/dev/sdb1" -> "/org/freedesktop/UDisks2/block_devices/sdb1").
func blockObjectPath(device string) dbus.ObjectPath {
	return dbus.ObjectPath(udisksBlockDevicesPath + udisksEscapeObjectPathSegment(filepath.Base(device)))
}

// udisksEscapeObjectPathSegment escapes a string the way UDisks2 (and
// systemd before it) escapes arbitrary strings into D-Bus object path
// segments: any byte outside [A-Za-z0-9_] becomes "_xx" (its value in
// lower-case hex). Real device basenames ("sdb1", "mmcblk0p1", "nvme0n1p1")
// never need it, but it keeps blockObjectPath correct if one ever did.
func udisksEscapeObjectPathSegment(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "_%02x", c)
		}
	}
	return b.String()
}

// isDeviceBusyErr reports whether err is UDisks2's "DeviceBusy" D-Bus error
// (Filesystem.Unmount failing because something still has the filesystem
// open).
func isDeviceBusyErr(err error) bool {
	var dbusErr *dbus.Error
	if errors.As(err, &dbusErr) {
		return dbusErr.Name == udisksBusyErrorName
	}
	return false
}
