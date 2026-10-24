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

// appImage is the AppImage this process runs from ($APPIMAGE, set by the
// AppImage runtime), "" outside one.
func appImage() string {
	return os.Getenv("APPIMAGE")
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
	return appImage() != ""
}

// AutostartEnabled reports whether the autostart entry exists and runs
// this AppImage.
func AutostartEnabled(executable string) bool {
	target := appImage()
	path, err := desktopFile()
	if target == "" || err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	program, ok := desktopExecProgram(string(data))
	return ok && samePath(program, target)
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
	target := appImage()
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
		"Exec=" + escapeDesktopString(quoteExecArg(appimage)+" run") + "\n" +
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

// desktopExecProgram returns the program (first argument) of the Exec key
// in the [Desktop Entry] group of a desktop file, undoing the escapes
// desktopEntry applies.
func desktopExecProgram(file string) (string, bool) {
	var exec string
	found, inEntry := false, false
	for _, line := range strings.Split(file, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			inEntry = line == "[Desktop Entry]"
			continue
		}
		if key, value, ok := strings.Cut(line, "="); inEntry && ok && strings.TrimSpace(key) == "Exec" {
			exec, found = strings.TrimSpace(value), true
			break
		}
	}
	if !found {
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
