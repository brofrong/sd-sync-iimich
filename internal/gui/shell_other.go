//go:build !darwin

package gui

// showWindow shows the main window. Windows and Linux have no Dock icon
// to show alongside it.
func (sh *shell) showWindow() {
	sh.visible.Store(true)
	sh.window.Show()
	sh.window.Focus()
	if sh.onShow != nil {
		sh.onShow()
	}
}

// hideWindow hides the main window. Windows and Linux have no Dock icon
// to hide alongside it.
func (sh *shell) hideWindow() {
	sh.visible.Store(false)
	sh.window.Hide()
}

// trayClickOpensWindow is true: on Windows and Linux a left click on the
// tray icon opens the window instead of its menu (the menu is still
// reachable, e.g. with a right click). trayAvailable is platform-specific
// (traycheck_windows.go, traycheck_linux.go): Windows always has a tray;
// Linux may not.
const trayClickOpensWindow = true
