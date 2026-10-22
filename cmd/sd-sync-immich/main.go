//go:build darwin

package main

import (
	"fmt"
	"os"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/bundle"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/gui"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/install"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/version"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "run":
		runApp(false)
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
	case "scan-test":
		runScanTest()
	case "make-bundle":
		// Hidden: used by scripts/build-app.sh to wrap the built binary.
		makeBundle()
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`Usage:
  sd-sync-immich run          Run the menu bar app
  sd-sync-immich install      Install SD Sync Immich.app into ~/Applications
                              and start it at login
  sd-sync-immich uninstall    Remove the app, its LaunchAgent and v0.3.0's binary
  sd-sync-immich scan-test    Scan Brofrong SD card (dry-run)`)
}

func runApp(dryRun bool) {
	logPath, err := logx.Init()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logging:", err)
	} else {
		defer logx.Close()
		logx.Info("sd-sync-immich starting", "log", logPath)
	}

	if migrateFromBareBinary() {
		logx.Close()
		os.Exit(0)
	}
	takeOverLaunchAgent()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	dbPath, err := cfg.ResolvedDBPath()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	st, err := state.Open(dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer st.Close()

	application, err := app.New(cfg, st, dryRun)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	// gui.Run handles SIGINT/SIGTERM (through Wails) and exits the process
	// itself when the app quits; it only returns if the GUI fails to start.
	if err := gui.Run(application, cfg, st, version.Current); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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

func runScanTest() {
	mount := "/Volumes/Brofrong"
	if len(os.Args) > 2 {
		mount = os.Args[2]
	}

	cfg := config.Default()
	files, err := scanner.ScanVolume(mount, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	extCount := map[string]int{}
	for _, f := range files {
		ext := f.RelativePath
		if i := len(ext) - 1; i >= 0 {
			for j := len(ext) - 1; j >= 0; j-- {
				if ext[j] == '.' {
					ext = ext[j+1:]
					break
				}
			}
		}
		extCount[ext]++
	}

	info, _ := volume.GetInfo(mount)
	fmt.Printf("Volume: %s\n", mount)
	if info != nil {
		fmt.Printf("UUID: %s\n", info.UUID)
		fmt.Printf("Name: %s\n", info.Name)
	}
	fmt.Printf("Total media files: %d\n", len(files))
	for ext, n := range extCount {
		fmt.Printf("  .%s: %d\n", ext, n)
	}
	if len(files) > 0 {
		fmt.Printf("First: %s\n", files[0].RelativePath)
		fmt.Printf("Last: %s\n", files[len(files)-1].RelativePath)
	}
}
