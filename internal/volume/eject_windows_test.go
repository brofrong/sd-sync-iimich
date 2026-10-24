//go:build windows

package volume

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// withEjectSeams saves Eject's Win32 seams and restores them after the
// test.
func withEjectSeams(t *testing.T) {
	t.Helper()
	origCreate := createFileFn
	origIoctl := deviceIoControlFn
	origClose := closeHandleFn
	t.Cleanup(func() {
		createFileFn = origCreate
		deviceIoControlFn = origIoctl
		closeHandleFn = origClose
	})
}

func TestEjectSucceeds(t *testing.T) {
	withEjectSeams(t)

	var gotPath string
	var closed bool
	var ioctlCodes []uint32

	createFileFn = func(name *uint16, access uint32, mode uint32, sa *windows.SecurityAttributes, createmode uint32, attrs uint32, templatefile windows.Handle) (windows.Handle, error) {
		gotPath = windows.UTF16PtrToString(name)
		return windows.Handle(1), nil
	}
	deviceIoControlFn = func(handle windows.Handle, code uint32) error {
		ioctlCodes = append(ioctlCodes, code)
		return nil
	}
	closeHandleFn = func(handle windows.Handle) error {
		closed = true
		return nil
	}

	if err := Eject(`E:\`); err != nil {
		t.Fatalf("Eject() error = %v", err)
	}

	if gotPath != `\\.\E:` {
		t.Errorf("createFileFn path = %q, want %q", gotPath, `\\.\E:`)
	}
	wantCodes := []uint32{fsctlLockVolume, fsctlDismountVolume, ioctlStorageEjectMedia}
	if len(ioctlCodes) != len(wantCodes) {
		t.Fatalf("ioctl codes = %v, want %v", ioctlCodes, wantCodes)
	}
	for i, c := range wantCodes {
		if ioctlCodes[i] != c {
			t.Errorf("ioctl[%d] = %#x, want %#x", i, ioctlCodes[i], c)
		}
	}
	if !closed {
		t.Error("Eject() did not close the volume handle")
	}
}

func TestEjectBusyVolumeFails(t *testing.T) {
	withEjectSeams(t)

	createFileFn = func(name *uint16, access uint32, mode uint32, sa *windows.SecurityAttributes, createmode uint32, attrs uint32, templatefile windows.Handle) (windows.Handle, error) {
		return windows.Handle(1), nil
	}
	deviceIoControlFn = func(handle windows.Handle, code uint32) error {
		if code == fsctlLockVolume {
			return errors.New("ERROR_SHARING_VIOLATION")
		}
		t.Fatalf("unexpected ioctl %#x after lock failed", code)
		return nil
	}
	closeHandleFn = func(handle windows.Handle) error { return nil }

	err := Eject(`E:\`)
	if err == nil {
		t.Fatal("Eject() error = nil, want error for a busy volume")
	}
	if !strings.Contains(err.Error(), "in use") {
		t.Fatalf("Eject() error = %q, want it to mention the card being in use", err.Error())
	}
}

func TestEjectOpenFailureReportsError(t *testing.T) {
	withEjectSeams(t)

	createFileFn = func(name *uint16, access uint32, mode uint32, sa *windows.SecurityAttributes, createmode uint32, attrs uint32, templatefile windows.Handle) (windows.Handle, error) {
		return windows.Handle(0), windows.ERROR_FILE_NOT_FOUND
	}

	err := Eject(`E:\`)
	if err == nil {
		t.Fatal("Eject() error = nil, want error when the volume can't be opened")
	}
}
