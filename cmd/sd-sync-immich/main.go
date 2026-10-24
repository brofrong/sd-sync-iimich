package main

import (
	"fmt"
	"os"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/gui"
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
	case "scan-test":
		runScanTest()
	default:
		if !platformCommand(os.Args[1]) {
			usage()
			os.Exit(1)
		}
	}
}

func runApp(dryRun bool) {
	logPath, err := logx.Init()
	if err != nil {
		fmt.Fprintln(os.Stderr, "logging:", err)
	} else {
		defer logx.Close()
		logx.Info("sd-sync-immich starting", "log", logPath)
	}

	if beforeRun() {
		logx.Close()
		os.Exit(0)
	}

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
