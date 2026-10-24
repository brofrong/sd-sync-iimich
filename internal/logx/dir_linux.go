package logx

import (
	"os"
	"path/filepath"
)

// LogDir is the directory of the app's logs: $XDG_STATE_HOME/sd-sync-immich,
// or ~/.local/state/sd-sync-immich without it (a relative XDG_STATE_HOME is
// ignored, as the XDG spec says); "" when the home directory is unknown.
func LogDir() string {
	if xdg := os.Getenv("XDG_STATE_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, appDirName)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", appDirName)
}
