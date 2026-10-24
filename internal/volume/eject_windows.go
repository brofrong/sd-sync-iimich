//go:build windows

package volume

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// Device I/O control codes used to eject a volume's media without
// administrator rights. These aren't exposed by golang.org/x/sys/windows,
// so they're declared here from the Windows SDK headers (winioctl.h):
//
//   - FSCTL_LOCK_VOLUME: refuses if another process has the volume open,
//     which is how a "busy" card is detected.
//   - FSCTL_DISMOUNT_VOLUME: detaches the file system from the volume.
//   - IOCTL_STORAGE_EJECT_MEDIA: tells the underlying storage device (the
//     card reader) to eject its media, same as Explorer's "Eject".
//
// This sequence is the documented, elevation-free way to eject removable
// media opened with a plain CreateFile handle (no admin token, no UAC
// prompt) — the alternative, CM_Request_Device_EjectW, needs a PnP device
// instance handle resolved via SetupAPI, which is considerably more code
// for the same standard-user outcome.
const (
	fsctlLockVolume        = 0x00090018
	fsctlDismountVolume    = 0x00090020
	ioctlStorageEjectMedia = 0x002D4808
)

// Win32 calls behind seams so Eject's error handling can be tested without
// a real volume.
var (
	createFileFn      = windows.CreateFile
	deviceIoControlFn = func(handle windows.Handle, code uint32) error {
		var bytesReturned uint32
		return windows.DeviceIoControl(handle, code, nil, 0, nil, 0, &bytesReturned, nil)
	}
	closeHandleFn = windows.CloseHandle
)

// Eject ejects the removable media mounted at mountPoint (a drive letter
// such as "E:\") without requiring administrator rights. A volume that is
// still in use (e.g. a file open on it) fails at the lock step; the
// returned error says so.
func Eject(mountPoint string) error {
	root := normalizeRoot(mountPoint)
	devicePath := `\\.\` + strings.TrimSuffix(root, `\`)

	pathPtr, err := windows.UTF16PtrFromString(devicePath)
	if err != nil {
		return fmt.Errorf("eject %s: %w", root, err)
	}

	handle, err := createFileFn(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return fmt.Errorf("eject %s: open volume: %w", root, err)
	}
	defer closeHandleFn(handle)

	if err := deviceIoControlFn(handle, fsctlLockVolume); err != nil {
		return fmt.Errorf("eject %s: card is in use (lock volume failed): %w", root, err)
	}
	if err := deviceIoControlFn(handle, fsctlDismountVolume); err != nil {
		return fmt.Errorf("eject %s: dismount volume failed: %w", root, err)
	}
	if err := deviceIoControlFn(handle, ioctlStorageEjectMedia); err != nil {
		return fmt.Errorf("eject %s: eject media failed: %w", root, err)
	}
	return nil
}
