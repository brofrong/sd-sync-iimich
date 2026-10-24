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
// keeping its type (exFAT, or FAT32 as vfat) and label, through
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
// or vfat FAT32 (a FAT16 card could come back FAT32). Once unmounted, the
// block is read again and the card mounted back, unformatted, unless it is
// still the verified card.
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
	case st.IdType == "vfat" && st.IdVersion == "FAT32":
		// Only FAT32: Block.Format has no portable way to ask mkfs.fat
		// for FAT16, so a FAT16 card could come back FAT32.
		fsType = "vfat"
	default:
		return Info{}, fmt.Errorf("erase %s: refusing to erase filesystem %q %q, want exfat or vfat FAT32", mountPoint, st.IdType, st.IdVersion)
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

	// Unmounting takes time and lets go of the card: read the block again
	// and format only if it is still the card verified above.
	again, err := conn.blockState(path)
	if err != nil {
		mountBack(conn, path, device)
		return Info{}, fmt.Errorf("erase %s: refusing to format: could not re-check the card after unmounting: %w", mountPoint, err)
	}
	if why := blockChanged(st, again, device, wantUUID); why != "" {
		mountBack(conn, path, device)
		return Info{}, fmt.Errorf("erase %s: refusing to format: the card changed after unmounting (%s)", mountPoint, why)
	}

	if err := conn.format(path, fsType, label); err != nil {
		// Not formatted: give the card back to the user as it was.
		mountBack(conn, path, device)
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

// blockChanged reports how block state now differs from before, the state
// Erase verified for device and wantUUID ("" when it is still that card on
// the same partition and drive, with the same filesystem and label).
func blockChanged(before, now udisksBlockState, device, wantUUID string) string {
	switch {
	case now.Device != device:
		return fmt.Sprintf("device %q", now.Device)
	case now.IdUUID == "" || !strings.EqualFold(now.IdUUID, wantUUID):
		return fmt.Sprintf("UUID %q", now.IdUUID)
	case now.HintSystem:
		return "now a system device"
	case !now.IsPartition:
		return "no longer a partition"
	case now.Drive != before.Drive:
		return fmt.Sprintf("drive %q", now.Drive)
	case now.IdType != before.IdType || now.IdVersion != before.IdVersion:
		return fmt.Sprintf("filesystem %q %q", now.IdType, now.IdVersion)
	case now.IdLabel != before.IdLabel:
		return fmt.Sprintf("label %q", now.IdLabel)
	}
	return ""
}

// mountBack mounts the card at block path again after Erase unmounted it
// but did not format it, so the user gets it back as it was.
func mountBack(conn udisksEraseConn, path dbus.ObjectPath, device string) {
	if _, err := conn.mount(path); err != nil {
		logx.Warn("volume: mounting the card again after not formatting it failed", "device", device, "err", err)
	}
}

// maxMountBackoffShift caps the gap between Mount retries at 1<<6 = 64
// polls.
const maxMountBackoffShift = 6

// pollForRemount looks for the freshly formatted filesystem on block path
// (device node device): a non-empty IdUUID different from oldUUID, then
// mounted. It polls the block object, the device itself, never a name.
func pollForRemount(conn udisksEraseConn, path dbus.ObjectPath, device, oldUUID string) (Info, error) {
	attempts := int(eraseRemountTimeout / erasePollInterval)
	if attempts < 1 {
		attempts = 1
	}

	// Mount is not asked on every poll: never again once it was refused
	// for lack of authorization, otherwise with a doubling gap. The state
	// is still read every poll, in case the desktop mounts the card.
	var mountErr error
	mountRefused := false
	failures, nextMount := 0, 0
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(erasePollInterval)
		}
		st, ok := probeFormatted(conn, path, device, oldUUID)
		if !ok {
			continue
		}
		if len(st.MountPoints) > 0 && st.MountPoints[0] != "" {
			return formattedInfo(st, st.MountPoints[0]), nil
		}
		if mountRefused || attempt < nextMount {
			continue
		}
		mp, err := conn.mount(path)
		if err == nil && mp != "" {
			return formattedInfo(st, mp), nil
		}
		if err == nil {
			err = fmt.Errorf("Mount returned no mount point")
		}
		mountErr = err
		if isNotAuthorizedErr(err) {
			mountRefused = true
			continue
		}
		failures++
		nextMount = attempt + 1<<min(failures-1, maxMountBackoffShift)
	}

	if mountErr != nil {
		logx.Warn("volume: mounting the formatted card failed", "device", device, "err", mountErr)
		return Info{}, fmt.Errorf("device %s: %w (mount: %v)", device, ErrErasedNotRemounted, mountErr)
	}
	return Info{}, fmt.Errorf("device %s: %w", device, ErrErasedNotRemounted)
}

// probeFormatted makes one look at block path and reports whether it holds
// the freshly formatted filesystem: the same device, a filesystem, and a
// non-empty IdUUID different from oldUUID.
func probeFormatted(conn udisksEraseConn, path dbus.ObjectPath, device, oldUUID string) (udisksBlockState, bool) {
	st, err := conn.blockState(path)
	if err != nil || st.Device != device || !st.HasFilesystem {
		return udisksBlockState{}, false
	}
	if st.IdUUID == "" || strings.EqualFold(st.IdUUID, oldUUID) {
		return udisksBlockState{}, false
	}
	return st, true
}

// formattedInfo describes the formatted filesystem st mounted at mp.
func formattedInfo(st udisksBlockState, mp string) Info {
	name := st.IdLabel
	if name == "" {
		name = filepath.Base(mp)
	}
	return Info{
		Name:       name,
		MountPoint: mp,
		UUID:       strings.ToUpper(st.IdUUID),
		Removable:  true,
	}
}
