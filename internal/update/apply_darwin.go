package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/bundle"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/install"
)

// Seams for tests: no test may run codesign or open for real.
var (
	currentExecutable = func() (string, error) {
		executable, err := os.Executable()
		if err != nil {
			return "", fmt.Errorf("resolve executable: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(executable); err == nil {
			executable = resolved
		}
		return executable, nil
	}
	resignBundle   = bundle.Resign
	underLaunchd   = install.UnderLaunchd
	openWhenExited = install.OpenWhenExited
)

// Apply installs rel over the running executable and re-signs the app
// bundle. quit means the app must now exit with the code that makes
// launchd start the new version; when launchd did not start the app, the
// bundle is reopened once this process has exited. On an error the app
// must keep running.
func Apply(ctx context.Context, c *Client, rel *Release) (quit bool, err error) {
	executable, err := currentExecutable()
	if err != nil {
		return false, err
	}
	if err := c.Install(ctx, rel, executable); err != nil {
		return false, err
	}
	if err := prepareRestart(executable); err != nil {
		return false, fmt.Errorf("the update is installed but the app cannot restart: %w", err)
	}
	return true, nil
}

// prepareRestart runs after an update replaced executable. Inside a bundle
// it re-signs the bundle, whose signature the new executable broke; if
// that fails the app must not quit, as the bundle would not launch again.
// When launchd did not start the app it will not restart it either, so the
// bundle is reopened once this process has exited.
func prepareRestart(executable string) error {
	bundlePath, ok := bundle.Containing(executable)
	if !ok {
		return nil
	}
	if err := resignBundle(bundlePath); err != nil {
		return fmt.Errorf("re-sign %s: %w", bundlePath, err)
	}
	if underLaunchd() {
		return nil
	}
	return openWhenExited(bundlePath)
}
