package gui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/bundle"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/desktop"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/i18n"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/install"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/update"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/version"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

const (
	// maxHistory and maxLogLines bound what the window can request at once.
	maxHistory  = 500
	maxLogLines = 5000
	// logTailBytes is how much of the end of app.log LogTail reads.
	logTailBytes = 1 << 20

	updateCheckInterval  = 12 * time.Hour
	updateCheckTimeout   = 2 * time.Minute
	updateInstallTimeout = 10 * time.Minute
)

// Service is bound to the frontend: its exported methods are what the
// window calls (see the generated TypeScript bindings).
type Service struct {
	app     *app.App
	cfg     *config.Store
	st      *state.DB
	version string
	updater *update.Client
	shell   *shell

	// executable is this program; bundlePath is the app bundle it runs
	// from, "" when it runs outside one (a development build).
	executable string
	bundlePath string

	systemLanguages []string
	status          *throttle
	notifier        *notifier

	mu       sync.Mutex
	cat      *i18n.Catalog
	release  *update.Release
	lastCode app.Code

	checkMu   sync.Mutex
	installMu sync.Mutex
}

func newService(a *app.App, cfg *config.Store, st *state.DB, ver string, sh *shell, out sender) *Service {
	s := &Service{
		app:             a,
		cfg:             cfg,
		st:              st,
		version:         ver,
		updater:         update.NewClient(ver),
		shell:           sh,
		systemLanguages: i18n.SystemLanguages(),
		lastCode:        a.Status().Code,
		notifier:        newNotifier(out),
	}
	if exe, err := currentExecutable(); err != nil {
		logx.Warn("gui: cannot resolve executable", "err", err)
	} else {
		s.executable = exe
		s.bundlePath, _ = bundle.Containing(exe)
	}
	s.status = newThrottle(statusInterval, func(st app.Status) { sh.emit(eventStatus, st) })
	s.cat = s.loadCatalog(cfg.Get().UI.Language)
	return s
}

// loadCatalog loads the catalog for a language preference ("" = system),
// falling back to English.
func (s *Service) loadCatalog(preference string) *i18n.Catalog {
	lang := i18n.Resolve(preference, s.systemLanguages)
	c, err := i18n.Load(lang)
	if err == nil {
		return c
	}
	logx.Warn("failed to load locale, using English", "lang", lang, "err", err)
	c, err = i18n.Load("en")
	if err != nil {
		// The English catalog is embedded; failing to load it is a build bug.
		panic(fmt.Sprintf("load English locale: %v", err))
	}
	return c
}

func (s *Service) catalog() *i18n.Catalog {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cat
}

func (s *Service) availableUpdate() *update.Release {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.release
}

// start begins pushing app state to the window and tray and runs the
// background loops until ctx is cancelled. It is called once the Wails
// application has started.
func (s *Service) start(ctx context.Context) {
	s.app.OnStatus(s.onStatus)
	s.app.OnNotice(s.onNotice)
	go s.app.Run(ctx)
	go s.watchVolumes(ctx)
	go s.runUpdateChecks(ctx)
}

// onStatus receives every status change from the App.
func (s *Service) onStatus(st app.Status) {
	s.mu.Lock()
	prev := s.lastCode
	s.lastCode = st.Code
	s.mu.Unlock()
	changed := prev != st.Code

	s.forwardStatus(st, changed)
	s.shell.tray.showProgress(st)
	if !changed {
		return
	}
	s.shell.tray.refresh()
	// Only a code change can be a new notification (a countdown's seconds
	// are not); the notifier's gate keeps a card from repeating one.
	s.notifier.status(st, s.cfg.Get(), s.catalog())
}

// forwardStatus sends st to the window: a code change always, progress
// ticks only while the window is visible (the hidden window would render
// them for nobody; windowShown catches it up).
func (s *Service) forwardStatus(st app.Status, codeChanged bool) {
	if !codeChanged && !s.shell.visible.Load() {
		return
	}
	s.status.Push(st)
}

// windowShown sends the latest status st to the window as soon as it is
// shown, as progress ticks were skipped while it was hidden.
func (s *Service) windowShown(st app.Status) {
	s.status.Now(st)
}

// watchVolumes pushes a fresh state whenever a volume is mounted or
// unmounted, so the card list and the selected card stay current.
func (s *Service) watchVolumes(ctx context.Context) {
	w, err := volume.NewWatcher()
	if err != nil {
		logx.Warn("gui: failed to watch volumes", "err", err)
		return
	}
	defer w.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-w.Events():
			if !ok {
				return
			}
			s.stateChanged()
		}
	}
}

// stateChanged pushes the current state to the window and refreshes the
// tray menu.
func (s *Service) stateChanged() {
	s.shell.emit(eventState, s.State())
	s.shell.tray.refresh()
}

// canSync reports whether "Sync now" can start a sync right now.
func (s *Service) canSync() bool {
	return s.cfg.IsConfigured() && s.app.MountedTarget() != nil
}

// State returns everything the window renders.
func (s *Service) State() StateView {
	cfg := s.cfg.Get()
	s.mu.Lock()
	lang := s.cat.Lang()
	rel := s.release
	s.mu.Unlock()
	return StateView{
		Status:   s.app.Status(),
		Language: lang,
		Server: ServerView{
			URL:    cfg.Immich.URL,
			HasKey: strings.TrimSpace(cfg.Immich.APIKey) != "",
		},
		Card:     s.selectedCard(cfg),
		Settings: s.autostartSettings(settingsView(cfg)),
		Version:  s.version,
		Update:   updateView(rel),
	}
}

// selectedCard describes the selected card, mounted or not. It is empty
// when no card is selected.
func (s *Service) selectedCard(cfg config.Config) CardView {
	uuid := cfg.Watch.VolumeUUID
	if uuid == "" {
		return CardView{}
	}
	mounted, err := volume.FindByUUID(uuid)
	if err != nil {
		logx.Warn("gui: failed to look up selected card", "err", err)
		mounted = s.app.MountedTarget()
	}
	if mounted != nil {
		return mountedCard(*mounted, uuid)
	}
	return CardView{UUID: uuid, Name: cfg.Watch.VolumeName, Selected: true}
}

func mountedCard(v volume.Info, selectedUUID string) CardView {
	return CardView{
		UUID:       v.UUID,
		Name:       v.Name,
		MountPoint: v.MountPoint,
		Mounted:    true,
		Selected:   selectedUUID != "" && strings.EqualFold(v.UUID, selectedUUID),
		SizeBytes:  volumeSize(v.MountPoint),
	}
}

// Cards lists the selected card first (even when it is not mounted), then
// the mounted removable volumes, then the cards the app remembers that are
// not mounted.
func (s *Service) Cards() ([]CardView, error) {
	vols, err := volume.ListRemovable()
	if err != nil {
		return nil, fmt.Errorf("list removable volumes: %w", err)
	}
	remembered, err := s.st.RememberedCards()
	if err != nil {
		return nil, fmt.Errorf("list remembered cards: %w", err)
	}
	cfg := s.cfg.Get()
	mounted := make([]CardView, 0, len(vols))
	for _, v := range vols {
		mounted = append(mounted, mountedCard(v, cfg.Watch.VolumeUUID))
	}
	return listCards(mounted, remembered, cfg.Watch.VolumeUUID, cfg.Watch.VolumeName), nil
}

// listCards merges the mounted cards, the remembered ones and the selected
// card (selectedUUID, "" for none) into the order Cards returns.
func listCards(mounted []CardView, remembered []state.RememberedCard, selectedUUID, selectedName string) []CardView {
	cards := make([]CardView, 0, len(mounted)+len(remembered)+1)
	listed := func(uuid string) bool {
		for _, c := range cards {
			if strings.EqualFold(c.UUID, uuid) {
				return true
			}
		}
		return false
	}
	remember := func(c *CardView) {
		for _, r := range remembered {
			if strings.EqualFold(r.UUID, c.UUID) {
				c.Remembered = true
				c.Uploaded = r.Uploaded
				if c.Name == "" {
					c.Name = r.Name
				}
				return
			}
		}
	}

	if selectedUUID != "" {
		sel := CardView{UUID: selectedUUID, Name: selectedName, Selected: true}
		for _, c := range mounted {
			if strings.EqualFold(c.UUID, selectedUUID) {
				sel = c
				sel.Selected = true
			}
		}
		remember(&sel)
		cards = append(cards, sel)
	}
	for _, c := range mounted {
		if listed(c.UUID) {
			continue
		}
		c.Selected = false
		remember(&c)
		cards = append(cards, c)
	}
	for _, r := range remembered {
		if listed(r.UUID) {
			continue
		}
		cards = append(cards, CardView{UUID: r.UUID, Name: r.Name, Remembered: true, Uploaded: r.Uploaded})
	}
	return cards
}

// ForgetCard deletes what the app remembers about the card with uuid (see
// app.App.ForgetCard) and deselects it if it is the selected card.
func (s *Service) ForgetCard(uuid string) error {
	if err := s.app.ForgetCard(uuid); err != nil {
		return err
	}
	s.stateChanged()
	return nil
}

// SelectCard makes the mounted card with uuid the one to sync.
func (s *Service) SelectCard(uuid string) error {
	vols, err := volume.ListRemovable()
	if err != nil {
		return fmt.Errorf("list removable volumes: %w", err)
	}
	for _, v := range vols {
		if strings.EqualFold(v.UUID, uuid) {
			if err := s.app.SelectCard(v); err != nil {
				return err
			}
			s.stateChanged()
			return nil
		}
	}
	return fmt.Errorf("card %s is not mounted", uuid)
}

// EjectCard ejects the selected card; the unmount pushes a new state.
func (s *Service) EjectCard() error {
	return s.app.EjectCard()
}

// TestServer checks url and apiKey without saving them. An empty apiKey
// means the saved key, which is only sent to the saved server's host:
// another host gets checkKeyRequired.
func (s *Service) TestServer(url, apiKey string) ServerCheckView {
	u, key, err := normalizeServer(url, apiKey)
	if err != nil {
		return ServerCheckView{Code: checkInvalidURL, Params: map[string]string{}}
	}
	saved := s.cfg.Get().Immich
	key, ok := keyForServer(u, key, saved.URL, saved.APIKey)
	if !ok {
		return ServerCheckView{Code: checkKeyRequired, Params: map[string]string{}}
	}
	return checkServer(context.Background(), u, key)
}

// SaveServer saves the server URL and, unless apiKey is empty, the key.
// Moving to another host needs its key: the saved one is not kept for it
// (the error is checkKeyRequired). If saving completes the setup while the
// selected card is mounted, a sync starts right away.
func (s *Service) SaveServer(url, apiKey string) error {
	u, key, err := normalizeServer(url, apiKey)
	if err != nil {
		return err
	}
	saved := s.cfg.Get().Immich
	if _, ok := keyForServer(u, key, saved.URL, saved.APIKey); !ok {
		return &codeError{code: checkKeyRequired, err: errors.New("the saved API key is only used for " + hostOf(saved.URL))}
	}
	if err := s.cfg.Update(func(c *config.Config) {
		c.Immich.URL = u
		if key != "" {
			c.Immich.APIKey = key
		}
	}); err != nil {
		return fmt.Errorf("save server settings: %w", err)
	}
	logx.Info("server settings saved", "url", u, "key_changed", key != "")
	s.app.ConfigChanged()
	if s.canSync() {
		s.app.SyncNow()
	}
	s.stateChanged()
	return nil
}

// settingsConcurrency is the parallel uploads setting shown in Settings:
// 0 for automatic, else the fixed count (within the slider's range).
func settingsConcurrency(cfg config.Config) int {
	if cfg.AutoConcurrency() {
		return 0
	}
	return min(cfg.Upload.Concurrency, config.MaxConcurrency)
}

// validConcurrency reports whether n can be saved: 0 (automatic) or a fixed
// count up to config.MaxConcurrency.
func validConcurrency(n int) bool {
	return n >= 0 && n <= config.MaxConcurrency
}

// settingsView is the editable settings of cfg, without the autostart
// fields (see autostartSettings), and whether formatting needs
// administrator rights here.
func settingsView(cfg config.Config) SettingsView {
	n := cfg.Notifications
	return SettingsView{
		Language:    cfg.UI.Language,
		Concurrency: settingsConcurrency(cfg),
		AutoPaths:   cfg.Scan.AutoPaths,
		AutoEject:   cfg.Card.AutoEject,
		AutoFormat:  cfg.Card.AutoFormat,

		AutoFormatNeedsAdmin: eraseNeedsAdmin(),
		Notifications: NotificationsView{
			Enabled:   n.Enabled,
			Done:      n.Done,
			Errors:    n.Errors,
			OtherCard: n.OtherCard,
			Server:    n.Server,
			Card:      n.Card,
		},
	}
}

// applySettings copies the saved fields of v into c (the autostart fields
// are not saved here, see SetAutostart). v is validated by SaveSettings.
func applySettings(c *config.Config, v SettingsView) {
	c.UI.Language = v.Language
	c.Upload.Concurrency = v.Concurrency
	c.Scan.AutoPaths = v.AutoPaths
	c.Card = config.CardConfig{AutoEject: v.AutoEject, AutoFormat: v.AutoFormat}
	n := v.Notifications
	c.Notifications = config.NotifyConfig{
		Enabled:   n.Enabled,
		Done:      n.Done,
		Errors:    n.Errors,
		OtherCard: n.OtherCard,
		Server:    n.Server,
		Card:      n.Card,
	}
}

// SaveSettings saves the language, upload concurrency (0: automatic),
// automatic path discovery, the card settings and the notification
// switches.
func (s *Service) SaveSettings(v SettingsView) error {
	if v.Language != "" && !slices.Contains(i18n.Supported, v.Language) {
		return fmt.Errorf("unsupported language %q", v.Language)
	}
	if !validConcurrency(v.Concurrency) {
		return fmt.Errorf("concurrency must be 0 (auto) or between 1 and %d", config.MaxConcurrency)
	}
	prev := s.cfg.Get()
	if err := s.cfg.Update(func(c *config.Config) { applySettings(c, v) }); err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	n := v.Notifications
	logx.Info("settings saved", "language", v.Language, "concurrency", v.Concurrency, "auto_paths", v.AutoPaths,
		"auto_eject", v.AutoEject, "auto_format", v.AutoFormat,
		"notifications", n.Enabled, "notify_done", n.Done, "notify_errors", n.Errors,
		"notify_other_card", n.OtherCard, "notify_server", n.Server, "notify_card", n.Card)
	if prev.UI.Language != v.Language {
		c := s.loadCatalog(v.Language)
		s.mu.Lock()
		s.cat = c
		s.mu.Unlock()
	}
	if prev.Scan.AutoPaths != v.AutoPaths {
		s.app.ConfigChanged()
	}
	s.stateChanged()
	return nil
}

// autostartSettings fills in the autostart fields of v. Whether the app
// can start at login is install.AutostartAvailable: on macOS it runs from
// its bundle that is not translocated (whose path does not survive a
// restart), on Windows always, on Linux from an AppImage.
func (s *Service) autostartSettings(v SettingsView) SettingsView {
	v.AutostartTranslocated = s.autostartTranslocated()
	v.AutostartAvailable = autostartAvailable(s.executable)
	v.Autostart = v.AutostartAvailable && autostartEnabled(s.executable)
	return v
}

// autostartTranslocated reports whether the app runs from a translocated
// bundle (macOS), which can start at login once moved to Applications.
func (s *Service) autostartTranslocated() bool {
	return s.bundlePath != "" && install.Translocated(s.executable)
}

// errAutostartTranslocated is the message of the error SetAutostart
// returns for a translocated app; the window shows
// "settings.autostart_translocated" for it.
const errAutostartTranslocated = "autostart_translocated"

// codeError is an error whose message is a code the window translates.
type codeError struct {
	code string
	err  error
}

func (e *codeError) Error() string { return e.code }
func (e *codeError) Unwrap() error { return e.err }

// SetAutostart turns starting the app at login on or off. It needs
// SettingsView.AutostartAvailable; a translocated app is refused with
// errAutostartTranslocated.
func (s *Service) SetAutostart(enabled bool) error {
	if !autostartAvailable(s.executable) && !s.autostartTranslocated() {
		return errors.New("starting at login is not available for this copy of the app")
	}
	if err := setAutostart(enabled, s.executable); err != nil {
		logx.Error("failed to change autostart", "enabled", enabled, "err", err)
		if errors.Is(err, install.ErrTranslocated) {
			return &codeError{code: errAutostartTranslocated, err: err}
		}
		return fmt.Errorf("change autostart: %w", err)
	}
	logx.Info("autostart changed", "enabled", enabled)
	s.stateChanged()
	return nil
}

func (s *Service) SyncNow()  { s.app.SyncNow() }
func (s *Service) StopSync() { s.app.StopSync() }
func (s *Service) Retry()    { s.app.Retry() }

// CancelFormat cancels the countdown before an automatic format.
func (s *Service) CancelFormat() { s.app.CancelFormat() }

// errRetryUnavailable is the message of the error RetryFailures returns
// when the failed files can't be retried now (the run's card isn't the
// selected, mounted card, or a sync is running); the window shows
// "history.retry_unavailable" for it.
const errRetryUnavailable = "retry_unavailable"

// retryError maps an error of app.RetryFailures to what the window gets.
func retryError(err error) error {
	if errors.Is(err, app.ErrRetryUnavailable) {
		return &codeError{code: errRetryUnavailable, err: err}
	}
	return err
}

// RetryFailures syncs again the files that failed in run runID.
func (s *Service) RetryFailures(runID int64) error {
	err := s.app.RetryFailures(runID)
	if err != nil && !errors.Is(err, app.ErrRetryUnavailable) {
		logx.Error("failed to retry failed files", "run", runID, "err", err)
	}
	return retryError(err)
}

// RunFailures returns the files that failed in run runID (none for a run
// whose failures were never recorded, see state.SyncRun.FailuresRecorded).
func (s *Service) RunFailures(runID int64) ([]state.FileFailure, error) {
	return s.st.RunFailures(runID)
}

// History returns up to limit most recent sync runs, newest first.
func (s *Service) History(limit int) ([]state.SyncRun, error) {
	return s.st.RecentRuns(min(max(limit, 1), maxHistory))
}

// LogTail returns the last lines of app.log.
func (s *Service) LogTail(lines int) (string, error) {
	return tailLines(logx.LogPath(), min(max(lines, 1), maxLogLines))
}

// tailLines returns the last n lines of the file at path, reading at most
// logTailBytes from its end.
func tailLines(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	offset := max(info.Size()-logTailBytes, 0)
	data, err := io.ReadAll(io.NewSectionReader(f, offset, info.Size()-offset))
	if err != nil {
		return "", err
	}
	if offset > 0 {
		// Drop the partial first line.
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	data = bytes.TrimRight(data, "\n")
	if len(data) == 0 {
		return "", nil
	}
	lines := bytes.Split(data, []byte{'\n'})
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return string(bytes.Join(lines, []byte{'\n'})), nil
}

// RevealLog shows app.log in the file manager.
func (s *Service) RevealLog() error {
	path := logx.LogPath()
	if _, err := os.Stat(path); err != nil {
		return err
	}
	return desktop.Reveal(path)
}

// OpenImmich opens the configured Immich server in the browser.
func (s *Service) OpenImmich() error {
	u, _, err := normalizeServer(s.cfg.Get().Immich.URL, "")
	if err != nil {
		return err
	}
	return desktop.OpenURL(u)
}

// CheckForUpdates checks GitHub for a newer release. It returns nil when
// the running version is the latest.
func (s *Service) CheckForUpdates() (*UpdateView, error) {
	if !version.IsRelease() {
		return nil, errors.New("update checks are disabled for development builds")
	}
	rel, err := s.checkForUpdates(context.Background())
	if err != nil {
		return nil, err
	}
	return updateView(rel), nil
}

// runUpdateChecks checks for updates now and every updateCheckInterval
// until ctx is cancelled. Development builds are never checked.
func (s *Service) runUpdateChecks(ctx context.Context) {
	if !version.IsRelease() {
		logx.Debug("automatic update checks disabled for development build", "version", version.Current)
		return
	}
	ticker := time.NewTicker(updateCheckInterval)
	defer ticker.Stop()
	for {
		if _, err := s.checkForUpdates(ctx); err != nil && ctx.Err() == nil {
			logx.Warn("automatic update check failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) checkForUpdates(ctx context.Context) (*update.Release, error) {
	s.checkMu.Lock()
	defer s.checkMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, updateCheckTimeout)
	defer cancel()
	rel, err := s.updater.Check(ctx)
	if err != nil {
		return nil, fmt.Errorf("check for updates: %w", err)
	}
	if rel != nil {
		logx.Info("update available", "current", s.version, "latest", rel.Version)
	}

	s.mu.Lock()
	changed := (rel == nil) != (s.release == nil) || (rel != nil && rel.Version != s.release.Version)
	s.release = rel
	s.mu.Unlock()
	if changed {
		s.stateChanged()
	}
	return rel, nil
}

// errUpdateManual is the message of the error InstallUpdate returns when
// this copy of the app cannot update itself (update.ErrManualUpdate); the
// window shows "settings.update_manual" for it.
const errUpdateManual = "update_manual"

// InstallUpdate installs the available update (update.Apply), then quits
// with updateExitCode so the new version starts: launchd starts it on
// macOS (or the bundle is reopened), the installer on Windows, and the
// new AppImage on Linux once this process has exited. On an error the app
// keeps running.
func (s *Service) InstallUpdate() error {
	if !s.installMu.TryLock() {
		return errors.New("an update is already being installed")
	}
	defer s.installMu.Unlock()

	rel := s.availableUpdate()
	if rel == nil {
		return errors.New("no update available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateInstallTimeout)
	defer cancel()
	quit, err := applyUpdate(ctx, s.updater, rel)
	if err != nil {
		logx.Error("update installation failed", "version", rel.Version, "err", err)
		if errors.Is(err, update.ErrManualUpdate) {
			return &codeError{code: errUpdateManual, err: err}
		}
		return fmt.Errorf("install update %s: %w", rel.Version, err)
	}
	if quit {
		logx.Info("update installed, restarting", "version", rel.Version)
		quitAfterUpdate(s.shell, updateExitCode)
	}
	return nil
}

// Seams for tests.
var (
	autostartAvailable = install.AutostartAvailable
	autostartEnabled   = install.AutostartEnabled
	setAutostart       = install.SetAutostart
	underLaunchd       = install.UnderLaunchd
	applyUpdate        = update.Apply
	quitAfterUpdate    = (*shell).quit
	eraseNeedsAdmin    = volume.EraseNeedsAdmin
)

// currentExecutable is the path of this program, with symlinks resolved.
func currentExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return executable, nil
}
