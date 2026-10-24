package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/testenv"
)

func readDesktop(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if lines[0] != "[Desktop Entry]" {
		t.Fatalf("%s starts with %q, want [Desktop Entry]", path, lines[0])
	}
	keys := map[string]string{}
	for _, line := range lines[1:] {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("%s: malformed line %q", path, line)
		}
		keys[k] = v
	}
	return keys
}

func TestLinuxAutostartDesktopFile(t *testing.T) {
	home := testenv.Home(t)
	appimage := filepath.Join(home, "Apps", "SD-Sync-Immich-x86_64.AppImage")
	t.Setenv("APPIMAGE", appimage)
	// The executable inside the mounted AppImage changes on every launch.
	exe := "/tmp/.mount_SDSyncX1/usr/bin/sd-sync-immich"

	if !AutostartAvailable(exe) {
		t.Fatal("AutostartAvailable() = false with $APPIMAGE set")
	}
	if AutostartEnabled(exe) {
		t.Fatal("AutostartEnabled() = true without a desktop file")
	}
	if err := SetAutostart(true, exe); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, ".config", "autostart", "sd-sync-immich.desktop")
	keys := readDesktop(t, path)
	want := map[string]string{
		"Type":                      "Application",
		"Name":                      "SD Sync Immich",
		"Exec":                      `"` + appimage + `" run`,
		"Terminal":                  "false",
		"X-GNOME-Autostart-enabled": "true",
	}
	for k, v := range want {
		if keys[k] != v {
			t.Errorf("%s = %q, want %q", k, keys[k], v)
		}
	}
	if !AutostartEnabled(exe) {
		t.Fatal("AutostartEnabled() = false after SetAutostart(true)")
	}

	// Another copy of the AppImage does not count as enabled.
	t.Setenv("APPIMAGE", filepath.Join(home, "Downloads", "SD-Sync-Immich-x86_64.AppImage"))
	if AutostartEnabled(exe) {
		t.Fatal("AutostartEnabled() = true for another AppImage")
	}
	t.Setenv("APPIMAGE", appimage)

	if err := SetAutostart(false, exe); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("desktop file left after SetAutostart(false): %v", err)
	}
	if err := SetAutostart(false, exe); err != nil {
		t.Fatalf("SetAutostart(false) twice: %v", err)
	}
}

func TestLinuxAutostartRespectsXDGConfigHome(t *testing.T) {
	home := testenv.Home(t)
	config := filepath.Join(home, "xdg")
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("APPIMAGE", filepath.Join(home, "app.AppImage"))
	if err := SetAutostart(true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(config, "autostart", "sd-sync-immich.desktop")); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxAutostartNeedsAppImage(t *testing.T) {
	home := testenv.Home(t)
	t.Setenv("APPIMAGE", "")
	if AutostartAvailable("/usr/local/bin/sd-sync-immich") {
		t.Fatal("AutostartAvailable() = true outside an AppImage")
	}
	if err := SetAutostart(true, "/usr/local/bin/sd-sync-immich"); err == nil {
		t.Fatal("SetAutostart(true) succeeded outside an AppImage")
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "autostart")); !os.IsNotExist(err) {
		t.Fatalf("autostart directory created outside an AppImage: %v", err)
	}
}

func TestLinuxAutostartQuotesExec(t *testing.T) {
	home := testenv.Home(t)
	// Characters the Desktop Entry spec reserves inside a quoted argument,
	// a field-code % and a key-file backslash.
	appimage := filepath.Join(home, `My "Apps" $HOME 100% back\slash`, "x.AppImage")
	t.Setenv("APPIMAGE", appimage)
	if err := SetAutostart(true, ""); err != nil {
		t.Fatal(err)
	}
	keys := readDesktop(t, filepath.Join(home, ".config", "autostart", "sd-sync-immich.desktop"))
	wantDir := home + `/My \\"Apps\\" \\$HOME 100%% back\\\\slash`
	if want := `"` + wantDir + `/x.AppImage" run`; keys["Exec"] != want {
		t.Fatalf("Exec = %q, want %q", keys["Exec"], want)
	}
	if !AutostartEnabled("") {
		t.Fatal("AutostartEnabled() = false: the quoted Exec does not parse back")
	}
}

func TestLinuxAutostartRejectsNewlineInPath(t *testing.T) {
	home := testenv.Home(t)
	t.Setenv("APPIMAGE", filepath.Join(home, "a\nExec=evil", "x.AppImage"))
	if err := SetAutostart(true, ""); err == nil {
		t.Fatal("SetAutostart(true) accepted a path with a newline")
	}
}
