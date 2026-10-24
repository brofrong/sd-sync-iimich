package update

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startDetached starts a program in its own process group, without a
// console, and does not wait for it: it outlives this process. A seam for
// tests, which must never start an installer.
var startDetached startFunc = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Apply downloads the installer of rel into %TEMP%, checks its SHA-256 and
// starts it silently. quit means the app must now exit with code 0: the
// installer waits for that, replaces the app and starts the new version.
// On an error nothing was started and the app must keep running.
func Apply(ctx context.Context, c *Client, rel *Release) (quit bool, err error) {
	if err := c.runInstaller(ctx, rel, os.TempDir(), startDetached); err != nil {
		return false, err
	}
	return true, nil
}
