package update

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startDetached starts a process in its own process group, without a
// console, and does not wait for it: it outlives this process. A seam for
// tests, which must never start an installer.
var startDetached startFunc = func(p process) error {
	return startWithFlags(windows.CREATE_NEW_PROCESS_GROUP|windows.DETACHED_PROCESS, func(flags uint32) error {
		cmd := exec.Command(p.name, p.args...)
		cmd.Env = p.env
		cmd.Dir = p.dir
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	})
}

// startWithFlags starts a process with try, first also breaking away from
// this process's job (a job, such as a launcher's, may kill its processes
// when this one exits), then, if the job forbids that, without.
func startWithFlags(flags uint32, try func(flags uint32) error) error {
	err := try(flags | windows.CREATE_BREAKAWAY_FROM_JOB)
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return try(flags)
	}
	return err
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
