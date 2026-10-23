package gui

import (
	"reflect"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
)

func TestListCards(t *testing.T) {
	mounted := []CardView{
		{UUID: "USB", Name: "USB", MountPoint: "/Volumes/USB", Mounted: true, SizeBytes: 1},
		{UUID: "NEW", Name: "Brofrong", MountPoint: "/Volumes/Brofrong", Mounted: true, SizeBytes: 2},
	}
	remembered := []state.RememberedCard{
		{UUID: "new", Name: "Brofrong", Uploaded: 5}, // the mounted NEW card
		{UUID: "OLD", Name: "", Uploaded: 18900},
		{UUID: "SEL", Name: "Old name", Uploaded: 7},
	}

	got := listCards(mounted, remembered, "SEL", "Selected card")
	want := []CardView{
		{UUID: "SEL", Name: "Selected card", Selected: true, Remembered: true, Uploaded: 7},
		{UUID: "USB", Name: "USB", MountPoint: "/Volumes/USB", Mounted: true, SizeBytes: 1},
		{UUID: "NEW", Name: "Brofrong", MountPoint: "/Volumes/Brofrong", Mounted: true, SizeBytes: 2, Remembered: true, Uploaded: 5},
		{UUID: "OLD", Name: "", Remembered: true, Uploaded: 18900},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listCards =\n%+v\nwant\n%+v", got, want)
	}
}

func TestListCardsSelectedMountedComesFirst(t *testing.T) {
	mounted := []CardView{
		{UUID: "A", Name: "A", Mounted: true},
		{UUID: "B", Name: "B", Mounted: true},
	}
	got := listCards(mounted, nil, "b", "B")
	if len(got) != 2 || got[0].UUID != "B" || !got[0].Selected || got[1].Selected {
		t.Fatalf("listCards = %+v, want B (selected) first", got)
	}
}

func TestListCardsSelectedNeverSynced(t *testing.T) {
	got := listCards(nil, nil, "SEL", "Card")
	want := []CardView{{UUID: "SEL", Name: "Card", Selected: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("listCards = %+v, want %+v", got, want)
	}
}

func TestListCardsNothing(t *testing.T) {
	if got := listCards(nil, nil, "", ""); len(got) != 0 {
		t.Fatalf("listCards = %+v, want none", got)
	}
}
