package config

import (
	"path/filepath"
	"testing"
)

func TestDirOnLinux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name string
		xdg  string
		want string
	}{
		{"default", "", filepath.Join(home, ".config", "sd-sync-immich")},
		{"XDG_CONFIG_HOME", filepath.Join(home, "xdg"), filepath.Join(home, "xdg", "sd-sync-immich")},
		// The XDG spec says to ignore a relative path.
		{"relative XDG_CONFIG_HOME is ignored", "xdg", filepath.Join(home, ".config", "sd-sync-immich")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", tt.xdg)
			got, err := Dir()
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Dir() = %q, want %q", got, tt.want)
			}
		})
	}
}
