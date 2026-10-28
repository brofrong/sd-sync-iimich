package desktop

import (
	"os/exec"
	"reflect"
	"strings"
	"testing"
)

// recordRun replaces run for the test and returns the commands it got.
func recordRun(t *testing.T) *[]*exec.Cmd {
	t.Helper()
	var got []*exec.Cmd
	run = func(cmd *exec.Cmd) error {
		got = append(got, cmd)
		return nil
	}
	t.Cleanup(func() { run = runCommand })
	return &got
}

func TestDesktopCommandsOnLinux(t *testing.T) {
	t.Setenv("APPDIR", "")
	cmds := recordRun(t)

	if err := OpenURL("https://photos.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := Open("/home/me/.local/state/sd-sync-immich/app.log"); err != nil {
		t.Fatal(err)
	}
	// xdg-open can't select a file: Reveal opens its folder.
	if err := Reveal("/home/me/.local/state/sd-sync-immich/app.log"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"xdg-open", "https://photos.example.com"},
		{"xdg-open", "/home/me/.local/state/sd-sync-immich/app.log"},
		{"xdg-open", "/home/me/.local/state/sd-sync-immich"},
	}
	var got [][]string
	for _, cmd := range *cmds {
		got = append(got, cmd.Args)
		// Outside an AppImage the helpers get this process's environment.
		if cmd.Env != nil {
			t.Errorf("%q: environment = %q, want this process's (nil)", cmd.Args, cmd.Env)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
}

func TestDesktopCommandsInAppImageGetCleanEnvironment(t *testing.T) {
	const appdir = "/tmp/.mount_SDSyncA1b2"
	t.Setenv("APPIMAGE", "/home/me/Applications/SD-Sync-Immich-x86_64.AppImage")
	t.Setenv("APPDIR", appdir)
	t.Setenv("LD_LIBRARY_PATH", appdir+"/usr/lib:"+appdir+"/usr/lib/x86_64-linux-gnu")
	t.Setenv("PATH", appdir+"/usr/bin:/usr/bin:/bin")
	t.Setenv("XDG_DATA_DIRS", appdir+"/usr/share:/usr/share")
	t.Setenv("SD_SYNC_KEEP", "kept")
	cmds := recordRun(t)

	if err := OpenURL("https://github.com/brofrong/sd-sync-immich/issues/new"); err != nil {
		t.Fatal(err)
	}
	if err := Open("/home/me/.local/state/sd-sync-immich/app.log"); err != nil {
		t.Fatal(err)
	}
	if err := Reveal("/home/me/.local/state/sd-sync-immich/app.log"); err != nil {
		t.Fatal(err)
	}
	if len(*cmds) != 3 {
		t.Fatalf("ran %d commands, want 3", len(*cmds))
	}
	for _, cmd := range *cmds {
		if cmd.Env == nil {
			t.Fatalf("%q inherits the AppImage's environment, want a cleaned copy", cmd.Args)
		}
		env := map[string]string{}
		for _, kv := range cmd.Env {
			name, value, _ := strings.Cut(kv, "=")
			env[name] = value
			if strings.Contains(value, appdir) {
				t.Errorf("%q: environment keeps %s, which points into the AppImage", cmd.Args, kv)
			}
		}
		for _, name := range []string{"APPIMAGE", "APPDIR", "LD_LIBRARY_PATH"} {
			if v, ok := env[name]; ok {
				t.Errorf("%q: environment keeps %s=%s", cmd.Args, name, v)
			}
		}
		if env["PATH"] != "/usr/bin:/bin" || env["XDG_DATA_DIRS"] != "/usr/share" {
			t.Errorf("%q: PATH=%q XDG_DATA_DIRS=%q, want the system's entries kept", cmd.Args, env["PATH"], env["XDG_DATA_DIRS"])
		}
		if env["SD_SYNC_KEEP"] != "kept" {
			t.Errorf("%q: an unrelated variable was dropped", cmd.Args)
		}
	}
}
