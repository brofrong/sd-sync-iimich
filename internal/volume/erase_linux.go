//go:build linux

package volume

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// erasePollInterval and eraseRemountTimeout control how Erase polls for the
// freshly formatted filesystem; formatTimeout bounds UDisks2's Format call.
// They are variables so tests can shrink them instead of waiting on a real
// timeout.
var (
	erasePollInterval   = 500 * time.Millisecond
	eraseRemountTimeout = 60 * time.Second
	formatTimeout       = 10 * time.Minute
)

// udisksBlockState is what Erase reads of one UDisks2 block object: its
// Block properties (one GetAll), whether it is a partition, and its
// Filesystem's mount points.
type udisksBlockState struct {
	// Device is the block's device node, e.g. "/dev/sdb1".
	Device    string
	IdUUID    string
	IdType    string
	IdVersion string
	IdLabel   string
	// HintSystem is UDisks2's "system device" hint; true when UDisks2 did
	// not report it, so an unknown device is never taken for a card.
	HintSystem bool
	Drive      dbus.ObjectPath
	// IsPartition: the block has a Partition interface with a table and a
	// number (not a whole disk).
	IsPartition bool
	// HasFilesystem: the block has a Filesystem interface (it can be
	// mounted), whose MountPoints are listed.
	HasFilesystem bool
	MountPoints   []string
}

// udisksEraseConn is the subset of UDisks2's D-Bus API Erase needs, behind
// a seam so its decisions can be tested with a fake bus (see
// erase_linux_test.go) instead of a real system bus or UDisks2 daemon.
type udisksEraseConn interface {
	blockState(path dbus.ObjectPath) (udisksBlockState, error)
	// unmount calls Filesystem.Unmount without user interaction.
	unmount(path dbus.ObjectPath) error
	// format calls Block.Format(fsType, formatOptions(label)) and waits for
	// the job to finish.
	format(path dbus.ObjectPath, fsType, label string) error
	// mount calls Filesystem.Mount without user interaction and returns
	// the mount point.
	mount(path dbus.ObjectPath) (string, error)
}

// connectUDisksEraseFn opens the system-bus connection Erase talks to
// UDisks2 over. A package var so tests never reach a real UDisks2.
var connectUDisksEraseFn = func() (udisksEraseConn, func(), error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, func() {}, err
	}
	return &realUDisksConn{conn: conn}, func() { conn.Close() }, nil
}

// Erase reformats the removable filesystem mounted at mountPoint in place,
// keeping its type (exFAT, or FAT32/FAT16 as vfat) and label, through
// UDisks2 and without user interaction: no password prompt, no root.
//
// wantUUID must equal (case-insensitively) the filesystem's IdUUID, read in
// the same UDisks2 call as the device node, type and label Erase formats
// with: Erase is about to format the exact card the caller already
// verified. It refuses otherwise, and refuses an empty wantUUID outright.
//
// Erase also refuses — without unmounting or formatting anything — "/",
// a mount that is not removable (see isRemovableEntry), a block UDisks2
// hints is a system device or reports for another device node or mount
// point, a block without a drive, a whole disk (only a partition is
// formatted), an empty or path-like label, and anything other than exfat
// or vfat FAT32/FAT16.
//
// UDisks2 refusing for lack of authorization (polkit) is ErrNeedsAdmin.
//
// After the format, Erase polls the same block object — the device, not a
// name — until it reports a new, different IdUUID, and mounts it (unless
// the desktop already did). If that never happens within
// eraseRemountTimeout, the error wraps ErrErasedNotRemounted: the card was
// erased, but Erase could not find the result.
func Erase(mountPoint, wantUUID string) (Info, error) {
	mountPoint = filepath.Clean(mountPoint)

	// The root gate comes first and is unconditional.
	if mountPoint == "/" {
		return Info{}, fmt.Errorf("erase %s: refusing to erase the root filesystem", mountPoint)
	}

	device, err := deviceForMountPoint(mountPoint)
	if err != nil {
		return Info{}, fmt.Errorf("erase %s: %w", mountPoint, err)
	}
	if !strings.HasPrefix(device, "/dev/") {
		return Info{}, fmt.Errorf("erase %s: refusing to erase %q, not a device node", mountPoint, device)
	}
	if !isRemovableEntry(mountEntry{MountPoint: mountPoint, Source: device}) {
		return Info{}, fmt.Errorf("erase %s: refusing to erase a non-removable volume", mountPoint)
	}

	conn, closeConn, err := connectUDisksEraseFn()
	if err != nil {
		return Info{}, fmt.Errorf("erase %s: connect to UDisks2: %w", mountPoint, err)
	}
	defer closeConn()

	return eraseBlock(conn, mountPoint, device, wantUUID)
}

// eraseBlock implements Erase against conn for the filesystem on device
// (e.g. "/dev/sdb1") mounted at mountPoint.
func eraseBlock(conn udisksEraseConn, mountPoint, device, wantUUID string) (Info, error) {
	path := blockObjectPath(device)
	st, err := conn.blockState(path)
	if err != nil {
		return Info{}, fmt.Errorf("erase %s: read UDisks2 block %s: %w", mountPoint, path, err)
	}

	if st.HintSystem {
		return Info{}, fmt.Errorf("erase %s: refusing to erase a system device", mountPoint)
	}
	if st.Device != device {
		return Info{}, fmt.Errorf("erase %s: refusing to erase: UDisks2 block %s is %q, want %q", mountPoint, path, st.Device, device)
	}
	if st.Drive == "" || st.Drive == "/" {
		return Info{}, fmt.Errorf("erase %s: refusing to erase a block without a drive", mountPoint)
	}
	if !st.IsPartition {
		return Info{}, fmt.Errorf("erase %s: refusing to erase %s, want a partition, not a whole disk", mountPoint, device)
	}
	if !st.HasFilesystem || !slices.Contains(st.MountPoints, mountPoint) {
		return Info{}, fmt.Errorf("erase %s: refusing to erase: UDisks2 reports %s mounted at %q", mountPoint, device, st.MountPoints)
	}

	if wantUUID == "" {
		return Info{}, fmt.Errorf("erase %s: refusing to erase without a card identity to verify", mountPoint)
	}
	oldUUID := st.IdUUID
	if oldUUID == "" || !strings.EqualFold(oldUUID, wantUUID) {
		return Info{}, fmt.Errorf("erase %s: card identity mismatch, want UUID %s", mountPoint, wantUUID)
	}

	label := st.IdLabel
	if label == "" || strings.Contains(label, "/") || strings.Contains(label, "..") {
		return Info{}, fmt.Errorf("erase %s: refusing to erase volume with invalid name %q", mountPoint, label)
	}

	var fsType string
	switch {
	case st.IdType == "exfat":
		fsType = "exfat"
	case st.IdType == "vfat" && (st.IdVersion == "FAT32" || st.IdVersion == "FAT16"):
		fsType = "vfat"
	default:
		return Info{}, fmt.Errorf("erase %s: refusing to erase filesystem %q %q, want exfat or vfat FAT32/FAT16", mountPoint, st.IdType, st.IdVersion)
	}

	logx.Info("volume: erasing", "mountPoint", mountPoint, "device", device, "format", fsType, "name", label)

	if err := conn.unmount(path); err != nil {
		switch {
		case isNotAuthorizedErr(err):
			return Info{}, fmt.Errorf("erase %s: unmount: %w: %v", mountPoint, ErrNeedsAdmin, err)
		case isDeviceBusyErr(err):
			return Info{}, fmt.Errorf("erase %s: card is in use: %w", mountPoint, err)
		default:
			return Info{}, fmt.Errorf("erase %s: unmount failed: %w", mountPoint, err)
		}
	}

	if err := conn.format(path, fsType, label); err != nil {
		// Not formatted: give the card back to the user as it was.
		if _, merr := conn.mount(path); merr != nil {
			logx.Warn("volume: mounting the card again after a failed format failed", "device", device, "err", merr)
		}
		if isNotAuthorizedErr(err) {
			return Info{}, fmt.Errorf("erase %s: format: %w: %v", mountPoint, ErrNeedsAdmin, err)
		}
		return Info{}, fmt.Errorf("erase %s: format %s %s: %w", mountPoint, fsType, device, err)
	}

	info, err := pollForRemount(conn, path, device, oldUUID)
	if err != nil {
		return Info{}, fmt.Errorf("erase %s: %w", mountPoint, err)
	}

	logx.Info("volume: erase complete", "mountPoint", info.MountPoint, "uuid", info.UUID)
	return info, nil
}

// pollForRemount looks for the freshly formatted filesystem on block path
// (device node device): a non-empty IdUUID different from oldUUID, then
// mounted. It polls the block object, the device itself, never a name.
func pollForRemount(conn udisksEraseConn, path dbus.ObjectPath, device, oldUUID string) (Info, error) {
	attempts := int(eraseRemountTimeout / erasePollInterval)
	if attempts < 1 {
		attempts = 1
	}

	var mountErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(erasePollInterval)
		}
		info, ok, err := probeRemount(conn, path, device, oldUUID)
		if ok {
			return info, nil
		}
		if err != nil {
			mountErr = err
		}
	}

	if mountErr != nil {
		logx.Warn("volume: mounting the formatted card failed", "device", device, "err", mountErr)
		return Info{}, fmt.Errorf("device %s: %w (mount: %v)", device, ErrErasedNotRemounted, mountErr)
	}
	return Info{}, fmt.Errorf("device %s: %w", device, ErrErasedNotRemounted)
}

// probeRemount makes one look at block path and reports whether it holds
// the freshly formatted filesystem, mounting it if nothing has yet; err is
// a failed Mount.
func probeRemount(conn udisksEraseConn, path dbus.ObjectPath, device, oldUUID string) (info Info, ok bool, err error) {
	st, err := conn.blockState(path)
	if err != nil || st.Device != device || !st.HasFilesystem {
		return Info{}, false, nil
	}
	if st.IdUUID == "" || strings.EqualFold(st.IdUUID, oldUUID) {
		return Info{}, false, nil
	}

	var mp string
	if len(st.MountPoints) > 0 {
		mp = st.MountPoints[0]
	} else {
		mp, err = conn.mount(path)
		if err != nil {
			return Info{}, false, err
		}
	}
	if mp == "" {
		return Info{}, false, nil
	}

	name := st.IdLabel
	if name == "" {
		name = filepath.Base(mp)
	}
	return Info{
		Name:       name,
		MountPoint: mp,
		UUID:       strings.ToUpper(st.IdUUID),
		Removable:  true,
	}, true, nil
}
