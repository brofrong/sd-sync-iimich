// Package bundle builds and signs the macOS application bundle,
// "SD Sync Immich.app", around the sd-sync-immich executable.
//
// The bundle is signed ad hoc ("codesign --sign -"): there is no Developer
// ID, so the first launch of a downloaded copy still needs right-click →
// Open (or removing the quarantine attribute), but the signature keeps the
// bundle valid for launchd and Gatekeeper once it is allowed. Replacing the
// executable (an update) breaks the signature, so it must be followed by
// Resign.
package bundle

import (
	_ "embed"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	// Name is the bundle's directory name.
	Name = "SD Sync Immich.app"
	// Executable is the file name of the executable in Contents/MacOS.
	Executable = "sd-sync-immich"
	// ID is the bundle identifier (also the LaunchAgent label).
	ID = "com.sd-sync-immich"
)

// displayName is the application's name in Finder and the menu bar.
var displayName = strings.TrimSuffix(Name, ".app")

//go:embed Info.plist.tmpl
var infoPlistTemplate string

// icons is Contents/Resources/icons.icns, rendered from build/icon/app.svg
// by scripts/icons.sh.
//
//go:embed icons.icns
var icons []byte

var infoPlist = template.Must(template.New("Info.plist").Funcs(template.FuncMap{
	"xml": func(s string) (string, error) {
		var b strings.Builder
		err := xml.EscapeText(&b, []byte(s))
		return b.String(), err
	},
}).Parse(infoPlistTemplate))

// runCommand runs an external command and returns its combined output.
// Tests replace it.
var runCommand = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// Create builds a signed bundle at dest around a copy of binary, with
// version in its Info.plist. The bundle is built next to dest and moved
// into place only once it is signed, so an existing bundle at dest is
// replaced only by a complete one. binary may be the executable of the
// bundle being replaced.
func Create(binary, dest, version string) error {
	data, err := os.ReadFile(binary)
	if err != nil {
		return fmt.Errorf("read executable: %w", err)
	}
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".sd-sync-immich-bundle-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)

	// Signed under its final name: the name is part of what users see in
	// codesign's output, and moving a signed bundle keeps it valid.
	built := filepath.Join(staging, filepath.Base(dest))
	if err := layOut(built, data, version); err != nil {
		return err
	}
	// Extended attributes (quarantine, Finder info) make codesign fail
	// with "resource fork, Finder information, or similar detritus".
	if out, err := runCommand("xattr", "-cr", built); err != nil {
		return commandError("xattr -cr", built, out, err)
	}
	if err := Resign(built); err != nil {
		return err
	}
	return replace(built, dest, filepath.Join(staging, "previous"))
}

// layOut writes the bundle's files under dir.
func layOut(dir string, executable []byte, version string) error {
	contents := filepath.Join(dir, "Contents")
	for _, d := range []string{"MacOS", "Resources"} {
		if err := os.MkdirAll(filepath.Join(contents, d), 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(contents, "MacOS", Executable), executable, 0o755); err != nil {
		return fmt.Errorf("write executable: %w", err)
	}

	var plist strings.Builder
	if err := infoPlist.Execute(&plist, map[string]string{
		"DisplayName":  displayName,
		"Executable":   Executable,
		"ID":           ID,
		"Version":      version,
		"ShortVersion": strings.TrimPrefix(version, "v"),
	}); err != nil {
		return fmt.Errorf("render Info.plist: %w", err)
	}
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist.String()), 0o644); err != nil {
		return fmt.Errorf("write Info.plist: %w", err)
	}
	if err := os.WriteFile(filepath.Join(contents, "Resources", "icons.icns"), icons, 0o644); err != nil {
		return fmt.Errorf("write icons: %w", err)
	}
	return nil
}

// replace moves built to dest. An existing dest is first moved to
// previous (inside the staging directory, removed by the caller) and is
// put back if the move fails.
func replace(built, dest, previous string) error {
	hadPrevious := true
	if err := os.Rename(dest, previous); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("move old bundle aside: %w", err)
		}
		hadPrevious = false
	}
	if err := os.Rename(built, dest); err != nil {
		if hadPrevious {
			_ = os.Rename(previous, dest)
		}
		return fmt.Errorf("move bundle into place: %w", err)
	}
	return nil
}

// Resign signs the bundle ad hoc (after its executable was replaced) and
// verifies the signature.
func Resign(bundlePath string) error {
	if out, err := runCommand("codesign", "--force", "--deep", "--sign", "-", bundlePath); err != nil {
		return commandError("codesign", bundlePath, out, err)
	}
	if out, err := runCommand("codesign", "--verify", "--deep", "--strict", bundlePath); err != nil {
		return commandError("codesign --verify", bundlePath, out, err)
	}
	return nil
}

func commandError(command, path string, out []byte, err error) error {
	if msg := strings.TrimSpace(string(out)); msg != "" {
		return fmt.Errorf("%s %s: %w: %s", command, path, err, msg)
	}
	return fmt.Errorf("%s %s: %w", command, path, err)
}

// Containing returns the bundle that executable belongs to, if it is a
// bundle's main executable (<bundle>.app/Contents/MacOS/<file>).
func Containing(executable string) (string, bool) {
	macOS := filepath.Dir(filepath.Clean(executable))
	contents := filepath.Dir(macOS)
	app := filepath.Dir(contents)
	if filepath.Base(macOS) != "MacOS" || filepath.Base(contents) != "Contents" ||
		!strings.HasSuffix(app, ".app") || filepath.Base(app) == ".app" {
		return "", false
	}
	return app, true
}
