//go:build windows

package volume

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// erasePollInterval and eraseRemountTimeout control how Erase polls for the
// freshly formatted volume; formatTimeout bounds format.com itself. They are
// variables so tests can shrink them instead of waiting on a real timeout.
var (
	erasePollInterval   = 500 * time.Millisecond
	eraseRemountTimeout = 60 * time.Second
	formatTimeout       = 10 * time.Minute
)

// driveRootRE matches a bare drive letter root, "E:" or "E:\" — nothing
// longer (a folder on the drive, a UNC share) is ever formatted.
var driveRootRE = regexp.MustCompile(`^[A-Za-z]:\\?$`)

// volumeGUIDPathRE matches a volume GUID path as
// GetVolumeNameForVolumeMountPoint reports it,
// "\\?\Volume{xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx}\": the volume (a
// partition, or the card itself when it has no partition table) the drive
// letter is mounted from. It is Erase's "device": for removable media the
// mount manager derives it from the reader, so it survives the format.
var volumeGUIDPathRE = regexp.MustCompile(`^\\\\\?\\Volume\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}\\$`)

// volumeDetails is what one GetVolumeInformation call reports about a
// volume: its label, serial number (the card's ID) and filesystem name
// ("exFAT", "FAT32", "FAT", "NTFS", ...).
type volumeDetails struct {
	Name       string
	Serial     uint32
	FileSystem string
}

// formatRequest is one format.com run: the drive root ("E:\"), the
// filesystem to create (as format.com's /FS: spells it) and the label.
type formatRequest struct {
	Root       string
	FileSystem string
	Label      string
}

// Erase's Win32 calls and the formatter behind seams, so its decisions can
// be tested without touching (let alone formatting) a real drive.
var (
	// getVolumeDetailsFn reads root's label, serial and filesystem in a
	// single GetVolumeInformation call, so the serial Erase checks and the
	// filesystem and label it formats with describe the same volume.
	getVolumeDetailsFn = func(root string) (volumeDetails, error) {
		p, err := windows.UTF16PtrFromString(root)
		if err != nil {
			return volumeDetails{}, err
		}
		nameBuf := make([]uint16, windows.MAX_PATH+1)
		fsBuf := make([]uint16, windows.MAX_PATH+1)
		var serial, maxComponentLen, fsFlags uint32
		if err := windows.GetVolumeInformation(p, &nameBuf[0], uint32(len(nameBuf)), &serial, &maxComponentLen, &fsFlags, &fsBuf[0], uint32(len(fsBuf))); err != nil {
			return volumeDetails{}, err
		}
		return volumeDetails{
			Name:       windows.UTF16ToString(nameBuf),
			Serial:     serial,
			FileSystem: windows.UTF16ToString(fsBuf),
		}, nil
	}

	// getVolumeGUIDPathFn returns the volume GUID path root is mounted from.
	getVolumeGUIDPathFn = func(root string) (string, error) {
		p, err := windows.UTF16PtrFromString(root)
		if err != nil {
			return "", err
		}
		buf := make([]uint16, 50) // "\\?\Volume{GUID}\" is 49 characters.
		if err := windows.GetVolumeNameForVolumeMountPoint(p, &buf[0], uint32(len(buf))); err != nil {
			return "", err
		}
		return windows.UTF16ToString(buf), nil
	}

	// systemDrivesFn lists the roots that hold Windows: %SystemDrive% and
	// the Windows directory's drive (normally both "C:\"). Empty when
	// neither can be found, which Erase treats as "anything may be the
	// system drive".
	systemDrivesFn = func() []string {
		var roots []string
		if d := os.Getenv("SystemDrive"); driveRootRE.MatchString(d) {
			roots = append(roots, normalizeRoot(d))
		}
		if dir, err := windows.GetSystemWindowsDirectory(); err == nil && len(dir) >= 2 && driveRootRE.MatchString(dir[:2]) {
			roots = append(roots, normalizeRoot(dir[:2]))
		}
		return roots
	}

	// isElevatedFn reports whether the app runs with administrator rights.
	isElevatedFn = func() bool {
		return windows.GetCurrentProcessToken().IsElevated()
	}

	// runFormatFn runs format.com for req and returns its combined output.
	runFormatFn = runFormatCom
)

// Erase reformats the removable volume at the drive letter mountPoint ("E:\")
// in place, keeping its filesystem (exFAT, FAT32 or FAT) and label.
//
// wantUUID must equal (case-insensitively) the volume's current serial
// number ("XXXX-XXXX"), read in the same GetVolumeInformation call as the
// filesystem and label it formats with, right before formatting: Erase is
// about to format the exact card the caller already verified. It refuses
// otherwise, and refuses an empty wantUUID or a volume without a serial.
//
// Erase also refuses — without ever starting the formatter — anything but
// a bare drive letter, the system drive (or any drive when the system drive
// can't be found), a drive that is not DRIVE_REMOVABLE, a letter without a
// volume GUID path, an empty or path-like label, and filesystems other than
// exFAT, FAT32 and FAT.
//
// format.com only runs with administrator rights, which the app never asks
// for: without them Erase returns ErrNeedsAdmin without starting it, and an
// "access denied" from format.com is ErrNeedsAdmin too.
//
// After format.com succeeds, Erase polls the same drive letter until it is
// still removable, still the same volume GUID path (the same device), and
// reports a non-zero serial different from the old one. If that never
// happens within eraseRemountTimeout, the error wraps ErrErasedNotRemounted:
// the card was erased, but Erase could not find the result.
func Erase(mountPoint, wantUUID string) (Info, error) {
	if !driveRootRE.MatchString(mountPoint) {
		return Info{}, fmt.Errorf("erase %q: refusing to erase anything but a drive letter like E:\\", mountPoint)
	}
	root := normalizeRoot(mountPoint)

	// The system drive gate comes first and is unconditional: nothing about
	// the volume (even a serial the caller verified) can override it.
	system := systemDrivesFn()
	if len(system) == 0 {
		return Info{}, fmt.Errorf("erase %s: refusing to erase: the system drive is unknown", root)
	}
	for _, s := range system {
		if strings.EqualFold(normalizeRoot(s), root) {
			return Info{}, fmt.Errorf("erase %s: refusing to erase the system drive", root)
		}
	}

	disableCriticalErrorDialogs()

	if dt := getDriveTypeFn(root); dt != windows.DRIVE_REMOVABLE {
		return Info{}, fmt.Errorf("erase %s: refusing to erase a non-removable drive (type %d)", root, dt)
	}

	guid, err := getVolumeGUIDPathFn(root)
	if err != nil {
		return Info{}, fmt.Errorf("erase %s: refusing to erase: no volume name: %w", root, err)
	}
	if !volumeGUIDPathRE.MatchString(guid) {
		return Info{}, fmt.Errorf("erase %s: refusing to erase volume %q, want a volume GUID path", root, guid)
	}

	if wantUUID == "" {
		return Info{}, fmt.Errorf("erase %s: refusing to erase without a card identity to verify", root)
	}

	d, err := getVolumeDetailsFn(root)
	if err != nil {
		return Info{}, fmt.Errorf("GetVolumeInformation %s: %w", root, err)
	}
	oldUUID := formatSerial(d.Serial)
	if d.Serial == 0 || !strings.EqualFold(oldUUID, wantUUID) {
		return Info{}, fmt.Errorf("erase %s: card identity mismatch, want serial %s", root, wantUUID)
	}

	if !validEraseLabel(d.Name) {
		return Info{}, fmt.Errorf("erase %s: refusing to erase volume with invalid name %q", root, d.Name)
	}

	var fs string
	switch {
	case strings.EqualFold(d.FileSystem, "exFAT"):
		fs = "exFAT"
	case strings.EqualFold(d.FileSystem, "FAT32"):
		fs = "FAT32"
	case strings.EqualFold(d.FileSystem, "FAT"):
		fs = "FAT"
	default:
		return Info{}, fmt.Errorf("erase %s: refusing to erase filesystem %q, want exFAT, FAT32 or FAT", root, d.FileSystem)
	}

	if !isElevatedFn() {
		return Info{}, fmt.Errorf("erase %s: format.com needs an elevated process: %w", root, ErrNeedsAdmin)
	}

	req := formatRequest{Root: root, FileSystem: fs, Label: d.Name}
	logx.Info("volume: erasing", "mountPoint", root, "device", guid, "format", fs, "name", d.Name)

	out, err := runFormatFn(req)
	if err != nil {
		if formatNeedsAdmin(out, err) {
			return Info{}, fmt.Errorf("format %s: %w: %v: %s", root, ErrNeedsAdmin, err, strings.TrimSpace(string(out)))
		}
		return Info{}, fmt.Errorf("format %s: %w: %s", root, err, strings.TrimSpace(string(out)))
	}

	info, err := pollForRemount(root, guid, oldUUID)
	if err != nil {
		return Info{}, fmt.Errorf("erase %s: %w", root, err)
	}

	logx.Info("volume: erase complete", "mountPoint", info.MountPoint, "uuid", info.UUID)
	return info, nil
}

// validEraseLabel reports whether label may be formatted with: not empty,
// nothing path-like ("/", "\", ".."), and nothing that would break
// format.com's command line or its stdin answers (a quote, a control
// character).
func validEraseLabel(label string) bool {
	if label == "" || strings.ContainsAny(label, `/\"`) || strings.Contains(label, "..") {
		return false
	}
	for _, r := range label {
		if r < 0x20 || r == 0x7F {
			return false
		}
	}
	return true
}

// formatArgs is format.com's command line for req: the drive letter, the
// same filesystem and label, a quick format, and /Y so it does not ask for
// confirmation.
func formatArgs(req formatRequest) []string {
	return []string{
		strings.TrimSuffix(req.Root, `\`),
		"/FS:" + req.FileSystem,
		"/V:" + req.Label,
		"/Q",
		"/Y",
	}
}

// formatStdin is what format.com reads if it prompts anyway: two lines
// holding the label Erase verified. One answers "Insert new disk ... and
// press ENTER when ready", the other "Enter current volume label for drive
// E:", in whichever order they come; anything else then reads end of
// input. It never answers a Y/N question with "Y".
func formatStdin(req formatRequest) string {
	return req.Label + "\r\n" + req.Label + "\r\n"
}

// runFormatCom runs %SystemRoot%\System32\format.com for req, with no
// console window, its prompts answered by formatStdin, and killed after
// formatTimeout.
func runFormatCom(req formatRequest) ([]byte, error) {
	sysDir, err := windows.GetSystemDirectory()
	if err != nil {
		return nil, fmt.Errorf("find format.com: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), formatTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, filepath.Join(sysDir, "format.com"), formatArgs(req)...)
	cmd.Stdin = strings.NewReader(formatStdin(req))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("timed out after %s: %w", formatTimeout, err)
	}
	return out, err
}

// formatNeedsAdmin reports whether format.com failed for lack of
// administrator rights: Windows refused to start it, or it said so ("Access
// Denied as you do not have sufficient privileges ... invoke this utility
// running in elevated mode").
func formatNeedsAdmin(out []byte, err error) bool {
	if errors.Is(err, windows.ERROR_ELEVATION_REQUIRED) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return true
	}
	s := strings.ToLower(string(out))
	return strings.Contains(s, "access denied") || strings.Contains(s, "elevated mode") || strings.Contains(s, "sufficient privileges")
}

// pollForRemount looks for the freshly formatted volume at root: the same
// device (volume GUID path guid), still removable, with a serial different
// from oldUUID.
func pollForRemount(root, guid, oldUUID string) (Info, error) {
	attempts := int(eraseRemountTimeout / erasePollInterval)
	if attempts < 1 {
		attempts = 1
	}

	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			time.Sleep(erasePollInterval)
		}
		if info, ok := probeRemount(root, guid, oldUUID); ok {
			return info, nil
		}
	}

	return Info{}, fmt.Errorf("device %s: %w", guid, ErrErasedNotRemounted)
}

// probeRemount makes one look at root and reports whether it holds the
// freshly formatted volume.
func probeRemount(root, guid, oldUUID string) (Info, bool) {
	disableCriticalErrorDialogs()
	if getDriveTypeFn(root) != windows.DRIVE_REMOVABLE {
		return Info{}, false
	}
	gotGUID, err := getVolumeGUIDPathFn(root)
	if err != nil || !strings.EqualFold(gotGUID, guid) {
		return Info{}, false
	}
	d, err := getVolumeDetailsFn(root)
	if err != nil || d.Serial == 0 {
		return Info{}, false
	}
	uuid := formatSerial(d.Serial)
	if strings.EqualFold(uuid, oldUUID) {
		return Info{}, false
	}
	return Info{
		Name:       d.Name,
		MountPoint: root,
		UUID:       uuid,
		Removable:  true,
	}, true
}
