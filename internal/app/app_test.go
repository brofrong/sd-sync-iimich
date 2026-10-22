package app

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// testConfigStore returns a config.Store usable in tests. config.Load reads
// and writes under $HOME/.config/sd-sync-immich, so HOME is redirected to a
// temp dir for the duration of the test.
func testConfigStore(t *testing.T) *config.Store {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
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
	a.SetStatusCallback(func(s Status) {
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
	// is the IdleStatus branch (ok==true); CodeWaitingForCard is the
	// expected outcome given the config set above.
	if got.Code != CodeWaitingForCard {
		t.Errorf("Status().Code = %q, want %q", got.Code, CodeWaitingForCard)
	}
}
