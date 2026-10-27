package gui

import (
	"github.com/dmitiigorshkov/sd-sync-immich/internal/gallery"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// galleryFor returns what the window's gallery shows for target, the
// mounted card (nil when none is mounted, selected or not): nil, nil
// without target, g.List(*target) with it. Losing the mounted card clears
// g's cached scan, so a stale generation is never served once a different
// card is mounted next.
func galleryFor(target *volume.Info, g *gallery.Gallery) (*gallery.List, error) {
	if target == nil {
		g.Clear()
		return nil, nil
	}
	list, err := g.List(*target)
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// Gallery returns the photos on the selected, mounted card, nil when no
// card is mounted.
func (s *Service) Gallery() (*gallery.List, error) {
	return galleryFor(s.app.MountedTarget(), s.gallery)
}

// SetFullscreen turns the window's fullscreen mode on or off. It is a
// no-op before the window exists.
func (s *Service) SetFullscreen(on bool) {
	if s.shell.window == nil {
		return
	}
	if on {
		s.shell.window.Fullscreen()
		return
	}
	s.shell.window.UnFullscreen()
}

// IsFullscreen reports whether the window is in fullscreen mode, false
// before the window exists.
func (s *Service) IsFullscreen() bool {
	if s.shell.window == nil {
		return false
	}
	return s.shell.window.IsFullscreen()
}
