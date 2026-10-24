package gui

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/update"
)

// fakeApply replaces update.Apply and the quit after it.
func fakeApply(t *testing.T, quit bool, err error) (applied *int, quitCodes *[]int) {
	t.Helper()
	var n int
	var codes []int
	origApply, origQuit := applyUpdate, quitAfterUpdate
	applyUpdate = func(context.Context, *update.Client, *update.Release) (bool, error) {
		n++
		return quit, err
	}
	quitAfterUpdate = func(_ *shell, code int) { codes = append(codes, code) }
	t.Cleanup(func() { applyUpdate, quitAfterUpdate = origApply, origQuit })
	return &n, &codes
}

func serviceWithUpdate() *Service {
	return &Service{shell: &shell{}, release: &update.Release{Version: "v9.9.9"}}
}

func TestInstallUpdateQuitsAfterApply(t *testing.T) {
	applied, codes := fakeApply(t, true, nil)
	if err := serviceWithUpdate().InstallUpdate(); err != nil {
		t.Fatal(err)
	}
	if *applied != 1 {
		t.Fatalf("Apply called %d times, want 1", *applied)
	}
	if len(*codes) != 1 || (*codes)[0] != updateExitCode {
		t.Fatalf("quit codes = %v, want [%d]", *codes, updateExitCode)
	}
}

func TestInstallUpdateKeepsRunningWhenApplyFails(t *testing.T) {
	_, codes := fakeApply(t, false, errors.New("SHA-256 mismatch"))
	err := serviceWithUpdate().InstallUpdate()
	if err == nil {
		t.Fatal("InstallUpdate() succeeded although Apply failed")
	}
	if len(*codes) != 0 {
		t.Fatalf("quit with %v after a failed update", *codes)
	}
}

func TestInstallUpdateManualIsACode(t *testing.T) {
	_, codes := fakeApply(t, false, fmt.Errorf("not an AppImage: %w", update.ErrManualUpdate))
	err := serviceWithUpdate().InstallUpdate()
	if err == nil || err.Error() != errUpdateManual {
		t.Fatalf("InstallUpdate() error = %v, want %q (the code the window translates)", err, errUpdateManual)
	}
	if !errors.Is(err, update.ErrManualUpdate) {
		t.Fatalf("error %v does not wrap update.ErrManualUpdate", err)
	}
	if len(*codes) != 0 {
		t.Fatalf("quit with %v", *codes)
	}
}

// TestUpdateExitCode: launchd restarts the app after exit code 75 on
// macOS; on Windows and Linux the installer or the relaunch script starts
// the new version, and the app exits with 0.
func TestUpdateExitCode(t *testing.T) {
	want := 0
	if runtime.GOOS == "darwin" {
		want = 75
	}
	if updateExitCode != want {
		t.Fatalf("updateExitCode = %d on %s, want %d", updateExitCode, runtime.GOOS, want)
	}
}
