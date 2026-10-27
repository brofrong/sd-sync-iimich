package install

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/testenv"
)

// runAsFlatpak makes this test process look like it runs in a Flatpak
// sandbox with the given app id, and records the Background portal
// requests instead of making them.
func runAsFlatpak(t *testing.T, id string) (home string, requests *[]bool) {
	t.Helper()
	home = testenv.Home(t)
	info := filepath.Join(t.TempDir(), ".flatpak-info")
	if err := os.WriteFile(info, []byte("[Application]\nname="+id+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	savedInfo, savedRequest := flatpakInfoPath, requestBackgroundFn
	t.Cleanup(func() { flatpakInfoPath, requestBackgroundFn = savedInfo, savedRequest })
	flatpakInfoPath = info
	t.Setenv("FLATPAK_ID", id)
	t.Setenv("APPIMAGE", "")
	t.Setenv("APPDIR", "")
	var got []bool
	requestBackgroundFn = func(autostart bool) error {
		got = append(got, autostart)
		return nil
	}
	return home, &got
}

func TestFlatpakAutostartGoesThroughThePortal(t *testing.T) {
	const id = "io.github.brofrong.SDSyncImmich"
	home, requests := runAsFlatpak(t, id)

	if !Flatpak() || !AutostartAvailable("/app/bin/sd-sync-immich") {
		t.Fatal("Flatpak() or AutostartAvailable() = false in a Flatpak")
	}
	if AutostartEnabled("/app/bin/sd-sync-immich") {
		t.Fatal("AutostartEnabled() = true before the portal wrote an entry")
	}
	if err := SetAutostart(true, "/app/bin/sd-sync-immich"); err != nil {
		t.Fatalf("SetAutostart(true) error = %v", err)
	}
	if err := SetAutostart(false, "/app/bin/sd-sync-immich"); err != nil {
		t.Fatalf("SetAutostart(false) error = %v", err)
	}
	if got := *requests; len(got) != 2 || !got[0] || got[1] {
		t.Fatalf("portal requests = %v, want [true false]", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "autostart", "sd-sync-immich.desktop")); !os.IsNotExist(err) {
		t.Fatalf("the AppImage entry was written in a Flatpak (stat err = %v)", err)
	}

	// The entry the portal writes on the host.
	entry := filepath.Join(home, ".config", "autostart", id+".desktop")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(entry, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[Desktop Entry]\nType=Application\nExec=flatpak run --command=sd-sync-immich " + id + " run --autostart\nX-Flatpak=" + id + "\n")
	if !AutostartEnabled("/app/bin/sd-sync-immich") {
		t.Fatal("AutostartEnabled() = false with the portal's entry")
	}
	write("[Desktop Entry]\nType=Application\nExec=flatpak run " + id + "\nX-GNOME-Autostart-enabled=false\n")
	if AutostartEnabled("/app/bin/sd-sync-immich") {
		t.Fatal("AutostartEnabled() = true with the entry turned off")
	}
	if rewritten, err := RefreshAutostart("/app/bin/sd-sync-immich"); rewritten || err != nil {
		t.Fatalf("RefreshAutostart() = %v, %v; want it to leave the portal's entry alone", rewritten, err)
	}
}

func TestBackgroundResult(t *testing.T) {
	granted := map[string]dbus.Variant{"background": dbus.MakeVariant(true), "autostart": dbus.MakeVariant(true)}
	denied := map[string]dbus.Variant{"background": dbus.MakeVariant(true), "autostart": dbus.MakeVariant(false)}
	tests := []struct {
		name      string
		autostart bool
		response  uint32
		results   map[string]dbus.Variant
		wantErr   bool
	}{
		{name: "granted", autostart: true, results: granted},
		{name: "autostart refused", autostart: true, results: denied, wantErr: true},
		{name: "no autostart in results", autostart: true, results: map[string]dbus.Variant{}, wantErr: true},
		{name: "user said no", autostart: true, response: 1, results: granted, wantErr: true},
		{name: "turned off", autostart: false, results: denied},
		{name: "turning off failed", autostart: false, response: 2, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := backgroundResult(tt.autostart, tt.response, tt.results)
			if (err != nil) != tt.wantErr {
				t.Fatalf("backgroundResult() error = %v, want error %v", err, tt.wantErr)
			}
		})
	}
}
