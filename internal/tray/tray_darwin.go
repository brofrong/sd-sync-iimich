//go:build darwin

package tray

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/notify"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/settings"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/tray/icon"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/update"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/version"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
	"github.com/getlantern/systray"
)

type Controller struct {
	App    *app.App
	Config *config.Store

	mu            sync.Mutex
	statusItem    *systray.MenuItem
	syncItem      *systray.MenuItem
	urlLabel      *systray.MenuItem
	sdLabel       *systray.MenuItem
	uploadWorkers *systray.MenuItem
	autoPaths     *systray.MenuItem

	updater             *update.Client
	updateAvailableItem *systray.MenuItem
	checkUpdatesItem    *systray.MenuItem
	updateMu            sync.Mutex
	availableUpdate     *update.Release
	checkMu             sync.Mutex
	installMu           sync.Mutex

	animCancel   context.CancelFunc
	updateCancel context.CancelFunc
}

func Run(application *app.App, cfg *config.Store) {
	c := &Controller{
		App:     application,
		Config:  cfg,
		updater: update.NewClient(version.Current),
	}
	application.SetStatusCallback(func(s app.Status) {
		c.updateStatus(s)
	})
	systray.Run(c.onReady, c.onExit)
}

func (c *Controller) onReady() {
	notify.Init()
	setTrayIcon(icon.IdlePNG())
	systray.SetTitle("")
	systray.SetTooltip("SD Sync Immich")

	c.statusItem = systray.AddMenuItem("Ожидание — настройте SD-карту", "")
	c.statusItem.Disable()

	systray.AddSeparator()
	settingsMenu := systray.AddMenuItem("Настройки", "")

	urlMenu := settingsMenu.AddSubMenuItem("Immich URL", "")
	c.urlLabel = urlMenu
	changeURL := urlMenu.AddSubMenuItem("Изменить URL...", "")
	go c.listen(changeURL, c.onChangeURL)

	apiMenu := settingsMenu.AddSubMenuItem("API Key: ********", "")
	changeAPIKey := apiMenu.AddSubMenuItem("Изменить API Key...", "")
	go c.listen(changeAPIKey, c.onChangeAPIKey)

	sdMenu := settingsMenu.AddSubMenuItem("SD-карта", "")
	c.sdLabel = sdMenu
	sdNone := sdMenu.AddSubMenuItem("(не выбрана)", "")
	go c.listen(sdNone, c.onSelectNoCard)
	c.rebuildSDList(sdMenu)

	workersMenu := settingsMenu.AddSubMenuItem("Воркеры", "")
	cfg := c.Config.Get()

	uploadMenu := workersMenu.AddSubMenuItem(
		"Параллельность: "+cfg.WorkerLabel(cfg.Upload.Concurrency, func(c config.Config) int { return c.EffectiveConcurrency() }),
		"",
	)
	c.uploadWorkers = uploadMenu
	changeUpload := uploadMenu.AddSubMenuItem("Изменить...", "")
	go c.listen(changeUpload, c.onChangeUploadWorkers)

	c.autoPaths = settingsMenu.AddSubMenuItem(
		"Автопоиск медиа с камер",
		"Искать фото и видео в известных каталогах камер",
	)
	go c.listen(c.autoPaths, c.onToggleAutoPaths)

	systray.AddSeparator()
	c.syncItem = systray.AddMenuItem("Синхронизировать сейчас", "")
	c.syncItem.Disable()
	go c.listen(c.syncItem, func() { c.App.SyncNow() })

	systray.AddSeparator()
	openLogs := systray.AddMenuItem("Открыть логи", logx.LogPath())
	go c.listen(openLogs, func() {
		if err := logx.OpenLog(); err != nil {
			logx.Warn("failed to open log", "err", err)
		}
	})

	systray.AddSeparator()
	c.updateAvailableItem = systray.AddMenuItem("", "")
	c.updateAvailableItem.Hide()
	go c.listen(c.updateAvailableItem, func() {
		go c.installAvailableUpdate()
	})
	c.checkUpdatesItem = systray.AddMenuItem("Проверить обновления…", "")
	go c.listen(c.checkUpdatesItem, func() {
		go c.checkForUpdates(true)
	})

	systray.AddSeparator()
	quitItem := systray.AddMenuItem("Выход", "")
	go c.listen(quitItem, func() { systray.Quit() })

	c.refreshLabels()
	c.updateSyncEnabled()
	c.startAutomaticUpdateChecks()
}

func (c *Controller) onExit() {
	c.stopIconAnimation()
	if c.updateCancel != nil {
		c.updateCancel()
	}
	c.App.CancelSync()
}

func (c *Controller) listen(item *systray.MenuItem, fn func()) {
	for range item.ClickedCh {
		fn()
	}
}

func (c *Controller) updateStatus(s app.Status) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.statusItem != nil {
		// TODO(task 6): this menu is replaced by the Wails GUI, which
		// renders s.Code (+ s.Params) through the i18n catalog.
		c.statusItem.SetTitle(string(s.Code))
	}
	c.updateIconLocked(s.Code)
	c.updateSyncEnabledLocked()
}

func (c *Controller) updateIconLocked(code app.Code) {
	switch code {
	case app.CodeScanning, app.CodeSyncing:
		c.startIconAnimation()
	default:
		c.stopIconAnimationLocked()
		setTrayIcon(icon.IdlePNG())
	}
}

func setTrayIcon(png []byte) {
	systray.SetTemplateIcon(png, png)
}

func (c *Controller) startIconAnimation() {
	if c.animCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.animCancel = cancel
	go c.runIconAnimation(ctx)
}

func (c *Controller) stopIconAnimation() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopIconAnimationLocked()
}

func (c *Controller) stopIconAnimationLocked() {
	if c.animCancel != nil {
		c.animCancel()
		c.animCancel = nil
	}
}

func (c *Controller) runIconAnimation(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	frame := 0
	setTrayIcon(icon.SyncingPNG(frame))

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			frame = (frame + 1) % icon.FrameCount
			setTrayIcon(icon.SyncingPNG(frame))
		}
	}
}

func (c *Controller) refreshLabels() {
	cfg := c.Config.Get()
	if c.urlLabel != nil {
		url := cfg.Immich.URL
		if url == "" {
			url = "(не задан)"
		}
		c.urlLabel.SetTitle("Immich URL: " + truncate(url, 40))
	}
	if c.sdLabel != nil {
		name := cfg.Watch.VolumeName
		if name == "" {
			name = "(не выбрана)"
		}
		c.sdLabel.SetTitle("SD-карта: " + name)
	}
	if c.uploadWorkers != nil {
		c.uploadWorkers.SetTitle("Параллельность: " + cfg.WorkerLabel(cfg.Upload.Concurrency, func(c config.Config) int { return c.EffectiveConcurrency() }))
	}
	if c.autoPaths != nil {
		if cfg.Scan.AutoPaths {
			c.autoPaths.Check()
		} else {
			c.autoPaths.Uncheck()
		}
	}
}

func (c *Controller) rebuildSDList(sdMenu *systray.MenuItem) {
	volumes, err := volume.ListRemovable()
	if err != nil {
		return
	}
	cfg := c.Config.Get()
	for _, v := range volumes {
		vol := v
		label := fmt.Sprintf("%s (%s)", vol.Name, vol.MountPoint)
		if strings.EqualFold(vol.UUID, cfg.Watch.VolumeUUID) {
			label = "● " + label
		} else {
			label = "○ " + label
		}
		item := sdMenu.AddSubMenuItem(label, vol.UUID)
		go c.listen(item, func() { c.onSelectVolume(vol) })
	}
}

func (c *Controller) onChangeURL() {
	cfg := c.Config.Get()
	val, err := settings.PromptURL(cfg.Immich.URL)
	if err != nil || val == "" {
		return
	}
	_ = c.Config.Update(func(c *config.Config) {
		c.Immich.URL = strings.TrimRight(strings.TrimSpace(val), "/")
	})
	c.refreshLabels()
	c.App.ConfigChanged()
}

func (c *Controller) onChangeAPIKey() {
	val, err := settings.PromptAPIKey()
	if err != nil || val == "" {
		return
	}
	_ = c.Config.Update(func(c *config.Config) {
		c.Immich.APIKey = strings.TrimSpace(val)
	})
	c.App.ConfigChanged()
}

func (c *Controller) onSelectVolume(vol volume.Info) {
	_ = c.Config.Update(func(c *config.Config) {
		c.Watch.VolumeUUID = vol.UUID
		c.Watch.VolumeName = vol.Name
	})
	c.refreshLabels()
	c.App.ConfigChanged()
	if c.App.MountedTarget() != nil {
		c.App.SyncNow()
	}
}

func (c *Controller) onSelectNoCard() {
	_ = c.Config.Update(func(c *config.Config) {
		c.Watch.VolumeUUID = ""
		c.Watch.VolumeName = ""
	})
	c.refreshLabels()
	c.App.ConfigChanged()
}

func (c *Controller) onChangeUploadWorkers() {
	cfg := c.Config.Get()
	n, err := settings.PromptWorkers("параллельность синхронизации", cfg.Upload.Concurrency)
	if err != nil {
		return
	}
	_ = c.Config.Update(func(c *config.Config) {
		c.Upload.Concurrency = n
	})
	c.refreshLabels()
}

func (c *Controller) onToggleAutoPaths() {
	if err := c.Config.Update(func(cfg *config.Config) {
		cfg.Scan.AutoPaths = !cfg.Scan.AutoPaths
	}); err != nil {
		logx.Warn("failed to save automatic media discovery setting", "err", err)
		c.refreshLabels()
		return
	}
	c.refreshLabels()
	c.App.ConfigChanged()
}

func (c *Controller) startAutomaticUpdateChecks() {
	if !version.IsRelease() {
		logx.Debug("automatic update checks disabled for development build", "version", version.Current)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.updateCancel = cancel
	go func() {
		c.checkForUpdates(false)
		ticker := time.NewTicker(12 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.checkForUpdates(false)
			}
		}
	}()
}

func (c *Controller) checkForUpdates(manual bool) {
	if !version.IsRelease() {
		if manual {
			_ = settings.ShowMessage("Проверка обновлений недоступна для локальной dev-сборки.")
		}
		return
	}
	c.checkMu.Lock()
	defer c.checkMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	release, err := c.updater.Check(ctx)
	if err != nil {
		logx.Warn("update check failed", "err", err, "manual", manual)
		if manual {
			_ = settings.ShowMessage("Не удалось проверить обновления:\n" + err.Error())
		}
		return
	}
	if release == nil {
		if manual {
			_ = settings.ShowMessage("Установлена актуальная версия " + version.Current + ".")
		}
		return
	}

	c.updateMu.Lock()
	c.availableUpdate = release
	c.updateMu.Unlock()
	c.updateAvailableItem.SetTitle("Установить обновление " + release.Version + "…")
	c.updateAvailableItem.Show()
	logx.Info("update available", "current", version.Current, "latest", release.Version)

	if manual {
		c.installAvailableUpdate()
	}
}

func (c *Controller) installAvailableUpdate() {
	if !c.installMu.TryLock() {
		return
	}
	defer c.installMu.Unlock()
	c.updateAvailableItem.Disable()
	c.checkUpdatesItem.Disable()
	defer c.updateAvailableItem.Enable()
	defer c.checkUpdatesItem.Enable()

	c.updateMu.Lock()
	release := c.availableUpdate
	c.updateMu.Unlock()
	if release == nil {
		return
	}

	confirmed, err := settings.ConfirmUpdate(release.Version, release.Notes)
	if err != nil || !confirmed {
		if err != nil {
			logx.Warn("update confirmation failed", "err", err)
		}
		return
	}
	executable, err := os.Executable()
	if err != nil {
		logx.Error("failed to resolve executable for update", "err", err)
		_ = settings.ShowMessage("Не удалось определить путь приложения:\n" + err.Error())
		return
	}
	if resolved, resolveErr := filepath.EvalSymlinks(executable); resolveErr == nil {
		executable = resolved
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := c.updater.Install(ctx, release, executable); err != nil {
		logx.Error("update installation failed", "version", release.Version, "err", err)
		_ = settings.ShowMessage("Не удалось установить обновление:\n" + err.Error())
		return
	}
	logx.Info("update installed", "version", release.Version, "path", executable)
	_ = settings.ShowMessage("Обновление " + release.Version + " установлено. Приложение перезапустится.")
	systray.Quit()
}

func (c *Controller) updateSyncEnabled() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.updateSyncEnabledLocked()
}

func (c *Controller) updateSyncEnabledLocked() {
	if c.syncItem == nil {
		return
	}
	if c.Config.IsConfigured() && c.App.MountedTarget() != nil {
		c.syncItem.Enable()
	} else {
		c.syncItem.Disable()
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
