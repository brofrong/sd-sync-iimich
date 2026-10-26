package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// manualFormatApp is formatApp with auto-format off: formatting by hand
// does not depend on it.
func manualFormatApp(t *testing.T) (*App, *verifyRecorder, *eraseRecorder, volume.Info) {
	t.Helper()
	a, v, e, newCard := formatApp(t)
	if err := a.Config.Update(func(c *config.Config) { c.Card.AutoFormat = false }); err != nil {
		t.Fatal(err)
	}
	return a, v, e, newCard
}

func TestFormatCardErasesVerifiedCard(t *testing.T) {
	a, v, e, newCard := manualFormatApp(t)
	ej := fakeEject(a, nil)
	statuses := recordFullStatuses(a)
	mount := a.MountedTarget().MountPoint

	if err := a.FormatCard(); err != nil {
		t.Fatalf("FormatCard() = %v", err)
	}
	waitFor(t, "formatted", func() bool { return statuses.has(CodeFormatted) })
	waitSyncFinished(t, a)

	// No countdown: the window asked the user before.
	got := squash(statuses.all())
	want := []Code{CodeVerifying, CodeFormatting, CodeFormatted}
	if !slices.Equal(got, want) {
		t.Fatalf("statuses = %v, want %v", got, want)
	}
	if v.count() != 1 {
		t.Errorf("verify calls = %d, want 1", v.count())
	}
	if got := e.all(); len(got) != 1 || got[0] != mount+"|VOL-UUID" {
		t.Errorf("erase calls = %v, want the selected card once", got)
	}
	if got := a.Config.Get().Watch.VolumeUUID; got != newCard.UUID {
		t.Errorf("selected = %q, want the formatted card %q", got, newCard.UUID)
	}
	if mt := a.MountedTarget(); mt == nil || *mt != newCard {
		t.Errorf("MountedTarget() = %+v, want the new card", mt)
	}
	if got := ej.all(); len(got) != 0 {
		t.Errorf("eject calls = %v, want none: the user ejects from the window", got)
	}
	if runs, err := a.State.RecentRuns(10); err != nil || len(runs) != 0 {
		t.Errorf("runs = %d (%v), want no sync run recorded for a format", len(runs), err)
	}

	// The blank card's mount echo is not a new insertion.
	a.handleVolumeEvent(volume.Event{Type: volume.EventMounted, Info: newCard})
	waitSyncFinished(t, a)
	if runs, _ := a.State.RecentRuns(10); len(runs) != 0 {
		t.Errorf("runs = %d after the formatted card's mount event, want none", len(runs))
	}
}

func TestFormatCardKeepsCardWithFilesNotInImmich(t *testing.T) {
	a, _, e, _ := manualFormatApp(t)
	fakeVerify(a, uploader.VerifyResult{Media: 5, MissingCount: 2, Missing: []string{"DCIM/A.JPG"}}, nil)
	statuses := recordFullStatuses(a)

	if err := a.FormatCard(); err != nil {
		t.Fatalf("FormatCard() = %v", err)
	}
	waitFor(t, "skipped", func() bool { return statuses.has(CodeFormatSkippedMissing) })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 0 {
		t.Fatalf("erase calls = %v, want none", got)
	}
	if st, _ := statuses.last(CodeFormatSkippedMissing); st.Params["count"] != "2" {
		t.Errorf("format_skipped_missing params = %v, want count=2", st.Params)
	}
	if got := a.Config.Get().Watch.VolumeUUID; got != "VOL-UUID" {
		t.Errorf("selected = %q, want VOL-UUID kept", got)
	}
}

func TestFormatCardKeepsCardWithUnsupportedFiles(t *testing.T) {
	a, _, e, _ := manualFormatApp(t)
	fakeVerify(a, uploader.VerifyResult{Media: 1, Unsupported: map[string]int{"mxf": 1}}, nil)
	statuses := recordFullStatuses(a)

	if err := a.FormatCard(); err != nil {
		t.Fatalf("FormatCard() = %v", err)
	}
	waitFor(t, "skipped", func() bool { return statuses.has(CodeFormatSkippedUnsupported) })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 0 {
		t.Fatalf("erase calls = %v, want none", got)
	}
}

// An empty card is formatted when the user asks (auto-format leaves it).
func TestFormatCardErasesEmptyCard(t *testing.T) {
	a, _, e, _ := manualFormatApp(t)
	fakeVerify(a, uploader.VerifyResult{}, nil)
	statuses := recordFullStatuses(a)

	if err := a.FormatCard(); err != nil {
		t.Fatalf("FormatCard() = %v", err)
	}
	waitFor(t, "formatted", func() bool { return statuses.has(CodeFormatted) })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 1 {
		t.Fatalf("erase calls = %v, want one", got)
	}
}

func TestFormatCardVerificationFailure(t *testing.T) {
	a, _, e, _ := manualFormatApp(t)
	fakeVerify(a, uploader.VerifyResult{}, errors.New("server down"))
	statuses := recordFullStatuses(a)

	if err := a.FormatCard(); err != nil {
		t.Fatalf("FormatCard() = %v", err)
	}
	waitFor(t, "failed", func() bool { return statuses.has(CodeFormatFailed) })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 0 {
		t.Fatalf("erase calls = %v, want none", got)
	}
}

func TestFormatCardStoppedDuringVerification(t *testing.T) {
	a, v, e, _ := manualFormatApp(t)
	before := a.Status()
	v.mu.Lock()
	v.during = func(ctx context.Context) {
		a.StopSync()
		<-ctx.Done()
	}
	v.mu.Unlock()

	if err := a.FormatCard(); err != nil {
		t.Fatalf("FormatCard() = %v", err)
	}
	waitFor(t, "verification", func() bool { return v.count() == 1 })
	waitSyncFinished(t, a)

	if got := e.all(); len(got) != 0 {
		t.Fatalf("erase calls = %v, want none after Stop", got)
	}
	if got := a.Status().Code; got != before.Code {
		t.Errorf("Status().Code = %q, want %q shown again", got, before.Code)
	}
}

func TestFormatCardRefused(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, a *App)
		want  error
	}{
		{"card not mounted", func(t *testing.T, a *App) {
			a.mu.Lock()
			a.setMountedTargetLocked(nil)
			a.mu.Unlock()
		}, ErrCardNotMounted},
		{"server not set up", func(t *testing.T, a *App) {
			if err := a.Config.Update(func(c *config.Config) { c.Immich.APIKey = "" }); err != nil {
				t.Fatal(err)
			}
		}, ErrNotConfigured},
		{"sync running", func(t *testing.T, a *App) {
			useHeldImmich(t, a)
			putPhotos(t, a, "A.JPG")
			a.SyncNow()
			waitFor(t, "sync to start", a.syncRunning)
		}, ErrCardSyncing},
		{"needs administrator rights", func(t *testing.T, a *App) {
			a.eraseNeedsAdmin = func() bool { return true }
		}, volume.ErrNeedsAdmin},
		{"dry run", func(t *testing.T, a *App) { a.dryRun = true }, ErrDryRun},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, v, e, _ := manualFormatApp(t)
			tt.setup(t, a)

			if err := a.FormatCard(); !errors.Is(err, tt.want) {
				t.Fatalf("FormatCard() = %v, want %v", err, tt.want)
			}
			if v.count() != 0 || len(e.all()) != 0 {
				t.Errorf("verify calls = %d, erase calls = %v; want none", v.count(), e.all())
			}
		})
	}
}

// A card plugged in (or SyncNow) while the card is being formatted does
// not start a sync of the card being erased.
func TestNoSyncDuringFormatCard(t *testing.T) {
	a, v, _, _ := manualFormatApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	release := make(chan struct{})
	v.mu.Lock()
	v.during = func(context.Context) { <-release }
	v.mu.Unlock()

	if err := a.FormatCard(); err != nil {
		t.Fatalf("FormatCard() = %v", err)
	}
	waitFor(t, "verification", func() bool { return v.count() == 1 })
	a.SyncNow()
	close(release)
	waitSyncFinished(t, a)

	if runs, err := a.State.RecentRuns(10); err != nil || len(runs) != 0 {
		t.Errorf("runs = %d (%v), want no sync while formatting", len(runs), err)
	}
}
