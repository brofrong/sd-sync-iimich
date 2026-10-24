package desktop

import (
	"os"
	"path/filepath"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/appimageenv"
)

// helperEnv is the helpers' environment. In an AppImage ($APPDIR set) it
// is this process's without the AppImage's paths: its AppRun puts the
// bundled GTK and GLib first on LD_LIBRARY_PATH (and its directories on
// PATH, XDG_DATA_DIRS, ...), which would break xdg-open and the browser or
// file manager it starts on a newer system. Otherwise nil, this
// process's.
func helperEnv() []string {
	appdir := os.Getenv("APPDIR")
	if appdir == "" {
		return nil
	}
	return appimageenv.Clean(os.Environ(), appdir)
}

// OpenURL opens url in the default browser.
func OpenURL(url string) error { return run(command("xdg-open", url)) }

// Open opens path with its default app.
func Open(path string) error { return run(command("xdg-open", path)) }

// Reveal opens the folder that holds path in the file manager (xdg-open
// can't select the file itself).
func Reveal(path string) error { return run(command("xdg-open", filepath.Dir(path))) }
