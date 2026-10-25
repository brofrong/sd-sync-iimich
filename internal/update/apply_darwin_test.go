package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeRestart replaces the commands prepareRestart runs and records them.
func fakeRestart(t *testing.T, launchd bool, resignErr error) *[]string {
	t.Helper()
	var calls []string
	origResign, origLaunchd, origOpen := resignBundle, underLaunchd, openWhenExited
	resignBundle = func(path string) error {
		calls = append(calls, "resign "+path)
		return resignErr
	}
	underLaunchd = func() bool { return launchd }
	openWhenExited = func(path string) error {
		calls = append(calls, "open "+path)
		return nil
	}
	t.Cleanup(func() { resignBundle, underLaunchd, openWhenExited = origResign, origLaunchd, origOpen })
	return &calls
}

const bundledExe = "/Users/x/Applications/SD Sync Immich.app/Contents/MacOS/sd-sync-immich"

func TestPrepareRestartResignsBundleUnderLaunchd(t *testing.T) {
	calls := fakeRestart(t, true, nil)
	if err := prepareRestart(bundledExe); err != nil {
		t.Fatal(err)
	}
	if want := []string{"resign /Users/x/Applications/SD Sync Immich.app"}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %q, want %q", *calls, want)
	}
}

func TestPrepareRestartReopensBundleOutsideLaunchd(t *testing.T) {
	calls := fakeRestart(t, false, nil)
	if err := prepareRestart(bundledExe); err != nil {
		t.Fatal(err)
	}
	want := []string{"resign /Users/x/Applications/SD Sync Immich.app", "open /Users/x/Applications/SD Sync Immich.app"}
	if !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %q, want %q", *calls, want)
	}
}

func TestPrepareRestartFailsWhenResignFails(t *testing.T) {
	calls := fakeRestart(t, false, errors.New("codesign failed"))
	if err := prepareRestart(bundledExe); err == nil {
		t.Fatal("prepareRestart succeeded although re-signing failed")
	}
	if want := []string{"resign /Users/x/Applications/SD Sync Immich.app"}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %q, want %q (no restart after a failed re-sign)", *calls, want)
	}
}

func TestPrepareRestartOutsideBundleDoesNothing(t *testing.T) {
	calls := fakeRestart(t, false, nil)
	if err := prepareRestart("/Users/x/Applications/sd-sync-immich"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("calls = %q, want none", *calls)
	}
}

func TestDarwinApplyReplacesExecutableAndRestarts(t *testing.T) {
	newBinary := []byte("new executable")
	client, rel, _ := fakeRelease(t, AssetName("darwin", "arm64"), newBinary, "")
	exe := filepath.Join(t.TempDir(), "SD Sync Immich.app", "Contents", "MacOS", "sd-sync-immich")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	origExe := currentExecutable
	currentExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { currentExecutable = origExe })
	calls := fakeRestart(t, true, nil)

	quit, err := Apply(context.Background(), client, rel)
	if err != nil || !quit {
		t.Fatalf("Apply() = %v, %v; want quit, no error", quit, err)
	}
	if got, _ := os.ReadFile(exe); string(got) != string(newBinary) {
		t.Fatalf("executable = %q, want %q", got, newBinary)
	}
	if want := []string{"resign " + filepath.Dir(filepath.Dir(filepath.Dir(exe)))}; !reflect.DeepEqual(*calls, want) {
		t.Fatalf("calls = %q, want %q", *calls, want)
	}
}

func TestDarwinApplyDoesNotQuitWhenResignFails(t *testing.T) {
	client, rel, _ := fakeRelease(t, AssetName("darwin", "arm64"), []byte("new executable"), "")
	exe := filepath.Join(t.TempDir(), "SD Sync Immich.app", "Contents", "MacOS", "sd-sync-immich")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("old executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	origExe := currentExecutable
	currentExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { currentExecutable = origExe })
	fakeRestart(t, false, errors.New("codesign failed"))

	if quit, err := Apply(context.Background(), client, rel); err == nil || quit {
		t.Fatalf("Apply() = %v, %v; want no quit and an error", quit, err)
	}
}
