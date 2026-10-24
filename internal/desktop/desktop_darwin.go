package desktop

// helperEnv is the helpers' environment: nil, this process's.
func helperEnv() []string { return nil }

// OpenURL opens url in the default browser.
func OpenURL(url string) error { return run(command("open", url)) }

// Open opens path with its default app.
func Open(path string) error { return run(command("open", path)) }

// Reveal selects path in a Finder window.
func Reveal(path string) error { return run(command("open", "-R", path)) }
