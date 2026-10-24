package desktop

import "path/filepath"

// OpenURL opens url in the default browser.
func OpenURL(url string) error { return run("xdg-open", url) }

// Open opens path with its default app.
func Open(path string) error { return run("xdg-open", path) }

// Reveal opens the folder that holds path in the file manager (xdg-open
// can't select the file itself).
func Reveal(path string) error { return run("xdg-open", filepath.Dir(path)) }
