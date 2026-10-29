// On Windows the installer (NSIS) puts the app in place; this package
// only manages starting it at login: a value in the user's Run key.

package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	// runKeyPath is the per-user key whose values Windows runs at login.
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	// runValueName is the app's value in it; the installer writes and its
	// uninstaller removes the same one.
	runValueName = "SD Sync Immich"
)

// runKey reads and writes string values of the Run key. A seam for tests,
// which must never touch the real registry.
type runKey interface {
	// get returns the value name, or an error wrapping os.ErrNotExist.
	get(name string) (string, error)
	set(name, value string) error
	// remove deletes the value name; a missing one is not an error.
	remove(name string) error
}

var runValues runKey = registryRun{}

// registryRun is HKCU\Software\Microsoft\Windows\CurrentVersion\Run.
type registryRun struct{}

func (registryRun) get(name string) (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", notExist(err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue(name)
	return v, notExist(err)
}

func (registryRun) set(name, value string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(name, value)
}

func (registryRun) remove(name string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// notExist makes a missing key or value an os.ErrNotExist.
func notExist(err error) error {
	if errors.Is(err, registry.ErrNotExist) {
		return fmt.Errorf("%w: %v", os.ErrNotExist, err)
	}
	return err
}

// runCommand is the Run value that starts executable at login: its quoted
// path and "run --autostart" (so the window stays closed).
func runCommand(executable string) string {
	return `"` + executable + `" run --autostart`
}

// runProgram is the program a Run value starts: the quoted path at its
// start, or everything before the first space when it is not quoted.
func runProgram(value string) string {
	value = strings.TrimSpace(value)
	if rest, ok := strings.CutPrefix(value, `"`); ok {
		program, _, _ := strings.Cut(rest, `"`)
		return program
	}
	program, _, _ := strings.Cut(value, " ")
	return program
}

// sameWindowsPath reports whether a and b name the same file: Windows
// paths are case-insensitive.
func sameWindowsPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// AutostartAvailable reports whether the app can start at login: always,
// it runs from where the installer put it.
func AutostartAvailable(executable string) bool { return true }

// AutostartEnabled reports whether the Run value starts executable.
func AutostartEnabled(executable string) bool {
	value, err := runValues.get(runValueName)
	return err == nil && sameWindowsPath(runProgram(value), executable)
}

// SetAutostart writes (enabled) or removes the Run value that starts
// executable at login.
func SetAutostart(enabled bool, executable string) error {
	if !enabled {
		if err := runValues.remove(runValueName); err != nil {
			return fmt.Errorf("remove the autostart registry value: %w", err)
		}
		return nil
	}
	if executable == "" || strings.ContainsRune(executable, '"') {
		return fmt.Errorf("cannot start %q at login", executable)
	}
	if err := runValues.set(runValueName, runCommand(executable)); err != nil {
		return fmt.Errorf("write the autostart registry value: %w", err)
	}
	return nil
}

// RefreshAutostart rewrites the Run value when it starts executable with
// another command than SetAutostart writes today (an earlier version's, or
// the installer's), and reports whether it did. A value that starts
// another program is left alone, and so is a missing one.
func RefreshAutostart(executable string) (rewritten bool, err error) {
	value, err := runValues.get(runValueName)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !sameWindowsPath(runProgram(value), executable) || value == runCommand(executable) {
		return false, nil
	}
	if err := SetAutostart(true, executable); err != nil {
		return false, err
	}
	return true, nil
}

// UnderLaunchd reports whether launchd started this process: never
// outside macOS.
func UnderLaunchd() bool { return false }

// Translocated reports whether path is inside macOS App Translocation:
// never outside macOS.
func Translocated(path string) bool { return false }

// Flatpak reports whether the app runs as a Flatpak: never outside Linux.
func Flatpak() bool { return false }

// AppImage is the AppImage this process runs from: never outside Linux.
func AppImage() string { return "" }
