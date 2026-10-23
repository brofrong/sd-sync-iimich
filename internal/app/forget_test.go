package app

import (
	"errors"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
)

func rememberCard(t *testing.T, a *App, uuid string) {
	t.Helper()
	now := time.Now()
	if err := a.State.Upsert(state.FileRecord{VolumeUUID: uuid, RelativePath: "DCIM/1.JPG", Size: 1, Mtime: now, SHA1: "s", UploadedAt: &now}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
}

func rememberedUUIDs(t *testing.T, a *App) []string {
	t.Helper()
	cards, err := a.State.RememberedCards()
	if err != nil {
		t.Fatalf("RememberedCards: %v", err)
	}
	var uuids []string
	for _, c := range cards {
		uuids = append(uuids, c.UUID)
	}
	return uuids
}

func TestForgetSelectedCardDeselectsIt(t *testing.T) {
	a := testApp(t)
	rememberCard(t, a, "VOL-UUID")
	rememberCard(t, a, "OTHER-UUID")

	if err := a.ForgetCard("vol-uuid"); err != nil { // UUIDs compare case-insensitively
		t.Fatalf("ForgetCard: %v", err)
	}

	cfg := a.Config.Get()
	if cfg.Watch.VolumeUUID != "" || cfg.Watch.VolumeName != "" {
		t.Errorf("watch = (%q, %q), want the selection cleared", cfg.Watch.VolumeUUID, cfg.Watch.VolumeName)
	}
	if mt := a.MountedTarget(); mt != nil {
		t.Errorf("MountedTarget() = %+v, want nil", mt)
	}
	if got := rememberedUUIDs(t, a); len(got) != 1 || got[0] != "OTHER-UUID" {
		t.Errorf("remembered = %v, want [OTHER-UUID]", got)
	}
	if got := a.Status().Code; got != CodeNoCardSelected {
		t.Errorf("Status().Code = %q, want %q", got, CodeNoCardSelected)
	}
}

func TestForgetOtherCardKeepsSelection(t *testing.T) {
	a := testApp(t)
	rememberCard(t, a, "VOL-UUID")
	rememberCard(t, a, "OTHER-UUID")

	if err := a.ForgetCard("OTHER-UUID"); err != nil {
		t.Fatalf("ForgetCard: %v", err)
	}

	if got := a.Config.Get().Watch.VolumeUUID; got != "VOL-UUID" {
		t.Errorf("selected = %q, want VOL-UUID kept", got)
	}
	if got := rememberedUUIDs(t, a); len(got) != 1 || got[0] != "VOL-UUID" {
		t.Errorf("remembered = %v, want [VOL-UUID]", got)
	}
}

func TestForgetCardWhileItSyncsFails(t *testing.T) {
	a := testApp(t)
	rememberCard(t, a, "VOL-UUID")
	a.syncCancel = func() {} // a sync of the selected card is running

	if err := a.ForgetCard("VOL-UUID"); !errors.Is(err, ErrCardSyncing) {
		t.Fatalf("ForgetCard = %v, want ErrCardSyncing", err)
	}
	if got := a.Config.Get().Watch.VolumeUUID; got != "VOL-UUID" {
		t.Errorf("selected = %q, want unchanged", got)
	}
	if got := rememberedUUIDs(t, a); len(got) != 1 {
		t.Errorf("remembered = %v, want the card kept", got)
	}
}

func TestForgetOtherCardWhileSelectedSyncs(t *testing.T) {
	a := testApp(t)
	rememberCard(t, a, "OTHER-UUID")
	a.syncCancel = func() {}

	if err := a.ForgetCard("OTHER-UUID"); err != nil {
		t.Fatalf("ForgetCard = %v, want nil: only the selected card syncs", err)
	}
	if got := rememberedUUIDs(t, a); len(got) != 0 {
		t.Errorf("remembered = %v, want none", got)
	}
}
