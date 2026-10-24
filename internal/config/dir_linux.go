package config

import (
	"os"
	"path/filepath"
)

// Dir is the app's config directory, which holds config.yaml and, by
// default, the database: $XDG_CONFIG_HOME/sd-sync-immich, or
// ~/.config/sd-sync-immich without it (a relative XDG_CONFIG_HOME is
// ignored, as the XDG spec says).
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); filepath.IsAbs(xdg) {
		return filepath.Join(xdg, appDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", appDirName), nil
}
