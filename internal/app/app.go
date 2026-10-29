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
	"github.com/dmitiigorshkov/sd-sync-immich/internal/power"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

type App struct {
	Config   *config.Store
	State    *state.DB
	Uploader *uploader.Service
	Watcher  *volume.Watcher

	mu     sync.RWMutex
	status Status
	// statusGen counts published statuses (guarded by mu), so a delayed
	// revert can tell whether its status is still the one shown.
	statusGen     uint64
	mountedTarget *volume.Info
	// syncCancel is non-nil while a sync goroutine is running, until it has
	// published its result; cancelling does not clear it.
	syncCancel context.CancelFunc
	syncMu     sync.Mutex
	onStatus   func(Status)
	onSynced   func(SyncReport)
	dryRun     bool

	// resumer restarts a sync once Immich is reachable again; see resume().
	resumer    *resumer
	resumeOnce sync.Once

	// cardRemoved is set just before cancelling an in-flight sync because its
	// volume was unmounted, so the sync goroutine can tell that apart from a
	// user-requested stop once the sync unwinds.
	cardRemoved atomic.Bool

	onNotice func(Notice)
	// ejecting is the mount point the app is ejecting itself after a sync
	// (guarded by mu), so the unmount that follows is not taken for the
	// card being pulled out.
	ejecting string
	// followMu serializes following a formatted card (startup and mount
	// events may both try). It is taken before any other App lock.
	followMu sync.Mutex
	// revertAfter is how long a terminal status that reverts on its own
	// stays before the idle status replaces it (0: defaultRevertAfter).
	revertAfter time.Duration

	// countdownCancel cancels the running format countdown (guarded by mu;
	// nil when none runs). See CancelFormat.
	countdownCancel context.CancelFunc
	// erasing is the mount point of the card being erased (guarded by mu):
	// every volume event there is ignored until the erase is over.
	erasing string
	// adopted is the mount point of a card the app just erased and selected
	// (guarded by mu): the watcher's mount event for it is an echo of the
	// format, not a card being plugged in.
	adopted string
	// formatFailures holds the lower-case UUIDs of cards whose format
	// failed during their current insertion (guarded by mu): they are not
	// formatted again until pulled out.
	formatFailures map[string]bool
	// needsAdminReported holds the lower-case UUIDs of cards told during
	// their current insertion that formatting needs administrator rights
	// (guarded by mu; see autoFormat). It is forgotten when the card is
	// mounted again or pulled out.
	needsAdminReported map[string]bool

	// Seams set by New; tests replace them so they never touch the Mac.
	keepAwake     func() (release func())
	eject         func(mountPoint string) error
	isCameraCard  func(mountPoint string) bool
	listRemovable func() ([]volume.Info, error)
	verify        func(ctx context.Context, vol volume.Info, onProgress func(done, total int)) (uploader.VerifyResult, error)
	erase         func(mountPoint, wantUUID string) (volume.Info, error)
	// eraseNeedsAdmin reports whether erase would refuse every card for
	// lack of administrator rights (see volume.EraseNeedsAdmin).
	eraseNeedsAdmin func() bool
}

// defaultRevertAfter is how long a status like CodeDone stays on screen
// before the idle status replaces it.
const defaultRevertAfter = 5 * time.Second

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

		keepAwake:     power.KeepAwake,
		eject:         volume.Eject,
		isCameraCard:  isCameraCardDir,
		listRemovable: volume.ListRemovable,
		erase:         volume.Erase,

		eraseNeedsAdmin: volume.EraseNeedsAdmin,
	}
	a.verify = a.Uploader.VerifyCard
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
	mounted, err := a.removableVolumes()
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

// SyncReport is a finished sync: its status code (before ejecting or
// formatting the card), its counts and how long it took.
type SyncReport struct {
	Code     Code
	Result   uploader.Result
	Duration time.Duration
}

// OnSynced registers fn to receive every finished sync, dry runs aside.
// Call it before Run.
func (a *App) OnSynced(fn func(SyncReport)) {
	a.onSynced = fn
}

// resume returns the App's resumer, creating it on first use (tests may
// set a.resumer beforehand).
func (a *App) resume() *resumer {
	a.resumeOnce.Do(func() {
		if a.resumer == nil {
			a.resumer = newResumer(a.pingServer, a.canResume, a.startFullSync)
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
	if a.MountedTarget() == nil {
		// The selected card may have been formatted while the app wasn't
		// running; following it starts its sync.
		a.maybeFollowCard(nil)
	} else if a.Config.IsConfigured() {
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
	if a.Watcher == nil {
		return
	}
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
	if a.ignoreFormatEvent(ev) {
		return
	}
	cfg := a.Config.Get()
	watchUUID := cfg.Watch.VolumeUUID
	if watchUUID == "" {
		a.updateIdleStatus()
		return
	}

	switch ev.Type {
	case volume.EventMounted:
		logx.Info("volume mounted", "name", ev.Info.Name, "uuid", ev.Info.UUID, "path", ev.Info.MountPoint)
		a.mu.Lock()
		if a.ejecting == ev.Info.MountPoint {
			// Whatever the app ejected from here is gone: a new volume
			// took its mount point (its unmount event was missed).
			a.ejecting = ""
		}
		a.mu.Unlock()
		if strings.EqualFold(ev.Info.UUID, watchUUID) {
			a.mu.Lock()
			a.setMountedTargetLocked(&ev.Info)
			// A new insertion (also after the app ejected the card).
			delete(a.needsAdminReported, strings.ToLower(ev.Info.UUID))
			a.mu.Unlock()
			a.updateIdleStatus()
			if a.Config.IsConfigured() {
				logx.Info("target volume connected, starting sync", "name", ev.Info.Name)
				go a.SyncNow()
			}
		} else if a.MountedTarget() == nil && !a.maybeFollowCard(&ev.Info) {
			// Another card: it replaces whatever status was shown (e.g.
			// ejected). With the selected card mounted, its sync or result
			// stays on screen instead.
			a.updateIdleStatusWith(&ev.Info)
		}
	case volume.EventUnmounted:
		logx.Info("volume unmounted", "name", ev.Info.Name, "path", ev.Info.MountPoint)
		a.mu.Lock()
		own := a.takeOwnEjectLocked(ev.Info.MountPoint)
		removed := a.mountedTarget != nil && a.mountedTarget.MountPoint == ev.Info.MountPoint
		if !own {
			// The card was pulled out (events during the app's own erase
			// never get here): a failed format may be tried again on its
			// next insertion.
			a.clearFormatSuppressionLocked(ev.Info.UUID)
			if removed {
				a.clearFormatSuppressionLocked(a.mountedTarget.UUID)
			}
		}
		if removed {
			a.setMountedTargetLocked(nil)
		}
		a.mu.Unlock()
		if own {
			// The app ejected the card after a sync: nothing was pulled
			// out mid-sync, and the ejected status stays until the next
			// mount or user action.
			logx.Info("card ejected by the app is unmounted", "name", ev.Info.Name)
			return
		}
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
	a.setMountedTargetLocked(info)
	a.mu.Unlock()
}

func (a *App) ConfigChanged() {
	a.refreshMountedTarget()
	a.updateIdleStatus()
	logx.Debug("config changed", "configured", a.Config.IsConfigured())
}

// setMountedTargetLocked sets mountedTarget to a copy of v (nil: none) and
// forgets any own-eject and formatted-card marker: they belonged to the
// previous target. Call with a.mu held.
func (a *App) setMountedTargetLocked(v *volume.Info) {
	a.ejecting = ""
	a.adopted = ""
	if v == nil {
		a.mountedTarget = nil
		return
	}
	c := *v
	a.mountedTarget = &c
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

// ErrCardNotMounted is returned by EjectCard and FormatCard when the
// selected card isn't mounted.
var ErrCardNotMounted = errors.New("selected card is not mounted")

// EjectCard ejects the selected card. It fails if the card isn't mounted;
// on success the unmount event updates the status.
func (a *App) EjectCard() error {
	vol := a.MountedTarget()
	if vol == nil {
		return ErrCardNotMounted
	}
	logx.Info("ejecting card", "name", vol.Name, "path", vol.MountPoint)
	return volume.Eject(vol.MountPoint)
}

// ErrCardSyncing is returned by ForgetCard for the card being synced.
var ErrCardSyncing = errors.New("the card is syncing")

// ForgetCard deletes what the app remembers about the card with uuid: which
// of its files were uploaded and where its sync stopped (the history is
// kept). The selected card is deselected. It fails while that card syncs.
func (a *App) ForgetCard(uuid string) error {
	selectedUUID := a.Config.Get().Watch.VolumeUUID
	selected := strings.EqualFold(selectedUUID, uuid)
	if selected {
		uuid = selectedUUID // as the database has it
		// Under syncMu no sync can start: once it is released the card is
		// no longer the target, so none will.
		a.syncMu.Lock()
		if a.syncCancel != nil {
			a.syncMu.Unlock()
			return ErrCardSyncing
		}
		err := a.Config.Update(func(c *config.Config) {
			c.Watch.VolumeUUID = ""
			c.Watch.VolumeName = ""
		})
		if err == nil {
			a.mu.Lock()
			a.setMountedTargetLocked(nil)
			a.mu.Unlock()
		}
		a.syncMu.Unlock()
		if err != nil {
			return fmt.Errorf("deselect card: %w", err)
		}
		a.resume().Stop()
	}
	if err := a.State.ForgetCard(uuid); err != nil {
		return fmt.Errorf("forget card: %w", err)
	}
	logx.Info("card forgotten", "uuid", uuid, "was_selected", selected)
	if selected {
		a.ConfigChanged()
	}
	return nil
}

// SyncNow stops waiting for an automatic resume and starts a sync of the
// selected card. It does nothing if a sync is already running.
func (a *App) SyncNow() {
	a.resume().Stop()
	a.startSync(nil)
}

// Retry is SyncNow, offered after a failed sync.
func (a *App) Retry() {
	a.SyncNow()
}

// startFullSync starts a sync of every new file (see startSync). The
// resumer calls it directly so that it doesn't stop itself.
func (a *App) startFullSync() {
	a.startSync(nil)
}

// startSync starts a sync unless one is already running and reports
// whether it did. only, when non-empty, limits it to those relative paths
// (see uploader.SyncOptions.Only).
func (a *App) startSync(only []string) bool {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()

	if !a.Config.IsConfigured() {
		logx.Debug("sync skipped: not configured")
		return false
	}
	vol := a.MountedTarget()
	if vol == nil {
		logx.Debug("sync skipped: volume not mounted")
		return false
	}
	if a.syncCancel != nil {
		logx.Debug("sync skipped: already running")
		return false
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.syncCancel = cancel
	a.cardRemoved.Store(false)

	logx.Info("sync requested", "volume", vol.Name, "path", vol.MountPoint, "only", len(only))

	go func() {
		// The Mac stays awake for the sync and the post-sync phase.
		release := a.keepMacAwake()
		defer release()

		syncStart := time.Now()
		a.setStatus(newStatus(CodeScanning, nil))

		runID, startErr := a.State.StartRun(vol.UUID, vol.Name, syncStart)
		if startErr != nil {
			logx.Error("state: failed to record sync run start", "err", startErr)
		}

		result, err := a.Uploader.Sync(ctx, *vol, uploader.SyncOptions{DryRun: a.dryRun, Only: only}, func(p uploader.Progress) {
			switch p.Phase {
			case "scanning":
				a.setStatus(newStatus(CodeScanning, nil))
			case "syncing":
				st := newStatus(CodeSyncing, nil)
				st.Progress = progressFrom(p)
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
				Duplicates:   result.Duplicates,
				Bytes:        result.Bytes,
				ErrorSummary: result.ErrorSummary,
			}); finishErr != nil {
				logx.Error("state: failed to record sync run result", "err", finishErr)
			}
			if recErr := a.State.RecordFailures(runID, result.Failures); recErr != nil {
				logx.Error("state: failed to record failed files", "err", recErr)
			}
		}

		if a.onSynced != nil && !a.dryRun {
			a.onSynced(SyncReport{Code: st.Code, Result: result, Duration: time.Since(syncStart)})
		}

		a.setStatus(st)
		// The post-sync phase still counts as the sync running.
		st = a.afterSync(ctx, *vol, result, st)

		a.syncMu.Lock()
		a.syncCancel = nil
		a.syncMu.Unlock()
		cancel()
		a.maybeResume(st.Code)

		// Decided on the final status: CodeEjected stays on screen.
		if revertsAutomatically(st.Code) {
			delay := a.revertAfter
			if delay <= 0 {
				delay = defaultRevertAfter
			}
			gen := a.currentStatusGen()
			time.AfterFunc(delay, func() { a.revertToIdle(gen) })
		}
	}()
	return true
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
	a.updateIdleStatusWith(nil)
}

// updateIdleStatusWith is updateIdleStatus counting extra (a volume that
// was just mounted and may not be listed yet) as mounted.
func (a *App) updateIdleStatusWith(extra *volume.Info) {
	cfg := a.Config.Get()
	mounted, err := a.removableVolumes()
	if err != nil {
		logx.Warn("failed to list removable volumes", "err", err)
		mounted = nil
	}
	if extra != nil && !containsVolume(mounted, extra.UUID) {
		mounted = append(mounted, *extra)
	}
	if st, ok := IdleStatus(cfg, mounted); ok {
		a.setStatus(st)
	}
}

// revertToIdle replaces the status with the idle status, but only if no
// other status was published since generation gen (e.g. a later sync's
// CodeEjected, which must stay).
func (a *App) revertToIdle(gen uint64) {
	if a.currentStatusGen() != gen {
		return
	}
	a.updateIdleStatus()
}

func (a *App) currentStatusGen() uint64 {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.statusGen
}

// setStatus publishes s and returns its generation (see statusGen).
func (a *App) setStatus(s Status) uint64 {
	a.mu.Lock()
	a.status = s
	a.statusGen++
	gen := a.statusGen
	a.mu.Unlock()
	if a.onStatus != nil {
		a.onStatus(s)
	}
	return gen
}

func (a *App) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}
