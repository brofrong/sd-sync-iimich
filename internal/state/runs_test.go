package state

import (
	"database/sql"
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
		Duplicates:   4,
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

// TestOpenAddsDuplicatesColumn opens a database whose sync_runs table was
// created before the duplicates column existed.
func TestOpenAddsDuplicatesColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`
		CREATE TABLE sync_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at INTEGER NOT NULL,
			finished_at INTEGER,
			volume_uuid TEXT NOT NULL DEFAULT '',
			volume_name TEXT NOT NULL DEFAULT '',
			result_code TEXT NOT NULL DEFAULT '',
			result_params TEXT NOT NULL DEFAULT '{}',
			uploaded INTEGER NOT NULL DEFAULT 0,
			skipped INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			bytes INTEGER NOT NULL DEFAULT 0,
			error_summary TEXT NOT NULL DEFAULT '{}'
		);
		INSERT INTO sync_runs (started_at, finished_at, volume_uuid, result_code, uploaded)
		VALUES (100, 200, 'VOL', 'done', 7);
	`); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()

	for range 2 { // and opening it again is fine
		db, err := Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		runs, err := db.RecentRuns(10)
		_ = db.Close()
		if err != nil {
			t.Fatalf("RecentRuns: %v", err)
		}
		if len(runs) != 1 || runs[0].Uploaded != 7 || runs[0].Duplicates != 0 {
			t.Fatalf("runs = %+v, want the old run with 0 duplicates", runs)
		}
	}
}

func TestRunByID(t *testing.T) {
	db := openTestDB(t)

	id, err := db.StartRun("VOL", "CARD", time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RecordFailures(id, nil); err != nil {
		t.Fatal(err)
	}
	run, err := db.Run(id)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if run == nil || run.ID != id || run.VolumeUUID != "VOL" || !run.FailuresRecorded {
		t.Errorf("Run(%d) = %+v, want the run with failures recorded", id, run)
	}

	missing, err := db.Run(id + 100)
	if err != nil || missing != nil {
		t.Errorf("Run(unknown) = %+v, %v; want nil, nil", missing, err)
	}
}
