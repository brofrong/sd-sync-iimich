package install

import "errors"

// Stubs until this system's autostart support lands.

// errUnsupported is what the stubs that can fail return.
var errUnsupported = errors.New("not supported on this system yet")

// AutostartAvailable reports whether the app can start at login: not yet.
func AutostartAvailable(executable string) bool { return false }

// AutostartEnabled reports whether the app starts at login: never yet.
func AutostartEnabled(executable string) bool { return false }

// SetAutostart turns starting at login on or off: not supported yet.
func SetAutostart(enabled bool, executable string) error { return errUnsupported }

// UnderLaunchd reports whether launchd started this process: never
// outside macOS.
func UnderLaunchd() bool { return false }

// Translocated reports whether path is inside macOS App Translocation:
// never outside macOS.
func Translocated(path string) bool { return false }

// OpenWhenExited restarts the app once this process has exited after an
// update: not supported yet.
func OpenWhenExited(bundlePath string) error { return errUnsupported }
