package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// NoticeKind identifies a one-off event worth telling the user about,
// independent of language (the GUI translates it, like a Code).
type NoticeKind string

const (
	// NoticeCardFollowed: the selected card was replaced by a camera card
	// with the same name (it was formatted). Params: name.
	NoticeCardFollowed NoticeKind = "card_followed"
	// NoticeEjectFailed: the automatic eject after a sync failed (e.g. the
	// card is open in Finder). Params: name, detail.
	NoticeEjectFailed NoticeKind = "eject_failed"
)

// Notice is an event that is not a status: it happens once and does not
// change what the status card shows.
type Notice struct {
	Kind   NoticeKind        `json:"kind"`
	Params map[string]string `json:"params"`
}

// OnNotice registers fn to receive every Notice. Call it before Run. fn is
// called synchronously, with no App lock held.
func (a *App) OnNotice(fn func(Notice)) {
	a.onNotice = fn
}

func (a *App) notify(n Notice) {
	if n.Params == nil {
		n.Params = map[string]string{}
	}
	logx.Info("notice", "kind", n.Kind, "params", n.Params)
	if a.onNotice != nil {
		a.onNotice(n)
	}
}

// ErrRetryUnavailable is returned by RetryFailures when the failed files of
// a run can't be retried right now: the run's card isn't the selected,
// mounted card, a sync is running, or the run has no recorded failures.
var ErrRetryUnavailable = errors.New("retrying the failed files is not available")

// RetryFailures syncs again only the files that failed in run runID. The
// retry is recorded as a new run.
func (a *App) RetryFailures(runID int64) error {
	run, err := a.State.Run(runID)
	if err != nil {
		return fmt.Errorf("load run: %w", err)
	}
	if run == nil || !run.FailuresRecorded {
		return ErrRetryUnavailable
	}
	vol := a.MountedTarget()
	if vol == nil || !strings.EqualFold(vol.UUID, run.VolumeUUID) ||
		!strings.EqualFold(a.Config.Get().Watch.VolumeUUID, run.VolumeUUID) {
		return ErrRetryUnavailable
	}
	if a.syncRunning() {
		return ErrRetryUnavailable
	}
	failures, err := a.State.RunFailures(runID)
	if err != nil {
		return fmt.Errorf("load failed files: %w", err)
	}
	if len(failures) == 0 {
		return ErrRetryUnavailable
	}
	only := make([]string, len(failures))
	for i, f := range failures {
		only[i] = f.RelativePath
	}
	if !a.startSync(only) {
		return ErrRetryUnavailable
	}
	logx.Info("retrying failed files", "run", runID, "files", len(only))
	// Only now that the retry runs is a pending auto-resume obsolete (if it
	// fires meanwhile, its start is a no-op). Neither a.mu nor syncMu is
	// held: Stop waits for an in-progress resume.
	a.resume().Stop()
	return nil
}

// afterSync runs in the sync goroutine once the result of a sync has been
// recorded and st (its ResultStatus) published, while the sync still counts
// as running and the Mac is kept awake. It decides what happens to the
// card next — auto-format (see autoFormat), then auto-eject — and returns
// the final status. The decisions use the sync's result (st.Code is the
// result code) or whether the card was just formatted, never whatever
// status is current.
func (a *App) afterSync(ctx context.Context, vol volume.Info, res uploader.Result, st Status) Status {
	ejectable := st.Code == CodeDone
	if a.wantsFormat(res, st) && a.stillMounted(ctx, vol) {
		out := a.autoFormat(ctx, vol, st)
		if out.failed {
			// The card stays as it is for the user to look at.
			return out.status
		}
		st, vol = out.status, out.vol
		ejectable = ejectable || out.formatted
	}
	if ejectable && a.Config.Get().Card.AutoEject && a.stillMounted(ctx, vol) {
		st = a.autoEject(vol, res, st)
	}
	return st
}

// stillMounted reports whether vol is still the mounted target, at the same
// mount point, and the post-sync phase was not stopped.
func (a *App) stillMounted(ctx context.Context, vol volume.Info) bool {
	if ctx.Err() != nil {
		return false
	}
	mt := a.MountedTarget()
	return mt != nil && strings.EqualFold(mt.UUID, vol.UUID) && mt.MountPoint == vol.MountPoint
}

// autoEject ejects vol after a successful sync. On success it publishes and
// returns CodeEjected; on failure it raises NoticeEjectFailed once and keeps
// st. There are no retries.
func (a *App) autoEject(vol volume.Info, res uploader.Result, st Status) Status {
	// Mark the mount point first: the unmount event may arrive before
	// eject returns, and must not be taken for the card being pulled out.
	a.mu.Lock()
	a.ejecting = vol.MountPoint
	a.mu.Unlock()

	logx.Info("auto-ejecting card", "name", vol.Name, "path", vol.MountPoint)
	if err := a.ejectVolume(vol.MountPoint); err != nil {
		a.mu.Lock()
		if a.ejecting == vol.MountPoint {
			a.ejecting = ""
		}
		a.mu.Unlock()
		logx.Warn("auto-eject failed", "name", vol.Name, "err", err)
		a.notify(Notice{Kind: NoticeEjectFailed, Params: map[string]string{
			"name":   vol.Name,
			"detail": err.Error(),
		}})
		return st
	}
	logx.Info("card ejected", "name", vol.Name)
	// The card is gone even if its unmount event never comes (the watcher
	// may not have seen a just-formatted card mount yet). The own-eject
	// marker stays for an unmount event that does come.
	a.mu.Lock()
	if mt := a.mountedTarget; mt != nil && strings.EqualFold(mt.UUID, vol.UUID) && mt.MountPoint == vol.MountPoint {
		a.mountedTarget = nil
	}
	if a.adopted == vol.MountPoint {
		a.adopted = ""
	}
	a.mu.Unlock()
	ej := newStatus(CodeEjected, map[string]string{"uploaded": strconv.Itoa(res.Uploaded)})
	a.setStatus(ej)
	return ej
}

// takeOwnEjectLocked reports whether mountPoint is the one the app is ejecting
// itself, and forgets it if so. Call with a.mu held.
func (a *App) takeOwnEjectLocked(mountPoint string) bool {
	if a.ejecting == "" || a.ejecting != mountPoint {
		return false
	}
	a.ejecting = ""
	return true
}

// followCandidate returns the card to switch to when the selected card was
// formatted (its volume UUID changed): the server is configured, a card is
// selected but not mounted, and exactly one mounted volume has the
// selected card's name (case-insensitively) — and it is a camera card.
// (maybeFollowCard also skips a candidate the app has synced before.)
// Several same-name volumes, camera cards or not, mean no candidate: the
// app must not guess which one is the selected card.
func followCandidate(cfg config.Config, mounted []volume.Info, isCameraCard func(string) bool) (volume.Info, bool) {
	if !cfg.ServerConfigured() || cfg.Watch.VolumeUUID == "" || cfg.Watch.VolumeName == "" {
		return volume.Info{}, false
	}
	var match []volume.Info
	for _, v := range mounted {
		if strings.EqualFold(v.UUID, cfg.Watch.VolumeUUID) {
			return volume.Info{}, false
		}
		if strings.EqualFold(v.Name, cfg.Watch.VolumeName) {
			match = append(match, v)
		}
	}
	if len(match) != 1 || !isCameraCard(match[0].MountPoint) {
		return volume.Info{}, false
	}
	return match[0], true
}

// isCameraCardDir reports whether the volume at mountPoint looks like a
// camera's card: it has a DCIM or PRIVATE/M4ROOT (Sony video) folder.
func isCameraCardDir(mountPoint string) bool {
	for _, dir := range []string{"DCIM", filepath.Join("PRIVATE", "M4ROOT")} {
		if fi, err := os.Stat(filepath.Join(mountPoint, dir)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// maybeFollowCard switches to a formatted copy of the selected card (see
// followCandidate) and starts syncing it. extra is a volume that was just
// mounted and may not be listed yet (nil at startup). It reports whether
// it followed. No App lock may be held: it calls ForgetCard and SyncNow.
func (a *App) maybeFollowCard(extra *volume.Info) bool {
	candidate, ok := a.switchToFormattedCard(extra)
	if !ok {
		return false
	}
	a.notify(Notice{Kind: NoticeCardFollowed, Params: map[string]string{"name": candidate.Name}})
	a.SyncNow()
	return true
}

// switchToFormattedCard does the switch for maybeFollowCard, under
// followMu, and returns the card it selected.
func (a *App) switchToFormattedCard(extra *volume.Info) (volume.Info, bool) {
	a.followMu.Lock()
	defer a.followMu.Unlock()

	cfg := a.Config.Get()
	mounted, err := a.removableVolumes()
	if err != nil {
		logx.Warn("failed to list removable volumes", "err", err)
		return volume.Info{}, false
	}
	if extra != nil && !containsVolume(mounted, extra.UUID) {
		mounted = append(mounted, *extra)
	}
	candidate, ok := followCandidate(cfg, mounted, a.cameraCard)
	if !ok {
		return volume.Info{}, false
	}
	// A card the app has synced before is not a freshly formatted one.
	known, err := a.isRememberedCard(candidate.UUID)
	if err != nil {
		logx.Warn("failed to list remembered cards", "err", err)
		return volume.Info{}, false
	}
	if known {
		logx.Info("not following a card synced before", "name", candidate.Name, "uuid", candidate.UUID)
		return volume.Info{}, false
	}

	oldUUID := cfg.Watch.VolumeUUID
	logx.Info("selected card was formatted, following it",
		"name", candidate.Name, "old_uuid", oldUUID, "new_uuid", candidate.UUID)
	// The old card's files are gone: forget its state (this deselects it;
	// the history is kept).
	if err := a.ForgetCard(oldUUID); err != nil {
		logx.Warn("failed to forget the formatted card", "uuid", oldUUID, "err", err)
		return volume.Info{}, false
	}
	if err := a.Config.Update(func(c *config.Config) {
		c.Watch.VolumeUUID = candidate.UUID
		c.Watch.VolumeName = candidate.Name
	}); err != nil {
		logx.Error("failed to select the formatted card", "err", err)
		return volume.Info{}, false
	}
	a.mu.Lock()
	a.setMountedTargetLocked(&candidate)
	a.mu.Unlock()
	logx.Info("card selected", "name", candidate.Name, "uuid", candidate.UUID)
	return candidate, true
}

// isRememberedCard reports whether the app has sync state for uuid.
func (a *App) isRememberedCard(uuid string) (bool, error) {
	cards, err := a.State.RememberedCards()
	if err != nil {
		return false, err
	}
	for _, c := range cards {
		if strings.EqualFold(c.UUID, uuid) {
			return true, nil
		}
	}
	return false, nil
}

func containsVolume(vols []volume.Info, uuid string) bool {
	for _, v := range vols {
		if strings.EqualFold(v.UUID, uuid) {
			return true
		}
	}
	return false
}

// The seams below fall back to safe behaviour when unset (Apps built in
// tests without New): no sleep prevention, no eject, no camera cards.

func (a *App) keepMacAwake() (release func()) {
	if a.keepAwake == nil {
		return func() {}
	}
	return a.keepAwake()
}

func (a *App) ejectVolume(mountPoint string) error {
	if a.eject == nil {
		return errors.New("eject is not available")
	}
	return a.eject(mountPoint)
}

func (a *App) cameraCard(mountPoint string) bool {
	return a.isCameraCard != nil && a.isCameraCard(mountPoint)
}

func (a *App) removableVolumes() ([]volume.Info, error) {
	if a.listRemovable == nil {
		return volume.ListRemovable()
	}
	return a.listRemovable()
}
