package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/notify"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

type App struct {
	Config   *config.Store
	State    *state.DB
	Uploader *uploader.Service
	Watcher  *volume.Watcher

	mu            sync.RWMutex
	status        Status
	mountedTarget *volume.Info
	// syncCancel is non-nil while a sync goroutine is running, until it has
	// published its result; cancelling does not clear it.
	syncCancel context.CancelFunc
	syncMu     sync.Mutex
	onStatus   func(Status)
	dryRun     bool

	// resumer restarts a sync once Immich is reachable again; see resume().
	resumer    *resumer
	resumeOnce sync.Once

	// cardRemoved is set just before cancelling an in-flight sync because its
	// volume was unmounted, so the sync goroutine can tell that apart from a
	// user-requested stop once the sync unwinds.
	cardRemoved atomic.Bool
}

func New(cfg *config.Store, st *state.DB, dryRun bool) (*App, error) {
	w, err := volume.NewWatcher()
	if err != nil {
		return nil, err
	}
	a := &App{
		Config: cfg,
		State:  st,
		Uploader: &uploader.Service{
			Config: cfg,
			State:  st,
		},
		Watcher: w,
		dryRun:  dryRun,
	}
	a.setStatus(a.startupStatus())
	logx.Info("app started", "configured", cfg.IsConfigured(), "dry_run", dryRun)
	return a, nil
}

// startupStatus computes the status to show right when the app starts,
// before Run() begins watching volumes or kicks off a sync. It is
// IdleStatus, unless the selected card is already mounted — in that case
// Run() is about to start a sync, so the last finished run's outcome is
// shown instead (see statusFromRun) rather than a blank status.
func (a *App) startupStatus() Status {
	cfg := a.Config.Get()
	mounted, err := volume.ListRemovable()
	if err != nil {
		logx.Warn("failed to list removable volumes", "err", err)
		mounted = nil
	}
	if st, ok := IdleStatus(cfg, mounted); ok {
		return st
	}
	run, err := a.State.LastFinishedRun()
	if err != nil {
		logx.Warn("failed to load last finished run", "err", err)
		return newStatus(CodeScanning, nil)
	}
	return statusFromRun(run)
}

// OnStatus registers fn to receive every status change. Call it before Run.
func (a *App) OnStatus(fn func(Status)) {
	a.onStatus = fn
}

// resume returns the App's resumer, creating it on first use (tests may
// set a.resumer beforehand).
func (a *App) resume() *resumer {
	a.resumeOnce.Do(func() {
		if a.resumer == nil {
			a.resumer = newResumer(a.pingServer, a.canResume, a.startSync)
		}
	})
	return a.resumer
}

// pingServer checks whether the configured Immich server is reachable.
func (a *App) pingServer(ctx context.Context) error {
	cfg := a.Config.Get()
	return immich.NewClient(cfg.Immich.URL, cfg.Immich.APIKey).Ping(ctx)
}

// canResume reports whether an automatic resume may start a sync: none is
// running and the selected card is still mounted.
func (a *App) canResume() bool {
	return !a.syncRunning() && a.MountedTarget() != nil
}

// maybeResume starts waiting for the server if a sync that finished with
// code should be resumed automatically.
func (a *App) maybeResume(code Code) {
	if shouldResume(code, a.MountedTarget() != nil) {
		logx.Info("waiting for immich to come back to resume sync", "code", code)
		a.resume().Start()
	}
}

func (a *App) Run(ctx context.Context) {
	logx.Info("app run loop started")
	go a.watchVolumes(ctx)
	a.refreshMountedTarget()
	a.updateIdleStatus()
	if a.Config.IsConfigured() && a.MountedTarget() != nil {
		go a.SyncNow()
	}

	<-ctx.Done()
	logx.Info("app shutting down")
	a.resume().Stop()
	if a.Watcher != nil {
		a.Watcher.Close()
	}
}

func (a *App) watchVolumes(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-a.Watcher.Events():
			if !ok {
				return
			}
			a.handleVolumeEvent(ev)
		}
	}
}

func (a *App) handleVolumeEvent(ev volume.Event) {
	cfg := a.Config.Get()
	watchUUID := cfg.Watch.VolumeUUID
	if watchUUID == "" {
		a.updateIdleStatus()
		return
	}

	switch ev.Type {
	case volume.EventMounted:
		logx.Info("volume mounted", "name", ev.Info.Name, "uuid", ev.Info.UUID, "path", ev.Info.MountPoint)
		if strings.EqualFold(ev.Info.UUID, watchUUID) {
			a.mu.Lock()
			v := ev.Info
			a.mountedTarget = &v
			a.mu.Unlock()
			a.updateIdleStatus()
			if a.Config.IsConfigured() {
				logx.Info("target volume connected, starting sync", "name", ev.Info.Name)
				go a.SyncNow()
			}
		}
	case volume.EventUnmounted:
		logx.Info("volume unmounted", "name", ev.Info.Name, "path", ev.Info.MountPoint)
		a.mu.Lock()
		removed := a.mountedTarget != nil && a.mountedTarget.MountPoint == ev.Info.MountPoint
		if removed {
			a.mountedTarget = nil
		}
		a.mu.Unlock()
		if removed {
			// Neither a.mu nor syncMu may be held here: Stop waits for an
			// in-progress resume, which takes both.
			a.resume().Stop()
			a.cardRemoved.Store(true)
			a.cancelSync()
		}
		a.updateIdleStatus()
	}
}

// refreshMountedTarget looks up whether the selected card is mounted. It
// clears mountedTarget when it isn't (e.g. another card was just selected).
func (a *App) refreshMountedTarget() {
	cfg := a.Config.Get()
	var info *volume.Info
	if cfg.Watch.VolumeUUID != "" {
		var err error
		info, err = volume.FindByUUID(cfg.Watch.VolumeUUID)
		if err != nil {
			logx.Warn("failed to look up selected card", "err", err)
			return
		}
	}
	a.mu.Lock()
	a.mountedTarget = info
	a.mu.Unlock()
}

func (a *App) ConfigChanged() {
	a.refreshMountedTarget()
	a.updateIdleStatus()
	logx.Debug("config changed", "configured", a.Config.IsConfigured())
}

func (a *App) MountedTarget() *volume.Info {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.mountedTarget == nil {
		return nil
	}
	v := *a.mountedTarget
	return &v
}

// SelectCard makes v the card to sync and, if it is mounted and the server
// is configured, starts syncing it right away (as when it is plugged in).
func (a *App) SelectCard(v volume.Info) error {
	if err := a.Config.Update(func(c *config.Config) {
		c.Watch.VolumeUUID = v.UUID
		c.Watch.VolumeName = v.Name
	}); err != nil {
		return fmt.Errorf("save selected card: %w", err)
	}
	logx.Info("card selected", "name", v.Name, "uuid", v.UUID)
	a.ConfigChanged()
	if a.Config.IsConfigured() && a.MountedTarget() != nil {
		a.SyncNow()
	}
	return nil
}

// EjectCard ejects the selected card. It fails if the card isn't mounted;
// on success the unmount event updates the status.
func (a *App) EjectCard() error {
	vol := a.MountedTarget()
	if vol == nil {
		return errors.New("selected card is not mounted")
	}
	logx.Info("ejecting card", "name", vol.Name, "path", vol.MountPoint)
	return volume.Eject(vol.MountPoint)
}

// SyncNow stops waiting for an automatic resume and starts a sync of the
// selected card. It does nothing if a sync is already running.
func (a *App) SyncNow() {
	a.resume().Stop()
	a.startSync()
}

// Retry is SyncNow, offered after a failed sync.
func (a *App) Retry() {
	a.SyncNow()
}

// startSync starts a sync unless one is already running. The resumer calls
// it directly so that it doesn't stop itself.
func (a *App) startSync() {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()

	if !a.Config.IsConfigured() {
		logx.Debug("sync skipped: not configured")
		return
	}
	vol := a.MountedTarget()
	if vol == nil {
		logx.Debug("sync skipped: volume not mounted")
		return
	}
	if a.syncCancel != nil {
		logx.Debug("sync skipped: already running")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.syncCancel = cancel
	a.cardRemoved.Store(false)

	logx.Info("sync requested", "volume", vol.Name, "path", vol.MountPoint)

	go func() {
		syncStart := time.Now()
		a.setStatus(newStatus(CodeScanning, nil))

		runID, startErr := a.State.StartRun(vol.UUID, vol.Name, syncStart)
		if startErr != nil {
			logx.Error("state: failed to record sync run start", "err", startErr)
		}

		result, err := a.Uploader.Sync(ctx, *vol, a.dryRun, func(p uploader.Progress) {
			switch p.Phase {
			case "scanning":
				a.setStatus(newStatus(CodeScanning, nil))
			case "syncing":
				st := newStatus(CodeSyncing, nil)
				st.Progress = &Progress{
					Current:     p.Current,
					Total:       p.Total,
					BytesDone:   p.BytesDone,
					BytesTotal:  p.BytesTotal,
					BytesPerSec: p.BytesPerSec,
					ETASeconds:  int(p.ETA.Round(time.Second).Seconds()),
					File:        p.File,
				}
				a.setStatus(st)
			}
		})

		cardRemoved := a.cardRemoved.Load()
		st := ResultStatus(result, err, cardRemoved, serverHostFromConfig(a.Config.Get()))

		switch {
		case err == nil:
			logx.Info("sync finished",
				"uploaded", result.Uploaded,
				"skipped", result.Skipped,
				"errors", result.Errors,
				"duration", time.Since(syncStart).Round(time.Second).String(),
			)
		case errors.Is(err, context.Canceled):
			logx.Info("sync cancelled",
				"card_removed", cardRemoved,
				"uploaded", result.Uploaded,
				"duration", time.Since(syncStart).Round(time.Second),
			)
		default:
			logx.Error("sync failed", "err", err, "duration", time.Since(syncStart).Round(time.Second))
		}

		if startErr == nil {
			finishedAt := time.Now()
			if finishErr := a.State.FinishRun(state.SyncRun{
				ID:           runID,
				FinishedAt:   &finishedAt,
				ResultCode:   string(st.Code),
				ResultParams: st.Params,
				Uploaded:     result.Uploaded,
				Skipped:      result.Skipped,
				Errors:       result.Errors,
				Bytes:        result.Bytes,
				ErrorSummary: result.ErrorSummary,
			}); finishErr != nil {
				logx.Error("state: failed to record sync run result", "err", finishErr)
			}
		}

		a.setStatus(st)
		a.syncMu.Lock()
		a.syncCancel = nil
		a.syncMu.Unlock()
		cancel()
		a.maybeResume(st.Code)

		if err == nil && !a.dryRun && result.Uploaded > 0 {
			notify.SyncComplete(result.Uploaded)
		}
		if revertsAutomatically(st.Code) {
			time.AfterFunc(5*time.Second, func() { a.updateIdleStatus() })
		}
	}()
}

// revertsAutomatically reports whether a terminal sync status should fall
// back to the idle status on its own after a short delay. Codes that need
// user attention (errors, auth problems) stay on screen instead.
func revertsAutomatically(code Code) bool {
	switch code {
	case CodeNothingNew, CodeDone, CodeCancelled, CodeCardRemoved:
		return true
	default:
		return false
	}
}

// serverHostFromConfig returns the host part of the configured Immich URL,
// falling back to the raw URL when it cannot be parsed.
func serverHostFromConfig(cfg config.Config) string {
	u, err := url.Parse(cfg.Immich.URL)
	if err != nil || u.Host == "" {
		return cfg.Immich.URL
	}
	return u.Host
}

// StopSync stops waiting for an automatic resume and cancels a running
// sync, which then finishes with CodeCancelled.
func (a *App) StopSync() {
	a.resume().Stop()
	a.cancelSync()
}

func (a *App) cancelSync() {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	if a.syncCancel != nil {
		a.syncCancel()
	}
}

// syncRunning reports whether a sync goroutine is running.
func (a *App) syncRunning() bool {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	return a.syncCancel != nil
}

// updateIdleStatus recomputes why sync isn't running right now and publishes
// it, unless the selected card is mounted: in that case a sync is about to
// run (or just did), so the current status is left alone.
func (a *App) updateIdleStatus() {
	cfg := a.Config.Get()
	mounted, err := volume.ListRemovable()
	if err != nil {
		logx.Warn("failed to list removable volumes", "err", err)
		mounted = nil
	}
	if st, ok := IdleStatus(cfg, mounted); ok {
		a.setStatus(st)
	}
}

func (a *App) setStatus(s Status) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	if a.onStatus != nil {
		a.onStatus(s)
	}
}

func (a *App) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}
