//go:build linux

package volume

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/godbus/dbus/v5"
)

// udisksConn is the subset of UDisks2's D-Bus API ejectDevice needs,
// behind a seam so its unmount/power-off/eject decision logic can be
// tested with a fake bus instead of a real system bus or UDisks2 daemon
// (see udisks_linux.go for the real implementation, and
// eject_linux_test.go for the fake).
type udisksConn interface {
	// blockState reads the block device at path (one GetAll of its Block
	// properties, as Erase reads it): its device node, system-device hint
	// and Drive, the drive object that controls ejecting/powering off its
	// hardware.
	blockState(path dbus.ObjectPath) (udisksBlockState, error)
	// unmount calls Filesystem.Unmount on the block device at path.
	unmount(path dbus.ObjectPath) error
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
//
// Like Erase, it first refuses — without unmounting anything — a block
// UDisks2 hints is a system device (or does not say), one it reports for
// another device node, and one without a drive: an internal partition the
// user opened in the file manager is mounted under /media/<user> like a
// card, but is never unmounted or powered off.
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

// ejectDevice implements Eject's UDisks2 sequence against conn: check that
// the block is a removable card's (see Eject), unmount the filesystem, then
// power off the drive if it supports that, falling back to a plain eject
// when it doesn't (or when PowerOff itself fails).
func ejectDevice(conn udisksConn, mountPoint, device string) error {
	blockPath := blockObjectPath(device)

	st, err := conn.blockState(blockPath)
	if err != nil {
		return fmt.Errorf("eject %s: read UDisks2 block %s: %w", mountPoint, blockPath, err)
	}
	if st.HintSystem {
		return fmt.Errorf("eject %s: refusing to eject a system device", mountPoint)
	}
	if st.Device != device {
		return fmt.Errorf("eject %s: refusing to eject: UDisks2 block %s is %q, want %q", mountPoint, blockPath, st.Device, device)
	}
	drive := st.Drive
	if drive == "" || drive == "/" {
		return fmt.Errorf("eject %s: refusing to eject a block without a drive", mountPoint)
	}

	if err := conn.unmount(blockPath); err != nil {
		if isDeviceBusyErr(err) {
			return fmt.Errorf("eject %s: card is in use: %w", mountPoint, err)
		}
		return fmt.Errorf("eject %s: unmount failed: %w", mountPoint, err)
	}

	canPowerOff, cpoErr := conn.canPowerOff(drive)
	if cpoErr != nil {
		// Not fatal: a drive this can't read the property of (or doesn't
		// have it at all) is simply treated as not power-off-capable,
		// falling through to a plain eject below — but it's worth a log
		// line since it means UDisks2 behaved unexpectedly for this drive.
		logx.Warn("udisks2 CanPowerOff query failed", "drive", string(drive), "err", cpoErr)
	}
	if canPowerOff {
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
//
// godbus's real dispatch path (conn.go's handleDBusError ->
// finalizeWithError(..., Error{name, msg.Body})) sets Call.Err to a VALUE
// dbus.Error, not a pointer — only the dbus.NewError helper (used by this
// package's own tests, and by some callers that construct an error
// locally rather than receiving one off the wire) returns *dbus.Error. The
// first draft of this function only checked the pointer form, so it would
// never have recognized a real "DeviceBusy" reply from UDisks2: both forms
// are checked, by dbusErrorName (udisks_linux.go; see TestIsDeviceBusyErr and
// TestEjectDeviceBusyFilesystemValueFormMentionsCardInUse, which construct
// the error exactly as real dispatch does).
func isDeviceBusyErr(err error) bool {
	return dbusErrorName(err) == udisksBusyErrorName
}
