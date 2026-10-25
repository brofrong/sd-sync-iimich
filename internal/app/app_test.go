package app

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/testenv"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// testConfigStore returns a config.Store usable in tests. config.Load reads
// and writes under the user's config directory, so the home directory is
// redirected to a temp dir for the duration of the test.
func testConfigStore(t *testing.T) *config.Store {
	t.Helper()
	testenv.Home(t)
	store, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	return store
}

// TestSyncNowRecordsRun checks that SyncNow records a sync_runs row (via
// state.StartRun/FinishRun) with the outcome of the attempt, end to end.
// The watched volume's mount point is an empty directory with no DCIM
// folder, so the uploader finds nothing to upload and the sync finishes as
// CodeNothingNew without needing a real Immich server.
func TestSyncNowRecordsRun(t *testing.T) {
	cfgStore := testConfigStore(t)
	if err := cfgStore.Update(func(c *config.Config) {
		c.Immich = config.ImmichConfig{URL: "https://immich.example", APIKey: "key"}
		c.Watch = config.WatchConfig{VolumeUUID: "VOL-UUID", VolumeName: "CARD"}
	}); err != nil {
		t.Fatalf("cfgStore.Update: %v", err)
	}

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	vol := volume.Info{UUID: "VOL-UUID", Name: "CARD", MountPoint: t.TempDir()}

	a := &App{
		Config:        cfgStore,
		State:         st,
		Uploader:      &uploader.Service{Config: cfgStore, State: st},
		mountedTarget: &vol,
	}

	done := make(chan Status, 1)
	a.OnStatus(func(s Status) {
		if s.Code == CodeNothingNew {
			select {
			case done <- s:
			default:
			}
		}
	})

	a.SyncNow()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for sync to finish")
	}

	runs, err := st.RecentRuns(10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("RecentRuns: got %d runs, want 1", len(runs))
	}

	run := runs[0]
	if run.VolumeUUID != "VOL-UUID" || run.VolumeName != "CARD" {
		t.Errorf("run volume = (%q, %q), want (VOL-UUID, CARD)", run.VolumeUUID, run.VolumeName)
	}
	if run.ResultCode != string(CodeNothingNew) {
		t.Errorf("run.ResultCode = %q, want %q", run.ResultCode, CodeNothingNew)
	}
	if run.FinishedAt == nil {
		t.Fatal("run.FinishedAt = nil, want set")
	}
	if run.Uploaded != 0 || run.Errors != 0 {
		t.Errorf("run = %+v, want Uploaded=0 Errors=0", run)
	}
}

// TestNewUsesLastFinishedRunAsStartupStatus checks the ok==false path of
// startupStatus end to end via New(): with the selected card already
// mounted (simulated by a real USB/SD volume being unavailable on the test
// machine, this exercises the "no run history" fallback instead) New()
// must not leave Status zero-valued.
func TestNewUsesLastFinishedRunAsStartupStatus(t *testing.T) {
	cfgStore := testConfigStore(t)
	if err := cfgStore.Update(func(c *config.Config) {
		c.Immich = config.ImmichConfig{URL: "https://immich.example", APIKey: "key"}
		c.Watch = config.WatchConfig{VolumeUUID: "VOL-UUID", VolumeName: "CARD"}
	}); err != nil {
		t.Fatalf("cfgStore.Update: %v", err)
	}

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	a, err := New(cfgStore, st, true)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if a.Watcher != nil {
			a.Watcher.Close()
		}
	})

	got := a.Status()
	if got.Code == "" {
		t.Fatalf("Status().Code is empty, want a concrete startup status")
	}
	// The selected card isn't actually mounted on the test machine, so this
	// is the IdleStatus branch (ok==true): waiting for the card, or another
	// card is mounted when the machine has a removable volume plugged in.
	want := CodeWaitingForCard
	if mounted, err := volume.ListRemovable(); err == nil && len(mounted) > 0 {
		want = CodeOtherCardMounted
	}
	if got.Code != want {
		t.Errorf("Status().Code = %q, want %q", got.Code, want)
	}
}

// testApp returns an App with a configured server and selected card "CARD"
// (VOL-UUID) whose mount point is an empty temp dir, plus a resumer whose
// ping always fails so it keeps waiting until stopped.
func testApp(t *testing.T) *App {
	t.Helper()
	cfgStore := testConfigStore(t)
	if err := cfgStore.Update(func(c *config.Config) {
		c.Immich = config.ImmichConfig{URL: "https://immich.example", APIKey: "key"}
		c.Watch = config.WatchConfig{VolumeUUID: "VOL-UUID", VolumeName: "CARD"}
	}); err != nil {
		t.Fatalf("cfgStore.Update: %v", err)
	}
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	vol := volume.Info{UUID: "VOL-UUID", Name: "CARD", MountPoint: t.TempDir()}
	a := &App{
		Config:        cfgStore,
		State:         st,
		Uploader:      &uploader.Service{Config: cfgStore, State: st},
		mountedTarget: &vol,
	}
	// Never touch the real Mac: no caffeinate, no diskutil, no real volume
	// list (tests that need these replace the seams).
	a.keepAwake = func() func() { return func() {} }
	a.eject = func(mountPoint string) error {
		t.Errorf("unexpected eject of %s", mountPoint)
		return errors.New("eject not expected in this test")
	}
	a.isCameraCard = func(string) bool { return false }
	a.listRemovable = func() ([]volume.Info, error) { return nil, nil }
	fastResumeIntervals(t)
	a.resumer = newResumer(
		func(context.Context) error { return errPingDown },
		func() bool { return true },
		func() { t.Error("resumer started a sync unexpectedly") },
	)
	t.Cleanup(a.resumer.Stop)
	return a
}

func TestSyncNowIsNoOpWhileSyncRuns(t *testing.T) {
	a := testApp(t)

	// Simulate an in-flight sync.
	var cancelled atomic.Bool
	a.syncCancel = func() { cancelled.Store(true) }

	var statuses atomic.Int32
	a.OnStatus(func(Status) { statuses.Add(1) })

	a.SyncNow()

	if cancelled.Load() {
		t.Error("SyncNow cancelled the running sync, want no-op")
	}
	runs, err := a.State.RecentRuns(10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("SyncNow started a new run while one was running: %d runs recorded", len(runs))
	}
	if got := statuses.Load(); got != 0 {
		t.Errorf("SyncNow published %d statuses, want 0", got)
	}
}

func TestSyncNowStopsResumer(t *testing.T) {
	a := testApp(t)
	a.resumer.Start()

	done := make(chan struct{}, 1)
	a.OnStatus(func(s Status) {
		if s.Code == CodeNothingNew {
			select {
			case done <- struct{}{}:
			default:
			}
		}
	})
	a.Retry()

	if a.resumer.Waiting() {
		t.Error("resumer still waiting after Retry")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for sync to finish")
	}
}

func TestSyncCanRunAgainAfterFinishing(t *testing.T) {
	a := testApp(t)

	finished := make(chan struct{}, 2)
	a.OnStatus(func(s Status) {
		if s.Code == CodeNothingNew {
			finished <- struct{}{}
		}
	})
	for i := 0; i < 2; i++ {
		a.SyncNow()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			t.Fatalf("sync %d: timed out waiting for it to finish", i+1)
		}
		waitFor(t, "sync to be marked finished", func() bool { return !a.syncRunning() })
	}

	runs, err := a.State.RecentRuns(10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Errorf("got %d runs, want 2", len(runs))
	}
}

func TestStopSyncCancelsSyncAndStopsResumer(t *testing.T) {
	a := testApp(t)
	a.resumer.Start()

	var cancelled atomic.Bool
	a.syncCancel = func() { cancelled.Store(true) }

	a.StopSync()

	if !cancelled.Load() {
		t.Error("StopSync did not cancel the running sync")
	}
	if a.resumer.Waiting() {
		t.Error("resumer still waiting after StopSync")
	}
}

func TestMaybeResumeStartsResumerOnlyForServerErrors(t *testing.T) {
	a := testApp(t)

	a.maybeResume(CodeAuthFailed)
	if a.resumer.Waiting() {
		t.Fatal("resumer started after auth_failed")
	}
	a.maybeResume(CodeServerUnreachable)
	if !a.resumer.Waiting() {
		t.Fatal("resumer not started after server_unreachable with card mounted")
	}
	a.resumer.Stop()

	a.mu.Lock()
	a.mountedTarget = nil
	a.mu.Unlock()
	a.maybeResume(CodeServerError)
	if a.resumer.Waiting() {
		t.Fatal("resumer started after server_error without a mounted card")
	}
}

func TestEjectCardWithoutMountedCardFails(t *testing.T) {
	a := testApp(t)
	a.mu.Lock()
	a.mountedTarget = nil
	a.mu.Unlock()
	a.setStatus(newStatus(CodeWaitingForCard, nil))

	if err := a.EjectCard(); err == nil {
		t.Fatal("EjectCard() = nil, want error when the selected card is not mounted")
	}
	if got := a.Status().Code; got != CodeWaitingForCard {
		t.Errorf("Status().Code = %q after failed eject, want unchanged %q", got, CodeWaitingForCard)
	}
}

func TestSelectCardPersistsSelection(t *testing.T) {
	a := testApp(t)

	// The new card isn't mounted on the test machine.
	if err := a.SelectCard(volume.Info{UUID: "NEW-UUID", Name: "NEWCARD"}); err != nil {
		t.Fatalf("SelectCard: %v", err)
	}

	cfg := a.Config.Get()
	if cfg.Watch.VolumeUUID != "NEW-UUID" || cfg.Watch.VolumeName != "NEWCARD" {
		t.Errorf("watch = (%q, %q), want (NEW-UUID, NEWCARD)", cfg.Watch.VolumeUUID, cfg.Watch.VolumeName)
	}
	if mt := a.MountedTarget(); mt != nil {
		t.Errorf("MountedTarget() = %+v after selecting an unmounted card, want nil", mt)
	}
	runs, err := a.State.RecentRuns(10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("SelectCard started a sync for an unmounted card: %d runs", len(runs))
	}
}

func TestUnmountStopsResumerOnlyForSelectedCard(t *testing.T) {
	a := testApp(t)
	card := a.MountedTarget()
	a.resumer.Start()

	a.handleVolumeEvent(volume.Event{
		Type: volume.EventUnmounted,
		Info: volume.Info{UUID: "OTHER-UUID", Name: "USB", MountPoint: "/Volumes/USB"},
	})
	if !a.resumer.Waiting() {
		t.Fatal("resumer stopped after an unrelated volume was unmounted, want still waiting")
	}

	a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: *card})
	if a.resumer.Waiting() {
		t.Fatal("resumer still waiting after the selected card was unmounted")
	}
}
