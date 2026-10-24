//go:build linux

package gui

import (
	"github.com/godbus/dbus/v5"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// statusNotifierWatcherName is the well-known bus name a tray host (an
// AppIndicator implementation, such as KDE's or Ubuntu's, or a
// StatusNotifierItem host) owns on the session bus while it is running.
// GNOME has none without an extra extension, so there is nowhere to put
// the tray icon: see the design doc's "Нет трея" clarification.
const statusNotifierWatcherName = "org.kde.StatusNotifierWatcher"

// sessionBusNameHasOwnerFn reports whether name has an owner on the
// session bus. A package var so trayAvailable can be tested against a
// fake instead of a real bus connection, which may not even exist in the
// sandboxed environment these tests run in.
var sessionBusNameHasOwnerFn = func(name string) (bool, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return false, err
	}
	defer conn.Close()
	var has bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&has)
	return has, err
}

// trayAvailable reports whether this platform has a tray to reopen the
// window from: on Linux, only when something (an AppIndicator host) owns
// org.kde.StatusNotifierWatcher on the session bus. If the session bus
// can't even be reached, there is no tray. Decided once at startup
// (app.go's Run).
func trayAvailable() bool {
	has, err := sessionBusNameHasOwnerFn(statusNotifierWatcherName)
	if err != nil {
		logx.Info("gui: session bus unavailable, assuming there is no tray", "err", err)
		return false
	}
	return has
}
