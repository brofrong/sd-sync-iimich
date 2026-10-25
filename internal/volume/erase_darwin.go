//go:build darwin

package volume

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"howett.net/plist"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// erasePollInterval and eraseRemountTimeout control how Erase polls for the
// freshly erased partition to remount. They are variables so tests can
// shrink them instead of waiting on a real timeout.
var erasePollInterval = 500 * time.Millisecond
var eraseRemountTimeout = 60 * time.Second

// partitionIdentifier matches a diskutil partition identifier like
// "disk4s1". A bare whole-disk identifier such as "disk4" never matches, so
// Erase refuses to format an entire disk instead of a single partition.
var partitionIdentifier = regexp.MustCompile(`^disk[0-9]+s[0-9]+$`)

// EraseNeedsAdmin reports whether Erase would refuse every card for lack
// of administrator rights: never on macOS, where diskutil erases a
// removable card without them.
func EraseNeedsAdmin() bool {
	return false
}

// Erase reformats the volume mounted at mountPoint in place, keeping its
// filesystem type (exFAT or FAT32/FAT16) and volume name.
//
// wantUUID must equal (case-insensitively) the volume's current
// VolumeUUID, confirming Erase is about to format the exact card the
// caller already verified. Erase refuses otherwise, and refuses an empty
// wantUUID outright.
//
// Erase also refuses — without ever calling `diskutil eraseVolume` — when
// mountPoint is the boot volume, when the volume is not removable, when
// diskutil did not report a partition identifier, when the volume name is
// empty or looks like a path ("/" or ".."), or when the filesystem is
// anything other than exFAT, FAT32 or FAT16.
//
// After a successful eraseVolume, Erase polls `diskutil info -plist
// <DeviceIdentifier>` for the new volume by device — not by name, since
// diskutil can briefly report stale data for the name before the new
// volume settles — until it reports a MountPoint and a VolumeUUID that
// differs from the old one. If that never happens within
// eraseRemountTimeout, Erase returns an error wrapping
// ErrErasedNotRemounted: the card was erased, but Erase could not find the
// result.
func Erase(mountPoint, wantUUID string) (Info, error) {
	out, err := runDiskutil("info", "-plist", mountPoint)
	if err != nil {
		return Info{}, fmt.Errorf("diskutil info %s: %w: %s", mountPoint, err, out)
	}

	var data map[string]any
	if _, err := plist.Unmarshal(out, &data); err != nil {
		return Info{}, fmt.Errorf("diskutil info %s: parse plist: %w", mountPoint, err)
	}

	deviceID, _ := data["DeviceIdentifier"].(string)
	fsType, _ := data["FilesystemType"].(string)
	fsName, _ := data["FilesystemName"].(string)
	volumeName, _ := data["VolumeName"].(string)
	oldUUID, _ := data["VolumeUUID"].(string)

	// The boot volume gate comes first and is unconditional: nothing else
	// about the plist (even a UUID the caller verified) can override it.
	if mountPoint == "/" {
		return Info{}, fmt.Errorf("erase %s: refusing to erase the boot volume", mountPoint)
	}

	removable := false
	if v, ok := data["Removable"].(bool); ok && v {
		removable = true
	}
	if v, ok := data["Ejectable"].(bool); ok && v {
		removable = true
	}
	if v, ok := data["External"].(bool); ok && v {
		removable = true
	}
	if !removable {
		return Info{}, fmt.Errorf("erase %s: refusing to erase a non-removable volume", mountPoint)
	}

	if deviceID == "" || !partitionIdentifier.MatchString(deviceID) {
		return Info{}, fmt.Errorf("erase %s: refusing to erase device identifier %q, want a partition like diskNsN", mountPoint, deviceID)
	}

	if wantUUID == "" {
		return Info{}, fmt.Errorf("erase %s: refusing to erase without a card identity to verify", mountPoint)
	}
	if oldUUID == "" || !strings.EqualFold(oldUUID, wantUUID) {
		return Info{}, fmt.Errorf("erase %s: card identity mismatch, want UUID %s", mountPoint, wantUUID)
	}

	if volumeName == "" || strings.Contains(volumeName, "/") || strings.Contains(volumeName, "..") {
		return Info{}, fmt.Errorf("erase %s: refusing to erase volume with invalid name %q", mountPoint, volumeName)
	}

	var format string
	switch fsType {
	case "exfat":
		format = "ExFAT"
	case "msdos":
		switch {
		case strings.Contains(fsName, "FAT32"):
			format = "MS-DOS FAT32"
		case strings.Contains(fsName, "FAT16"):
			format = "MS-DOS FAT16"
		default:
			return Info{}, fmt.Errorf("erase %s: refusing to erase unsupported FAT variant %q", mountPoint, fsName)
		}
	default:
		return Info{}, fmt.Errorf("erase %s: refusing to erase filesystem type %q, want exfat or msdos", mountPoint, fsType)
	}

	logx.Info("volume: erasing", "mountPoint", mountPoint, "device", deviceID, "format", format, "name", volumeName)

	eraseOut, err := runDiskutil("eraseVolume", format, volumeName, deviceID)
	if err != nil {
		return Info{}, fmt.Errorf("diskutil eraseVolume %s %s %s: %w: %s", format, volumeName, deviceID, err, eraseOut)
	}

	info, err := pollForRemount(deviceID, oldUUID)
	if err != nil {
		return Info{}, fmt.Errorf("erase %s: %w", mountPoint, err)
	}

	logx.Info("volume: erase complete", "mountPoint", info.MountPoint, "uuid", info.UUID)
	return info, nil
}

// pollForRemount looks for partition deviceID to come back online with a
// mount point and a VolumeUUID different from oldUUID, after a successful
// eraseVolume. It polls by device identifier, not by volume name, because
// diskutil can briefly present stale data under the old name before the
// new volume's identity settles.
func pollForRemount(deviceID, oldUUID string) (Info, error) {
	attempts := int(eraseRemountTimeout / erasePollInterval)
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(erasePollInterval)
		}
		if info, ok := probeRemount(deviceID, oldUUID); ok {
			return info, nil
		}
	}

	return Info{}, fmt.Errorf("device %s: %w", deviceID, ErrErasedNotRemounted)
}

// probeRemount makes one `diskutil info -plist <deviceID>` call and reports
// whether it describes the freshly erased volume: the same device, now
// mounted, with a VolumeUUID that actually changed from oldUUID.
func probeRemount(deviceID, oldUUID string) (Info, bool) {
	out, err := runDiskutil("info", "-plist", deviceID)
	if err != nil {
		return Info{}, false
	}

	var data map[string]any
	if _, err := plist.Unmarshal(out, &data); err != nil {
		return Info{}, false
	}

	gotDevice, _ := data["DeviceIdentifier"].(string)
	gotMount, _ := data["MountPoint"].(string)
	gotUUID, _ := data["VolumeUUID"].(string)
	gotName, _ := data["VolumeName"].(string)

	if gotDevice != deviceID || gotMount == "" || gotUUID == "" {
		return Info{}, false
	}
	if strings.EqualFold(gotUUID, oldUUID) {
		return Info{}, false
	}

	return Info{
		Name:       gotName,
		MountPoint: gotMount,
		UUID:       strings.ToUpper(gotUUID),
		Removable:  true,
	}, true
}
