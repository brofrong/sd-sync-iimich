package main

import (
	"fmt"
	"os"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/bundle"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/install"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/version"
)

// platformCommand runs the macOS-only command name (install, uninstall and
// the hidden make-bundle) and reports whether it was one.
func platformCommand(name string) bool {
	switch name {
	case "install":
		bin, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		bundlePath, err := install.Install(bin, version.Current)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("Installed %s. It is running now and will start at login.\n", bundlePath)
	case "uninstall":
		if err := install.Uninstall(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("Uninstalled.")
	case "make-bundle":
		// Hidden: used by scripts/build-app.sh to wrap the built binary.
		makeBundle()
	default:
		return false
	}
	return true
}

func usage() {
	fmt.Println(`Usage:
  sd-sync-immich run          Run the menu bar app
  sd-sync-immich install      Install SD Sync Immich.app into ~/Applications
                              and start it at login
  sd-sync-immich uninstall    Remove the app, its LaunchAgent and v0.3.0's binary
  sd-sync-immich scan-test    Scan Brofrong SD card (dry-run)`)
}

// beforeRun runs before the sync engine starts. It moves a v0.3.0
// install to the app bundle and takes over the LaunchAgent; it returns
// true when this process must exit with code 0 (see
// migrateFromBareBinary).
func beforeRun() (exit bool) {
	if migrateFromBareBinary() {
		return true
	}
	takeOverLaunchAgent()
	return false
}

// migrateFromBareBinary moves a v0.3.0 install (a bare binary run by the
// LaunchAgent, just replaced by v0.3.0's updater) to the app bundle. It
// returns true when the bundle is installed and the LaunchAgent will start
// it, so this process must exit with code 0. On failure it keeps running
// as before.
func migrateFromBareBinary() bool {
	exe, err := os.Executable()
	if err != nil {
		logx.Warn("migration: cannot resolve executable", "err", err)
		return false
	}
	migrated, err := install.MigrateIfNeeded(exe, version.Current)
	if err != nil {
		logx.Error("migration to the app bundle failed; running the bare binary", "err", err)
		return false
	}
	if migrated {
		logx.Info("migrated to the app bundle; the LaunchAgent restarts it", "from", exe)
	}
	return migrated
}

// takeOverLaunchAgent points the LaunchAgent at this bundle when it still
// runs an older install (v0.3.0's bare binary, typically: its user opened
// the downloaded bundle), and stops that install, so two sync engines never
// share the database. It must run before the sync engine starts.
func takeOverLaunchAgent() {
	exe, err := os.Executable()
	if err != nil {
		logx.Warn("launch agent: cannot resolve executable", "err", err)
		return
	}
	res, err := install.TakeOverAgent(exe)
	switch {
	case err != nil:
		logx.Error("failed to take over the LaunchAgent", "from", res.Previous, "err", err)
	case res.Done:
		logx.Info("LaunchAgent now starts this app; stopped the previous install", "from", res.Previous, "to", exe)
	case res.Kept:
		logx.Warn("LaunchAgent starts another copy of the app; leaving it", "program", res.Previous, "this", exe)
	case res.Translocated:
		logx.Warn("app runs from App Translocation; LaunchAgent left alone until it is moved to Applications", "program", res.Previous, "this", exe)
	}
}

// makeBundle wraps this executable into an app bundle at the given path.
func makeBundle() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: sd-sync-immich make-bundle <dest.app>")
		os.Exit(2)
	}
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := bundle.Create(exe, os.Args[2], version.Current); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
