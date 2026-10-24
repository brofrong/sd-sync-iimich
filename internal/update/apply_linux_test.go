package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// fakeStart replaces startDetached for the test and records its calls.
func fakeStart(t *testing.T) *recordStart {
	t.Helper()
	rec := &recordStart{}
	orig := startDetached
	startDetached = rec.start
	t.Cleanup(func() { startDetached = orig })
	return rec
}

// runFromAppImage makes this test process look like it runs from the
// AppImage at appimage: $APPDIR holds its executable.
func runFromAppImage(t *testing.T, appimage string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	t.Setenv("APPIMAGE", appimage)
	t.Setenv("APPDIR", filepath.Dir(exe))
}

func TestApplyWithoutAppImageEnv(t *testing.T) {
	client, rel, requests := fakeRelease(t, AssetName("linux"), []byte("new AppImage"), "")
	rec := fakeStart(t)
	t.Setenv("APPIMAGE", "")
	quit, err := Apply(context.Background(), client, rel)
	if quit || !errors.Is(err, ErrManualUpdate) {
		t.Fatalf("Apply() = %v, %v; want no quit and ErrManualUpdate", quit, err)
	}
	if requests.Load() != 0 || len(rec.calls) != 0 {
		t.Fatalf("downloads = %d, started %q; want nothing", requests.Load(), rec.calls)
	}
}

// TestApplyDistrustsAppImageOfAnotherProcess: $APPIMAGE leaks into
// processes an AppImage starts (a terminal opened from it, say); a copy
// that does not run from it must not replace it.
func TestApplyDistrustsAppImageOfAnotherProcess(t *testing.T) {
	for name, appdir := range map[string]string{
		"no APPDIR":                 "",
		"executable outside APPDIR": t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			client, rel, requests := fakeRelease(t, AssetName("linux"), []byte("new AppImage"), "")
			rec := fakeStart(t)
			appimage := oldAppImage(t)
			t.Setenv("APPIMAGE", appimage)
			t.Setenv("APPDIR", appdir)
			quit, err := Apply(context.Background(), client, rel)
			if quit || !errors.Is(err, ErrManualUpdate) {
				t.Fatalf("Apply() = %v, %v; want no quit and ErrManualUpdate", quit, err)
			}
			if requests.Load() != 0 || len(rec.calls) != 0 {
				t.Fatalf("downloads = %d, started %q; want nothing", requests.Load(), rec.calls)
			}
			assertOnlyFile(t, appimage, "old AppImage")
		})
	}
}

func TestApplyReplacesAppImageFromEnv(t *testing.T) {
	client, rel, _ := fakeRelease(t, AssetName("linux"), []byte("new AppImage"), "")
	rec := fakeStart(t)
	appimage := oldAppImage(t)
	runFromAppImage(t, appimage)
	quit, err := Apply(context.Background(), client, rel)
	if !quit || err != nil {
		t.Fatalf("Apply() = %v, %v; want quit, no error", quit, err)
	}
	assertOnlyFile(t, appimage, "new AppImage")
	if len(rec.calls) != 1 || rec.calls[0][4] != strconv.Itoa(os.Getpid()) {
		t.Fatalf("started %q, want a relaunch waiting for pid %d", rec.calls, os.Getpid())
	}
}
