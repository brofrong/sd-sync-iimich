package gui

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/gui/icon"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/i18n"
)

// spinnerFrameInterval is how often the tray spinner advances.
const spinnerFrameInterval = 100 * time.Millisecond

// trayLineInterval limits progress updates of the tray's status line to
// one per second; a code change shows at once.
const trayLineInterval = time.Second

// trayMenu is the menu bar icon and its menu: a status line, "Sync now" or
// "Stop", "Open window…", "Update available" (hidden without an update)
// and "Quit". It is refreshed when the status code, the language or the
// available update changes; while syncing, the status line also shows the
// progress (see showProgress).
type trayMenu struct {
	svc  *Service
	tray *application.SystemTray
	line *throttle

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
	t.line = newTrayLine(func(st app.Status) {
		t.status.SetLabel(trayStatusLine(t.svc.catalog(), st))
	})

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
	setTrayIcon(t.tray, icon.IdlePNG())
	t.tray.SetTooltip(appName)
	t.tray.SetMenu(menu)
	if trayClickOpensWindow {
		t.tray.OnClick(func() { sh.showWindow() })
	}
	return t
}

// trayIconSetter is the part of *application.SystemTray that sets its
// icon, factored out so setTrayIcon can be tested with a fake tray
// instead of a real, running one.
type trayIconSetter interface {
	SetTemplateIcon(icon []byte) *application.SystemTray
	SetIcon(icon []byte) *application.SystemTray
}

// setTrayIcon sets png as tray's icon: a template (monochrome, tinted by
// the menu bar) image on macOS, matching icon.Template; the colored logo
// elsewhere, so it reads on both a light and a dark panel.
func setTrayIcon(tray trayIconSetter, png []byte) {
	if icon.Template {
		tray.SetTemplateIcon(png)
	} else {
		tray.SetIcon(png)
	}
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

	t.status.SetLabel(trayStatusLine(c, st))
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

// showProgress updates the status line from st, at most once per
// trayLineInterval.
func (t *trayMenu) showProgress(st app.Status) {
	t.line.Push(st)
}

// newTrayLine returns the throttle for the tray's status line.
func newTrayLine(show func(app.Status)) *throttle {
	return newThrottle(trayLineInterval, show)
}

// trayStatusLine is the tray's status line for st: the status title, or
// while syncing "Syncing 5 of 100 · about 3 min left" (without the time
// left while it is unknown).
func trayStatusLine(c *i18n.Catalog, st app.Status) string {
	p := st.Progress
	if st.Code != app.CodeSyncing || p == nil || p.Total <= 0 {
		return c.T("status."+string(st.Code)+".title", st.Params)
	}
	params := map[string]string{
		"current": strconv.Itoa(p.Current),
		"total":   strconv.Itoa(p.Total),
	}
	if p.ETASeconds <= 0 {
		return c.T("tray.progress_no_eta", params)
	}
	params["eta"] = formatETA(c, p.ETASeconds)
	return c.T("tray.progress", params)
}

// formatETA formats a time left in whole minutes, rounded up: "3 min",
// "2 h 14 min".
func formatETA(c *i18n.Catalog, seconds int) string {
	minutes := (seconds + 59) / 60
	if minutes < 60 {
		return c.T("tray.eta_minutes", map[string]string{"minutes": strconv.Itoa(minutes)})
	}
	return c.T("tray.eta_hours", map[string]string{
		"hours":   strconv.Itoa(minutes / 60),
		"minutes": strconv.Itoa(minutes % 60),
	})
}

// isRunning reports whether code is a phase StopSync ends: the sync itself
// and the auto-format phase after it (verifying, the countdown, erasing).
func isRunning(code app.Code) bool {
	switch code {
	case app.CodeScanning, app.CodeSyncing,
		app.CodeVerifying, app.CodeFormatPending, app.CodeFormatting:
		return true
	}
	return false
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
		setTrayIcon(t.tray, icon.SyncingPNG(frame))
		select {
		case <-ctx.Done():
			setTrayIcon(t.tray, icon.IdlePNG())
			return
		case <-ticker.C:
		}
	}
}
