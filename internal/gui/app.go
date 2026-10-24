// Package gui runs the Wails application: the tray icon and menu, the main
// window (with a Dock icon only while it is shown, macOS only; hidden at
// startup when launchd starts a set-up app at login, see
// showWindowAtStartup) and the Service bound to the frontend.
package gui

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/dmitiigorshkov/sd-sync-immich/frontend"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/notify"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
)

const appName = "SD Sync Immich"

// Events sent to the window.
const (
	eventStatus   = "status"   // app.Status, throttled (see statusInterval)
	eventState    = "state"    // StateView, after settings, card or volume changes
	eventNavigate = "navigate" // section id to show, e.g. "settings"
)

// statusInterval limits "status" events to 4 per second; a code change is
// sent immediately.
const statusInterval = 250 * time.Millisecond

func init() {
	application.RegisterEvent[app.Status](eventStatus)
	application.RegisterEvent[StateView](eventState)
	application.RegisterEvent[string](eventNavigate)
}

// shell owns the Wails objects the Service and the tray act on.
type shell struct {
	app    *application.App
	window *application.WebviewWindow
	dock   *dock.DockService
	tray   *trayMenu

	// visible is true while the window is shown; onShow runs each time it
	// is shown.
	visible atomic.Bool
	onShow  func()

	// hasTray is decided once at startup (see trayAvailable, which is
	// platform-specific): whether there is a tray to reopen the window
	// from once it is hidden. It decides what closing the window does
	// (see decideWindowClose).
	hasTray bool

	// exitCode is the process exit code once the application has shut down.
	exitCode atomic.Int32
}

func (sh *shell) emit(name string, data any) {
	sh.app.Event.Emit(name, data)
}

// showWindow and hideWindow are platform-specific (shell_darwin.go,
// shell_other.go): on macOS they also show/hide the Dock icon; elsewhere
// there is no Dock to touch.

// windowCloseAction is what closing the window does.
type windowCloseAction int

const (
	// closeActionHide hides the window; it can be reopened from the tray.
	closeActionHide windowCloseAction = iota
	// closeActionMinimise minimises the window instead: without a tray
	// (GNOME without an AppIndicator extension) hiding it would leave no
	// way to reopen it, so it stays in the taskbar/dock while syncing
	// keeps running.
	closeActionMinimise
)

// decideWindowClose decides what closing the window does, given whether a
// tray is available (see trayAvailable).
func decideWindowClose(trayAvailable bool) windowCloseAction {
	if trayAvailable {
		return closeActionHide
	}
	return closeActionMinimise
}

// showWindowAtStartup reports whether the window opens when the app
// starts: always when the user opened the app (not launchd at login), and
// whenever setup is unfinished, as the window is where it is finished.
// Started at login and set up, the app stays in the menu bar.
func showWindowAtStartup(code app.Code, startedByLaunchd bool) bool {
	return !startedByLaunchd || code == app.CodeNotConfigured || code == app.CodeNoCardSelected
}

// quit shuts the application down and exits the process with code.
func (sh *shell) quit(code int) {
	sh.exitCode.Store(int32(code))
	sh.app.Quit()
}

// Run starts the GUI and the sync engine and blocks until the application
// quits. On macOS quitting ends the process from inside Run (with the exit
// code set by the caller of quit: 0 for "Quit" and SIGINT/SIGTERM), so Run
// only returns if the application fails to start.
func Run(a *app.App, cfg *config.Store, st *state.DB, version string) error {
	assets, err := fs.Sub(frontend.Dist, "dist")
	if err != nil {
		return fmt.Errorf("frontend assets: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sh := &shell{dock: dock.New(), hasTray: trayAvailable()}
	// The service's responses reach svc, which is created after it.
	var svc *Service
	notes := notify.NewCenter(notifications.New(), func(action string, data map[string]string) {
		svc.onNotificationResponse(action, data)
	})
	svc = newService(a, cfg, st, version, sh, notes)

	sh.app = application.New(application.Options{
		Name:        appName,
		Description: "Uploads photos and videos from camera SD cards to Immich",
		Services: []application.Service{
			application.NewService(svc),
			application.NewService(sh.dock),
			// Starts native notifications (asking for permission); without
			// them notifications fall back to osascript.
			notes.Service(),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			ActivationPolicy: application.ActivationPolicyAccessory,
		},
		// Wails' own messages go to app.log; below Warn they are noise
		// (e.g. every asset request).
		Logger: slog.New(slog.NewTextHandler(logx.Writer(), &slog.HandlerOptions{Level: slog.LevelWarn})),
		// Runs on the main thread: it must not wait for anything that may
		// itself need the main thread (menu and tray updates do).
		OnShutdown: func() {
			logx.Info("gui shutting down")
			cancel()
		},
		PostShutdown: func() {
			os.Exit(int(sh.exitCode.Load()))
		},
	})

	sh.window = sh.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      "main",
		Title:     appName,
		Width:     960,
		Height:    620,
		MinWidth:  780,
		MinHeight: 540,
		Hidden:    true,
		Mac: application.MacWindow{
			TitleBar:                application.MacTitleBarHiddenInset,
			InvisibleTitleBarHeight: 50,
		},
		BackgroundColour: application.NewRGB(15, 14, 20),
		URL:              "/",
	})
	// Closing the window hides it when there is a tray to reopen it from
	// (together with the Dock icon on macOS); without one (GNOME without
	// an AppIndicator extension) it is minimised instead, so it stays
	// reachable and syncing keeps running. See decideWindowClose.
	sh.window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		e.Cancel()
		switch decideWindowClose(sh.hasTray) {
		case closeActionHide:
			sh.hideWindow()
		case closeActionMinimise:
			sh.window.Minimise()
		}
	})
	// Clicking the Dock icon (shown while the window is open) shows it.
	sh.app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) {
		sh.showWindow()
	})

	sh.tray = newTray(sh.app, svc, sh)
	sh.onShow = func() { svc.windowShown(a.Status()) }

	sh.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		logx.Info("gui started")
		sh.tray.refresh()
		if code := a.Status().Code; showWindowAtStartup(code, underLaunchd()) {
			logx.Info("gui: showing the window at startup", "status", code, "launchd", underLaunchd())
			sh.showWindow()
		}
		svc.start(ctx)
	})

	return sh.app.Run()
}
