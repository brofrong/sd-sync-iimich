package install

import (
	"os"
	"testing"
)

// fakeRun is a Run key in memory: no test touches the real registry.
type fakeRun map[string]string

func (f fakeRun) get(name string) (string, error) {
	v, ok := f[name]
	if !ok {
		return "", os.ErrNotExist
	}
	return v, nil
}
func (f fakeRun) set(name, value string) error { f[name] = value; return nil }
func (f fakeRun) remove(name string) error     { delete(f, name); return nil }

func useFakeRun(t *testing.T) fakeRun {
	t.Helper()
	f := fakeRun{}
	orig := runValues
	runValues = f
	t.Cleanup(func() { runValues = orig })
	return f
}

const windowsExe = `C:\Users\John Smith\AppData\Local\Programs\SD Sync Immich\sd-sync-immich.exe`

func TestWindowsAutostartRunValue(t *testing.T) {
	run := useFakeRun(t)
	if !AutostartAvailable(windowsExe) {
		t.Fatal("AutostartAvailable() = false on Windows")
	}
	if AutostartEnabled(windowsExe) {
		t.Fatal("AutostartEnabled() = true without a Run value")
	}
	if err := SetAutostart(true, windowsExe); err != nil {
		t.Fatal(err)
	}
	want := `"` + windowsExe + `" run --autostart`
	if got := run["SD Sync Immich"]; got != want {
		t.Fatalf("Run value = %q, want %q", got, want)
	}
	if len(run) != 1 {
		t.Fatalf("Run key = %q, want only the SD Sync Immich value", run)
	}
	if !AutostartEnabled(windowsExe) {
		t.Fatal("AutostartEnabled() = false after SetAutostart(true)")
	}
	// Windows paths are case-insensitive.
	if !AutostartEnabled(`c:\users\john smith\appdata\local\programs\sd sync immich\SD-SYNC-IMMICH.EXE`) {
		t.Fatal("AutostartEnabled() is case-sensitive")
	}
	if AutostartEnabled(`C:\Other\sd-sync-immich.exe`) {
		t.Fatal("AutostartEnabled() = true for another copy of the app")
	}
	if err := SetAutostart(false, windowsExe); err != nil {
		t.Fatal(err)
	}
	if _, ok := run["SD Sync Immich"]; ok || AutostartEnabled(windowsExe) {
		t.Fatalf("Run value left after SetAutostart(false): %q", run)
	}
	// Turning it off twice is fine.
	if err := SetAutostart(false, windowsExe); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsAutostartAcceptsValueWithoutArguments(t *testing.T) {
	// The installer may write the bare quoted path.
	run := useFakeRun(t)
	run["SD Sync Immich"] = `"` + windowsExe + `"`
	if !AutostartEnabled(windowsExe) {
		t.Fatal("AutostartEnabled() = false for a quoted path without arguments")
	}
}

func TestWindowsRefreshAutostart(t *testing.T) {
	current := `"` + windowsExe + `" run --autostart`
	tests := []struct {
		name, value string // "" = no value
		rewritten   bool
		want        string
	}{
		{"none", "", false, ""},
		{"current", current, false, current},
		{"older command", `"` + windowsExe + `" run`, true, current},
		{"bare path", `"` + windowsExe + `"`, true, current},
		{"other copy", `"C:\Other\sd-sync-immich.exe" run`, false, `"C:\Other\sd-sync-immich.exe" run`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := useFakeRun(t)
			if tt.value != "" {
				run["SD Sync Immich"] = tt.value
			}
			rewritten, err := RefreshAutostart(windowsExe)
			if err != nil || rewritten != tt.rewritten {
				t.Fatalf("RefreshAutostart() = %v, %v; want %v", rewritten, err, tt.rewritten)
			}
			if got := run["SD Sync Immich"]; got != tt.want {
				t.Fatalf("Run value = %q, want %q", got, tt.want)
			}
		})
	}
}
