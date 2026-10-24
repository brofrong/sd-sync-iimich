package app

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// fastFormatCountdown makes the 30-tick countdown take milliseconds.
func fastFormatCountdown(t *testing.T) {
	t.Helper()
	origCountdown, origTick := formatCountdown, formatTick
	formatCountdown, formatTick = 30*time.Millisecond, time.Millisecond
	t.Cleanup(func() { formatCountdown, formatTick = origCountdown, origTick })
}

// verifyRecorder is a fake verify seam.
type verifyRecorder struct {
	mu     sync.Mutex
	calls  int
	result uploader.VerifyResult
	err    error
	// during runs inside the fake verify, before it returns.
	during func(ctx context.Context)
}

func fakeVerify(a *App, res uploader.VerifyResult, err error) *verifyRecorder {
	r := &verifyRecorder{result: res, err: err}
	a.verify = func(ctx context.Context, vol volume.Info, onProgress func(done, total int)) (uploader.VerifyResult, error) {
		r.mu.Lock()
		r.calls++
		during := r.during
		r.mu.Unlock()
		if onProgress != nil {
			onProgress(0, 2)
			onProgress(2, 2)
		}
		if during != nil {
			during(ctx)
		}
		if ctx.Err() != nil {
			return uploader.VerifyResult{}, fmt.Errorf("verify card: %w", ctx.Err())
		}
		return r.result, r.err
	}
	return r
}

func (r *verifyRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// eraseRecorder is a fake erase seam.
type eraseRecorder struct {
	mu    sync.Mutex
	calls []string // "mountPoint|wantUUID"
	info  volume.Info
	err   error
	// during runs inside the fake erase, as volume events can arrive while
	// diskutil erases the card.
	during func()
}

func fakeErase(a *App, info volume.Info, err error) *eraseRecorder {
	r := &eraseRecorder{info: info, err: err}
	a.erase = func(mountPoint, wantUUID string) (volume.Info, error) {
		r.mu.Lock()
		r.calls = append(r.calls, mountPoint+"|"+wantUUID)
		during := r.during
		r.mu.Unlock()
		if during != nil {
			during()
		}
		if r.err != nil {
			return volume.Info{}, r.err
		}
		return r.info, nil
	}
	return r
}

func (r *eraseRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// fullStatusLog records every published status in full.
type fullStatusLog struct {
	mu       sync.Mutex
	statuses []Status
	// on, when set, runs for every status (in the publishing goroutine).
	on func(Status)
}

func recordFullStatuses(a *App) *fullStatusLog {
	l := &fullStatusLog{}
	a.OnStatus(func(s Status) {
		l.mu.Lock()
		l.statuses = append(l.statuses, s)
		on := l.on
		l.mu.Unlock()
		if on != nil {
			on(s)
		}
	})
	return l
}

func (l *fullStatusLog) setOn(fn func(Status)) {
	l.mu.Lock()
	l.on = fn
	l.mu.Unlock()
}

func (l *fullStatusLog) all() []Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.statuses)
}

func (l *fullStatusLog) codes() []Code {
	var codes []Code
	for _, s := range l.all() {
		codes = append(codes, s.Code)
	}
	return codes
}

// after returns the statuses published after the last one with code.
func (l *fullStatusLog) after(code Code) []Status {
	all := l.all()
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Code == code {
			return all[i+1:]
		}
	}
	return nil
}

func (l *fullStatusLog) has(code Code) bool {
	return slices.Contains(l.codes(), code)
}

func (l *fullStatusLog) last(code Code) (Status, bool) {
	all := l.all()
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Code == code {
			return all[i], true
		}
	}
	return Status{}, false
}

// formatApp is testApp with auto-format on, a fast countdown, and a card
// that verifies safe and is erased into newCard.
func formatApp(t *testing.T) (*App, *verifyRecorder, *eraseRecorder, volume.Info) {
	t.Helper()
	a := testApp(t)
	fastFormatCountdown(t)
	if err := a.Config.Update(func(c *config.Config) { c.Card.AutoFormat = true }); err != nil {
		t.Fatal(err)
	}
	// Remembered state of the card, older than any photo the tests put on
	// it (so the sync's cursor doesn't skip them).
	old := time.Unix(1_600_000_000, 0)
	if err := a.State.Upsert(state.FileRecord{VolumeUUID: "VOL-UUID", RelativePath: "DCIM/0.JPG", Size: 1, Mtime: old, SHA1: "s", UploadedAt: &old}); err != nil {
		t.Fatal(err)
	}
	mount := a.MountedTarget().MountPoint
	newCard := volume.Info{UUID: "NEW-UUID", Name: "CARD", MountPoint: mount, Removable: true}
	v := fakeVerify(a, uploader.VerifyResult{Media: 2}, nil)
	e := fakeErase(a, newCard, nil)
	return a, v, e, newCard
}

// squash collapses repeated codes (verifying progress, countdown
// ticks) of the statuses published after the sync's result.
func squash(statuses []Status) []Code {
	var codes []Code
	for _, s := range statuses {
		if len(codes) == 0 || codes[len(codes)-1] != s.Code {
			codes = append(codes, s.Code)
		}
	}
	return codes
}

func countdownSeconds(statuses []Status) []string {
	var secs []string
	for _, s := range statuses {
		if s.Code == CodeFormatPending {
			secs = append(secs, s.Params["seconds"])
		}
	}
	return secs
}

func wantCountdown() []string {
	var want []string
	for i := 30; i >= 1; i-- {
		want = append(want, strconv.Itoa(i))
	}
	return want
}

func TestFormatAfterDone(t *testing.T) {
	a, v, e, newCard := formatApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG", "B.JPG")
	ej := fakeEject(a, nil)
	notices := recordNotices(a)
	statuses := recordFullStatuses(a)
	mount := a.MountedTarget().MountPoint

	a.SyncNow()
	waitFor(t, "ejected", func() bool { return statuses.has(CodeEjected) })
	waitSyncFinished(t, a)

	got := squash(statuses.after(CodeSyncing))
	want := []Code{CodeDone, CodeVerifying, CodeFormatPending, CodeFormatting, CodeFormatted, CodeEjected}
	if !slices.Equal(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if secs := countdownSeconds(statuses.all()); !slices.Equal(secs, wantCountdown()) {
		t.Errorf("countdown seconds = %v, want 30..1", secs)
	}
	for _, s := range statuses.all() {
		switch s.Code {
		case CodeVerifying:
			if s.Progress == nil || !slices.Equal(s.Actions, []Action{ActionStop}) {
				t.Errorf("verifying = %+v, want progress and [stop]", s)
			}
		case CodeFormatPending:
			if s.Params["name"] != "CARD" || !slices.Equal(s.Actions, []Action{ActionCancelFormat}) {
				t.Errorf("format_pending = %+v, want name=CARD and [cancel_format]", s)
			}
		case CodeFormatting, CodeFormatted:
			if s.Params["name"] != "CARD" {
				t.Errorf("%s params = %v, want name=CARD", s.Code, s.Params)
			}
		case CodeEjected:
			if s.Params["uploaded"] != "2" {
				t.Errorf("ejected params = %v, want uploaded=2", s.Params)
			}
		}
	}
	if last, _ := statuses.last(CodeVerifying); last.Progress.Current != 2 || last.Progress.Total != 2 {
		t.Errorf("last verifying progress = %+v, want 2/2", last.Progress)
	}

	if v.count() != 1 {
		t.Errorf("verify calls = %d, want 1", v.count())
	}
	if got := e.all(); len(got) != 1 || got[0] != mount+"|VOL-UUID" {
		t.Fatalf("erase calls = %v, want one of %s for VOL-UUID", got, mount)
	}
	if got := ej.all(); len(got) != 1 || got[0] != newCard.MountPoint {
		t.Errorf("eject calls = %v, want the new card", got)
	}
	cfg := a.Config.Get()
	if cfg.Watch.VolumeUUID != "NEW-UUID" || cfg.Watch.VolumeName != "CARD" {
		t.Errorf("watch = %+v, want NEW-UUID/CARD selected", cfg.Watch)
	}
	if got := rememberedUUIDs(t, a); slices.Contains(got, "VOL-UUID") {
		t.Errorf("remembered = %v, want the old card forgotten", got)
	}
	// The run itself is kept in the history.
	if runs, err := a.State.RecentRuns(10); err != nil || len(runs) != 1 || runs[0].ResultCode != string(CodeDone) {
		t.Errorf("runs = %+v, %v; want the done run kept", runs, err)
	}
	// Ejected before the watcher saw the new volume: no unmount event will
	// come, so the app must not think it is still mounted.
	if mt := a.MountedTarget(); mt != nil {
		t.Errorf("MountedTarget() = %+v after the eject, want nil", mt)
	}
	if got := a.Status().Code; got != CodeEjected {
		t.Errorf("Status().Code = %q, want ejected", got)
	}
	if got := notices.others(); len(got) != 0 {
		t.Errorf("notices = %+v, want none but sync_done", got)
	}
	if got := syncDoneNotices(notices.all()); len(got) != 1 || got[0].Params["ejected"] != "true" {
		t.Errorf("sync_done notices = %+v, want one, ejected", got)
	}
}

func TestFormatAfterNothingNew(t *testing.T) {
	a, _, e, _ := formatApp(t)
	ej := fakeEject(a, nil)
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "ejected", func() bool { return statuses.has(CodeEjected) })
	waitSyncFinished(t, a)

	got := squash(statuses.after(CodeScanning))
	want := []Code{CodeNothingNew, CodeVerifying, CodeFormatPending, CodeFormatting, CodeFormatted, CodeEjected}
	if !slices.Equal(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if len(e.all()) != 1 || len(ej.all()) != 1 {
		t.Errorf("erase calls = %v, eject calls = %v; want one each", e.all(), ej.all())
	}
	if st, _ := statuses.last(CodeEjected); st.Params["uploaded"] != "0" {
		t.Errorf("ejected params = %v, want uploaded=0", st.Params)
	}
}

func TestFormattedNotEjectedWhenDisabled(t *testing.T) {
	a, _, e, newCard := formatApp(t)
	if err := a.Config.Update(func(c *config.Config) { c.Card.AutoEject = false }); err != nil {
		t.Fatal(err)
	}
	a.revertAfter = 20 * time.Millisecond
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "formatted", func() bool { return statuses.has(CodeFormatted) })
	waitSyncFinished(t, a)
	time.Sleep(10 * a.revertAfter)

	if len(e.all()) != 1 {
		t.Fatalf("erase calls = %v, want one", e.all())
	}
	if got := a.Status().Code; got != CodeFormatted {
		t.Errorf("Status().Code = %q, want formatted kept", got)
	}
	if mt := a.MountedTarget(); mt == nil || *mt != newCard {
		t.Errorf("MountedTarget() = %+v, want the new card", mt)
	}

	// The watcher reports the blank card's mount seconds later: it is the
	// card just adopted, not a new insertion to sync.
	a.handleVolumeEvent(volume.Event{Type: volume.EventMounted, Info: newCard})
	time.Sleep(50 * time.Millisecond)
	if runs, err := a.State.RecentRuns(10); err != nil || len(runs) != 1 {
		t.Errorf("runs = %d (%v) after the formatted card's mount event, want no new sync", len(runs), err)
	}
	if got := a.Status().Code; got != CodeFormatted {
		t.Errorf("Status().Code = %q after the mount event, want formatted kept", got)
	}

	// Later events are handled as usual.
	a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: newCard})
	if got := a.Status().Code; got != CodeWaitingForCard {
		t.Errorf("Status().Code = %q after the card was pulled out, want waiting_for_card", got)
	}
}

func TestNoFormatWhenDisabled(t *testing.T) {
	a, v, e, _ := formatApp(t)
	if err := a.Config.Update(func(c *config.Config) { c.Card.AutoFormat = false }); err != nil {
		t.Fatal(err)
	}
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	fakeEject(a, nil)
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "ejected", func() bool { return statuses.has(CodeEjected) })
	waitSyncFinished(t, a)

	if v.count() != 0 || len(e.all()) != 0 {
		t.Fatalf("verify calls = %d, erase calls = %v; want none with auto-format off", v.count(), e.all())
	}
	if got := squash(statuses.after(CodeSyncing)); !slices.Equal(got, []Code{CodeDone, CodeEjected}) {
		t.Errorf("statuses = %v, want [done ejected]", got)
	}
}

func TestNoFormatAfterErrors(t *testing.T) {
	a, v, e, _ := formatApp(t)
	useFakeImmich(t, a, "B.JPG")
	putPhotos(t, a, "A.JPG", "B.JPG")
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "done_with_errors", func() bool { return statuses.has(CodeDoneWithErrors) })
	waitSyncFinished(t, a)

	if v.count() != 0 || len(e.all()) != 0 {
		t.Fatalf("verify calls = %d, erase calls = %v; want none after errors", v.count(), e.all())
	}
}

func TestNoFormatInDryRun(t *testing.T) {
	a, v, e, _ := formatApp(t)
	a.dryRun = true
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "nothing_new", func() bool { return statuses.has(CodeNothingNew) })
	waitSyncFinished(t, a)

	if v.count() != 0 || len(e.all()) != 0 {
		t.Fatalf("verify calls = %d, erase calls = %v; want none in dry run", v.count(), e.all())
	}
}

func TestFormatSkippedMissing(t *testing.T) {
	a, _, e, _ := formatApp(t)
	fakeVerify(a, uploader.VerifyResult{Media: 5, MissingCount: 3, Missing: []string{"DCIM/A.JPG"}}, nil)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	ej := fakeEject(a, nil)
	statuses := recordFullStatuses(a)
	mount := a.MountedTarget().MountPoint

	a.SyncNow()
	waitFor(t, "ejected", func() bool { return statuses.has(CodeEjected) })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 0 {
		t.Fatalf("erase calls = %v, want none", got)
	}
	got := squash(statuses.after(CodeSyncing))
	want := []Code{CodeDone, CodeVerifying, CodeFormatSkippedMissing, CodeEjected}
	if !slices.Equal(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	st, _ := statuses.last(CodeFormatSkippedMissing)
	if st.Params["count"] != "3" {
		t.Errorf("format_skipped_missing params = %v, want count=3", st.Params)
	}
	if got := ej.all(); len(got) != 1 || got[0] != mount {
		t.Errorf("eject calls = %v, want the card ejected as after any done", got)
	}
	if got := a.Config.Get().Watch.VolumeUUID; got != "VOL-UUID" {
		t.Errorf("selected = %q, want VOL-UUID kept", got)
	}
	if got := rememberedUUIDs(t, a); !slices.Contains(got, "VOL-UUID") {
		t.Errorf("remembered = %v, want VOL-UUID kept", got)
	}
}

// TestFormatSkippedNoEjectAfterNothingNew: the skip does not change the
// eject rule — nothing_new is not ejected.
func TestFormatSkippedNoEjectAfterNothingNew(t *testing.T) {
	a, _, e, _ := formatApp(t)
	fakeVerify(a, uploader.VerifyResult{Media: 5, MissingCount: 1}, nil)
	ej := fakeEject(a, nil)
	a.revertAfter = 20 * time.Millisecond
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "format_skipped_missing", func() bool { return statuses.has(CodeFormatSkippedMissing) })
	waitSyncFinished(t, a)
	time.Sleep(10 * a.revertAfter)

	if len(e.all()) != 0 || len(ej.all()) != 0 {
		t.Fatalf("erase calls = %v, eject calls = %v; want none", e.all(), ej.all())
	}
	if got := a.Status().Code; got != CodeFormatSkippedMissing {
		t.Errorf("Status().Code = %q, want format_skipped_missing kept", got)
	}
}

func TestFormatSkippedUnsupported(t *testing.T) {
	a, _, e, _ := formatApp(t)
	fakeVerify(a, uploader.VerifyResult{Media: 5, Unsupported: map[string]int{"hif": 1}}, nil)
	ej := fakeEject(a, nil)
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "format_skipped_unsupported", func() bool { return statuses.has(CodeFormatSkippedUnsupported) })
	waitSyncFinished(t, a)

	if len(e.all()) != 0 || len(ej.all()) != 0 {
		t.Fatalf("erase calls = %v, eject calls = %v; want none", e.all(), ej.all())
	}
	st, _ := statuses.last(CodeFormatSkippedUnsupported)
	if st.Params["files"] != "1 × .HIF" {
		t.Errorf("files = %q, want %q", st.Params["files"], "1 × .HIF")
	}
}

func TestUnsupportedFilesParam(t *testing.T) {
	got := unsupportedFiles(map[string]int{"mxf": 3, "hif": 12, "(none)": 2, "pdf": 1})
	want := "2 × (none), 12 × .HIF, 3 × .MXF, 1 × .PDF"
	if got != want {
		t.Errorf("unsupportedFiles = %q, want %q", got, want)
	}
}

// TestFormatEmptyCard: nothing to free on an empty card — no format and no
// format status; the eject rule applies as usual.
func TestFormatEmptyCard(t *testing.T) {
	a, _, e, _ := formatApp(t)
	fakeVerify(a, uploader.VerifyResult{}, nil)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	ej := fakeEject(a, nil)
	notices := recordNotices(a)
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "ejected", func() bool { return statuses.has(CodeEjected) })
	waitSyncFinished(t, a)

	if len(e.all()) != 0 || len(ej.all()) != 1 {
		t.Fatalf("erase calls = %v, eject calls = %v; want no erase, one eject", e.all(), ej.all())
	}
	got := squash(statuses.after(CodeSyncing))
	want := []Code{CodeDone, CodeVerifying, CodeDone, CodeEjected}
	if !slices.Equal(got, want) {
		t.Errorf("statuses = %v, want %v", got, want)
	}
	if got := notices.others(); len(got) != 0 {
		t.Errorf("notices = %+v, want none but sync_done", got)
	}
}

func TestCancelFormat(t *testing.T) {
	a, _, e, _ := formatApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	ej := fakeEject(a, nil)
	statuses := recordFullStatuses(a)
	mount := a.MountedTarget().MountPoint
	statuses.setOn(func(s Status) {
		if s.Code == CodeFormatPending && s.Params["seconds"] == "28" {
			a.CancelFormat()
		}
	})

	a.SyncNow()
	waitFor(t, "ejected", func() bool { return statuses.has(CodeEjected) })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 0 {
		t.Fatalf("erase calls = %v after CancelFormat, want none", got)
	}
	if secs := countdownSeconds(statuses.all()); !slices.Equal(secs, []string{"30", "29", "28"}) {
		t.Errorf("countdown seconds = %v, want it to stop at 28", secs)
	}
	got := squash(statuses.after(CodeSyncing))
	want := []Code{CodeDone, CodeVerifying, CodeFormatPending, CodeDone, CodeEjected}
	if !slices.Equal(got, want) {
		t.Errorf("statuses = %v, want %v", got, want)
	}
	if got := ej.all(); len(got) != 1 || got[0] != mount {
		t.Errorf("eject calls = %v, want the done card ejected as usual", got)
	}
	if got := a.Config.Get().Watch.VolumeUUID; got != "VOL-UUID" {
		t.Errorf("selected = %q, want VOL-UUID kept", got)
	}
}

func TestCancelFormatWithoutCountdown(t *testing.T) {
	a := testApp(t)
	a.CancelFormat() // nothing to cancel: must not panic or publish
	if got := a.Status().Code; got != "" {
		t.Errorf("Status().Code = %q, want nothing published", got)
	}
}

// TestFormatSkippedWhenCardChanged: the selected card is no longer the
// mounted target at that mount point when the countdown ends — no erase,
// silently.
func TestFormatSkippedWhenCardChanged(t *testing.T) {
	tests := []struct {
		name   string
		change func(t *testing.T, a *App, mount string)
	}{
		{"another card at the mount point", func(t *testing.T, a *App, mount string) {
			a.mu.Lock()
			a.mountedTarget = &volume.Info{UUID: "OTHER-UUID", Name: "CARD", MountPoint: mount}
			a.mu.Unlock()
		}},
		{"the card at another mount point", func(t *testing.T, a *App, mount string) {
			a.mu.Lock()
			a.mountedTarget = &volume.Info{UUID: "VOL-UUID", Name: "CARD", MountPoint: mount + " 1"}
			a.mu.Unlock()
		}},
		{"no card mounted", func(t *testing.T, a *App, mount string) {
			a.mu.Lock()
			a.mountedTarget = nil
			a.mu.Unlock()
		}},
		{"another card selected", func(t *testing.T, a *App, mount string) {
			if err := a.Config.Update(func(c *config.Config) { c.Watch.VolumeUUID = "OTHER-UUID" }); err != nil {
				t.Error(err)
			}
		}},
		{"auto-format turned off", func(t *testing.T, a *App, mount string) {
			if err := a.Config.Update(func(c *config.Config) { c.Card.AutoFormat = false }); err != nil {
				t.Error(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, e, _ := formatApp(t)
			if err := a.Config.Update(func(c *config.Config) { c.Card.AutoEject = false }); err != nil {
				t.Fatal(err)
			}
			notices := recordNotices(a)
			statuses := recordFullStatuses(a)
			mount := a.MountedTarget().MountPoint
			var once sync.Once
			statuses.setOn(func(s Status) {
				if s.Code == CodeFormatPending && s.Params["seconds"] == "2" {
					once.Do(func() { tt.change(t, a, mount) })
				}
			})

			a.SyncNow()
			waitFor(t, "the countdown", func() bool { return statuses.has(CodeFormatPending) })
			waitSyncFinished(t, a)

			if got := e.all(); len(got) != 0 {
				t.Fatalf("erase calls = %v, want none", got)
			}
			for _, c := range []Code{CodeFormatting, CodeFormatted, CodeFormatFailed} {
				if statuses.has(c) {
					t.Errorf("statuses = %v, want no %s", statuses.codes(), c)
				}
			}
			if got := notices.all(); len(got) != 0 {
				t.Errorf("notices = %+v, want none", got)
			}
			if got := a.Status().Code; got == CodeFormatPending {
				t.Errorf("Status().Code = %q, want the countdown gone", got)
			}
		})
	}
}

func TestStopDuringCountdown(t *testing.T) {
	a, _, e, _ := formatApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	ej := fakeEject(a, nil)
	notices := recordNotices(a)
	statuses := recordFullStatuses(a)
	statuses.setOn(func(s Status) {
		if s.Code == CodeFormatPending && s.Params["seconds"] == "28" {
			a.StopSync()
		}
	})

	a.SyncNow()
	waitFor(t, "the countdown", func() bool { return statuses.has(CodeFormatPending) })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 0 {
		t.Fatalf("erase calls = %v after StopSync, want none", got)
	}
	if got := ej.all(); len(got) != 0 {
		t.Errorf("eject calls = %v after StopSync, want none", got)
	}
	if secs := countdownSeconds(statuses.all()); !slices.Equal(secs, []string{"30", "29", "28"}) {
		t.Errorf("countdown seconds = %v, want it to stop at 28", secs)
	}
	if got := a.Status().Code; got != CodeDone {
		t.Errorf("Status().Code = %q, want the sync's result (done)", got)
	}
	if statuses.has(CodeFormatFailed) || len(notices.others()) != 0 {
		t.Errorf("statuses = %v, notices = %+v; want a silent stop", statuses.codes(), notices.all())
	}
	// The sync itself succeeded: that is still told, once.
	if got := syncDoneNotices(notices.all()); len(got) != 1 || got[0].Params["ejected"] != "false" {
		t.Errorf("sync_done notices = %+v, want one, not ejected", got)
	}
}

// TestCardRemovedDuringCountdown: pulling the card out cancels the format
// silently, with the usual idle status.
func TestCardRemovedDuringCountdown(t *testing.T) {
	a, _, e, _ := formatApp(t)
	notices := recordNotices(a)
	statuses := recordFullStatuses(a)
	vol := *a.MountedTarget()
	statuses.setOn(func(s Status) {
		if s.Code == CodeFormatPending && s.Params["seconds"] == "28" {
			a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: vol})
		}
	})

	a.SyncNow()
	waitFor(t, "the countdown", func() bool { return statuses.has(CodeFormatPending) })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 0 {
		t.Fatalf("erase calls = %v, want none", got)
	}
	if got := a.Status().Code; got != CodeWaitingForCard {
		t.Errorf("Status().Code = %q, want waiting_for_card", got)
	}
	if statuses.has(CodeFormatFailed) || len(notices.all()) != 0 {
		t.Errorf("statuses = %v, notices = %+v; want a silent cancel", statuses.codes(), notices.all())
	}
}

// TestStopDuringVerify: the verify error caused by StopSync is not a
// format failure.
func TestStopDuringVerify(t *testing.T) {
	a, v, e, _ := formatApp(t)
	v.during = func(context.Context) { a.StopSync() }
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "verifying", func() bool { return statuses.has(CodeVerifying) })
	waitSyncFinished(t, a)

	if len(e.all()) != 0 || statuses.has(CodeFormatPending) || statuses.has(CodeFormatFailed) {
		t.Fatalf("erase calls = %v, statuses = %v; want a silent stop", e.all(), statuses.codes())
	}
	if got := a.Status().Code; got != CodeNothingNew {
		t.Errorf("Status().Code = %q, want the sync's result (nothing_new)", got)
	}
}

func TestVerifyErrorFailsFormat(t *testing.T) {
	a, _, e, _ := formatApp(t)
	fakeVerify(a, uploader.VerifyResult{}, errors.New("verify card: permission denied"))
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	ej := fakeEject(a, nil)
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "format_failed", func() bool { return statuses.has(CodeFormatFailed) })
	waitSyncFinished(t, a)

	if len(e.all()) != 0 || len(ej.all()) != 0 {
		t.Fatalf("erase calls = %v, eject calls = %v; want none", e.all(), ej.all())
	}
	st, _ := statuses.last(CodeFormatFailed)
	if !strings.Contains(st.Params["detail"], "permission denied") || !slices.Equal(st.Actions, []Action{ActionOpenLogs}) {
		t.Errorf("format_failed = %+v, want the verify error and [open_logs]", st)
	}
	if got := a.Status().Code; got != CodeFormatFailed {
		t.Errorf("Status().Code = %q, want format_failed", got)
	}
}

func TestFormatFailure(t *testing.T) {
	a, _, e, _ := formatApp(t)
	e.err = errors.New("diskutil eraseVolume: resource busy")
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	ej := fakeEject(a, nil)
	card := *a.MountedTarget()
	listed := true
	var listedMu sync.Mutex
	a.listRemovable = func() ([]volume.Info, error) {
		listedMu.Lock()
		defer listedMu.Unlock()
		if !listed {
			return nil, nil
		}
		return []volume.Info{card}, nil
	}
	statuses := recordFullStatuses(a)
	a.revertAfter = 20 * time.Millisecond

	a.SyncNow()
	waitFor(t, "format_failed", func() bool { return statuses.has(CodeFormatFailed) })
	waitSyncFinished(t, a)
	time.Sleep(10 * a.revertAfter)

	if len(e.all()) != 1 {
		t.Fatalf("erase calls = %v, want one", e.all())
	}
	if got := ej.all(); len(got) != 0 {
		t.Errorf("eject calls = %v after a failed format, want none", got)
	}
	got := squash(statuses.after(CodeSyncing))
	want := []Code{CodeDone, CodeVerifying, CodeFormatPending, CodeFormatting, CodeFormatFailed}
	if !slices.Equal(got, want) {
		t.Errorf("statuses = %v, want %v", got, want)
	}
	st, _ := statuses.last(CodeFormatFailed)
	if !strings.Contains(st.Params["detail"], "resource busy") {
		t.Errorf("format_failed params = %v, want the erase error", st.Params)
	}
	if got := a.Status().Code; got != CodeFormatFailed {
		t.Errorf("Status().Code = %q, want format_failed kept", got)
	}
	if got := a.Config.Get().Watch.VolumeUUID; got != "VOL-UUID" {
		t.Errorf("selected = %q, want VOL-UUID kept", got)
	}
	if got := rememberedUUIDs(t, a); !slices.Contains(got, "VOL-UUID") {
		t.Errorf("remembered = %v, want VOL-UUID kept (not erased)", got)
	}
	if mt := a.MountedTarget(); mt == nil || mt.UUID != "VOL-UUID" {
		t.Errorf("MountedTarget() = %+v, want the card still mounted", mt)
	}
	// Events at the mount point are handled again.
	listedMu.Lock()
	listed = false
	listedMu.Unlock()
	a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: card})
	if mt := a.MountedTarget(); mt != nil {
		t.Errorf("MountedTarget() = %+v after an unmount, want nil", mt)
	}
}

// TestFormatFailureCardGone: the erase failed and the card is no longer
// mounted (its unmount was ignored while erasing).
func TestFormatFailureCardGone(t *testing.T) {
	a, _, e, _ := formatApp(t)
	e.err = errors.New("diskutil eraseVolume: failed")
	if err := a.Config.Update(func(c *config.Config) { c.Card.AutoEject = false }); err != nil {
		t.Fatal(err)
	}
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "format_failed", func() bool { return statuses.has(CodeFormatFailed) })
	waitSyncFinished(t, a)

	if mt := a.MountedTarget(); mt != nil {
		t.Errorf("MountedTarget() = %+v, want nil: the card is not listed as mounted", mt)
	}
}

func TestFormatErasedNotRemounted(t *testing.T) {
	a, _, e, _ := formatApp(t)
	e.err = fmt.Errorf("device disk4s1: %w", volume.ErrErasedNotRemounted)
	ej := fakeEject(a, nil)
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "format_failed", func() bool { return statuses.has(CodeFormatFailed) })
	waitSyncFinished(t, a)

	if got := ej.all(); len(got) != 0 {
		t.Errorf("eject calls = %v, want none: the new volume is unknown", got)
	}
	st, _ := statuses.last(CodeFormatFailed)
	if !strings.Contains(st.Params["detail"], "not found") {
		t.Errorf("format_failed params = %v, want the remount error", st.Params)
	}
	if got := a.Config.Get().Watch.VolumeUUID; got != "VOL-UUID" {
		t.Errorf("selected = %q, want VOL-UUID kept", got)
	}
	if got := rememberedUUIDs(t, a); slices.Contains(got, "VOL-UUID") {
		t.Errorf("remembered = %v, want the erased card forgotten", got)
	}
	if mt := a.MountedTarget(); mt != nil {
		t.Errorf("MountedTarget() = %+v, want nil: the erased volume is gone", mt)
	}
	if got := a.Status().Code; got != CodeFormatFailed {
		t.Errorf("Status().Code = %q, want format_failed", got)
	}
}

// TestVolumeEventsIgnoredWhileErasing: the card's unmount and the blank
// card's mount while diskutil erases it neither cancel the phase nor
// change the status.
func TestVolumeEventsIgnoredWhileErasing(t *testing.T) {
	a, _, e, newCard := formatApp(t)
	ej := fakeEject(a, nil)
	notices := recordNotices(a)
	statuses := recordFullStatuses(a)
	old := *a.MountedTarget()
	a.isCameraCard = func(string) bool { return true }
	a.listRemovable = func() ([]volume.Info, error) { return []volume.Info{newCard}, nil }
	e.during = func() {
		a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: old})
		a.handleVolumeEvent(volume.Event{Type: volume.EventMounted, Info: newCard})
	}

	a.SyncNow()
	waitFor(t, "ejected", func() bool { return statuses.has(CodeEjected) })
	waitSyncFinished(t, a)

	if a.cardRemoved.Load() {
		t.Error("the unmount while erasing was taken for the card being pulled out")
	}
	got := squash(statuses.after(CodeScanning))
	want := []Code{CodeNothingNew, CodeVerifying, CodeFormatPending, CodeFormatting, CodeFormatted, CodeEjected}
	if !slices.Equal(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if len(ej.all()) != 1 {
		t.Errorf("eject calls = %v, want one", ej.all())
	}
	if got := notices.all(); len(got) != 0 {
		t.Errorf("notices = %+v, want none (no follow)", got)
	}
	if runs, err := a.State.RecentRuns(10); err != nil || len(runs) != 1 {
		t.Errorf("runs = %d (%v), want no sync of the blank card", len(runs), err)
	}
}

// TestOtherCardSelectedWhileErasing: the user selected another card while
// the erase ran — their choice stays; the formatted card is not adopted
// nor ejected, and the erased card's state is forgotten.
func TestOtherCardSelectedWhileErasing(t *testing.T) {
	a, _, e, _ := formatApp(t)
	ej := fakeEject(a, nil)
	statuses := recordFullStatuses(a)
	e.during = func() {
		if err := a.Config.Update(func(c *config.Config) {
			c.Watch.VolumeUUID = "OTHER-UUID"
			c.Watch.VolumeName = "OTHER"
		}); err != nil {
			t.Error(err)
		}
	}

	a.SyncNow()
	waitFor(t, "formatted", func() bool { return statuses.has(CodeFormatted) })
	waitSyncFinished(t, a)

	if got := a.Config.Get().Watch; got.VolumeUUID != "OTHER-UUID" || got.VolumeName != "OTHER" {
		t.Errorf("watch = %+v, want the user's choice OTHER-UUID kept", got)
	}
	if mt := a.MountedTarget(); mt != nil {
		t.Errorf("MountedTarget() = %+v, want nil (the selected card is not mounted)", mt)
	}
	if got := ej.all(); len(got) != 0 {
		t.Errorf("eject calls = %v, want none", got)
	}
	if got := rememberedUUIDs(t, a); slices.Contains(got, "VOL-UUID") {
		t.Errorf("remembered = %v, want the erased card forgotten", got)
	}
}

// TestFormatSkippedOnlyUnsupported: no media in scope, but files the app
// does not upload — the reason is shown (the card is not empty).
func TestFormatSkippedOnlyUnsupported(t *testing.T) {
	a, _, e, _ := formatApp(t)
	fakeVerify(a, uploader.VerifyResult{Unsupported: map[string]int{"hif": 12}}, nil)
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "format_skipped_unsupported", func() bool { return statuses.has(CodeFormatSkippedUnsupported) })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 0 {
		t.Fatalf("erase calls = %v, want none", got)
	}
	st, _ := statuses.last(CodeFormatSkippedUnsupported)
	if st.Params["files"] != "12 × .HIF" {
		t.Errorf("files = %q, want %q", st.Params["files"], "12 × .HIF")
	}
}

func TestEjectedCarriesFormatOutcome(t *testing.T) {
	tests := []struct {
		name   string
		verify uploader.VerifyResult
		want   map[string]string
	}{
		{"formatted", uploader.VerifyResult{Media: 2},
			map[string]string{"uploaded": "1", "format": "formatted", "name": "CARD"}},
		{"skipped_missing", uploader.VerifyResult{Media: 2, MissingCount: 3},
			map[string]string{"uploaded": "1", "format": "skipped_missing", "count": "3"}},
		{"skipped_unsupported", uploader.VerifyResult{Media: 2, Unsupported: map[string]int{"mxf": 3, "hif": 12}},
			map[string]string{"uploaded": "1", "format": "skipped_unsupported", "files": "12 × .HIF, 3 × .MXF"}},
		{"empty card", uploader.VerifyResult{},
			map[string]string{"uploaded": "1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, _, _ := formatApp(t)
			fakeVerify(a, tt.verify, nil)
			useFakeImmich(t, a)
			putPhotos(t, a, "A.JPG")
			fakeEject(a, nil)
			statuses := recordFullStatuses(a)

			a.SyncNow()
			waitFor(t, "ejected", func() bool { return statuses.has(CodeEjected) })
			waitSyncFinished(t, a)

			st, _ := statuses.last(CodeEjected)
			if !maps.Equal(st.Params, tt.want) {
				t.Errorf("ejected params = %v, want %v", st.Params, tt.want)
			}
		})
	}
}

// TestEjectedWithoutFormatHasNoOutcome: a cancelled countdown is not a
// format outcome.
func TestEjectedAfterCancelHasNoOutcome(t *testing.T) {
	a, _, _, _ := formatApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	fakeEject(a, nil)
	statuses := recordFullStatuses(a)
	statuses.setOn(func(s Status) {
		if s.Code == CodeFormatPending && s.Params["seconds"] == "29" {
			a.CancelFormat()
		}
	})

	a.SyncNow()
	waitFor(t, "ejected", func() bool { return statuses.has(CodeEjected) })
	waitSyncFinished(t, a)

	st, _ := statuses.last(CodeEjected)
	if want := map[string]string{"uploaded": "1"}; !maps.Equal(st.Params, want) {
		t.Errorf("ejected params = %v, want %v", st.Params, want)
	}
}

// TestCancelFormatAtLastTick: a cancel during the last second still stops
// the format.
func TestCancelFormatAtLastTick(t *testing.T) {
	a, _, e, _ := formatApp(t)
	statuses := recordFullStatuses(a)
	statuses.setOn(func(s Status) {
		if s.Code == CodeFormatPending && s.Params["seconds"] == "1" {
			a.CancelFormat()
		}
	})

	a.SyncNow()
	waitFor(t, "the countdown", func() bool { return statuses.has(CodeFormatPending) })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 0 {
		t.Fatalf("erase calls = %v after CancelFormat, want none", got)
	}
	if got := a.Status().Code; got != CodeNothingNew {
		t.Errorf("Status().Code = %q, want the sync's result (nothing_new)", got)
	}
}

// TestFormatCardChangedAfterLastTick isolates the check right before the
// erase: the card changes after the countdown's last per-tick check.
func TestFormatCardChangedAfterLastTick(t *testing.T) {
	tests := []struct {
		name   string
		change func(t *testing.T, a *App, mount string)
	}{
		{"another card at the mount point", func(t *testing.T, a *App, mount string) {
			a.mu.Lock()
			a.mountedTarget = &volume.Info{UUID: "OTHER-UUID", Name: "CARD", MountPoint: mount}
			a.mu.Unlock()
		}},
		{"another card selected", func(t *testing.T, a *App, mount string) {
			if err := a.Config.Update(func(c *config.Config) { c.Watch.VolumeUUID = "OTHER-UUID" }); err != nil {
				t.Error(err)
			}
		}},
		{"auto-format turned off", func(t *testing.T, a *App, mount string) {
			if err := a.Config.Update(func(c *config.Config) { c.Card.AutoFormat = false }); err != nil {
				t.Error(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, e, _ := formatApp(t)
			if err := a.Config.Update(func(c *config.Config) { c.Card.AutoEject = false }); err != nil {
				t.Fatal(err)
			}
			statuses := recordFullStatuses(a)
			mount := a.MountedTarget().MountPoint
			statuses.setOn(func(s Status) {
				if s.Code == CodeFormatPending && s.Params["seconds"] == "1" {
					tt.change(t, a, mount)
				}
			})

			a.SyncNow()
			waitFor(t, "the countdown", func() bool { return statuses.has(CodeFormatPending) })
			waitSyncFinished(t, a)

			if got := e.all(); len(got) != 0 {
				t.Fatalf("erase calls = %v, want none", got)
			}
			if statuses.has(CodeFormatting) || statuses.has(CodeFormatFailed) {
				t.Errorf("statuses = %v, want a silent skip", statuses.codes())
			}
		})
	}
}

// TestNoFormatRetryAfterFailure: after a failed format the card is not
// verified or erased again during the same insertion.
func TestNoFormatRetryAfterFailure(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(a *App, v *verifyRecorder, e *eraseRecorder)
	}{
		{"erase failed", func(a *App, v *verifyRecorder, e *eraseRecorder) {
			e.err = errors.New("diskutil eraseVolume: resource busy")
			card := *a.MountedTarget()
			a.listRemovable = func() ([]volume.Info, error) { return []volume.Info{card}, nil }
		}},
		{"verify failed", func(a *App, v *verifyRecorder, e *eraseRecorder) {
			v.err = errors.New("verify card: permission denied")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, v, e, _ := formatApp(t)
			tt.setup(a, v, e)
			statuses := recordFullStatuses(a)

			a.SyncNow()
			waitFor(t, "format_failed", func() bool { return statuses.has(CodeFormatFailed) })
			waitSyncFinished(t, a)
			verifies, erases := v.count(), len(e.all())

			a.SyncNow()
			waitFor(t, "the second sync", func() bool {
				runs, err := a.State.RecentRuns(10)
				return err == nil && len(runs) == 2 && runs[0].FinishedAt != nil
			})
			waitSyncFinished(t, a)

			if v.count() != verifies || len(e.all()) != erases {
				t.Errorf("verify calls %d→%d, erase calls %d→%d; want no retry", verifies, v.count(), erases, len(e.all()))
			}
			if got := a.Status().Code; got != CodeNothingNew {
				t.Errorf("Status().Code = %q, want nothing_new", got)
			}
		})
	}
}

func TestNoFormatRetryAfterErasedNotRemounted(t *testing.T) {
	a, v, e, _ := formatApp(t)
	e.err = fmt.Errorf("device disk4s1: %w", volume.ErrErasedNotRemounted)
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "format_failed", func() bool { return statuses.has(CodeFormatFailed) })
	waitSyncFinished(t, a)

	if !a.formatSuppressed("vol-uuid") {
		t.Error("formatSuppressed(VOL-UUID) = false after the card was erased but not found, want true")
	}
	if v.count() != 1 {
		t.Errorf("verify calls = %d, want 1", v.count())
	}
}

// TestFormatRetryAfterCardReinserted: the no-retry rule after a failed
// format lasts one insertion. Pulling the card out (an unmount that is not
// the app's own eject) clears it: the next insertion verifies and formats
// again.
func TestFormatRetryAfterCardReinserted(t *testing.T) {
	a, v, e, _ := formatApp(t)
	if err := a.Config.Update(func(c *config.Config) { c.Card.AutoEject = false }); err != nil {
		t.Fatal(err)
	}
	v.err = errors.New("verify card: permission denied")
	statuses := recordFullStatuses(a)
	card := *a.MountedTarget()

	a.SyncNow()
	waitFor(t, "format_failed", func() bool { return statuses.has(CodeFormatFailed) })
	waitSyncFinished(t, a)
	if !a.formatSuppressed(card.UUID) {
		t.Fatal("formatSuppressed = false after format_failed, want true")
	}

	a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: card})
	if a.formatSuppressed(card.UUID) {
		t.Fatal("formatSuppressed = true after the card was pulled out, want false")
	}

	v.mu.Lock()
	v.err = nil
	v.mu.Unlock()
	a.handleVolumeEvent(volume.Event{Type: volume.EventMounted, Info: card})
	waitFor(t, "the card formatted after reinsertion", func() bool { return statuses.has(CodeFormatted) })
	waitSyncFinished(t, a)
	if v.count() != 2 {
		t.Errorf("verify calls = %d, want 2 (once per insertion)", v.count())
	}
	if got := e.all(); len(got) != 1 {
		t.Errorf("erase calls = %v, want 1", got)
	}
}

// TestFormatSuppressionKeptOnOwnEject: the app's own eject of the card is
// not a removal by the user and keeps the no-retry rule.
func TestFormatSuppressionKeptOnOwnEject(t *testing.T) {
	a, v, _, _ := formatApp(t)
	v.err = errors.New("verify card: permission denied")
	statuses := recordFullStatuses(a)
	card := *a.MountedTarget()

	a.SyncNow()
	waitFor(t, "format_failed", func() bool { return statuses.has(CodeFormatFailed) })
	waitSyncFinished(t, a)

	a.mu.Lock()
	a.ejecting = card.MountPoint
	a.mu.Unlock()
	a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: card})
	if !a.formatSuppressed(card.UUID) {
		t.Error("formatSuppressed = false after the app's own eject, want true")
	}
}

// TestFormatSelectFailedDetail: the card was erased but saving the new
// card as the selected one failed. The format_failed detail follows
// "Couldn't format the card:", so it must not claim the card was
// formatted.
func TestFormatSelectFailedDetail(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits for chmod to remove")
	}
	if os.Geteuid() == 0 {
		t.Skip("running as root ignores file permissions")
	}
	a, _, e, _ := formatApp(t)
	if err := a.Config.Update(func(c *config.Config) { c.Card.AutoEject = false }); err != nil {
		t.Fatal(err)
	}
	cfgPath, err := config.ConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	// testConfigStore points HOME at a temp dir: this is never the real config.
	if !strings.HasPrefix(cfgPath, os.Getenv("HOME")) || !strings.Contains(cfgPath, os.TempDir()) {
		t.Fatalf("config path %q is not under a temporary HOME", cfgPath)
	}
	e.during = func() {
		// Saving the config after the erase fails.
		if err := os.Chmod(cfgPath, 0o400); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { _ = os.Chmod(cfgPath, 0o600) })
	statuses := recordFullStatuses(a)

	a.SyncNow()
	waitFor(t, "format_failed", func() bool { return statuses.has(CodeFormatFailed) })
	waitSyncFinished(t, a)

	st, _ := statuses.last(CodeFormatFailed)
	detail := st.Params["detail"]
	if strings.Contains(detail, "was formatted") {
		t.Errorf("format_failed detail = %q: reads \"Couldn't format the card: the card was formatted…\"", detail)
	}
	if !strings.Contains(detail, "could not be selected") || !strings.Contains(detail, "permission denied") {
		t.Errorf("format_failed detail = %q, want that the new card could not be selected, with the error", detail)
	}
}
