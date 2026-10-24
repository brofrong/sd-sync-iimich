package logx

import (
	"os"
	"path/filepath"
)

// LogDir is the directory of the app's logs: ~/Library/Logs/sd-sync-immich;
// "" when the home directory is unknown.
func LogDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Logs", appDirName)
}
