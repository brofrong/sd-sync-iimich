// On Linux the app is an AppImage the user keeps wherever they like; this
// package only manages starting it at login: an XDG autostart entry,
// ~/.config/autostart/sd-sync-immich.desktop, that runs the AppImage.

package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// errNoAppImage is what SetAutostart returns outside an AppImage: the
// executable then is either a development build or inside the AppImage's
// mount, whose path changes on every launch.
var errNoAppImage = errors.New("starting at login needs the app to run from its AppImage")

// AppImage is the AppImage this process runs from, "" outside one. The
// AppImage runtime sets $APPIMAGE (its path) and $APPDIR (where it is
// mounted), but both are inherited by every program the app starts, so
// they count only when this executable is inside $APPDIR.
func AppImage() string {
	appimage, appdir := os.Getenv("APPIMAGE"), os.Getenv("APPDIR")
	if appimage == "" || appdir == "" {
		return ""
	}
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if !within(resolve(exe), resolve(appdir)) {
		return ""
	}
	return appimage
}

// resolve is path with symlinks resolved, or cleaned when that fails.
func resolve(path string) string {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		return r
	}
	return filepath.Clean(path)
}

// within reports whether path is inside dir.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}

// desktopFile is the autostart entry, under $XDG_CONFIG_HOME (default
// ~/.config).
func desktopFile() (string, error) {
	config, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(config, "autostart", "sd-sync-immich.desktop"), nil
}

// AutostartAvailable reports whether the app can start at login: when it
// runs from an AppImage, whose path the autostart entry can run.
func AutostartAvailable(executable string) bool {
	return AppImage() != ""
}

// AutostartEnabled reports whether the autostart entry exists, runs this
// AppImage and is not turned off (Hidden=true, or
// X-GNOME-Autostart-enabled / X-MATE-Autostart-enabled false, which the
// sessions' settings apps write).
func AutostartEnabled(executable string) bool {
	_, enabled := currentEntry()
	return enabled
}

// currentEntry reads the autostart entry of this AppImage: its content,
// and whether it is enabled (see AutostartEnabled).
func currentEntry() (file string, enabled bool) {
	target := AppImage()
	path, err := desktopFile()
	if target == "" || err != nil {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	keys := desktopEntryKeys(string(data))
	program, ok := execProgram(keys["Exec"])
	if !ok || !samePath(program, target) {
		return "", false
	}
	off := strings.EqualFold(keys["Hidden"], "true") ||
		strings.EqualFold(keys["X-GNOME-Autostart-enabled"], "false") ||
		strings.EqualFold(keys["X-MATE-Autostart-enabled"], "false")
	return string(data), !off
}

// RefreshAutostart rewrites the enabled autostart entry of this AppImage
// when it differs from the one SetAutostart writes today (an earlier
// version's, without --autostart), and reports whether it did. An entry
// that is turned off or runs another AppImage is left alone.
func RefreshAutostart(executable string) (rewritten bool, err error) {
	file, enabled := currentEntry()
	if !enabled {
		return false, nil
	}
	want, err := desktopEntry(AppImage())
	if err != nil || file == want {
		return false, err
	}
	if err := SetAutostart(true, executable); err != nil {
		return false, err
	}
	return true, nil
}

// SetAutostart writes (enabled) or removes the autostart entry that runs
// this AppImage at login. Enabling fails outside an AppImage.
func SetAutostart(enabled bool, executable string) error {
	path, err := desktopFile()
	if err != nil {
		return err
	}
	if !enabled {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove the autostart entry: %w", err)
		}
		return nil
	}
	target := AppImage()
	if target == "" {
		return errNoAppImage
	}
	entry, err := desktopEntry(target)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(entry), 0o644); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write the autostart entry: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write the autostart entry: %w", err)
	}
	return nil
}

// desktopEntry is the autostart entry that runs the AppImage at appimage.
func desktopEntry(appimage string) (string, error) {
	if !filepath.IsAbs(appimage) || strings.ContainsAny(appimage, "\x00\n\r") {
		return "", fmt.Errorf("cannot start %q at login", appimage)
	}
	return "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=SD Sync Immich\n" +
		"Exec=" + escapeDesktopString(quoteExecArg(appimage)+" run --autostart") + "\n" +
		"Terminal=false\n" +
		"X-GNOME-Autostart-enabled=true\n", nil
}

// quoteExecArg quotes arg as one argument of an Exec key (Desktop Entry
// Specification, "The Exec key"): in double quotes, with ", `, $ and \
// escaped by a backslash and % (a field code) doubled.
func quoteExecArg(arg string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range arg {
		switch r {
		case '"', '`', '$', '\\':
			b.WriteByte('\\')
		case '%':
			b.WriteByte('%')
		}
		b.WriteRune(r)
	}
	b.WriteByte('"')
	return b.String()
}

// escapeDesktopString escapes s as a string value of a desktop file,
// whose backslashes are themselves escapes (\\ is one backslash).
func escapeDesktopString(s string) string {
	return strings.ReplaceAll(s, `\`, `\\`)
}

// desktopEntryKeys returns the keys of the [Desktop Entry] group of a
// desktop file (the first value of each, without locale variants).
func desktopEntryKeys(file string) map[string]string {
	keys := map[string]string{}
	inEntry := false
	for _, line := range strings.Split(file, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !inEntry || !ok {
			continue
		}
		if key = strings.TrimSpace(key); keys[key] == "" {
			keys[key] = strings.TrimSpace(value)
		}
	}
	return keys
}

// execProgram returns the program (first argument) of an Exec value,
// undoing the escapes desktopEntry applies.
func execProgram(exec string) (string, bool) {
	if exec == "" {
		return "", false
	}
	exec = unescapeDesktopString(exec)
	if !strings.HasPrefix(exec, `"`) {
		program, _, _ := strings.Cut(exec, " ")
		return strings.ReplaceAll(program, "%%", "%"), program != ""
	}
	var b strings.Builder
	for i := 1; i < len(exec); i++ {
		switch c := exec[i]; {
		case c == '"':
			return b.String(), true
		case c == '\\' && i+1 < len(exec):
			i++
			b.WriteByte(exec[i])
		case c == '%' && i+1 < len(exec) && exec[i+1] == '%':
			i++
			b.WriteByte('%')
		default:
			b.WriteByte(c)
		}
	}
	return "", false // unterminated quote
}

// unescapeDesktopString undoes the escapes of a desktop file string value
// (\s, \n, \t, \r and \\).
func unescapeDesktopString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 == len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 's':
			b.WriteByte(' ')
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// UnderLaunchd reports whether launchd started this process: never
// outside macOS.
func UnderLaunchd() bool { return false }

// Translocated reports whether path is inside macOS App Translocation:
// never outside macOS.
func Translocated(path string) bool { return false }
