package logx

import (
	"path/filepath"
	"testing"
)

// TestLogDirOnMac checks that macOS keeps its logs where they always were.
func TestLogDirOnMac(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	want := filepath.Join(home, "Library", "Logs", "sd-sync-immich")
	if got := LogDir(); got != want {
		t.Fatalf("LogDir() = %q, want %q", got, want)
	}
	if got := LogPath(); got != filepath.Join(want, "app.log") {
		t.Fatalf("LogPath() = %q, want %q", got, filepath.Join(want, "app.log"))
	}
}
