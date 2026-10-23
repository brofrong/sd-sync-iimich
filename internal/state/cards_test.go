package state

import (
	"reflect"
	"testing"
	"time"
)

func TestRememberedCards(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second)
	uploaded := now

	// CARD-A: two uploaded files, one only hashed, named by its newest run.
	for _, rec := range []FileRecord{
		{VolumeUUID: "CARD-A", RelativePath: "DCIM/1.JPG", Size: 1, Mtime: now, SHA1: "a1", UploadedAt: &uploaded},
		{VolumeUUID: "CARD-A", RelativePath: "DCIM/2.JPG", Size: 1, Mtime: now, SHA1: "a2", UploadedAt: &uploaded},
		{VolumeUUID: "CARD-A", RelativePath: "DCIM/3.JPG", Size: 1, Mtime: now, SHA1: "a3"},
	} {
		if err := db.Upsert(rec); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	if _, err := db.StartRun("CARD-A", "OLD NAME", now.Add(-time.Hour)); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	if _, err := db.StartRun("CARD-A", "BROFRONG", now); err != nil {
		t.Fatalf("StartRun: %v", err)
	}
	// CARD-B: only a sync cursor, never ran in this version (no name).
	if err := db.SetVolumeSync("CARD-B", now, "DCIM/9.JPG"); err != nil {
		t.Fatalf("SetVolumeSync: %v", err)
	}
	// CARD-C: only history; nothing to forget, so not remembered.
	if _, err := db.StartRun("CARD-C", "OTHER", now); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	got, err := db.RememberedCards()
	if err != nil {
		t.Fatalf("RememberedCards: %v", err)
	}
	want := []RememberedCard{
		{UUID: "CARD-A", Name: "BROFRONG", Uploaded: 2},
		{UUID: "CARD-B", Name: "", Uploaded: 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RememberedCards = %+v, want %+v", got, want)
	}
}

func TestRememberedCardsEmpty(t *testing.T) {
	db := openTestDB(t)
	got, err := db.RememberedCards()
	if err != nil {
		t.Fatalf("RememberedCards: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("RememberedCards = %+v, want none", got)
	}
}

func TestForgetCardKeepsOtherCardsAndHistory(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().Truncate(time.Second)
	for _, uuid := range []string{"CARD-A", "CARD-B"} {
		if err := db.Upsert(FileRecord{VolumeUUID: uuid, RelativePath: "DCIM/1.JPG", Size: 1, Mtime: now, SHA1: uuid, UploadedAt: &now}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
		if err := db.SetVolumeSync(uuid, now, "DCIM/1.JPG"); err != nil {
			t.Fatalf("SetVolumeSync: %v", err)
		}
	}
	if _, err := db.StartRun("CARD-A", "A", now); err != nil {
		t.Fatalf("StartRun: %v", err)
	}

	if err := db.ForgetCard("CARD-A"); err != nil {
		t.Fatalf("ForgetCard: %v", err)
	}

	if rec, err := db.Get("CARD-A", "DCIM/1.JPG"); err != nil || rec != nil {
		t.Fatalf("Get(CARD-A) = %+v, %v; want nothing", rec, err)
	}
	cur, err := db.GetVolumeSync("CARD-A")
	if err != nil {
		t.Fatalf("GetVolumeSync: %v", err)
	}
	if cur != (VolumeSyncCursor{}) {
		t.Fatalf("GetVolumeSync(CARD-A) = %+v, want the zero cursor", cur)
	}
	if rec, err := db.Get("CARD-B", "DCIM/1.JPG"); err != nil || rec == nil {
		t.Fatalf("Get(CARD-B) = %+v, %v; want the record kept", rec, err)
	}
	runs, err := db.RecentRuns(10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("RecentRuns = %+v, %v; want the history kept", runs, err)
	}
	cards, err := db.RememberedCards()
	if err != nil {
		t.Fatalf("RememberedCards: %v", err)
	}
	if len(cards) != 1 || cards[0].UUID != "CARD-B" {
		t.Fatalf("RememberedCards = %+v, want only CARD-B", cards)
	}
}
