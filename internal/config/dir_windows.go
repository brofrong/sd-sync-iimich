package config

import (
	"os"
	"path/filepath"
)

// Dir is the app's config directory, which holds config.yaml and, by
// default, the database: %APPDATA%\sd-sync-immich.
func Dir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, appDirName), nil
}
