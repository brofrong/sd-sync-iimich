//go:build darwin

package tray

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/notify"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/settings"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/tray/icon"
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

	animCancel context.CancelFunc
}

func Run(application *app.App, cfg *config.Store) {
	c := &Controller{App: application, Config: cfg}
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
	quitItem := systray.AddMenuItem("Выход", "")
	go c.listen(quitItem, func() { systray.Quit() })

	c.refreshLabels()
	c.updateSyncEnabled()
}

func (c *Controller) onExit() {
	c.stopIconAnimation()
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
		c.statusItem.SetTitle(c.App.StatusText())
	}
	c.updateIconLocked(s.Kind)
	c.updateSyncEnabledLocked()
}

func (c *Controller) updateIconLocked(kind app.StatusKind) {
	switch kind {
	case app.StatusScanning, app.StatusSyncing:
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
