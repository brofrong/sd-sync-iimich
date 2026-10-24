//go:build !darwin

package main

import "fmt"

// platformCommand runs a command only this system has: there are none.
func platformCommand(name string) bool { return false }

func usage() {
	fmt.Println(`Usage:
  sd-sync-immich run          Run the app
  sd-sync-immich scan-test    Scan a card (dry-run): scan-test <mount point>`)
}

// beforeRun runs before the sync engine starts: nothing to do here.
func beforeRun() (exit bool) { return false }
