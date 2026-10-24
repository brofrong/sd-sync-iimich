package update

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// ErrManualUpdate means this copy of the app cannot update itself (on
// Linux: it does not run from an AppImage); nothing was downloaded or
// changed, and the user has to get the new version from GitHub.
var ErrManualUpdate = errors.New("this copy of the app cannot update itself; download the new version from GitHub")

// startFunc starts a program detached from this process and does not wait
// for it.
type startFunc func(name string, args ...string) error

// installerPattern names the installers runInstaller writes, so the ones
// earlier updates left behind can be found and removed.
const installerPattern = "SD-Sync-Immich-Setup-*.exe"

// runInstaller downloads the Windows installer into dir, checks its
// SHA-256 and starts it silently ("/S"), asking it to start the app again
// once installed ("/RELAUNCH"). The installer waits for this process to
// exit before it replaces the app. Nothing is written or started unless
// the checksum matches.
func (c *Client) runInstaller(ctx context.Context, rel *Release, dir string, start startFunc) error {
	setup, err := c.fetchVerified(ctx, rel)
	if err != nil {
		return err
	}
	// Installers of earlier updates: one still running (it never is, the
	// app it relaunched is this one) cannot be removed, which is fine.
	if old, err := filepath.Glob(filepath.Join(dir, installerPattern)); err == nil {
		for _, path := range old {
			_ = os.Remove(path)
		}
	}
	path, err := writeTemp(dir, installerPattern, setup)
	if err != nil {
		return err
	}
	if err := start(path, "/S", "/RELAUNCH"); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("start the installer: %w", err)
	}
	return nil
}

// relaunchScript waits for process $1 (the app that is quitting) to exit,
// then starts the AppImage $2: the new version must not sync alongside the
// old one. Paths go in as positional parameters, never into the script.
const relaunchScript = `while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; exec "$2" run`

// replaceAppImage replaces the AppImage the app runs from with the
// release's, checked against checksums.txt, and starts the new one once
// process pid (this one) has exited. Without an AppImage it returns
// ErrManualUpdate and touches nothing. The old AppImage stays in place
// unless the new one is complete and verified: it is written next to it
// and renamed over it (the running app keeps the old file open).
func (c *Client) replaceAppImage(ctx context.Context, rel *Release, appimage string, pid int, start startFunc) error {
	if appimage == "" {
		return fmt.Errorf("not running from an AppImage: %w", ErrManualUpdate)
	}
	// A symlink to the AppImage stays a symlink: its target is replaced.
	target, err := filepath.EvalSymlinks(appimage)
	if err != nil {
		return fmt.Errorf("AppImage %s: %v: %w", appimage, err, ErrManualUpdate)
	}
	if info, err := os.Stat(target); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("AppImage %s is not a file: %w", target, ErrManualUpdate)
	}
	if err := c.Install(ctx, rel, target); err != nil {
		return err
	}
	if err := start("/bin/sh", "-c", relaunchScript, "sh", strconv.Itoa(pid), target); err != nil {
		// The new version is installed; it starts the next time the app is
		// opened. Quitting now would leave the user without the app.
		return fmt.Errorf("the update is installed but cannot start; reopen the app: %w", err)
	}
	return nil
}

// writeTemp writes data to a new file in dir named after pattern (see
// os.CreateTemp) and returns its path. On an error no file is left.
func writeTemp(dir, pattern string, data []byte) (string, error) {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("create temporary file: %w", err)
	}
	path := tmp.Name()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}
