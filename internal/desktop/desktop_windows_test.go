package desktop

import (
	"reflect"
	"testing"
)

func TestDesktopCommandsOnWindows(t *testing.T) {
	var got [][2]string
	shellExecute = func(file, args string) error {
		got = append(got, [2]string{file, args})
		return nil
	}
	t.Cleanup(func() { shellExecute = shellExecuteOpen })

	if err := OpenURL("https://photos.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := Open(`C:\Users\Jane Doe\AppData\Local\sd-sync-immich\logs\app.log`); err != nil {
		t.Fatal(err)
	}
	if err := Reveal(`C:\Users\Jane Doe\AppData\Local\sd-sync-immich\logs\app.log`); err != nil {
		t.Fatal(err)
	}
	want := [][2]string{
		{"https://photos.example.com", ""},
		{`C:\Users\Jane Doe\AppData\Local\sd-sync-immich\logs\app.log`, ""},
		// The path is quoted on its own: explorer doesn't parse a quoted
		// "/select,…" argument.
		{"explorer.exe", `/select,"C:\Users\Jane Doe\AppData\Local\sd-sync-immich\logs\app.log"`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}
