package update

import (
	"context"
	"os"
	"os/exec"
	"syscall"
)

// startDetached starts a program in its own session and does not wait for
// it: it outlives this process. A seam for tests, which must never start
// a program.
var startDetached startFunc = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Apply replaces the AppImage the app runs from ($APPIMAGE) with rel's,
// checked against checksums.txt, and starts the new one once this process
// has exited. quit means the app must now exit with code 0. Outside an
// AppImage it returns ErrManualUpdate and changes nothing; on any other
// error the old AppImage is in place, or (if only the restart failed) the
// new one is, and the app must keep running.
func Apply(ctx context.Context, c *Client, rel *Release) (quit bool, err error) {
	if err := c.replaceAppImage(ctx, rel, os.Getenv("APPIMAGE"), os.Getpid(), startDetached); err != nil {
		return false, err
	}
	return true, nil
}
