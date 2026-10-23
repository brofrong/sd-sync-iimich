package gui

import (
	"errors"
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
