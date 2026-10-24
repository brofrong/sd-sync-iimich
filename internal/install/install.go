// Package install manages how the app starts at login (AutostartAvailable,
// AutostartEnabled, SetAutostart) on every system, and on macOS installs
// the app bundle and migrates older installs.
package install

import "errors"

// ErrTranslocated means the app runs from App Translocation (a quarantined
// macOS download opened in place), whose path changes on every launch: it
// must be moved to an Applications folder before it can start at login.
var ErrTranslocated = errors.New("the app runs from App Translocation; move it to the Applications folder first")
