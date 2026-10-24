//go:build linux

// On Linux the system is kept awake by a systemd-logind inhibitor lock,
// held for as long as its file descriptor stays open.

package power

import (
	"fmt"
	"os"
	"sync"
	"syscall"

	"github.com/godbus/dbus/v5"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// closer is the held inhibitor lock: closing it releases the lock.
type closer interface {
	Close() error
}

// inhibitArgs are logind's Manager.Inhibit arguments: what (sleep and
// idle), who, why, and mode "block" (not merely delay).
func inhibitArgs() []any {
	return []any{"sleep:idle", "SD Sync Immich", "Uploading photos", "block"}
}

// inhibitSleepFn takes a logind inhibitor lock over the system bus and
// returns its file descriptor. It is a variable so tests can substitute a
// fake instead of talking to this machine's logind.
var inhibitSleepFn = func() (closer, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect to the system bus: %w", err)
	}
	// The lock lives in the descriptor, not the connection.
	defer conn.Close()

	var fd dbus.UnixFD
	err = conn.Object("org.freedesktop.login1", "/org/freedesktop/login1").
		Call("org.freedesktop.login1.Manager.Inhibit", 0, inhibitArgs()...).
		Store(&fd)
	if err != nil {
		return nil, fmt.Errorf("logind Inhibit: %w", err)
	}
	if fd < 0 {
		return nil, fmt.Errorf("logind Inhibit: invalid descriptor %d", fd)
	}
	syscall.CloseOnExec(int(fd))
	return os.NewFile(uintptr(fd), "logind-inhibitor"), nil
}

// KeepAwake prevents the system from sleeping until the returned release
// function is called, which closes the inhibitor lock. release is safe to
// call more than once (only the first call has any effect), and safe to
// call even when the lock could not be taken — that failure is only logged.
func KeepAwake() (release func()) {
	lock, err := inhibitSleepFn()
	if err != nil || lock == nil {
		logx.Warn("power: logind inhibitor lock failed; the system may sleep during the sync", "err", err)
		return func() {}
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			if err := lock.Close(); err != nil {
				logx.Warn("power: closing the logind inhibitor lock failed", "err", err)
			}
		})
	}
}
