package config

import (
	"path/filepath"
	"testing"
)

// TestDirOnMac checks that macOS keeps the config where it always was.
func TestDirOnMac(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "xdg")) // not used on macOS

	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".config", "sd-sync-immich"); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}
