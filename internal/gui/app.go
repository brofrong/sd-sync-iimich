// Package gui runs the Wails application: the tray icon and menu, the main
// window (with a Dock icon only while it is shown, macOS only; hidden at
// startup when a set-up app starts at login, see decideStartupWindow) and
// the Service bound to the frontend. Only one copy runs at a time (see
// singleInstanceOptions).
package gui

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/dock"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/dmitiigorshkov/sd-sync-immich/frontend"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/bundle"
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
// there is no Dock to touch. Both call showWindowNow to actually show it.

// showableWindow is the part of *application.WebviewWindow showWindowNow
// needs, factored out so it can be tested with a fake window instead of a
// real, running one.
type showableWindow interface {
	IsMinimised() bool
	UnMinimise()
	Show() application.Window
	Focus()
}

// showWindowNow un-minimises w first if it is minimised, then shows and
// focuses it. Show alone does not bring back a minimised window (Windows:
// SW_SHOW; Linux: gtk_widget_show_all neither un-iconifies), so without
// this a tray/taskbar click on a window minimised by the user, or by our
// own no-tray close behaviour (decideWindowClose), would leave it
// minimised.
func showWindowNow(w showableWindow) {
	if w.IsMinimised() {
		w.UnMinimise()
	}
	w.Show()
	w.Focus()
}

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

// startupWindow is what happens to the window when the app starts.
type startupWindow int

const (
	// startupHidden keeps the window closed: the app is in the tray.
	startupHidden startupWindow = iota
	// startupShown opens the window.
	startupShown
	// startupMinimised opens the window minimised (taskbar or dock).
	startupMinimised
)

func (w startupWindow) String() string {
	return [...]string{"hidden", "shown", "minimised"}[w]
}

// decideStartupWindow decides what the window does when the app starts.
// It opens when the user opened the app (it was not started at login, see
// startedAtLogin), and whenever setup is unfinished, as the window is where
// it is finished. Started at login and set up, the app stays in the tray;
// without a tray (GNOME without AppIndicator) a hidden window could not be
// reopened, so it starts minimised instead.
func decideStartupWindow(code app.Code, atLogin, hasTray bool) startupWindow {
	switch {
	case !atLogin || code == app.CodeNotConfigured || code == app.CodeNoCardSelected:
		return startupShown
	case !hasTray:
		return startupMinimised
	}
	return startupHidden
}

// singleInstanceOptions makes a second copy of the app exit with code 0
// before it starts syncing (inside application.New), after asking the
// running copy to call show: the user opened the app again, so its window
// comes up. A second copy started by an autostart entry (--autostart)
// leaves the running one in the tray.
func singleInstanceOptions(show func()) *application.SingleInstanceOptions {
	return &application.SingleInstanceOptions{
		UniqueID: bundle.ID,
		ExitCode: 0,
		OnSecondInstanceLaunch: func(data application.SecondInstanceData) {
			logx.Info("gui: the app was opened again", "args", data.Args)
			if slices.Contains(data.Args, "--autostart") {
				return
			}
			show()
		},
	}
}

// showGate holds back the requests to show the window that arrive before
// the application has started. On Linux the single-instance lock is a
// D-Bus name taken inside application.New, so a second copy opened right
// then (a double double-click, or autostart and a click at login) asks to
// be shown on a D-Bus goroutine before Run: application.InvokeAsync would
// then panic (Wails sets up its main thread only in Run), killing this
// copy. Requests before start are remembered and the window is shown once
// the application has started; later ones are dispatched at once.
type showGate struct {
	mu      sync.Mutex
	started bool
	pending bool
	// dispatch shows the window from any goroutine; only called after
	// start.
	dispatch func()
}

// request asks for the window: dispatched now if the application has
// started, else remembered for start.
func (g *showGate) request() {
	g.mu.Lock()
	if !g.started {
		g.pending = true
		g.mu.Unlock()
		logx.Info("gui: the window will be shown once the app has started")
		return
	}
	g.mu.Unlock()
	g.dispatch()
}

// start records that the application has started (on its main thread) and
// reports whether the window was asked for before: the caller shows it.
func (g *showGate) start() (pending bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.started = true
	pending, g.pending = g.pending, false
	return pending
}

// quit shuts the application down and exits the process with code.
func (sh *shell) quit(code int) {
	sh.exitCode.Store(int32(code))
	sh.app.Quit()
}

// startedAtLogin reports whether the app was started at login: by the
// LaunchAgent on macOS, by an autostart entry ("run --autostart") on
// Windows and Linux.
func startedAtLogin(autostart bool) bool {
	return autostart || underLaunchd()
}

// Run starts the GUI and the sync engine and blocks until the application
// quits. autostart means an autostart entry started it at login. On macOS quitting ends the process from inside Run (with the exit
// code set by the caller of quit: 0 for "Quit" and SIGINT/SIGTERM), so Run
// only returns if the application fails to start.
func Run(a *app.App, cfg *config.Store, st *state.DB, version string, autostart bool) error {
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

	// The window is shown on the main thread, where Wails wants it; a
	// request before ApplicationStarted waits for it (see showGate).
	reopen := &showGate{dispatch: func() {
		application.InvokeAsync(func() {
			if sh.window != nil {
				sh.showWindow()
			}
		})
	}}

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
			Handler:    application.AssetFileServerFS(assets),
			Middleware: svc.gallery.Middleware,
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
		// A second copy exits here, before the sync engine starts. Its
		// request reaches this copy off the main thread, on Linux possibly
		// before Run.
		SingleInstance: singleInstanceOptions(reopen.request),
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
		atLogin := startedAtLogin(autostart)
		code := a.Status().Code
		switch w := decideStartupWindow(code, atLogin, sh.hasTray); w {
		case startupShown, startupMinimised:
			logx.Info("gui: opening the window at startup", "window", w, "status", code, "at_login", atLogin, "tray", sh.hasTray)
			sh.showWindow()
			if w == startupMinimised {
				sh.window.Minimise()
			}
		}
		// The app was opened again while it was starting: that wins over
		// starting hidden (or minimised) at login.
		if reopen.start() {
			logx.Info("gui: showing the window asked for during startup")
			sh.showWindow()
		}
		svc.start(ctx)
	})

	return sh.app.Run()
}
