package bundle

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeCommands replaces runCommand for the test and records every call as
// "name arg1 arg2 …". fail, if set, decides which calls fail.
func fakeCommands(t *testing.T, fail func(call string) bool) *[]string {
	t.Helper()
	var calls []string
	orig := runCommand
	runCommand = func(name string, args ...string) ([]byte, error) {
		call := strings.Join(append([]string{name}, args...), " ")
		calls = append(calls, call)
		if fail != nil && fail(call) {
			return []byte("boom"), errors.New("exit status 1")
		}
		return nil, nil
	}
	t.Cleanup(func() { runCommand = orig })
	return &calls
}

func writeBinary(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sd-sync-immich")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCreateLaysOutBundle(t *testing.T) {
	calls := fakeCommands(t, nil)
	bin := writeBinary(t, "binary contents")
	dest := filepath.Join(t.TempDir(), "Apps", Name)

	if err := Create(bin, dest, "v9.9.9"); err != nil {
		t.Fatalf("Create: %v", err)
	}

	exe := filepath.Join(dest, "Contents", "MacOS", Executable)
	info, err := os.Stat(exe)
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	// Windows has no executable bit.
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("executable mode = %v, want 0755", info.Mode().Perm())
	}
	if got, _ := os.ReadFile(exe); string(got) != "binary contents" {
		t.Fatalf("executable content = %q", got)
	}

	plist, err := os.ReadFile(filepath.Join(dest, "Contents", "Info.plist"))
	if err != nil {
		t.Fatalf("Info.plist: %v", err)
	}
	for _, want := range []string{
		"<string>v9.9.9</string>",
		"<string>" + ID + "</string>",
		"<key>LSUIElement</key>",
		"<string>" + Executable + "</string>",
		"<key>CFBundleIconFile</key>",
		"<string>13.0</string>",
	} {
		if !bytes.Contains(plist, []byte(want)) {
			t.Errorf("Info.plist lacks %s:\n%s", want, plist)
		}
	}

	icon, err := os.ReadFile(filepath.Join(dest, "Contents", "Resources", "icons.icns"))
	if err != nil {
		t.Fatalf("icons.icns: %v", err)
	}
	if !bytes.HasPrefix(icon, []byte("icns")) {
		t.Fatalf("icons.icns is not an icns file")
	}

	// The bundle is built and signed next to dest under its final name,
	// then moved into place.
	if len(*calls) == 0 {
		t.Fatal("no commands run")
	}
	built := strings.TrimPrefix((*calls)[0], "xattr -cr ")
	if filepath.Base(built) != Name || filepath.Dir(filepath.Dir(built)) != filepath.Dir(dest) {
		t.Fatalf("bundle built at %q, want <dir of dest>/<staging>/%s", built, Name)
	}
	want := []string{
		"xattr -cr " + built,
		"codesign --force --deep --sign - " + built,
		"codesign --verify --deep --strict " + built,
	}
	if strings.Join(*calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands:\n%s\nwant:\n%s", strings.Join(*calls, "\n"), strings.Join(want, "\n"))
	}
}

func TestCreateReplacesExistingBundle(t *testing.T) {
	fakeCommands(t, nil)
	dest := filepath.Join(t.TempDir(), Name)
	stale := filepath.Join(dest, "Contents", "Resources", "stale.txt")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Create(writeBinary(t, "new"), dest, "v1.0.0"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale file survived: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "Contents", "MacOS", Executable)); string(got) != "new" {
		t.Fatalf("executable content = %q, want new", got)
	}
	assertNoLeftovers(t, filepath.Dir(dest), Name)
}

func TestCreateFromExecutableInsideTheBundle(t *testing.T) {
	fakeCommands(t, nil)
	dest := filepath.Join(t.TempDir(), Name)
	if err := Create(writeBinary(t, "v1"), dest, "v1.0.0"); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dest, "Contents", "MacOS", Executable)
	if err := Create(exe, dest, "v1.0.1"); err != nil {
		t.Fatalf("Create from own executable: %v", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "v1" {
		t.Fatalf("executable content = %q, want v1", got)
	}
}

func TestCreateKeepsExistingBundleWhenSigningFails(t *testing.T) {
	dest := filepath.Join(t.TempDir(), Name)
	fakeCommands(t, nil)
	if err := Create(writeBinary(t, "old"), dest, "v1.0.0"); err != nil {
		t.Fatal(err)
	}

	fakeCommands(t, func(call string) bool { return strings.HasPrefix(call, "codesign --force") })
	err := Create(writeBinary(t, "new"), dest, "v2.0.0")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("Create error = %v, want codesign failure with its output", err)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "Contents", "MacOS", Executable)); string(got) != "old" {
		t.Fatalf("existing bundle was replaced: executable = %q", got)
	}
	assertNoLeftovers(t, filepath.Dir(dest), Name)
}

// assertNoLeftovers fails if dir holds anything but want.
func assertNoLeftovers(t *testing.T, dir, want string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != want {
			t.Errorf("leftover %s in %s", e.Name(), dir)
		}
	}
}

func TestResign(t *testing.T) {
	calls := fakeCommands(t, nil)
	if err := Resign("/A/SD Sync Immich.app"); err != nil {
		t.Fatal(err)
	}
	want := "codesign --force --deep --sign - /A/SD Sync Immich.app\ncodesign --verify --deep --strict /A/SD Sync Immich.app"
	if got := strings.Join(*calls, "\n"); got != want {
		t.Fatalf("commands:\n%s\nwant:\n%s", got, want)
	}
}

func TestResignReportsVerifyFailure(t *testing.T) {
	fakeCommands(t, func(call string) bool { return strings.HasPrefix(call, "codesign --verify") })
	if err := Resign("/A/SD Sync Immich.app"); err == nil {
		t.Fatal("Resign succeeded although verification failed")
	}
}

func TestContaining(t *testing.T) {
	tests := []struct {
		executable string
		want       string
		ok         bool
	}{
		{"/A/SD Sync Immich.app/Contents/MacOS/sd-sync-immich", "/A/SD Sync Immich.app", true},
		{"/Users/x/Applications/sd-sync-immich", "", false},
		{"/A/Other.app/Contents/MacOS/other", "/A/Other.app", true},
		{"/A/Contents/MacOS/sd-sync-immich", "", false},
		{"/A/X.app/Contents/Resources/sd-sync-immich", "", false},
		{"sd-sync-immich", "", false},
	}
	for _, tt := range tests {
		got, ok := Containing(tt.executable)
		// Windows cleans the path with "\".
		if got = filepath.ToSlash(got); got != tt.want || ok != tt.ok {
			t.Errorf("Containing(%q) = %q, %v; want %q, %v", tt.executable, got, ok, tt.want, tt.ok)
		}
	}
}
