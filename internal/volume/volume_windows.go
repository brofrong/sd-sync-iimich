//go:build windows

package volume

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// Win32 calls behind seams so the logic above them can be tested without
// touching real drives.
var (
	// setErrorModeFn is windows.SetErrorMode behind a seam so
	// disableCriticalErrorDialogs's ordering and flag-preserving behavior
	// can be asserted without touching real process state.
	setErrorModeFn = windows.SetErrorMode

	getLogicalDrivesFn = func() (uint32, error) {
		return windows.GetLogicalDrives()
	}

	getDriveTypeFn = func(root string) uint32 {
		p, err := windows.UTF16PtrFromString(root)
		if err != nil {
			return windows.DRIVE_UNKNOWN
		}
		return windows.GetDriveType(p)
	}

	// getVolumeInformationFn returns the volume label and serial number for
	// root (e.g. "E:\"), as GetVolumeInformation reports them.
	getVolumeInformationFn = func(root string) (name string, serial uint32, err error) {
		p, err := windows.UTF16PtrFromString(root)
		if err != nil {
			return "", 0, err
		}
		nameBuf := make([]uint16, 261) // MAX_PATH + 1
		var serialNumber, maxComponentLen, fsFlags uint32
		if err := windows.GetVolumeInformation(p, &nameBuf[0], uint32(len(nameBuf)), &serialNumber, &maxComponentLen, &fsFlags, nil, 0); err != nil {
			return "", 0, err
		}
		return windows.UTF16ToString(nameBuf), serialNumber, nil
	}
)

// disableCriticalErrorDialogs sets SEM_FAILCRITICALERRORS for the process
// so that querying a removable or optical drive with no media in it (a
// GetVolumeInformation call below, on an empty card reader or CD/DVD tray)
// returns an error instead of raising Windows' blocking "There is no disk
// in the drive" dialog — which, with the watcher polling every
// pollInterval, would otherwise freeze the whole process behind a modal box
// no user asked for every couple of seconds. It preserves any error-mode
// flags something else in the process already set, by ORing the flag into
// whatever SetErrorMode reports as the previous mode, rather than
// overwriting it outright. Called before every GetVolumeInformation call
// (it's a single cheap syscall) rather than once via sync.Once, so it is
// also simple to assert in a test without cross-test state to reset.
func disableCriticalErrorDialogs() {
	old := setErrorModeFn(windows.SEM_FAILCRITICALERRORS)
	if full := old | windows.SEM_FAILCRITICALERRORS; full != windows.SEM_FAILCRITICALERRORS {
		setErrorModeFn(full)
	}
}

// formatSerial renders a volume serial number the way Windows Explorer
// shows it: two groups of 4 uppercase hex digits, e.g. 0x1234ABCD ->
// "1234-ABCD".
func formatSerial(serial uint32) string {
	return fmt.Sprintf("%04X-%04X", serial>>16, serial&0xFFFF)
}

// normalizeRoot turns a drive letter in any of its usual spellings ("e",
// "E:", "e:\", "E:\") into the canonical root path GetDriveType and
// GetVolumeInformation expect: "E:\".
func normalizeRoot(mountPoint string) string {
	s := strings.TrimSpace(mountPoint)
	if s == "" {
		return s
	}
	letter := strings.ToUpper(s[:1])
	return letter + `:\`
}

// listAllVolumes describes every drive letter currently assigned, removable
// or not. A drive letter with no media in it (e.g. an empty card reader) is
// silently skipped: GetVolumeInformation fails for it.
func listAllVolumes() ([]Info, error) {
	disableCriticalErrorDialogs()

	mask, err := getLogicalDrivesFn()
	if err != nil {
		return nil, fmt.Errorf("GetLogicalDrives: %w", err)
	}

	var result []Info
	for i := 0; i < 26; i++ {
		if mask&(uint32(1)<<uint(i)) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		name, serial, err := getVolumeInformationFn(root)
		if err != nil {
			continue
		}
		result = append(result, Info{
			Name:       name,
			MountPoint: root,
			UUID:       formatSerial(serial),
			Removable:  getDriveTypeFn(root) == windows.DRIVE_REMOVABLE,
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

// GetInfo describes the volume mounted at mountPoint (a drive letter such
// as "E:" or "E:\").
func GetInfo(mountPoint string) (*Info, error) {
	root := normalizeRoot(mountPoint)
	disableCriticalErrorDialogs()
	name, serial, err := getVolumeInformationFn(root)
	if err != nil {
		return nil, fmt.Errorf("GetVolumeInformation %s: %w", root, err)
	}
	return &Info{
		Name:       name,
		MountPoint: root,
		UUID:       formatSerial(serial),
		Removable:  getDriveTypeFn(root) == windows.DRIVE_REMOVABLE,
	}, nil
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
