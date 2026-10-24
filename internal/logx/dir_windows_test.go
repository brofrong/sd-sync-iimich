package logx

import (
	"path/filepath"
	"testing"
)

func TestLogDirOnWindows(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)

	want := filepath.Join(local, "sd-sync-immich", "logs")
	if got := LogDir(); got != want {
		t.Fatalf("LogDir() = %q, want %q", got, want)
	}
	if got := LogPath(); got != filepath.Join(want, "app.log") {
		t.Fatalf("LogPath() = %q, want %q", got, filepath.Join(want, "app.log"))
	}
}
