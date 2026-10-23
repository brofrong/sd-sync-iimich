package uploader

import (
	"context"
	"errors"
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
	Phase       string
	Current     int
	Total       int
	ETA         time.Duration
	File        string
	BytesDone   int64
	BytesTotal  int64
	BytesPerSec float64
	// How the files done so far ended (they add up to Current).
	Uploaded   int
	Duplicates int
	Errors     int
}

type ProgressFunc func(Progress)

type Service struct {
	Config *config.Store
	State  *state.DB

	// tuned is the worker count the last automatic sync settled on (0: none
	// yet); the next one starts there.
	tunedMu sync.Mutex
	tuned   int
}

// startWorkers is where an automatic sync starts.
func (s *Service) startWorkers() int {
	s.tunedMu.Lock()
	defer s.tunedMu.Unlock()
	if s.tuned > 0 {
		return s.tuned
	}
	return config.DefaultConcurrency
}

func (s *Service) rememberWorkers(n int) {
	s.tunedMu.Lock()
	defer s.tunedMu.Unlock()
	s.tuned = n
}

// Result counts the files of a sync: Uploaded were stored by Immich,
// Duplicates it already had, Skipped were not sent (synced before, or left
// over when the sync stopped). Bytes is the size of the uploaded ones.
type Result struct {
	Uploaded   int
	Duplicates int
	Skipped    int
	Errors     int
	Bytes      int64
	// ErrorSummary counts failures by kind (see the errorKind* constants),
	// e.g. {"rejected": 2, "network": 1}.
	ErrorSummary map[string]int
	// Failures lists the files that failed, up to maxRecordedFailures of
	// them, so a later retry can target just them.
	Failures []state.FileFailure
}

// SyncOptions configures a Sync call.
type SyncOptions struct {
	// DryRun scans and reports what would be uploaded without uploading.
	DryRun bool
	// Only, when non-empty, restricts the sync to scanned files whose
	// RelativePath is listed here — e.g. to retry only the files that
	// failed last time. The cursor watermark is not used to filter
	// candidates in this mode, and the sync never moves the cursor at all
	// (not backward, and not forward past files between the cursor and a
	// retried one that were never uploaded — the failure list is capped);
	// retried files are still hashed into the files table. Result.Skipped
	// counts the scanned files not listed here; listed paths that no longer
	// exist on the card are ignored (logged at Info).
	Only []string
}

func (s *Service) Sync(ctx context.Context, vol volume.Info, opts SyncOptions, onProgress ProgressFunc) (Result, error) {
	cfg := s.Config.Get()
	if !s.Config.IsConfigured() {
		return Result{}, fmt.Errorf("not configured")
	}

	workers := cfg.EffectiveConcurrency()
	auto := cfg.AutoConcurrency()
	if auto {
		workers = s.startWorkers()
	}

	if onProgress != nil {
		onProgress(Progress{Phase: "scanning"})
	}
	logx.Info("sync: scanning volume", "mount", vol.MountPoint, "uuid", vol.UUID)

	files, err := scanner.ScanVolume(vol.MountPoint, cfg)
	if err != nil {
		logx.Error("sync: scan failed", "err", err)
		return Result{}, err
	}

	var candidates []scanner.File
	var skipped int
	if len(opts.Only) > 0 {
		candidates, skipped = filterOnly(files, opts.Only)
	} else {
		candidates, skipped, err = s.filterNew(vol.UUID, files)
		if err != nil {
			logx.Error("sync: filter failed", "err", err)
			return Result{}, err
		}
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
		"auto", auto,
		"only", len(opts.Only) > 0,
	)

	if len(candidates) == 0 {
		logx.Info("sync: nothing new to upload")
		return Result{Skipped: len(files)}, nil
	}

	if opts.DryRun {
		logx.Info("sync: dry-run mode, skipping upload")
		return Result{Skipped: len(files)}, nil
	}

	var totalBytes int64
	for _, f := range candidates {
		totalBytes += f.Size
	}

	client := immich.NewClient(cfg.Immich.URL, cfg.Immich.APIKey)
	prog := newSyncProgress(len(candidates), totalBytes, onProgress)
	prog.reportStart()

	logx.Info("sync: upload started", "candidates", len(candidates))
	session := newSyncSession(s.State, vol.UUID, candidates)
	session.keepCursor = len(opts.Only) > 0
	checkpoint := newSyncCheckpoint(s.State, session, cfg.EffectiveCheckpointInterval())
	return s.uploadAll(ctx, vol.UUID, client, workers, auto, len(files), skipped, candidates, prog, session, checkpoint)
}

func (s *Service) uploadAll(
	ctx context.Context,
	volumeUUID string,
	client *immich.Client,
	workers int,
	auto bool,
	totalFiles, alreadySkipped int,
	candidates []scanner.File,
	prog *syncProgress,
	session *syncSession,
	checkpoint *syncCheckpoint,
) (Result, error) {
	parent := ctx
	ctx, abort := context.WithCancelCause(ctx)
	defer abort(nil)

	jobs := make(chan scanner.File)
	var wg sync.WaitGroup
	var bytesUploaded atomic.Int64
	// congestion counts uploads that failed with a network or server error
	// (and are retried): the automatic mode backs off on them.
	var congestion atomic.Int64
	tally := newErrorTally()
	failures := newFailureCollector()
	uploadStart := time.Now()

	defer checkpoint.flush()

	// In automatic mode every possible worker runs and the limiter decides
	// how many upload at once.
	slots := workers
	if auto {
		slots = config.MaxConcurrency
	}
	lim := newLimiter(ctx, workers)
	var tn *tuner
	stopTuning := make(chan struct{})
	tuningDone := make(chan struct{})
	if auto {
		tn = newTuner(workers, config.MaxConcurrency)
		go func() {
			defer close(tuningDone)
			autotune(ctx, stopTuning, tn, lim, prog, &congestion)
		}()
	} else {
		close(tuningDone)
	}

	for range slots {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for lim.acquire() {
				f, ok := <-jobs
				if !ok || ctx.Err() != nil {
					lim.release()
					return
				}
				o := s.syncOne(ctx, abort, volumeUUID, client, f, session, checkpoint, &bytesUploaded, &congestion, tally, failures)
				prog.done(f.Size, f.RelativePath, o)
				lim.release()
				if cur := prog.doneCount.Load(); cur%50 == 0 {
					logx.Info("sync: progress",
						"done", cur,
						"total", prog.total,
						"uploaded", prog.uploaded.Load(),
						"duplicates", prog.duplicates.Load(),
						"errors", prog.failed.Load(),
					)
				}
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
	close(stopTuning)
	<-tuningDone
	if auto {
		s.rememberWorkers(tn.kept())
		logx.Info("sync: parallel uploads settled", "workers", tn.kept())
	}

	upl := int(prog.uploaded.Load())
	dup := int(prog.duplicates.Load())
	errCount := int(prog.failed.Load())
	skipped := alreadySkipped + len(candidates) - upl - dup - errCount
	bytesDone := bytesUploaded.Load()
	errSummary := tally.snapshot()
	failed := failures.list()

	if cause := context.Cause(ctx); parent.Err() == nil && (errors.Is(cause, ErrServerUnavailable) || errors.Is(cause, ErrAuthFailed)) {
		logx.Error("sync: stopped, upload aborted, progress saved",
			"uploaded", upl,
			"errors", errCount,
			"err", cause,
			"duration", time.Since(uploadStart).Round(time.Second),
		)
		return Result{Uploaded: upl, Duplicates: dup, Skipped: skipped, Errors: errCount, Bytes: bytesDone, ErrorSummary: errSummary, Failures: failed}, cause
	}

	if parent.Err() != nil {
		logx.Info("sync: interrupted, progress saved",
			"uploaded", upl,
			"errors", errCount,
			"duration", time.Since(uploadStart).Round(time.Second),
		)
		return Result{Uploaded: upl, Duplicates: dup, Skipped: skipped, Errors: errCount, Bytes: bytesDone, ErrorSummary: errSummary, Failures: failed}, parent.Err()
	}

	logx.Info("sync: complete",
		"uploaded", upl,
		"duplicates", dup,
		"skipped", skipped,
		"errors", errCount,
		"upload_duration", time.Since(uploadStart).Round(time.Second).String(),
	)
	return Result{Uploaded: upl, Duplicates: dup, Skipped: skipped, Errors: errCount, Bytes: bytesDone, ErrorSummary: errSummary, Failures: failed}, nil
}

// syncOne uploads f and reports how it ended.
func (s *Service) syncOne(
	ctx context.Context,
	abort context.CancelCauseFunc,
	volumeUUID string,
	client *immich.Client,
	f scanner.File,
	session *syncSession,
	checkpoint *syncCheckpoint,
	bytesUploaded, congestion *atomic.Int64,
	tally *errorTally,
	failures *failureCollector,
) outcome {
	sha1hex, err := s.fileSHA1(volumeUUID, f)
	if err != nil {
		logx.Warn("sync: sha1 failed", "file", f.RelativePath, "err", err)
		tally.add(errorKindReadFailed)
		failures.add(state.FileFailure{RelativePath: f.RelativePath, Kind: errorKindReadFailed, Detail: err.Error()})
		return outcomeFailed
	}

	resp, err := uploadWithRetry(ctx, func() (*immich.UploadResponse, error) {
		resp, err := client.Upload(ctx, f.AbsolutePath, sha1hex, f.Mtime, f.SidecarPath)
		if err != nil && immich.IsRetryable(err) {
			congestion.Add(1)
		}
		return resp, err
	})
	if err != nil {
		logx.Error("sync: upload failed", "file", f.RelativePath, "err", err)
		kind := errorKind(err)
		tally.add(kind)
		failures.add(state.FileFailure{RelativePath: f.RelativePath, Kind: kind, Detail: immich.Detail(err)})
		switch {
		case isAuthError(err):
			abort(fmt.Errorf("%w: %w", ErrAuthFailed, err))
		case immich.IsRetryable(err):
			abort(fmt.Errorf("%w: %w", ErrServerUnavailable, err))
		}
		return outcomeFailed
	}

	if err := session.recordSuccess(f); err != nil {
		logx.Error("sync: failed to save progress", "file", f.RelativePath, "err", err)
		tally.add(errorKindState)
		failures.add(state.FileFailure{RelativePath: f.RelativePath, Kind: errorKindState, Detail: err.Error()})
		return outcomeFailed
	}

	checkpoint.onFileSaved()
	if resp != nil && resp.Status == "duplicate" {
		logx.Debug("sync: already on server", "file", f.RelativePath)
		return outcomeDuplicate
	}
	bytesUploaded.Add(f.Size)
	return outcomeUploaded
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

// filterOnly restricts files to those whose RelativePath is listed in only,
// logging (at Info) any listed path that no scanned file matches. It
// returns the matched files and how many scanned files were left out.
func filterOnly(files []scanner.File, only []string) ([]scanner.File, int) {
	found := make(map[string]bool, len(only))
	for _, p := range only {
		found[p] = false
	}

	candidates := make([]scanner.File, 0, len(only))
	for _, f := range files {
		if _, ok := found[f.RelativePath]; ok {
			found[f.RelativePath] = true
			candidates = append(candidates, f)
		}
	}

	for p, ok := range found {
		if !ok {
			logx.Info("sync: requested file not found on card", "path", p)
		}
	}

	return candidates, len(files) - len(candidates)
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
