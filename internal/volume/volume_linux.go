//go:build linux

package volume

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// mountinfoPath is where the kernel reports this process's current mount
// table — see proc(5) ("/proc/[pid]/mountinfo").
const mountinfoPath = "/proc/self/mountinfo"

// byUUIDDir holds one symlink per mounted filesystem's UUID, pointing at
// its device node (e.g. "1234-5678" -> "../../sdb1"), maintained by udev.
const byUUIDDir = "/dev/disk/by-uuid"

// byLabelDir holds one symlink per mounted filesystem's label, pointing at
// its device node (e.g. "CARD" -> "../../sdb1"), maintained by udev. A
// label can contain characters a filename can't, which udev encodes as
// "\xHH" (unescapeUdevName decodes it back).
const byLabelDir = "/dev/disk/by-label"

// IO behind seams so ListRemovable/GetInfo/FindByUUID can be driven by
// tests with fixtures instead of this machine's real /proc, /sys and /dev
// (which, per the project's development-safety rule, must never be
// consulted for real removable-media decisions during a test run).
var (
	// readMountinfoFn reads the raw contents of mountinfoPath.
	readMountinfoFn = func() (string, error) {
		data, err := os.ReadFile(mountinfoPath)
		return string(data), err
	}

	// isSysRemovableFn reports whether /sys/block/<disk>/removable says "1"
	// for disk (a kernel block device name such as "sdb" or "mmcblk0").
	// Many USB SD-card readers report "0" here even though the media
	// itself is removable (see isRemovableEntry and TestLinuxRemovableByMountPoint),
	// so this is only ever a fallback signal, never relied on alone.
	isSysRemovableFn = func(disk string) bool {
		data, err := os.ReadFile(filepath.Join("/sys/block", disk, "removable"))
		if err != nil {
			return false
		}
		return strings.TrimSpace(string(data)) == "1"
	}

	// listByUUIDFn lists byUUIDDir's UUID-to-device symlinks.
	listByUUIDFn = listByUUID

	// listByLabelFn lists byLabelDir's label-to-device symlinks.
	listByLabelFn = listByLabel
)

// mountEntry is one parsed line of /proc/self/mountinfo: the fields
// ListRemovable/GetInfo/FindByUUID/the eject code need, already unescaped.
type mountEntry struct {
	// MountPoint is where the filesystem is mounted, e.g. "/media/user/CARD".
	MountPoint string
	// Source is the mount's device node, e.g. "/dev/sdb1" — or something
	// else entirely ("proc", "tmpfs", "none", ...) for a pseudo filesystem,
	// which callers must recognize and skip rather than treat as a device.
	Source string
}

// parseMountinfo parses every line of /proc/self/mountinfo's contents.
//
// Format (see proc(5)): mandatory fields 1-6, then zero or more optional
// fields, then a literal "-" separator, then fields 9-11 (fs type, mount
// source, super options):
//
//	36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
//	(1)(2)(3)   (4)   (5)      (6)      (7)   (8) (9)   (10)         (11)
//
// Fields may contain space, tab, newline and backslash escaped as their
// octal value (e.g. a mount point containing a space is written with
// "\040" in it), which unescapeMountinfoField decodes.
func parseMountinfo(data string) ([]mountEntry, error) {
	var entries []mountEntry
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry, err := parseMountinfoLine(line)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseMountinfoLine(line string) (mountEntry, error) {
	fields := strings.Fields(line)
	if len(fields) < 6 {
		return mountEntry{}, fmt.Errorf("malformed mountinfo line (too few fields): %q", line)
	}

	// The optional fields (7) run from index 6 up to the "-" separator (8);
	// there are zero or more of them, so the separator's index varies.
	sep := -1
	for i := 6; i < len(fields); i++ {
		if fields[i] == "-" {
			sep = i
			break
		}
	}
	if sep == -1 || sep+2 >= len(fields) {
		return mountEntry{}, fmt.Errorf("malformed mountinfo line (no fs type/source after separator): %q", line)
	}

	// fields[sep+1] is the fs type (field 9); it isn't currently used by any
	// decision in this package, so it isn't kept on mountEntry — only its
	// position needs to be known, to find Source right after it.
	return mountEntry{
		MountPoint: unescapeMountinfoField(fields[4]),
		Source:     unescapeMountinfoField(fields[sep+2]),
	}, nil
}

// unescapeMountinfoField decodes the kernel's octal escaping of space, tab,
// newline and backslash (`\040`, `\011`, `\012`, `\134`) in a single
// mountinfo field.
func unescapeMountinfoField(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// mediaMountRE matches a mount point under /media/<user>/<name> or
// /run/media/<user>/<name> — the paths desktop Linux auto-mounts removable
// media under (GNOME/KDE's udisks2-backed auto-mounter, udiskie, etc.).
var mediaMountRE = regexp.MustCompile(`^/(?:run/media|media)/[^/]+/[^/]+`)

// isMediaMountPoint reports whether mountPoint is a desktop auto-mount path
// for removable media, regardless of what the underlying block device
// claims about itself.
func isMediaMountPoint(mountPoint string) bool {
	return mediaMountRE.MatchString(mountPoint)
}

// mmcOrNvmePartitionRE matches a partition device name on a controller
// whose own disk name already contains digits, so partitions are numbered
// after a literal "p" instead of being appended directly (mmcblk and
// nvme), e.g. "mmcblk0p1" (disk "mmcblk0", partition 1) or "nvme0n1p1"
// (disk "nvme0n1", partition 1).
var mmcOrNvmePartitionRE = regexp.MustCompile(`^((?:mmcblk\d+|nvme\d+n\d+))p\d+$`)

// letterDiskPartitionRE matches a plain numbered partition on a disk whose
// own name is letters only (sd, hd, vd, xvd, ...), e.g. "sdb1" (disk "sdb",
// partition 1).
var letterDiskPartitionRE = regexp.MustCompile(`^([a-zA-Z]+)\d+$`)

// diskName derives the whole-disk block device name /sys/block exposes
// (e.g. "sdb", "mmcblk0", "nvme0n1") from a partition's device path (e.g.
// "/dev/sdb1", "/dev/mmcblk0p1", "/dev/nvme0n1p1"). mmcblk/nvme are
// special-cased (checked, and excluded from the generic letters+digits
// rule below) because their own disk name already ends in a digit
// ("mmcblk0", "nvme0n1"): without the exclusion, a whole mmcblk/nvme disk
// mounted directly with no partition table (some SD cards are formatted
// this way) would have its trailing digit wrongly stripped as if it were a
// partition number. A device name that isn't a recognized numbered
// partition at all (e.g. a loop mount, which doesn't follow either
// convention) is returned unchanged; isSysRemovableFn then simply finds no
// matching /sys/block entry for it and reports not removable, which is the
// right answer for anything that isn't a physical disk.
func diskName(device string) string {
	base := filepath.Base(device)
	if m := mmcOrNvmePartitionRE.FindStringSubmatch(base); m != nil {
		return m[1]
	}
	if strings.HasPrefix(base, "mmcblk") || strings.HasPrefix(base, "nvme") {
		return base
	}
	if m := letterDiskPartitionRE.FindStringSubmatch(base); m != nil {
		return m[1]
	}
	return base
}

// isRemovableEntry decides whether a parsed mount entry is removable media:
// either it's mounted where desktop Linux auto-mounts removable media
// (isMediaMountPoint), which is trusted outright, or its underlying block
// device's /sys/block/<disk>/removable flag says so. The first check is
// why a card reader that reports removable=0 at the sysfs level (common for
// some USB SD-card readers) still shows up as removable once its card is
// mounted under /media/<user>/... or /run/media/<user>/... — see
// TestLinuxRemovableByMountPoint.
func isRemovableEntry(e mountEntry) bool {
	if isMediaMountPoint(e.MountPoint) {
		return true
	}
	if !strings.HasPrefix(e.Source, "/dev/") {
		return false
	}
	return isSysRemovableFn(diskName(e.Source))
}

// uuidLink is one entry of byUUIDDir: a filesystem UUID and the device node
// it points at.
type uuidLink struct {
	UUID   string
	Device string
}

// listByUUID reads byUUIDDir's UUID symlinks.
func listByUUID() ([]uuidLink, error) {
	entries, err := os.ReadDir(byUUIDDir)
	if err != nil {
		return nil, err
	}
	links := make([]uuidLink, 0, len(entries))
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(byUUIDDir, e.Name()))
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(byUUIDDir, target)
		}
		links = append(links, uuidLink{UUID: e.Name(), Device: filepath.Clean(target)})
	}
	return links, nil
}

// uuidForDevice returns the upper-cased filesystem UUID for device (e.g.
// "/dev/sdb1"), as reported by links (byUUIDDir's entries), or "" if device
// isn't listed there (e.g. udev hasn't created the symlink yet).
func uuidForDevice(device string, links []uuidLink) string {
	want := filepath.Base(device)
	for _, l := range links {
		if filepath.Base(l.Device) == want {
			return strings.ToUpper(l.UUID)
		}
	}
	return ""
}

// labelLink is one entry of byLabelDir: a filesystem label and the device
// node it points at.
type labelLink struct {
	Label  string
	Device string
}

// listByLabel reads byLabelDir's label symlinks, decoding udev's "\xHH"
// escaping of each symlink's name back into the real label text.
func listByLabel() ([]labelLink, error) {
	entries, err := os.ReadDir(byLabelDir)
	if err != nil {
		return nil, err
	}
	links := make([]labelLink, 0, len(entries))
	for _, e := range entries {
		target, err := os.Readlink(filepath.Join(byLabelDir, e.Name()))
		if err != nil {
			continue
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(byLabelDir, target)
		}
		links = append(links, labelLink{Label: unescapeUdevName(e.Name()), Device: filepath.Clean(target)})
	}
	return links, nil
}

// unescapeUdevName decodes udev's "\xHH" hex-escaping (HH is the byte's
// value in lower-case hex) of characters that can't appear literally in a
// symlink's filename — most commonly a space, written "\x20" — in a
// /dev/disk/by-label or /dev/disk/by-id entry's name. This is a different
// escaping scheme from mountinfo's octal "\NNN" (see
// unescapeMountinfoField): udev's own udev_util_encode_string/
// blkid_encode_string helpers always use hex, never octal.
func unescapeUdevName(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) && s[i+1] == 'x' {
			if v, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// labelForDevice returns the filesystem label for device (e.g.
// "/dev/sdb1"), as reported by links (byLabelDir's entries), or "" if
// device has no label (common for cards formatted on a camera) or isn't
// listed there yet.
func labelForDevice(device string, links []labelLink) string {
	want := filepath.Base(device)
	for _, l := range links {
		if filepath.Base(l.Device) == want {
			return l.Label
		}
	}
	return ""
}

// listAllVolumes describes every currently mounted volume backed by a real
// device node (under /dev/), removable or not — pseudo filesystems (proc,
// sysfs, tmpfs, cgroup, a snap's squashfs loop mount, ...) are skipped
// entirely, the same way darwin's ListMounted only sees /Volumes entries
// and windows' listAllVolumes only sees assigned drive letters with
// readable media.
func listAllVolumes() ([]Info, error) {
	data, err := readMountinfoFn()
	if err != nil {
		return nil, fmt.Errorf("read mountinfo: %w", err)
	}
	entries, err := parseMountinfo(data)
	if err != nil {
		return nil, err
	}
	links, err := listByUUIDFn() // best-effort: a missing UUID isn't fatal.
	labels, _ := listByLabelFn() // best-effort: falls back to the mount-point name below.
	if errors.Is(err, fs.ErrNotExist) {
		// No /dev/disk: a Flatpak sandbox. UDisks2 knows the same.
		links, labels, _ = listUDisksLinksFn()
	}

	var result []Info
	for _, e := range entries {
		if !strings.HasPrefix(e.Source, "/dev/") {
			continue
		}
		// The real filesystem label is preferred over the mount point's
		// base name: when two cards share a label, the desktop
		// auto-mounter dedup-suffixes the *directory* it mounts the
		// second one at (e.g. "/media/user/NO NAME1" when the first
		// unlabeled card already took "/media/user/NO NAME"), so the
		// directory name alone no longer matches the card's real label —
		// see TestLinuxVolumeNameDedupSuffixReturnsRealLabel.
		name := labelForDevice(e.Source, labels)
		if name == "" {
			name = filepath.Base(e.MountPoint)
		}
		result = append(result, Info{
			Name:       name,
			MountPoint: e.MountPoint,
			UUID:       uuidForDevice(e.Source, links),
			Removable:  isRemovableEntry(e),
		})
	}
	return result, nil
}

// ListRemovable lists the mounted removable volumes.
func ListRemovable() ([]Info, error) {
	all, err := listAllVolumes()
	if err != nil {
		return nil, err
	}
	var result []Info
	for _, v := range all {
		if v.Removable {
			result = append(result, v)
		}
	}
	return result, nil
}

// GetInfo describes the volume mounted at mountPoint.
func GetInfo(mountPoint string) (*Info, error) {
	mountPoint = filepath.Clean(mountPoint)
	all, err := listAllVolumes()
	if err != nil {
		return nil, err
	}
	for _, v := range all {
		if filepath.Clean(v.MountPoint) == mountPoint {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("no volume mounted at %s", mountPoint)
}

// FindByUUID returns the mounted volume with uuid, or nil if none is.
func FindByUUID(uuid string) (*Info, error) {
	uuid = strings.ToUpper(uuid)
	all, err := listAllVolumes()
	if err != nil {
		return nil, err
	}
	for _, v := range all {
		if strings.ToUpper(v.UUID) == uuid {
			return &v, nil
		}
	}
	return nil, nil
}

// deviceForMountPoint returns the device node (e.g. "/dev/sdb1") that
// mountPoint is mounted from, for Eject (eject_linux.go) to resolve into a
// UDisks2 block object path.
func deviceForMountPoint(mountPoint string) (string, error) {
	mountPoint = filepath.Clean(mountPoint)
	data, err := readMountinfoFn()
	if err != nil {
		return "", fmt.Errorf("read mountinfo: %w", err)
	}
	entries, err := parseMountinfo(data)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if filepath.Clean(e.MountPoint) == mountPoint {
			return e.Source, nil
		}
	}
	return "", fmt.Errorf("no volume mounted at %s", mountPoint)
}
