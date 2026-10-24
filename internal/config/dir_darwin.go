package config

import (
	"os"
	"path/filepath"
)

// Dir is the app's config directory, which holds config.yaml and, by
// default, the database: ~/.config/sd-sync-immich, as it always was on
// macOS.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", appDirName), nil
}
