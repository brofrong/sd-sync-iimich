//go:build darwin

package gui

// showWindow shows the main window together with the Dock icon.
func (sh *shell) showWindow() {
	sh.visible.Store(true)
	sh.dock.ShowAppIcon()
	sh.window.Show()
	sh.window.Focus()
	if sh.onShow != nil {
		sh.onShow()
	}
}

// hideWindow hides the main window together with the Dock icon.
func (sh *shell) hideWindow() {
	sh.visible.Store(false)
	sh.window.Hide()
	sh.dock.HideAppIcon()
}

// trayAvailable reports whether this platform has a tray to reopen the
// window from: the macOS menu bar always does.
func trayAvailable() bool { return true }

// trayClickOpensWindow is false: on macOS a click on the menu bar icon
// opens its menu, as it always has.
const trayClickOpensWindow = false
