//go:build darwin

package volume

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"howett.net/plist"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// eraseMountTimeout bounds how long Erase waits for the freshly formatted
// volume to remount after `diskutil eraseVolume`.
const eraseMountTimeout = 60 * time.Second

// partitionIdentifier matches a diskutil partition identifier like
// "disk4s1". A bare whole-disk identifier such as "disk4" never matches, so
// Erase refuses to format an entire disk instead of a single partition.
var partitionIdentifier = regexp.MustCompile(`^disk[0-9]+s[0-9]+$`)

// waitForMount and getInfo are seams over WaitForMount and GetInfo so tests
// can substitute fakes instead of touching a real mounted volume.
var waitForMount = WaitForMount
var getInfo = GetInfo

// Erase reformats the volume mounted at mountPoint in place, keeping its
// filesystem type (exFAT or FAT32/FAT16) and volume name, then returns the
// Info of the newly mounted volume.
//
// It refuses — without ever calling `diskutil eraseVolume` — when the
// volume is not removable, when its filesystem is anything other than exFAT
// or FAT, or when diskutil did not report a partition identifier (so a
// whole-disk identifier can never reach eraseVolume).
func Erase(mountPoint string) (Info, error) {
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
	if mountPoint == "/" {
		removable = false
	}

	if !removable {
		return Info{}, fmt.Errorf("erase %s: refusing to erase a non-removable volume", mountPoint)
	}
	if deviceID == "" || !partitionIdentifier.MatchString(deviceID) {
		return Info{}, fmt.Errorf("erase %s: refusing to erase device identifier %q, want a partition like diskNsN", mountPoint, deviceID)
	}

	var format string
	switch fsType {
	case "exfat":
		format = "ExFAT"
	case "msdos":
		if strings.Contains(fsName, "FAT16") {
			format = "MS-DOS FAT16"
		} else {
			format = "MS-DOS FAT32"
		}
	default:
		return Info{}, fmt.Errorf("erase %s: refusing to erase filesystem type %q, want exfat or msdos", mountPoint, fsType)
	}

	logx.Info("volume: erasing", "mountPoint", mountPoint, "device", deviceID, "format", format, "name", volumeName)

	eraseOut, err := runDiskutil("eraseVolume", format, volumeName, deviceID)
	if err != nil {
		return Info{}, fmt.Errorf("diskutil eraseVolume %s %s %s: %w: %s", format, volumeName, deviceID, err, eraseOut)
	}

	newMountPoint := filepath.Join(volumesRoot, volumeName)
	if err := waitForMount(newMountPoint, eraseMountTimeout); err != nil {
		return Info{}, fmt.Errorf("erase %s: %w", mountPoint, err)
	}

	info, err := getInfo(newMountPoint)
	if err != nil {
		return Info{}, fmt.Errorf("erase %s: diskutil info after erase: %w", mountPoint, err)
	}

	logx.Info("volume: erase complete", "mountPoint", newMountPoint, "uuid", info.UUID)
	return *info, nil
}
