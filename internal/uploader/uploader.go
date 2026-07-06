package uploader

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

type Progress struct {
	Phase   string
	Current int
	Total   int
	ETA     time.Duration
}

type ProgressFunc func(Progress)

type Service struct {
	Config *config.Store
	State  *state.DB
}

type Result struct {
	Uploaded int
	Skipped  int
	Errors   int
}

func (s *Service) Sync(ctx context.Context, vol volume.Info, dryRun bool, onProgress ProgressFunc) (Result, error) {
	cfg := s.Config.Get()
	if !s.Config.IsConfigured() {
		return Result{}, fmt.Errorf("not configured")
	}

	workers := cfg.EffectiveConcurrency()

	if onProgress != nil {
		onProgress(Progress{Phase: "scanning"})
	}
	logx.Info("sync: scanning volume", "mount", vol.MountPoint, "uuid", vol.UUID)

	files, err := scanner.ScanVolume(vol.MountPoint, cfg)
	if err != nil {
		logx.Error("sync: scan failed", "err", err)
		return Result{}, err
	}

	candidates, skipped, err := s.filterNew(vol.UUID, files)
	if err != nil {
		logx.Error("sync: filter failed", "err", err)
		return Result{}, err
	}

	cursor, err := s.State.GetVolumeSync(vol.UUID)
	if err != nil {
		logx.Error("sync: volume cursor failed", "err", err)
		return Result{}, err
	}
	if !cursor.Mtime.IsZero() {
		logx.Info("sync: last synced file",
			"mtime", cursor.Mtime.Format(time.RFC3339),
			"path", cursor.RelativePath,
		)
	}

	sortFilesByDate(candidates)

	logx.Info("sync: scan complete",
		"total", len(files),
		"candidates", len(candidates),
		"skipped_local", skipped,
		"workers", workers,
	)

	if len(candidates) == 0 {
		logx.Info("sync: nothing new to upload")
		return Result{Skipped: len(files)}, nil
	}

	if dryRun {
		logx.Info("sync: dry-run mode, skipping upload")
		return Result{Skipped: len(files)}, nil
	}

	client := immich.NewClient(cfg.Immich.URL, cfg.Immich.APIKey)
	prog := newSyncProgress(len(candidates), onProgress)
	prog.reportStart()

	logx.Info("sync: upload started", "candidates", len(candidates))
	session := newSyncSession(s.State, vol.UUID, candidates)
	checkpoint := newSyncCheckpoint(s.State, session, cfg.EffectiveCheckpointInterval())
	return s.uploadAll(ctx, vol.UUID, client, workers, len(files), skipped, candidates, prog, session, checkpoint)
}

func (s *Service) uploadAll(
	ctx context.Context,
	volumeUUID string,
	client *immich.Client,
	workers, totalFiles, alreadySkipped int,
	candidates []scanner.File,
	prog *syncProgress,
	session *syncSession,
	checkpoint *syncCheckpoint,
) (Result, error) {
	jobs := make(chan scanner.File, workers)
	var wg sync.WaitGroup
	var uploaded atomic.Int64
	var errors atomic.Int64
	uploadStart := time.Now()

	defer checkpoint.flush()

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				if ctx.Err() != nil {
					return
				}
				s.syncOne(ctx, volumeUUID, client, f, prog, session, checkpoint, &uploaded, &errors)
			}
		}()
	}

feed:
	for _, f := range candidates {
		select {
		case jobs <- f:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()

	upl := int(uploaded.Load())
	errCount := int(errors.Load())
	skipped := alreadySkipped + len(candidates) - upl - errCount

	if ctx.Err() != nil {
		logx.Info("sync: interrupted, progress saved",
			"uploaded", upl,
			"errors", errCount,
			"duration", time.Since(uploadStart).Round(time.Second),
		)
		return Result{Uploaded: upl, Skipped: skipped, Errors: errCount}, ctx.Err()
	}

	logx.Info("sync: complete",
		"uploaded", upl,
		"skipped", skipped,
		"errors", errCount,
		"upload_duration", time.Since(uploadStart).Round(time.Second).String(),
	)
	return Result{Uploaded: upl, Skipped: skipped, Errors: errCount}, nil
}

func (s *Service) syncOne(
	ctx context.Context,
	volumeUUID string,
	client *immich.Client,
	f scanner.File,
	prog *syncProgress,
	session *syncSession,
	checkpoint *syncCheckpoint,
	uploaded, errors *atomic.Int64,
) {
	defer prog.done()

	sha1hex, err := s.fileSHA1(volumeUUID, f)
	if err != nil {
		logx.Warn("sync: sha1 failed", "file", f.RelativePath, "err", err)
		errors.Add(1)
		return
	}

	resp, err := client.Upload(ctx, f.AbsolutePath, sha1hex, f.Mtime, f.SidecarPath)
	if err != nil {
		logx.Error("sync: upload failed", "file", f.RelativePath, "err", err)
		errors.Add(1)
		return
	}

	if err := session.recordSuccess(f); err != nil {
		logx.Error("sync: failed to save progress", "file", f.RelativePath, "err", err)
		errors.Add(1)
		return
	}

	checkpoint.onFileSaved()
	uploaded.Add(1)
	if resp != nil && resp.Status == "duplicate" {
		logx.Debug("sync: already on server", "file", f.RelativePath)
	}

	cur := int(prog.doneCount.Load())
	if cur%50 == 0 {
		logx.Info("sync: progress",
			"done", cur,
			"total", prog.total,
			"uploaded", uploaded.Load(),
			"errors", errors.Load(),
		)
	}
}

func (s *Service) fileSHA1(volumeUUID string, f scanner.File) (string, error) {
	rec, err := s.State.Get(volumeUUID, f.RelativePath)
	if err != nil {
		return "", err
	}
	if rec != nil && rec.SHA1 != "" && rec.Size == f.Size && rec.Mtime.Equal(f.Mtime) {
		return rec.SHA1, nil
	}

	sha1hex, err := scanner.ComputeSHA1(f.AbsolutePath)
	if err != nil {
		return "", err
	}

	if err := s.State.MarkHashed(volumeUUID, f.RelativePath, sha1hex, f.Size, f.Mtime); err != nil {
		logx.Warn("sync: hash state save failed", "file", f.RelativePath, "err", err)
	}

	return sha1hex, nil
}

func (s *Service) filterNew(volumeUUID string, files []scanner.File) ([]scanner.File, int, error) {
	cursor, err := s.State.GetVolumeSync(volumeUUID)
	if err != nil {
		return nil, 0, err
	}

	candidates := make([]scanner.File, 0, len(files))
	skipped := 0

	for _, f := range files {
		if !isAfterWatermark(f, cursor) {
			skipped++
			continue
		}
		candidates = append(candidates, f)
	}

	return candidates, skipped, nil
}

// sortFilesByDate orders files oldest-first (by mtime, then path).
func sortFilesByDate(files []scanner.File) {
	sort.Slice(files, func(i, j int) bool {
		a, b := files[i], files[j]
		if a.Mtime.Equal(b.Mtime) {
			return a.RelativePath < b.RelativePath
		}
		return a.Mtime.Before(b.Mtime)
	})
}
