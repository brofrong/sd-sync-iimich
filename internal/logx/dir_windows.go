package logx

import (
	"os"
	"path/filepath"
)

// LogDir is the directory of the app's logs:
// %LOCALAPPDATA%\sd-sync-immich\logs; "" when LOCALAPPDATA is not set.
func LogDir() string {
	// On Windows the user cache directory is %LOCALAPPDATA%.
	local, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(local, appDirName, "logs")
}
