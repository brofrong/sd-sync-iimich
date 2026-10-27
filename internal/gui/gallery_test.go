package gui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/gallery"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/testenv"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// newTestGallery returns a gallery.Gallery over the default config and an
// empty database, both under a temporary HOME.
func newTestGallery(t *testing.T) *gallery.Gallery {
	t.Helper()
	testenv.Home(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return gallery.New(cfg, st)
}

// TestGalleryWithoutCard: with no mounted target, galleryFor returns nil,
// nil without scanning anything (the cached scan, if any, is cleared so a
// stale generation is not served once a different card is mounted).
func TestGalleryWithoutCard(t *testing.T) {
	g := newTestGallery(t)
	got, err := galleryFor(nil, g)
	if err != nil {
		t.Fatalf("galleryFor(nil, g) error = %v, want nil", err)
	}
	if got != nil {
		t.Fatalf("galleryFor(nil, g) = %+v, want nil", got)
	}
}

// TestGalleryWithMountedCard: with a mounted target, galleryFor lists its
// photos (an empty card here, since gallery.List itself is covered in
// internal/gallery).
func TestGalleryWithMountedCard(t *testing.T) {
	g := newTestGallery(t)
	vol := volume.Info{Name: "CARD", MountPoint: t.TempDir(), UUID: "VOL-UUID", Removable: true}

	got, err := galleryFor(&vol, g)
	if err != nil {
		t.Fatalf("galleryFor(&vol, g) error = %v", err)
	}
	if got == nil {
		t.Fatal("galleryFor(&vol, g) = nil, want a list")
	}
	if got.CardUUID != vol.UUID || got.CardName != vol.Name {
		t.Fatalf("galleryFor(&vol, g) = %+v, want card %s/%s", got, vol.UUID, vol.Name)
	}
}

// TestServiceGalleryWithoutMountedTarget: Service.Gallery goes through
// galleryFor with the app's mounted target, so a Service whose app has none
// returns nil, nil.
func TestServiceGalleryWithoutMountedTarget(t *testing.T) {
	s := &Service{app: &app.App{}, gallery: newTestGallery(t)}
	got, err := s.Gallery()
	if err != nil || got != nil {
		t.Fatalf("Gallery() = %+v, %v; want nil, nil", got, err)
	}
}

// TestStatusWithoutCardClearsGallery: once the mounted card is gone, the
// next status drops the gallery's scan and thumbnails, even if the window
// never asks for the gallery again (they hold up to 128 MB).
func TestStatusWithoutCardClearsGallery(t *testing.T) {
	g := newTestGallery(t)
	vol := volume.Info{Name: "CARD", MountPoint: t.TempDir(), UUID: "VOL-UUID", Removable: true}
	dir := filepath.Join(vol.MountPoint, "DCIM", "100MSDCF")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "DSC00001.JPG"), []byte("jpeg"), 0o644); err != nil {
		t.Fatal(err)
	}
	list, err := g.List(vol)
	if err != nil || len(list.Photos) != 1 {
		t.Fatalf("List = %+v, %v; want one photo", list, err)
	}
	h := g.Middleware(http.NotFoundHandler())
	src := list.Photos[0].Src
	get := func() int {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://wails.localhost"+src, nil))
		return w.Code
	}
	if code := get(); code != http.StatusOK {
		t.Fatalf("GET %s before = %d, want 200", src, code)
	}

	discard := func(app.Status) {}
	s := &Service{
		app:     &app.App{}, // no mounted target
		gallery: g,
		shell:   &shell{tray: &trayMenu{line: newThrottle(0, discard)}},
		status:  newThrottle(0, discard),
	}
	s.onStatus(app.Status{})

	if code := get(); code != http.StatusNotFound {
		t.Errorf("GET %s after a status without card = %d, want 404 (gallery cleared)", src, code)
	}
}

// TestSetFullscreenWithoutWindow: before the window exists (sh.window is
// nil, e.g. early startup), SetFullscreen is a no-op and IsFullscreen
// reports false instead of panicking.
func TestSetFullscreenWithoutWindow(t *testing.T) {
	s := &Service{shell: &shell{}}
	s.SetFullscreen(true) // must not panic
	if s.IsFullscreen() {
		t.Fatal("IsFullscreen() = true without a window, want false")
	}
}
