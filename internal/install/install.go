// Package install manages how the app starts at login (AutostartAvailable,
// AutostartEnabled, SetAutostart) on every system, and on macOS installs
// the app bundle and migrates older installs.
package install

import (
	"errors"
	"path/filepath"
)

// ErrTranslocated means the app runs from App Translocation (a quarantined
// macOS download opened in place), whose path changes on every launch: it
// must be moved to an Applications folder before it can start at login.
var ErrTranslocated = errors.New("the app runs from App Translocation; move it to the Applications folder first")

// samePath reports whether a and b name the same file, following
// symlinks.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}
