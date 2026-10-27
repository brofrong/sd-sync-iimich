//go:build linux

package volume

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// UDisks2 bus name and interfaces used by Eject (eject_linux.go) and Erase
// (erase_linux.go).
const (
	udisksBusName         = "org.freedesktop.UDisks2"
	udisksBlockIface      = "org.freedesktop.UDisks2.Block"
	udisksFilesystemIface = "org.freedesktop.UDisks2.Filesystem"
	udisksDriveIface      = "org.freedesktop.UDisks2.Drive"
	udisksPartitionIface  = "org.freedesktop.UDisks2.Partition"

	// udisksBlockDevicesPath is where UDisks2 exposes one object per block
	// device, named after the device's basename (e.g. "sdb1").
	udisksBlockDevicesPath = "/org/freedesktop/UDisks2/block_devices/"

	// udisksBusyErrorName is the D-Bus error UDisks2's Filesystem.Unmount
	// returns when something still has the filesystem open.
	udisksBusyErrorName = "org.freedesktop.UDisks2.Error.DeviceBusy"
)

// noInteractionOptions is the options argument UDisks2 methods take, set so
// they never fall back to a polkit authentication prompt: the app never
// asks for administrator/root rights, for an eject or an erase.
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

// realUDisksConn implements udisksConn (eject_linux.go) and
// udisksEraseConn (erase_linux.go) over a real D-Bus system-bus connection.
type realUDisksConn struct {
	conn *dbus.Conn
}

func (c *realUDisksConn) object(path dbus.ObjectPath) dbus.BusObject {
	return c.conn.Object(udisksBusName, path)
}

// udisksCallTimeout bounds UDisks2's Unmount and Mount calls (Format has
// its own, longer formatTimeout), so a hung daemon can't hang the app.
var udisksCallTimeout = 2 * time.Minute

func (c *realUDisksConn) unmount(path dbus.ObjectPath) error {
	ctx, cancel := context.WithTimeout(context.Background(), udisksCallTimeout)
	defer cancel()
	return c.object(path).CallWithContext(ctx, udisksFilesystemIface+".Unmount", 0, noInteractionOptions()).Err
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

// formatOptions is Block.Format's options argument: the label to keep, no
// polkit prompt, and the partition table left alone.
func formatOptions(label string) map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"label":                    dbus.MakeVariant(label),
		"auth.no_user_interaction": dbus.MakeVariant(true),
		"update-partition-type":    dbus.MakeVariant(false),
	}
}

func (c *realUDisksConn) getAll(path dbus.ObjectPath, iface string) (map[string]dbus.Variant, error) {
	var props map[string]dbus.Variant
	err := c.object(path).Call("org.freedesktop.DBus.Properties.GetAll", 0, iface).Store(&props)
	return props, err
}

func (c *realUDisksConn) blockState(path dbus.ObjectPath) (udisksBlockState, error) {
	block, err := c.getAll(path, udisksBlockIface)
	if err != nil {
		return udisksBlockState{}, err
	}
	// A block without a Partition or Filesystem interface answers GetAll
	// for it with an error: then it is not one.
	part, _ := c.getAll(path, udisksPartitionIface)
	fs, err := c.getAll(path, udisksFilesystemIface)
	if err != nil {
		fs = nil
	}
	return blockStateFromProps(block, part, fs), nil
}

// blockStateFromProps builds a udisksBlockState from the GetAll results of
// a block's Block, Partition and Filesystem interfaces (nil when the block
// has none). Anything missing or of an unexpected type fails closed: an
// empty string, a system device, not a partition, no filesystem.
func blockStateFromProps(block, part, fs map[string]dbus.Variant) udisksBlockState {
	st := udisksBlockState{HintSystem: true}
	st.Device = bytesProp(block["Device"])
	st.IdUUID, _ = block["IdUUID"].Value().(string)
	st.IdType, _ = block["IdType"].Value().(string)
	st.IdVersion, _ = block["IdVersion"].Value().(string)
	st.IdLabel, _ = block["IdLabel"].Value().(string)
	if v, ok := block["HintSystem"].Value().(bool); ok {
		st.HintSystem = v
	}
	st.Drive, _ = block["Drive"].Value().(dbus.ObjectPath)

	table, _ := part["Table"].Value().(dbus.ObjectPath)
	number, _ := part["Number"].Value().(uint32)
	st.IsPartition = table != "" && table != "/" && number > 0

	if fs != nil {
		if raw, ok := fs["MountPoints"].Value().([][]byte); ok {
			st.HasFilesystem = true
			for _, b := range raw {
				if mp := strings.TrimRight(string(b), "\x00"); mp != "" {
					st.MountPoints = append(st.MountPoints, mp)
				}
			}
		}
	}
	return st
}

// bytesProp reads a UDisks2 bytestring property (type ay, NUL-terminated),
// "" when it is missing or of another type.
func bytesProp(v dbus.Variant) string {
	b, _ := v.Value().([]byte)
	return strings.TrimRight(string(b), "\x00")
}

func (c *realUDisksConn) format(path dbus.ObjectPath, fsType, label string) error {
	ctx, cancel := context.WithTimeout(context.Background(), formatTimeout)
	defer cancel()
	return c.object(path).CallWithContext(ctx, udisksBlockIface+".Format", 0, fsType, formatOptions(label)).Err
}

func (c *realUDisksConn) mount(path dbus.ObjectPath) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), udisksCallTimeout)
	defer cancel()
	var mountPoint string
	err := c.object(path).CallWithContext(ctx, udisksFilesystemIface+".Mount", 0, noInteractionOptions()).Store(&mountPoint)
	return mountPoint, err
}

// dbusErrorName returns the D-Bus error name err carries, "" when it is not
// a D-Bus error. godbus delivers a real error reply as a VALUE dbus.Error
// (conn.go's handleDBusError), while dbus.NewError returns a *dbus.Error:
// both forms are checked (see isDeviceBusyErr).
func dbusErrorName(err error) string {
	var valErr dbus.Error
	if errors.As(err, &valErr) {
		return valErr.Name
	}
	var ptrErr *dbus.Error
	if errors.As(err, &ptrErr) && ptrErr != nil {
		return ptrErr.Name
	}
	return ""
}

// isNotAuthorizedErr reports whether err is UDisks2, polkit or the bus
// refusing the call for lack of authorization: the operation would need
// an administrator password, which the app never asks for.
func isNotAuthorizedErr(err error) bool {
	name := dbusErrorName(err)
	switch {
	case strings.HasPrefix(name, "org.freedesktop.UDisks2.Error.NotAuthorized"):
		// NotAuthorized, NotAuthorizedCanObtain, NotAuthorizedDismissed.
		return true
	case name == "org.freedesktop.PolicyKit1.Error.NotAuthorized",
		name == "org.freedesktop.DBus.Error.AccessDenied",
		name == "org.freedesktop.DBus.Error.InteractiveAuthorizationRequired":
		return true
	}
	return false
}

// listUDisksLinksFn lists the UUIDs and labels of UDisks2's block devices,
// for a Flatpak, whose sandbox has no /dev/disk. A variable so tests never
// talk to this machine's UDisks2.
var listUDisksLinksFn = listUDisksLinks

func listUDisksLinks() ([]uuidLink, []labelLink, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, nil, err
	}
	defer conn.Close()
	c := &realUDisksConn{conn: conn}
	var paths []dbus.ObjectPath
	if err := c.object("/org/freedesktop/UDisks2/Manager").Call(
		"org.freedesktop.UDisks2.Manager.GetBlockDevices", 0, map[string]dbus.Variant{},
	).Store(&paths); err != nil {
		return nil, nil, err
	}
	blocks := make([]udisksBlockState, 0, len(paths))
	for _, p := range paths {
		if st, err := c.blockState(p); err == nil {
			blocks = append(blocks, st)
		}
	}
	links, labels := linksFromBlocks(blocks)
	return links, labels, nil
}

// linksFromBlocks turns UDisks2's block devices into the entries
// /dev/disk/by-uuid and /dev/disk/by-label would have.
func linksFromBlocks(blocks []udisksBlockState) ([]uuidLink, []labelLink) {
	var links []uuidLink
	var labels []labelLink
	for _, b := range blocks {
		if b.Device == "" {
			continue
		}
		if b.IdUUID != "" {
			links = append(links, uuidLink{UUID: b.IdUUID, Device: b.Device})
		}
		if b.IdLabel != "" {
			labels = append(labels, labelLink{Label: b.IdLabel, Device: b.Device})
		}
	}
	return links, labels
}
