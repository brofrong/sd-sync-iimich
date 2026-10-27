package state

import (
	"testing"
	"time"
)

func TestCursorCovers(t *testing.T) {
	zero := VolumeSyncCursor{}
	now := time.Now().Truncate(time.Second)
	cur := VolumeSyncCursor{Mtime: now, RelativePath: "DCIM/5.JPG"}

	if zero.Covers(now, "DCIM/5.JPG") {
		t.Errorf("zero cursor covers %v, want false", now)
	}
	if !cur.Covers(now.Add(-time.Minute), "DCIM/9.JPG") {
		t.Error("earlier mtime not covered, want true")
	}
	if !cur.Covers(now, "DCIM/5.JPG") {
		t.Error("same mtime, same path not covered, want true")
	}
	if !cur.Covers(now, "DCIM/1.JPG") {
		t.Error("same mtime, earlier path not covered, want true")
	}
	if cur.Covers(now, "DCIM/9.JPG") {
		t.Error("same mtime, later path covered, want false")
	}
	if cur.Covers(now.Add(time.Minute), "DCIM/1.JPG") {
		t.Error("later mtime covered, want false")
	}
}

func TestUploadedPaths(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second)

	if err := db.MarkHashed("CARD-A", "DCIM/1.JPG", "a1", 1, now); err != nil {
		t.Fatalf("MarkHashed: %v", err)
	}
	if err := db.MarkHashed("CARD-A", "DCIM/2.JPG", "a2", 1, now); err != nil {
		t.Fatalf("MarkHashed: %v", err)
	}
	if err := db.MarkUploaded("CARD-A", "DCIM/1.JPG", now); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}
	// A different card's uploaded file must not show up.
	if err := db.Upsert(FileRecord{VolumeUUID: "CARD-B", RelativePath: "DCIM/1.JPG", Size: 1, Mtime: now, SHA1: "b1", UploadedAt: &now}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := db.UploadedPaths("CARD-A")
	if err != nil {
		t.Fatalf("UploadedPaths: %v", err)
	}
	want := map[string]struct{}{"DCIM/1.JPG": {}}
	if len(got) != len(want) {
		t.Fatalf("UploadedPaths = %+v, want %+v", got, want)
	}
	for p := range want {
		if _, ok := got[p]; !ok {
			t.Errorf("UploadedPaths missing %q, got %+v", p, got)
		}
	}
}

func TestLastFinishedRunFor(t *testing.T) {
	db := openTestDB(t)
	base := time.Now().Truncate(time.Second)

	finishedAt := base
	id, err := db.StartRun("CARD-A", "A", base)
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := db.FinishRun(SyncRun{ID: id, StartedAt: base, FinishedAt: &finishedAt, VolumeUUID: "CARD-A", VolumeName: "A", ResultCode: "done"}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	// A newer run for the same card that never finished must not be picked.
	if _, err := db.StartRun("CARD-A", "A", base.Add(time.Minute)); err != nil {
		t.Fatalf("StartRun (unfinished): %v", err)
	}

	// Another card's finished run must not leak into CARD-A's result.
	otherFinishedAt := base.Add(2 * time.Minute)
	otherID, err := db.StartRun("CARD-B", "B", base.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if err := db.FinishRun(SyncRun{ID: otherID, StartedAt: base.Add(2 * time.Minute), FinishedAt: &otherFinishedAt, VolumeUUID: "CARD-B", VolumeName: "B", ResultCode: "done"}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	got, err := db.LastFinishedRunFor("CARD-A")
	if err != nil {
		t.Fatalf("LastFinishedRunFor: %v", err)
	}
	if got == nil || got.ID != id {
		t.Fatalf("LastFinishedRunFor(CARD-A) = %+v, want the finished run %d", got, id)
	}

	none, err := db.LastFinishedRunFor("CARD-C")
	if err != nil {
		t.Fatalf("LastFinishedRunFor: %v", err)
	}
	if none != nil {
		t.Fatalf("LastFinishedRunFor(CARD-C) = %+v, want nil", none)
	}
}

func TestSavedVolumeSyncIsReadOnly(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second)

	// A file is recorded as uploaded but no cursor is saved: GetVolumeSync
	// would rebuild and save one; SavedVolumeSync must not.
	if err := db.MarkHashed("CARD-A", "DCIM/2.JPG", "a2", 1, now); err != nil {
		t.Fatalf("MarkHashed: %v", err)
	}
	if err := db.MarkUploaded("CARD-A", "DCIM/2.JPG", now); err != nil {
		t.Fatalf("MarkUploaded: %v", err)
	}
	for i := 0; i < 2; i++ {
		cur, found, err := db.SavedVolumeSync("CARD-A")
		if err != nil {
			t.Fatalf("SavedVolumeSync: %v", err)
		}
		if found || cur != (VolumeSyncCursor{}) {
			t.Fatalf("SavedVolumeSync #%d = %+v, %v; want the zero cursor, not found", i, cur, found)
		}
	}

	// An older file uploaded later still moves the cursor: nothing was saved.
	older := now.Add(-time.Minute)
	if err := db.SetVolumeSync("CARD-A", older, "DCIM/1.JPG"); err != nil {
		t.Fatalf("SetVolumeSync: %v", err)
	}
	cur, found, err := db.SavedVolumeSync("CARD-A")
	if err != nil {
		t.Fatalf("SavedVolumeSync: %v", err)
	}
	if !found || !cur.Mtime.Equal(older) || cur.RelativePath != "DCIM/1.JPG" {
		t.Fatalf("SavedVolumeSync = %+v, %v; want %v DCIM/1.JPG, found", cur, found, older)
	}
}
