package update

import (
	"context"
	"errors"
	"os"
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

func TestApplyReplacesAppImageFromEnv(t *testing.T) {
	client, rel, _ := fakeRelease(t, AssetName("linux"), []byte("new AppImage"), "")
	rec := fakeStart(t)
	appimage := oldAppImage(t)
	t.Setenv("APPIMAGE", appimage)
	quit, err := Apply(context.Background(), client, rel)
	if !quit || err != nil {
		t.Fatalf("Apply() = %v, %v; want quit, no error", quit, err)
	}
	assertOnlyFile(t, appimage, "new AppImage")
	if len(rec.calls) != 1 || rec.calls[0][len(rec.calls[0])-2] != strconv.Itoa(os.Getpid()) {
		t.Fatalf("started %q, want a relaunch waiting for pid %d", rec.calls, os.Getpid())
	}
}
