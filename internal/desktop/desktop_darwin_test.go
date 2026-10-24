package desktop

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestDesktopCommandsOnMac(t *testing.T) {
	var got [][]string
	run = func(cmd *exec.Cmd) error {
		got = append(got, cmd.Args)
		if cmd.Env != nil {
			t.Errorf("%q: environment = %q, want this process's (nil)", cmd.Args, cmd.Env)
		}
		return nil
	}
	t.Cleanup(func() { run = runCommand })

	if err := OpenURL("https://photos.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := Open("/Users/me/Library/Logs/sd-sync-immich/app.log"); err != nil {
		t.Fatal(err)
	}
	if err := Reveal("/Users/me/Library/Logs/sd-sync-immich/app.log"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{
		{"open", "https://photos.example.com"},
		{"open", "/Users/me/Library/Logs/sd-sync-immich/app.log"},
		{"open", "-R", "/Users/me/Library/Logs/sd-sync-immich/app.log"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
}
