package desktop

import (
	"reflect"
	"testing"
)

func TestDesktopCommandsOnLinux(t *testing.T) {
	var got [][]string
	run = func(name string, args ...string) error {
		got = append(got, append([]string{name}, args...))
		return nil
	}
	t.Cleanup(func() { run = runCommand })

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
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
}
