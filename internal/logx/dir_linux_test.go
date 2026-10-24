package logx

import (
	"path/filepath"
	"testing"
)

func TestLogDirOnLinux(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	tests := []struct {
		name string
		xdg  string
		want string
	}{
		{"default", "", filepath.Join(home, ".local", "state", "sd-sync-immich")},
		{"XDG_STATE_HOME", filepath.Join(home, "state"), filepath.Join(home, "state", "sd-sync-immich")},
		{"relative XDG_STATE_HOME is ignored", "state", filepath.Join(home, ".local", "state", "sd-sync-immich")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", tt.xdg)
			if got := LogDir(); got != tt.want {
				t.Fatalf("LogDir() = %q, want %q", got, tt.want)
			}
			if got := LogPath(); got != filepath.Join(tt.want, "app.log") {
				t.Fatalf("LogPath() = %q, want %q", got, filepath.Join(tt.want, "app.log"))
			}
		})
	}
}
