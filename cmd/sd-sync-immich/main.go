//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/install"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/tray"
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
		if err := install.Install(bin); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("Installed. App will start at login.")
	case "uninstall":
		if err := install.Uninstall(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("Uninstalled.")
	case "scan-test":
		runScanTest()
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Println(`Usage:
  sd-sync-immich run          Run with systray
  sd-sync-immich install      Install LaunchAgent autostart
  sd-sync-immich uninstall    Remove LaunchAgent
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		cancel()
		systrayQuit()
	}()

	go application.Run(ctx)
	tray.Run(application, cfg)
}

func systrayQuit() {
	// systray.Quit must be called from menu; signal handler exits process
	os.Exit(0)
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
