// Package testenv points the per-user directories the app reads and writes
// (its config, database and logs) at a temporary directory for the length
// of a test, on every system, so no test ever touches the real ones.
package testenv

import (
	"path/filepath"
	"testing"
)

// Home makes a temporary directory the user's home for the rest of the
// test and returns it: HOME (macOS, Linux), USERPROFILE (Windows' home),
// APPDATA and LOCALAPPDATA (Windows' config and log roots) point into it,
// and XDG_CONFIG_HOME / XDG_STATE_HOME are cleared so Linux falls back to
// ~/.config and ~/.local/state under it.
func Home(t testing.TB) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	t.Setenv("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	return home
}
