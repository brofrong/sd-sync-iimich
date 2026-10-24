package desktop

// OpenURL opens url in the default browser.
func OpenURL(url string) error { return run("open", url) }

// Open opens path with its default app.
func Open(path string) error { return run("open", path) }

// Reveal selects path in a Finder window.
func Reveal(path string) error { return run("open", "-R", path) }
