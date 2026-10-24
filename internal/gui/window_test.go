package gui

import (
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
)

// fakeShowableWindow stands in for *application.WebviewWindow in
// TestShowWindowNow: it tracks IsMinimised/UnMinimise/Show/Focus calls
// instead of needing a real, running window.
type fakeShowableWindow struct {
	minimised        bool
	unMinimiseCalled bool
	shown, focused   bool
}

func (f *fakeShowableWindow) IsMinimised() bool { return f.minimised }

func (f *fakeShowableWindow) UnMinimise() {
	f.unMinimiseCalled = true
	f.minimised = false
}

func (f *fakeShowableWindow) Show() application.Window {
	f.shown = true
	return nil
}

func (f *fakeShowableWindow) Focus() { f.focused = true }

// TestShowWindowNow checks showWindowNow brings a minimised window back
// before showing it: Show (SW_SHOW on Windows, gtk_widget_show_all on
// Linux) and Focus alone do not un-minimise a window, so a tray/taskbar
// click on an already-minimised window (minimised by the user, or by our
// own no-tray close behaviour) must call UnMinimise first.
func TestShowWindowNow(t *testing.T) {
	minimised := &fakeShowableWindow{minimised: true}
	showWindowNow(minimised)
	if !minimised.unMinimiseCalled || minimised.minimised {
		t.Fatalf("a minimised window was not un-minimised: %+v", minimised)
	}
	if !minimised.shown || !minimised.focused {
		t.Fatalf("a minimised window was not shown/focused: %+v", minimised)
	}

	normal := &fakeShowableWindow{}
	showWindowNow(normal)
	if normal.unMinimiseCalled {
		t.Fatalf("UnMinimise called on a window that wasn't minimised: %+v", normal)
	}
	if !normal.shown || !normal.focused {
		t.Fatalf("a normal window was not shown/focused: %+v", normal)
	}
}

func TestStartupWindow(t *testing.T) {
	tests := []struct {
		code    app.Code
		atLogin bool
		hasTray bool
		want    startupWindow
	}{
		// Opened by the user (Finder, Spotlight, a terminal): show it.
		{app.CodeWaitingForCard, false, true, startupShown},
		{app.CodeScanning, false, true, startupShown},
		{app.CodeWaitingForCard, false, false, startupShown},
		// Started at login and set up: tray only.
		{app.CodeWaitingForCard, true, true, startupHidden},
		{app.CodeDone, true, true, startupHidden},
		{app.CodeScanning, true, true, startupHidden},
		// At login without a tray (GNOME without AppIndicator): hidden would
		// leave no way back to the app, so it starts minimised.
		{app.CodeWaitingForCard, true, false, startupMinimised},
		{app.CodeDone, true, false, startupMinimised},
		// Setup is not finished: the window is the way to finish it.
		{app.CodeNotConfigured, true, true, startupShown},
		{app.CodeNoCardSelected, true, true, startupShown},
		{app.CodeNotConfigured, true, false, startupShown},
		{app.CodeNotConfigured, false, true, startupShown},
	}
	for _, tt := range tests {
		if got := decideStartupWindow(tt.code, tt.atLogin, tt.hasTray); got != tt.want {
			t.Errorf("decideStartupWindow(%s, atLogin=%v, hasTray=%v) = %v, want %v", tt.code, tt.atLogin, tt.hasTray, got, tt.want)
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

func TestStartedAtLogin(t *testing.T) {
	for _, tt := range []struct{ autostart, launchd, want bool }{
		{false, false, false},
		// Windows and Linux: the autostart entry passes --autostart.
		{true, false, true},
		// macOS: the LaunchAgent.
		{false, true, true},
		{true, true, true},
	} {
		orig := underLaunchd
		underLaunchd = func() bool { return tt.launchd }
		got := startedAtLogin(tt.autostart)
		underLaunchd = orig
		if got != tt.want {
			t.Errorf("startedAtLogin(autostart=%v) with launchd=%v = %v, want %v", tt.autostart, tt.launchd, got, tt.want)
		}
	}
}

func TestSingleInstanceOptions(t *testing.T) {
	shown := 0
	opts := singleInstanceOptions(func() { shown++ })
	if opts.UniqueID != "com.sd-sync-immich" {
		t.Errorf("UniqueID = %q, want com.sd-sync-immich", opts.UniqueID)
	}
	if opts.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0 (a second copy is not an error, and launchd must not restart it)", opts.ExitCode)
	}
	if opts.OnSecondInstanceLaunch == nil {
		t.Fatal("no OnSecondInstanceLaunch")
	}
	// The user opened the app again: show the running one's window.
	opts.OnSecondInstanceLaunch(application.SecondInstanceData{Args: []string{"/x/sd-sync-immich"}})
	opts.OnSecondInstanceLaunch(application.SecondInstanceData{Args: []string{"/x/sd-sync-immich", "run"}})
	if shown != 2 {
		t.Fatalf("window shown %d times, want 2", shown)
	}
	// An autostart entry fired while the app runs: keep it in the tray.
	opts.OnSecondInstanceLaunch(application.SecondInstanceData{Args: []string{"/x/sd-sync-immich", "run", "--autostart"}})
	if shown != 2 {
		t.Fatalf("window shown for a second instance started at login")
	}
}
