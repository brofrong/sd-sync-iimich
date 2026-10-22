package gui

import (
	"context"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/gui/icon"
)

// spinnerFrameInterval is how often the tray spinner advances.
const spinnerFrameInterval = 100 * time.Millisecond

// trayMenu is the menu bar icon and its menu: a status line, "Sync now" or
// "Stop", "Open window…", "Update available" (hidden without an update)
// and "Quit". It is refreshed when the status code, the language or the
// available update changes, not on every progress tick.
type trayMenu struct {
	svc  *Service
	tray *application.SystemTray

	status     *application.MenuItem
	syncOrStop *application.MenuItem
	open       *application.MenuItem
	update     *application.MenuItem
	quit       *application.MenuItem

	// refreshMu serializes refreshes so the last one always wins.
	refreshMu sync.Mutex

	animMu     sync.Mutex
	animCancel context.CancelFunc
}

func newTray(wapp *application.App, svc *Service, sh *shell) *trayMenu {
	t := &trayMenu{svc: svc}

	menu := wapp.NewMenu()
	t.status = menu.Add("").SetEnabled(false)
	t.syncOrStop = menu.Add("").OnClick(func(*application.Context) {
		if isRunning(svc.app.Status().Code) {
			svc.StopSync()
		} else {
			svc.SyncNow()
		}
	})
	menu.AddSeparator()
	t.open = menu.Add("").OnClick(func(*application.Context) { sh.showWindow() })
	t.update = menu.Add("").SetHidden(true).OnClick(func(*application.Context) {
		sh.showWindow()
		sh.emit(eventNavigate, "settings")
	})
	menu.AddSeparator()
	t.quit = menu.Add("").OnClick(func(*application.Context) { sh.quit(0) })

	// The tray is not running yet, so labels can be set directly.
	t.apply()

	t.tray = wapp.SystemTray.New()
	t.tray.SetTemplateIcon(icon.IdlePNG())
	t.tray.SetTooltip(appName)
	t.tray.SetMenu(menu)
	return t
}

// refresh updates the menu from the current status, language and update.
func (t *trayMenu) refresh() {
	t.refreshMu.Lock()
	defer t.refreshMu.Unlock()
	running := t.apply()
	t.setAnimating(running)
}

// apply sets the menu labels and reports whether a sync is running. Menu
// item setters are safe to call off the main thread.
func (t *trayMenu) apply() (running bool) {
	st := t.svc.app.Status()
	c := t.svc.catalog()
	rel := t.svc.availableUpdate()
	running = isRunning(st.Code)

	t.status.SetLabel(c.T("status."+string(st.Code)+".title", st.Params))
	if running {
		t.syncOrStop.SetLabel(c.T("tray.stop", nil)).SetEnabled(true)
	} else {
		t.syncOrStop.SetLabel(c.T("tray.sync_now", nil)).SetEnabled(t.svc.canSync())
	}
	t.open.SetLabel(c.T("tray.open_window", nil))
	if rel != nil {
		t.update.SetLabel(c.T("tray.update_available", map[string]string{"version": rel.Version})).SetHidden(false)
	} else {
		t.update.SetHidden(true)
	}
	t.quit.SetLabel(c.T("tray.quit", nil))
	return running
}

func isRunning(code app.Code) bool {
	return code == app.CodeScanning || code == app.CodeSyncing
}

// setAnimating starts or stops the spinner on the tray icon.
func (t *trayMenu) setAnimating(on bool) {
	t.animMu.Lock()
	defer t.animMu.Unlock()
	switch {
	case on && t.animCancel == nil:
		ctx, cancel := context.WithCancel(context.Background())
		t.animCancel = cancel
		go t.animate(ctx)
	case !on && t.animCancel != nil:
		t.animCancel()
		t.animCancel = nil
	}
}

// animate cycles the spinner frames until ctx is cancelled, then restores
// the idle icon. It must not hold any lock while setting the icon: that
// call waits for the main thread.
func (t *trayMenu) animate(ctx context.Context) {
	ticker := time.NewTicker(spinnerFrameInterval)
	defer ticker.Stop()
	for frame := 0; ; frame = (frame + 1) % icon.FrameCount {
		t.tray.SetTemplateIcon(icon.SyncingPNG(frame))
		select {
		case <-ctx.Done():
			t.tray.SetTemplateIcon(icon.IdlePNG())
			return
		case <-ticker.C:
		}
	}
}
