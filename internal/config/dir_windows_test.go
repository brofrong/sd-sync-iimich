package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirOnWindows(t *testing.T) {
	appData := t.TempDir()
	t.Setenv("APPDATA", appData)

	got, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(appData, "sd-sync-immich"); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}

// TestExpandPathWithoutHome checks that "~" means the user's profile on
// Windows, where HOME is normally not set.
func TestExpandPathWithoutHome(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("USERPROFILE", profile)
	t.Setenv("HOME", "")
	os.Unsetenv("HOME")

	for _, in := range []string{"~/.config/sd-sync-immich/state.db", `~\.config\sd-sync-immich\state.db`} {
		got, err := ExpandPath(in)
		if err != nil {
			t.Fatalf("ExpandPath(%q): %v", in, err)
		}
		if want := filepath.Join(profile, ".config", "sd-sync-immich", "state.db"); got != want {
			t.Fatalf("ExpandPath(%q) = %q, want %q", in, got, want)
		}
	}
}
