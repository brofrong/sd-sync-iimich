//go:build windows

package gui

// trayAvailable reports whether this platform has a tray to reopen the
// window from: Windows always has a notification area.
func trayAvailable() bool { return true }
