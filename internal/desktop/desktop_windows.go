package desktop

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// shellExecuteOpen opens file through the shell (ShellExecute with the
// "open" verb), passing args when not empty: what double-clicking does.
// ShellExecute takes the command line as is, unlike exec.Command, which
// would quote "/select,<path>" into a form explorer does not understand.
func shellExecuteOpen(file, args string) error {
	verb, err := windows.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	f, err := windows.UTF16PtrFromString(file)
	if err != nil {
		return err
	}
	var a *uint16
	if args != "" {
		if a, err = windows.UTF16PtrFromString(args); err != nil {
			return err
		}
	}
	if err := windows.ShellExecute(0, verb, f, a, nil, windows.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("open %s: %w", file, err)
	}
	return nil
}

// shellExecute is the seam the tests replace.
var shellExecute = shellExecuteOpen

// OpenURL opens url in the default browser.
func OpenURL(url string) error { return shellExecute(url, "") }

// Open opens path with its default app.
func Open(path string) error { return shellExecute(path, "") }

// Reveal selects path in an Explorer window.
func Reveal(path string) error {
	return shellExecute("explorer.exe", `/select,"`+path+`"`)
}
