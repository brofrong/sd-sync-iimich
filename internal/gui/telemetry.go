package gui

import (
	"context"
	"fmt"
	"runtime"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/install"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/telemetry"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/version"
)

// installKind is how this copy is installed, as telemetry reports it.
func installKind(goos, bundlePath string) string {
	switch {
	case goos == "darwin" && bundlePath != "":
		return "app"
	case goos == "windows":
		return "installer"
	case install.Flatpak():
		return "flatpak"
	case install.AppImage() != "":
		return "appimage"
	}
	return "binary"
}

// newTelemetry returns the telemetry client for this copy of the app.
func (s *Service) newTelemetry() *telemetry.Client {
	return telemetry.New(s.cfg, telemetry.Info{
		Version:   s.version,
		OS:        runtime.GOOS,
		OSVersion: systemVersion(),
		Arch:      runtime.GOARCH,
		Install:   installKind(runtime.GOOS, s.bundlePath),
		Lang:      s.catalog().Lang(),
	}, version.IsRelease())
}

// startTelemetry sends the launch and, from then on, finished syncs and
// logged errors, while the user allows it (see package telemetry).
func (s *Service) startTelemetry(ctx context.Context) {
	logx.OnError(s.telemetry.Error)
	s.app.OnSynced(func(r app.SyncReport) {
		s.telemetry.Synced(telemetry.Sync{
			Result:     string(r.Code),
			Files:      r.Result.Uploaded + r.Result.Duplicates + r.Result.Skipped + r.Result.Errors,
			Uploaded:   r.Result.Uploaded,
			Duplicates: r.Result.Duplicates,
			Skipped:    r.Result.Skipped,
			Errors:     r.Result.Errors,
			Stacks:     r.Result.Stacks,
			Bytes:      r.Result.Bytes,
			Duration:   r.Duration,
			ErrorKinds: r.Result.ErrorSummary,
		})
	})
	s.telemetry.Launch()
	go s.telemetry.Run(ctx)
}

// SetTelemetry turns the anonymous statistics on or off: the user's
// answer to the question in the window, or the switch in Settings.
// Turning them on makes a new install id, turning them off forgets it.
func (s *Service) SetTelemetry(enabled bool) error {
	prev := s.cfg.Get().Telemetry
	if err := s.cfg.Update(func(c *config.Config) { applyTelemetry(c, enabled) }); err != nil {
		return fmt.Errorf("save statistics setting: %w", err)
	}
	logx.Info("statistics setting saved", "enabled", enabled)
	if enabled && !prev.Enabled {
		s.telemetry.Launch()
	}
	s.stateChanged()
	return nil
}

// applyTelemetry records the user's answer in c: on makes a new install
// id unless there is one, off forgets it.
func applyTelemetry(c *config.Config, enabled bool) {
	c.Telemetry.Asked = true
	c.Telemetry.Enabled = enabled
	switch {
	case !enabled:
		c.Telemetry.ID = ""
	case c.Telemetry.ID == "":
		c.Telemetry.ID = telemetry.NewID()
	}
}
