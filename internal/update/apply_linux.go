package update

import (
	"context"
	"os"
	"os/exec"
	"syscall"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/install"
)

// startDetached starts a process in its own session and does not wait for
// it: it outlives this process. A seam for tests, which must never start
// a program.
var startDetached startFunc = func(p process) error {
	cmd := exec.Command(p.name, p.args...)
	cmd.Env = p.env
	cmd.Dir = p.dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Apply replaces the AppImage the app runs from (install.AppImage) with
// rel's, checked against checksums.txt, and starts the new one once this
// process has exited. quit means the app must now exit with code 0.
// Outside an AppImage it returns ErrManualUpdate and changes nothing; on
// any other error the old AppImage is in place, or (if only the restart
// failed) the new one is, and the app must keep running.
func Apply(ctx context.Context, c *Client, rel *Release) (quit bool, err error) {
	if err := c.replaceAppImage(ctx, rel, install.AppImage(), os.Getenv("APPDIR"), os.Getpid(), startDetached); err != nil {
		return false, err
	}
	return true, nil
}
