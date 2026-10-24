package gui

import (
	"errors"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/install"
)

const bundledExe = "/Users/x/Applications/SD Sync Immich.app/Contents/MacOS/sd-sync-immich"

const translocatedExe = "/private/var/folders/ab/T/AppTranslocation/0F3B-11/d/SD Sync Immich.app/Contents/MacOS/sd-sync-immich"

func TestAutostartState(t *testing.T) {
	fakeAutostart(t, true)
	tests := []struct {
		name                             string
		bundlePath, exe                  string
		enabled, available, translocated bool
	}{
		{name: "development build", exe: "/Users/x/dev/sd-sync-immich"},
		{name: "bundle", bundlePath: "/Users/x/Applications/SD Sync Immich.app", exe: bundledExe, enabled: true, available: true},
		{name: "translocated", bundlePath: "/private/var/folders/ab/T/AppTranslocation/0F3B-11/d/SD Sync Immich.app", exe: translocatedExe, translocated: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Service{bundlePath: tt.bundlePath, executable: tt.exe}
			v := s.autostartSettings(SettingsView{})
			if v.Autostart != tt.enabled || v.AutostartAvailable != tt.available || v.AutostartTranslocated != tt.translocated {
				t.Fatalf("autostart = %v, available = %v, translocated = %v; want %v, %v, %v",
					v.Autostart, v.AutostartAvailable, v.AutostartTranslocated, tt.enabled, tt.available, tt.translocated)
			}
		})
	}
}

func TestSetAutostartRefusesTranslocatedApp(t *testing.T) {
	fakeAutostart(t, false)
	s := &Service{bundlePath: "/private/var/folders/ab/T/AppTranslocation/0F3B-11/d/SD Sync Immich.app", executable: translocatedExe}
	err := s.SetAutostart(true)
	if err == nil || err.Error() != errAutostartTranslocated {
		t.Fatalf("SetAutostart(true) error = %v, want %q (the code the window translates)", err, errAutostartTranslocated)
	}
	if !errors.Is(err, install.ErrTranslocated) {
		t.Fatalf("SetAutostart(true) error = %v does not wrap install.ErrTranslocated", err)
	}
}
