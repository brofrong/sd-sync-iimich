package gui

import (
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/install"
)

// fakeAutostart replaces the LaunchAgent calls with ones that report
// enabled and record SetAutostart.
func fakeAutostart(t *testing.T, enabled bool) *[]bool {
	t.Helper()
	var calls []bool
	origEnabled, origSet := autostartEnabled, setAutostart
	autostartEnabled = func(string) bool { return enabled }
	setAutostart = func(on bool, exe string) error {
		calls = append(calls, on)
		if on && install.Translocated(exe) {
			return install.ErrTranslocated
		}
		return nil
	}
	t.Cleanup(func() { autostartEnabled, setAutostart = origEnabled, origSet })
	return &calls
}

func TestAutostartAvailableComesFromInstall(t *testing.T) {
	fakeAutostart(t, true)
	for _, available := range []bool{false, true} {
		orig := autostartAvailable
		autostartAvailable = func(string) bool { return available }
		v := (&Service{executable: "/x/sd-sync-immich"}).autostartSettings(SettingsView{})
		autostartAvailable = orig
		if v.AutostartAvailable != available || v.Autostart != available {
			t.Fatalf("available %v: AutostartAvailable = %v, Autostart = %v", available, v.AutostartAvailable, v.Autostart)
		}
	}
}
