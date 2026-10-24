package gui

import (
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
)

func TestShowWindowAtStartup(t *testing.T) {
	tests := []struct {
		code    app.Code
		launchd bool
		want    bool
	}{
		// Opened by the user (Finder, Spotlight, a terminal): show it.
		{app.CodeWaitingForCard, false, true},
		{app.CodeScanning, false, true},
		// Started at login and set up: menu bar only.
		{app.CodeWaitingForCard, true, false},
		{app.CodeDone, true, false},
		{app.CodeScanning, true, false},
		// Setup is not finished: the window is the way to finish it.
		{app.CodeNotConfigured, true, true},
		{app.CodeNoCardSelected, true, true},
		{app.CodeNotConfigured, false, true},
	}
	for _, tt := range tests {
		if got := showWindowAtStartup(tt.code, tt.launchd); got != tt.want {
			t.Errorf("showWindowAtStartup(%s, launchd=%v) = %v, want %v", tt.code, tt.launchd, got, tt.want)
		}
	}
}

func TestForwardStatusSkipsProgressWhileWindowHidden(t *testing.T) {
	r := newRecorder()
	s := &Service{shell: &shell{}, status: newThrottle(0, r.emit)}

	s.forwardStatus(syncing(1), true) // code change: always sent
	s.forwardStatus(syncing(2), false)
	s.forwardStatus(syncing(3), false)
	if got := r.statuses(); len(got) != 1 || got[0].Progress.Current != 1 {
		t.Fatalf("sent %d statuses while hidden, want only the code change", len(got))
	}

	s.shell.visible.Store(true)
	s.forwardStatus(syncing(4), false)
	if got := r.statuses(); len(got) != 2 || got[1].Progress.Current != 4 {
		t.Fatalf("progress not sent while the window is visible: %d statuses", len(got))
	}
}

// TestCloseActionWithoutTray checks the pure decision closing the window
// makes: hide it when a tray is available to reopen it from, else
// minimise it so it stays reachable (and syncing keeps running) without
// one.
func TestCloseActionWithoutTray(t *testing.T) {
	if got := decideWindowClose(false); got != closeActionMinimise {
		t.Errorf("decideWindowClose(false) = %v, want closeActionMinimise", got)
	}
	if got := decideWindowClose(true); got != closeActionHide {
		t.Errorf("decideWindowClose(true) = %v, want closeActionHide", got)
	}
}

func TestWindowShownSendsLatestStatusAtOnce(t *testing.T) {
	r := newRecorder()
	s := &Service{shell: &shell{}, status: newThrottle(time.Hour, r.emit)}
	s.shell.visible.Store(true)
	s.forwardStatus(syncing(1), true)
	s.forwardStatus(syncing(2), false) // held back by the throttle

	s.windowShown(syncing(3))
	got := r.statuses()
	if len(got) != 2 || got[1].Progress.Current != 3 {
		t.Fatalf("statuses = %d (last %+v), want the latest sent at once", len(got), got[len(got)-1].Progress)
	}
}
