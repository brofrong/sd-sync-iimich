package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// The countdown before a format: CodeFormatPending is published once per
// formatTick, its "seconds" counting the ticks left (30 down to 1). Tests
// shorten both.
var (
	formatCountdown = 30 * time.Second
	formatTick      = time.Second
)

// formatOutcome is what the auto-format phase did.
type formatOutcome struct {
	// status is the status shown when the phase ended.
	status Status
	// vol is the card now: the new volume once it was formatted.
	vol volume.Info
	// formatted: the card was erased and adopted as the selected card.
	formatted bool
	// failed: verifying or erasing failed; the card must stay as it is.
	failed bool
}

// wantsFormat reports whether the sync that ended with st (and res) may be
// followed by an auto-format: the option is on, the run uploaded
// everything without errors (done or nothing_new) and was not a dry run.
// A card whose format failed is not formatted again during the same
// insertion (see formatSuppressed).
func (a *App) wantsFormat(vol volume.Info, res uploader.Result, st Status) bool {
	if a.dryRun || res.Errors != 0 || !a.Config.Get().Card.AutoFormat {
		return false
	}
	if st.Code != CodeDone && st.Code != CodeNothingNew {
		return false
	}
	if a.formatSuppressed(vol.UUID) {
		logx.Info("not formatting: an earlier format of this card failed", "name", vol.Name, "uuid", vol.UUID)
		return false
	}
	return true
}

// formatSuppressed reports whether a format of the card uuid failed during
// its current insertion: there are no format retries until the card is
// pulled out (see clearFormatSuppressionLocked) or the app restarts.
func (a *App) formatSuppressed(uuid string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.formatFailures[strings.ToLower(uuid)]
}

// suppressFormat records that a format of the card uuid failed.
func (a *App) suppressFormat(uuid string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.formatFailures == nil {
		a.formatFailures = map[string]bool{}
	}
	a.formatFailures[strings.ToLower(uuid)] = true
}

// clearFormatSuppressionLocked forgets a failed format of the card uuid:
// the user pulled it out, so its next insertion may format it again. Call
// with a.mu held.
func (a *App) clearFormatSuppressionLocked(uuid string) {
	if uuid != "" {
		delete(a.formatFailures, strings.ToLower(uuid))
	}
}

// autoFormat verifies that everything on vol is in Immich, counts down and
// erases the card, adopting its new volume as the selected card. st is the
// sync's result status, shown when the phase ends without formatting.
// Stopping the sync, pulling the card out or the card no longer being the
// selected, mounted one end the phase silently; nothing is erased unless
// every check passed right before the erase.
func (a *App) autoFormat(ctx context.Context, vol volume.Info, st Status) formatOutcome {
	keep := formatOutcome{status: st, vol: vol}

	logx.Info("verifying card before format", "name", vol.Name, "uuid", vol.UUID)
	gen := a.setStatus(verifyingStatus(0, 0))
	vr, err := a.verifyCard(ctx, vol, func(done, total int) {
		gen = a.setStatus(verifyingStatus(done, total))
	})
	if ctx.Err() != nil {
		logx.Info("format cancelled during verification", "name", vol.Name)
		a.restoreStatus(gen, st)
		return keep
	}
	if err != nil {
		logx.Error("card verification failed, not formatting", "name", vol.Name, "err", err)
		return a.formatFailed(keep, err)
	}
	logx.Info("card verified", "name", vol.Name, "media", vr.Media,
		"missing", vr.MissingCount, "unsupported", len(vr.Unsupported))
	switch {
	case vr.MissingCount > 0:
		logx.Info("not formatting: files are not in immich", "name", vol.Name,
			"count", vr.MissingCount, "first", vr.Missing)
		keep.status = newStatus(CodeFormatSkippedMissing, map[string]string{
			"count": strconv.Itoa(vr.MissingCount),
		})
		a.setStatus(keep.status)
		return keep
	case len(vr.Unsupported) > 0:
		files := unsupportedFiles(vr.Unsupported)
		logx.Info("not formatting: the card has files that are not uploaded", "name", vol.Name, "files", files)
		keep.status = newStatus(CodeFormatSkippedUnsupported, map[string]string{"files": files})
		a.setStatus(keep.status)
		return keep
	case vr.Media == 0:
		// An empty card: nothing to free, nothing to report. (Files the app
		// does not upload were reported above: such a card is not empty.)
		logx.Info("not formatting: no media on the card", "name", vol.Name)
		a.restoreStatus(gen, st)
		return keep
	case !vr.Safe():
		// Unreachable with the cases above; never erase if Safe disagrees.
		return a.formatFailed(keep, errors.New("the card did not pass verification"))
	}

	gen, ok := a.countdown(ctx, vol)
	if !ok {
		a.restoreStatus(gen, st)
		return keep
	}
	if !a.beginErase(ctx, vol) {
		logx.Info("not formatting: the card changed during the countdown", "name", vol.Name)
		a.restoreStatus(gen, st)
		return keep
	}
	return a.eraseCard(vol, keep)
}

func verifyingStatus(done, total int) Status {
	s := newStatus(CodeVerifying, nil)
	s.Progress = &Progress{Current: done, Total: total}
	return s
}

// countdown publishes CodeFormatPending once per formatTick until the
// countdown ends, and reports whether it did: false if CancelFormat or
// StopSync cancelled it, or vol stopped being the selected, mounted card.
// gen is the generation of the last status it published.
func (a *App) countdown(ctx context.Context, vol volume.Info) (gen uint64, ok bool) {
	cdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	a.mu.Lock()
	a.countdownCancel = cancel
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.countdownCancel = nil
		a.mu.Unlock()
		// CancelFormat cancels under a.mu: one accepted before the
		// countdown was unregistered above is seen here, even if the last
		// tick had already ended.
		if ok && cdCtx.Err() != nil {
			ok = false
		}
		if !ok {
			switch {
			case ctx.Err() != nil:
				logx.Info("format cancelled: sync stopped or card removed", "name", vol.Name)
			case cdCtx.Err() != nil:
				logx.Info("format cancelled by the user", "name", vol.Name)
			default:
				logx.Info("format cancelled: the card changed", "name", vol.Name)
			}
		}
	}()

	ticks := int(formatCountdown / formatTick)
	if ticks < 1 {
		ticks = 1
	}
	logx.Info("formatting the card after a countdown", "name", vol.Name, "uuid", vol.UUID, "ticks", ticks)
	for left := ticks; left >= 1; left-- {
		if !a.formatTargetOK(ctx, vol) {
			return gen, false
		}
		gen = a.setStatus(newStatus(CodeFormatPending, map[string]string{
			"seconds": strconv.Itoa(left),
			"name":    vol.Name,
		}))
		t := time.NewTimer(formatTick)
		select {
		case <-cdCtx.Done():
			t.Stop()
			return gen, false
		case <-t.C:
		}
	}
	return gen, true
}

// CancelFormat cancels the countdown before a format, if one is running;
// the sync's result status is shown again. Otherwise it does nothing.
//
// The countdown is cancelled while a.mu is held, the lock under which the
// countdown unregisters itself: a cancel accepted here is always seen by
// the countdown's final check, even at the very end of its last tick.
func (a *App) CancelFormat() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.countdownCancel != nil {
		logx.Info("format countdown cancel requested")
		a.countdownCancel()
		a.countdownCancel = nil
	}
}

// formatTargetOK reports whether vol may still be formatted: the phase was
// not stopped, auto-format is still on, vol is still the selected card and
// still the mounted target at the same mount point.
func (a *App) formatTargetOK(ctx context.Context, vol volume.Info) bool {
	cfg := a.Config.Get()
	if !cfg.Card.AutoFormat || !strings.EqualFold(cfg.Watch.VolumeUUID, vol.UUID) {
		return false
	}
	return a.stillMounted(ctx, vol)
}

// beginErase checks formatTargetOK one last time and, under the same lock
// as the mounted-target check, marks vol's mount point as being erased.
func (a *App) beginErase(ctx context.Context, vol volume.Info) bool {
	cfg := a.Config.Get()
	if !cfg.Card.AutoFormat || !strings.EqualFold(cfg.Watch.VolumeUUID, vol.UUID) {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	mt := a.mountedTarget
	if ctx.Err() != nil || mt == nil || !strings.EqualFold(mt.UUID, vol.UUID) || mt.MountPoint != vol.MountPoint {
		return false
	}
	a.erasing = vol.MountPoint
	return true
}

// eraseCard erases vol (beginErase marked it) and adopts the new volume as
// the selected card; the old card's state is forgotten (its history is
// kept). Any failure is reported as CodeFormatFailed (or
// CodeFormatNeedsAdmin, see formatFailed).
func (a *App) eraseCard(vol volume.Info, keep formatOutcome) formatOutcome {
	a.setStatus(newStatus(CodeFormatting, map[string]string{"name": vol.Name}))
	logx.Warn("erasing card", "name", vol.Name, "uuid", vol.UUID, "path", vol.MountPoint)
	newVol, err := a.eraseVolume(vol.MountPoint, vol.UUID)
	if err == nil && newVol.UUID == "" {
		err = fmt.Errorf("erase of %s returned no volume: %w", vol.MountPoint, volume.ErrErasedNotRemounted)
	}

	if err != nil && !errors.Is(err, volume.ErrErasedNotRemounted) {
		// The card was not erased. Volume events were ignored meanwhile:
		// look up whether it is still mounted.
		logx.Error("format failed", "name", vol.Name, "err", err)
		still := a.listedAt(vol)
		a.mu.Lock()
		a.erasing = ""
		if !still && a.mountedTarget != nil && a.mountedTarget.MountPoint == vol.MountPoint {
			a.setMountedTargetLocked(nil)
		}
		a.mu.Unlock()
		return a.formatFailed(keep, err)
	}

	// The card was erased: whatever the app remembers of it is gone.
	if ferr := a.State.ForgetCard(vol.UUID); ferr != nil {
		logx.Error("state: failed to forget the formatted card", "uuid", vol.UUID, "err", ferr)
	} else {
		logx.Info("card forgotten", "uuid", vol.UUID, "was_selected", true)
	}
	if err != nil {
		// Erased, but its new volume is unknown: keep the selection (the
		// user picks the card again), nothing is mounted to eject.
		logx.Error("card erased but its new volume was not found", "name", vol.Name, "err", err)
		a.mu.Lock()
		a.erasing = ""
		a.setMountedTargetLocked(nil)
		a.mu.Unlock()
		return a.formatFailed(keep, err)
	}

	logx.Info("card formatted", "name", newVol.Name, "old_uuid", vol.UUID, "new_uuid", newVol.UUID, "path", newVol.MountPoint)
	st := newStatus(CodeFormatted, map[string]string{"name": newVol.Name})
	adopt := false
	err = a.Config.Update(func(c *config.Config) {
		// A card the user selected while the erase ran stays selected.
		if strings.EqualFold(c.Watch.VolumeUUID, vol.UUID) {
			c.Watch.VolumeUUID = newVol.UUID
			c.Watch.VolumeName = newVol.Name
			adopt = true
		}
	})
	if err == nil && !adopt {
		logx.Info("another card was selected while erasing, not selecting the formatted card", "name", newVol.Name)
		a.mu.Lock()
		a.erasing = ""
		if mt := a.mountedTarget; mt != nil && strings.EqualFold(mt.UUID, vol.UUID) {
			a.setMountedTargetLocked(nil)
		}
		a.mu.Unlock()
		a.setStatus(st)
		return formatOutcome{status: st, vol: newVol, formatted: true}
	}
	if err != nil {
		logx.Error("failed to select the formatted card", "err", err)
		a.mu.Lock()
		a.erasing = ""
		a.setMountedTargetLocked(nil)
		a.mu.Unlock()
		// The detail follows "Couldn't format the card:": the last step of
		// the format, selecting the erased card, is what failed.
		return a.formatFailed(keep, fmt.Errorf("erased, but the new card could not be selected: %w", err))
	}
	a.mu.Lock()
	a.erasing = ""
	a.setMountedTargetLocked(&newVol)
	a.adopted = newVol.MountPoint
	a.mu.Unlock()
	logx.Info("card selected", "name", newVol.Name, "uuid", newVol.UUID)

	a.setStatus(st)
	return formatOutcome{status: st, vol: newVol, formatted: true}
}

// listedAt reports whether vol is listed as mounted at its mount point.
// When the list can't be read it assumes so (the old state is kept).
func (a *App) listedAt(vol volume.Info) bool {
	mounted, err := a.removableVolumes()
	if err != nil {
		logx.Warn("failed to list removable volumes", "err", err)
		return true
	}
	for _, v := range mounted {
		if strings.EqualFold(v.UUID, vol.UUID) && v.MountPoint == vol.MountPoint {
			return true
		}
	}
	return false
}

// formatOutcomeParams returns what the ejected status says about the
// format phase that produced st: its "format" outcome and that status's
// own params, or nil when no format outcome is shown (no format attempted,
// countdown cancelled, empty card).
func formatOutcomeParams(st Status) map[string]string {
	var outcome, key string
	switch st.Code {
	case CodeFormatted:
		outcome, key = "formatted", "name"
	case CodeFormatSkippedMissing:
		outcome, key = "skipped_missing", "count"
	case CodeFormatSkippedUnsupported:
		outcome, key = "skipped_unsupported", "files"
	default:
		return nil
	}
	return map[string]string{"format": outcome, key: st.Params[key]}
}

// formatFailed publishes CodeFormatFailed for err (CodeFormatNeedsAdmin
// when the system would only erase the card with administrator rights) and
// suppresses further formats of the card (keep.vol, the card the phase
// started with).
func (a *App) formatFailed(keep formatOutcome, err error) formatOutcome {
	a.suppressFormat(keep.vol.UUID)
	if errors.Is(err, volume.ErrNeedsAdmin) {
		keep.status = newStatus(CodeFormatNeedsAdmin, map[string]string{"name": keep.vol.Name})
	} else {
		keep.status = newStatus(CodeFormatFailed, map[string]string{"detail": err.Error()})
	}
	keep.failed = true
	a.setStatus(keep.status)
	return keep
}

// ignoreFormatEvent reports whether the volume event ev belongs to the
// auto-format and must not be handled: anything at the mount point being
// erased, and the mount echo of the card the app just formatted and
// adopted (the watcher reports it seconds later).
func (a *App) ignoreFormatEvent(ev volume.Event) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	mount := ev.Info.MountPoint
	if a.erasing != "" && mount == a.erasing {
		logx.Info("ignoring volume event while erasing the card", "type", ev.Type, "path", mount)
		return true
	}
	if a.adopted == "" || mount != a.adopted {
		return false
	}
	a.adopted = ""
	mt := a.mountedTarget
	if ev.Type == volume.EventMounted && mt != nil && mt.MountPoint == mount && strings.EqualFold(mt.UUID, ev.Info.UUID) {
		logx.Info("formatted card mounted", "name", ev.Info.Name, "path", mount)
		return true
	}
	return false
}

// restoreStatus publishes s unless another status was published since
// generation gen (e.g. the idle status after the card was pulled out).
func (a *App) restoreStatus(gen uint64, s Status) {
	a.mu.Lock()
	if a.statusGen != gen {
		a.mu.Unlock()
		return
	}
	a.status = s
	a.statusGen++
	a.mu.Unlock()
	if a.onStatus != nil {
		a.onStatus(s)
	}
}

// unsupportedFiles renders VerifyResult.Unsupported for the
// format_skipped_unsupported status, sorted by extension and without
// words: "12 × .HIF, 3 × .MXF" ("(none)" for files without one).
func unsupportedFiles(counts map[string]int) string {
	exts := make([]string, 0, len(counts))
	for ext := range counts {
		exts = append(exts, ext)
	}
	sort.Strings(exts)
	parts := make([]string, 0, len(exts))
	for _, ext := range exts {
		label := ext
		if ext != "(none)" {
			label = "." + strings.ToUpper(ext)
		}
		parts = append(parts, strconv.Itoa(counts[ext])+" × "+label)
	}
	return strings.Join(parts, ", ")
}

// The seams below fall back to safe behaviour when unset (Apps built in
// tests without New): verifying reads the card, erasing is unavailable.

func (a *App) verifyCard(ctx context.Context, vol volume.Info, onProgress func(done, total int)) (uploader.VerifyResult, error) {
	if a.verify == nil {
		return a.Uploader.VerifyCard(ctx, vol, onProgress)
	}
	return a.verify(ctx, vol, onProgress)
}

func (a *App) eraseVolume(mountPoint, wantUUID string) (volume.Info, error) {
	if a.erase == nil {
		return volume.Info{}, errors.New("erase is not available")
	}
	return a.erase(mountPoint, wantUUID)
}
