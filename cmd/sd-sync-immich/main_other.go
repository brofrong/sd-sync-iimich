//go:build !darwin

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/install"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// platformCommand runs a command only this system has: there are none.
func platformCommand(name string) bool { return false }

func usage() {
	fmt.Println(`Usage:
  sd-sync-immich [run]        Run the app (the default)
  sd-sync-immich scan-test    Scan a card (dry-run): scan-test <mount point>`)
}

// beforeRun runs before the sync engine starts: it brings an autostart
// entry of this executable up to date (see install.RefreshAutostart).
func beforeRun() (exit bool) {
	refreshAutostart()
	return false
}

// refreshAutostart rewrites the autostart entry of this executable when
// an earlier version (or the installer) wrote another command. Failing is
// harmless: the entry still starts the app, only with its window open.
func refreshAutostart() {
	exe, err := os.Executable()
	if err != nil {
		logx.Warn("autostart: cannot resolve executable", "err", err)
		return
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	rewritten, err := install.RefreshAutostart(exe)
	switch {
	case err != nil:
		logx.Warn("autostart: failed to update the autostart entry", "err", err)
	case rewritten:
		logx.Info("autostart: updated the autostart entry", "executable", exe)
	}
}
