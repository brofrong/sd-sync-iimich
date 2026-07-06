package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/notify"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/uploader"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

type StatusKind int

const (
	StatusNotConfigured StatusKind = iota
	StatusWaiting
	StatusScanning
	StatusSyncing
	StatusDone
	StatusError
)

type Status struct {
	Kind     StatusKind
	Message  string
	Current  int
	Total    int
	Uploaded int
	ETA      time.Duration
}

type App struct {
	Config   *config.Store
	State    *state.DB
	Uploader *uploader.Service
	Watcher  *volume.Watcher

	mu            sync.RWMutex
	status        Status
	mountedTarget *volume.Info
	syncCancel    context.CancelFunc
	syncMu        sync.Mutex
	onStatus      func(Status)
	dryRun        bool
}

func New(cfg *config.Store, st *state.DB, dryRun bool) (*App, error) {
	w, err := volume.NewWatcher()
	if err != nil {
		return nil, err
	}
	a := &App{
		Config: cfg,
		State:  st,
		Uploader: &uploader.Service{
			Config: cfg,
			State:  st,
		},
		Watcher: w,
		dryRun:  dryRun,
	}
	a.setStatus(Status{Kind: StatusNotConfigured, Message: "Ожидание — настройте SD-карту"})
	logx.Info("app started", "configured", cfg.IsConfigured(), "dry_run", dryRun)
	return a, nil
}

func (a *App) SetStatusCallback(fn func(Status)) {
	a.onStatus = fn
}

func (a *App) Run(ctx context.Context) {
	logx.Info("app run loop started")
	go a.watchVolumes(ctx)
	a.refreshMountedTarget()
	a.updateIdleStatus()
	if a.Config.IsConfigured() && a.MountedTarget() != nil {
		go a.SyncNow()
	}

	<-ctx.Done()
	logx.Info("app shutting down")
	if a.Watcher != nil {
		a.Watcher.Close()
	}
}

func (a *App) watchVolumes(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-a.Watcher.Events():
			if !ok {
				return
			}
			a.handleVolumeEvent(ev)
		}
	}
}

func (a *App) handleVolumeEvent(ev volume.Event) {
	cfg := a.Config.Get()
	watchUUID := cfg.Watch.VolumeUUID
	if watchUUID == "" {
		a.updateIdleStatus()
		return
	}

	switch ev.Type {
	case volume.EventMounted:
		logx.Info("volume mounted", "name", ev.Info.Name, "uuid", ev.Info.UUID, "path", ev.Info.MountPoint)
		if strings.EqualFold(ev.Info.UUID, watchUUID) {
			a.mu.Lock()
			v := ev.Info
			a.mountedTarget = &v
			a.mu.Unlock()
			a.updateIdleStatus()
			if a.Config.IsConfigured() {
				logx.Info("target volume connected, starting sync", "name", ev.Info.Name)
				go a.SyncNow()
			}
		}
	case volume.EventUnmounted:
		logx.Info("volume unmounted", "name", ev.Info.Name, "path", ev.Info.MountPoint)
		a.mu.Lock()
		if a.mountedTarget != nil && a.mountedTarget.MountPoint == ev.Info.MountPoint {
			a.mountedTarget = nil
			a.CancelSync()
		}
		a.mu.Unlock()
		a.updateIdleStatus()
	}
}

func (a *App) refreshMountedTarget() {
	cfg := a.Config.Get()
	if cfg.Watch.VolumeUUID == "" {
		return
	}
	info, err := volume.FindByUUID(cfg.Watch.VolumeUUID)
	if err != nil || info == nil {
		return
	}
	a.mu.Lock()
	a.mountedTarget = info
	a.mu.Unlock()
}

func (a *App) ConfigChanged() {
	a.refreshMountedTarget()
	a.updateIdleStatus()
	logx.Debug("config changed", "configured", a.Config.IsConfigured())
}

func (a *App) MountedTarget() *volume.Info {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.mountedTarget == nil {
		return nil
	}
	v := *a.mountedTarget
	return &v
}

func (a *App) SyncNow() {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()

	if !a.Config.IsConfigured() {
		logx.Debug("sync skipped: not configured")
		return
	}
	vol := a.MountedTarget()
	if vol == nil {
		logx.Debug("sync skipped: volume not mounted")
		return
	}

	a.cancelSyncLocked()
	ctx, cancel := context.WithCancel(context.Background())
	a.syncCancel = cancel

	logx.Info("sync requested", "volume", vol.Name, "path", vol.MountPoint)

	go func() {
		syncStart := time.Now()
		a.setStatus(Status{Kind: StatusScanning, Message: "Сканирование..."})

		result, err := a.Uploader.Sync(ctx, *vol, a.dryRun, func(p uploader.Progress) {
			switch p.Phase {
			case "scanning":
				a.setStatus(Status{Kind: StatusScanning, Message: "Сканирование..."})
			case "syncing":
				a.setStatus(Status{
					Kind:    StatusSyncing,
					Message: "Синхронизация",
					Current: p.Current,
					Total:   p.Total,
					ETA:     p.ETA,
				})
			}
		})

		if err != nil {
			if ctx.Err() != nil {
				logx.Info("sync cancelled", "duration", time.Since(syncStart).Round(time.Second))
				a.updateIdleStatus()
				return
			}
			logx.Error("sync failed", "err", err, "duration", time.Since(syncStart).Round(time.Second))
			a.setStatus(Status{Kind: StatusError, Message: err.Error()})
			return
		}

		msg := fmt.Sprintf("Готово — %d загружено", result.Uploaded)
		if a.dryRun {
			msg = fmt.Sprintf("Dry-run — %d новых файлов", result.Skipped)
		}
		logx.Info("sync finished",
			"uploaded", result.Uploaded,
			"skipped", result.Skipped,
			"errors", result.Errors,
			"duration", time.Since(syncStart).Round(time.Second).String(),
		)
		a.setStatus(Status{Kind: StatusDone, Message: msg, Uploaded: result.Uploaded})
		if !a.dryRun && result.Uploaded > 0 {
			notify.SyncComplete(result.Uploaded)
		}
		time.AfterFunc(5*time.Second, func() { a.updateIdleStatus() })
	}()
}

func (a *App) CancelSync() {
	a.syncMu.Lock()
	defer a.syncMu.Unlock()
	a.cancelSyncLocked()
}

func (a *App) cancelSyncLocked() {
	if a.syncCancel != nil {
		a.syncCancel()
		a.syncCancel = nil
	}
}

func (a *App) updateIdleStatus() {
	if !a.Config.IsConfigured() {
		a.setStatus(Status{Kind: StatusNotConfigured, Message: "Ожидание — настройте SD-карту"})
		return
	}
	if a.MountedTarget() != nil {
		a.setStatus(Status{Kind: StatusWaiting, Message: "Ожидание"})
		return
	}
	a.setStatus(Status{Kind: StatusWaiting, Message: "Ожидание"})
}

func (a *App) setStatus(s Status) {
	a.mu.Lock()
	a.status = s
	a.mu.Unlock()
	if a.onStatus != nil {
		a.onStatus(s)
	}
}

func (a *App) Status() Status {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status
}

func (a *App) StatusText() string {
	s := a.Status()
	switch s.Kind {
	case StatusSyncing:
		return formatProgressText("Синхронизация", s)
	case StatusScanning:
		if s.Total > 0 && s.Message == "Подготовка" {
			return formatProgressText("Подготовка", s)
		}
		if s.Total > 0 {
			return formatProgressText("Сканирование", s)
		}
		return "Сканирование..."
	case StatusDone:
		return s.Message
	case StatusError:
		return "Ошибка: " + s.Message
	default:
		return s.Message
	}
}

func formatProgressText(label string, s Status) string {
	if s.Total <= 0 {
		return label + "..."
	}
	remaining := s.Total - s.Current
	if remaining < 0 {
		remaining = 0
	}
	text := fmt.Sprintf("%s %d/%d (осталось %d)", label, s.Current, s.Total, remaining)
	if eta := FormatETA(s.ETA); eta != "" {
		text += " " + eta
	}
	return text
}
