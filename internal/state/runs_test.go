package state

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestRunsRoundTrip(t *testing.T) {
	db := openTestDB(t)

	startedAt := time.Now().Truncate(time.Second)
	id, err := db.StartRun("VOL-UUID", "CARD", startedAt)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	finishedAt := startedAt.Add(2 * time.Minute)
	run := SyncRun{
		ID:           id,
		StartedAt:    startedAt,
		FinishedAt:   &finishedAt,
		VolumeUUID:   "VOL-UUID",
		VolumeName:   "CARD",
		ResultCode:   "done",
		ResultParams: map[string]string{"host": "h"},
		Uploaded:     3,
		Skipped:      1,
		Errors:       2,
		Bytes:        12345,
		ErrorSummary: map[string]int{"rejected": 2},
	}
	if err := db.FinishRun(run); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	got, err := db.RecentRuns(10)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("RecentRuns: got %d runs, want 1", len(got))
	}

	if !got[0].StartedAt.Equal(run.StartedAt) {
		t.Errorf("StartedAt = %v, want %v", got[0].StartedAt, run.StartedAt)
	}
	if got[0].FinishedAt == nil || !got[0].FinishedAt.Equal(*run.FinishedAt) {
		t.Errorf("FinishedAt = %v, want %v", got[0].FinishedAt, run.FinishedAt)
	}
	// Zero the time fields before comparing the rest by value.
	got[0].StartedAt = run.StartedAt
	got[0].FinishedAt = run.FinishedAt
	if !reflect.DeepEqual(got[0], run) {
		t.Errorf("RecentRuns()[0] = %+v, want %+v", got[0], run)
	}
}

func TestRecentRunsNewestFirstAndLimited(t *testing.T) {
	db := openTestDB(t)

	base := time.Now().Truncate(time.Second)
	var ids []int64
	for i := 0; i < 3; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		id, err := db.StartRun("VOL-UUID", "CARD", at)
		if err != nil {
			t.Fatalf("StartRun[%d]: %v", i, err)
		}
		finishedAt := at.Add(30 * time.Second)
		if err := db.FinishRun(SyncRun{
			ID:         id,
			StartedAt:  at,
			FinishedAt: &finishedAt,
			VolumeUUID: "VOL-UUID",
			VolumeName: "CARD",
			ResultCode: "done",
		}); err != nil {
			t.Fatalf("FinishRun[%d]: %v", i, err)
		}
		ids = append(ids, id)
	}

	got, err := db.RecentRuns(2)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("RecentRuns: got %d runs, want 2", len(got))
	}
	if got[0].ID != ids[2] || got[1].ID != ids[1] {
		t.Errorf("RecentRuns order = [%d, %d], want [%d, %d]", got[0].ID, got[1].ID, ids[2], ids[1])
	}
}

func TestLastFinishedRunSkipsUnfinished(t *testing.T) {
	db := openTestDB(t)

	base := time.Now().Truncate(time.Second)

	finishedAt := base
	if err := func() error {
		id, err := db.StartRun("VOL-UUID", "CARD", base)
		if err != nil {
			return err
		}
		return db.FinishRun(SyncRun{
			ID:         id,
			StartedAt:  base,
			FinishedAt: &finishedAt,
			VolumeUUID: "VOL-UUID",
			VolumeName: "CARD",
			ResultCode: "done",
		})
	}(); err != nil {
		t.Fatalf("seed finished run: %v", err)
	}

	// Latest run is started but never finished.
	if _, err := db.StartRun("VOL-UUID", "CARD", base.Add(time.Minute)); err != nil {
		t.Fatalf("StartRun (unfinished): %v", err)
	}

	got, err := db.LastFinishedRun()
	if err != nil {
		t.Fatalf("LastFinishedRun: %v", err)
	}
	if got == nil {
		t.Fatal("LastFinishedRun: got nil, want the earlier finished run")
	}
	if got.ResultCode != "done" || !got.StartedAt.Equal(base) {
		t.Errorf("LastFinishedRun = %+v, want the finished run started at %v", got, base)
	}
}

func TestLastFinishedRunEmpty(t *testing.T) {
	db := openTestDB(t)

	got, err := db.LastFinishedRun()
	if err != nil {
		t.Fatalf("LastFinishedRun: %v", err)
	}
	if got != nil {
		t.Errorf("LastFinishedRun = %+v, want nil", got)
	}
}
