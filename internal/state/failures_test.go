package state

import (
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRecordAndReadFailures(t *testing.T) {
	db := openTestDB(t)

	id, err := db.StartRun("VOL-UUID", "CARD", time.Now())
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	failures := []FileFailure{
		{RelativePath: "DCIM/100/A.JPG", Kind: "upload_failed", Detail: "network error"},
		{RelativePath: "DCIM/100/B.JPG", Kind: "hash_failed", Detail: "io error"},
	}
	if err := db.RecordFailures(id, failures); err != nil {
		t.Fatalf("RecordFailures: %v", err)
	}

	got, err := db.RunFailures(id)
	if err != nil {
		t.Fatalf("RunFailures: %v", err)
	}
	if !reflect.DeepEqual(got, failures) {
		t.Fatalf("RunFailures() = %+v, want %+v", got, failures)
	}

	finishedAt := time.Now()
	if err := db.FinishRun(SyncRun{
		ID:         id,
		StartedAt:  time.Now(),
		FinishedAt: &finishedAt,
		VolumeUUID: "VOL-UUID",
		VolumeName: "CARD",
		ResultCode: "done",
	}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	runs, err := db.RecentRuns(1)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("RecentRuns: got %d runs, want 1", len(runs))
	}
	if !runs[0].FailuresRecorded {
		t.Errorf("RecentRuns()[0].FailuresRecorded = false, want true")
	}
}

func TestRunWithoutRecordedFailures(t *testing.T) {
	db := openTestDB(t)

	id, err := db.StartRun("VOL-UUID", "CARD", time.Now())
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	finishedAt := time.Now()
	if err := db.FinishRun(SyncRun{
		ID:         id,
		StartedAt:  time.Now(),
		FinishedAt: &finishedAt,
		VolumeUUID: "VOL-UUID",
		VolumeName: "CARD",
		ResultCode: "done",
	}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	runs, err := db.RecentRuns(1)
	if err != nil {
		t.Fatalf("RecentRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("RecentRuns: got %d runs, want 1", len(runs))
	}
	if runs[0].FailuresRecorded {
		t.Errorf("RecentRuns()[0].FailuresRecorded = true, want false")
	}

	got, err := db.RunFailures(id)
	if err != nil {
		t.Fatalf("RunFailures: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("RunFailures() = %+v, want empty", got)
	}
}

// TestOpenAddsFailuresRecordedColumn opens a database whose sync_runs table
// was created before the failures_recorded column existed.
func TestOpenAddsFailuresRecordedColumn(t *testing.T) {
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
			duplicates INTEGER NOT NULL DEFAULT 0,
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
		if len(runs) != 1 || runs[0].Uploaded != 7 || runs[0].FailuresRecorded {
			t.Fatalf("runs = %+v, want the old run with FailuresRecorded false", runs)
		}
	}
}
