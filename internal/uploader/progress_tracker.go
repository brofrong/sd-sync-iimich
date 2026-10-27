package uploader

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// outcome is how one file of a sync ended.
type outcome int

const (
	outcomeUploaded  outcome = iota // Immich stored it
	outcomeDuplicate                // Immich already had it
	outcomeFailed
)

type syncProgress struct {
	total      int
	totalBytes int64
	doneCount  atomic.Int64
	doneBytes  atomic.Int64
	uploaded   atomic.Int64
	duplicates atomic.Int64
	failed     atomic.Int64
	onProgress ProgressFunc
	eta        *etaTracker
	start      time.Time

	// inFlightMu protects inFlight (the set of files currently being
	// uploaded by the worker pool) and lastFile (the most recently
	// completed one, so started() can keep reporting it instead of
	// blanking Progress.File while the next file is still in flight).
	inFlightMu sync.Mutex
	inFlight   map[string]bool
	lastFile   string
}

func newSyncProgress(total int, totalBytes int64, onProgress ProgressFunc) *syncProgress {
	return &syncProgress{
		total:      total,
		totalBytes: totalBytes,
		onProgress: onProgress,
		eta:        newETATracker(),
		start:      time.Now(),
		inFlight:   make(map[string]bool),
	}
}

func (p *syncProgress) reportStart() {
	p.emit(0, 0, "")
}

// started records that file is now being uploaded, so it shows up in the
// in-flight set reported with the next progress update, until done removes
// it. The emitted Progress.File stays at the last completed file's name
// (as it was before this method existed) rather than going blank while
// file is still uploading.
func (p *syncProgress) started(file string) {
	p.inFlightMu.Lock()
	p.inFlight[file] = true
	last := p.lastFile
	p.inFlightMu.Unlock()
	p.emit(int(p.doneCount.Load()), p.doneBytes.Load(), last)
}

// done records one more processed file with its size and how it ended, so
// the progress bar and byte-rate estimate advance through the whole batch.
// It removes file from the in-flight set and records it as the last
// completed file before reporting, since it is no longer being uploaded.
func (p *syncProgress) done(size int64, file string, o outcome) {
	switch o {
	case outcomeUploaded:
		p.uploaded.Add(1)
	case outcomeDuplicate:
		p.duplicates.Add(1)
	default:
		p.failed.Add(1)
	}
	p.inFlightMu.Lock()
	delete(p.inFlight, file)
	p.lastFile = file
	p.inFlightMu.Unlock()
	cur := int(p.doneCount.Add(1))
	doneBytes := p.doneBytes.Add(size)
	p.emit(cur, doneBytes, file)
}

func (p *syncProgress) emit(current int, doneBytes int64, file string) {
	if p.onProgress == nil || p.total == 0 {
		return
	}
	var bytesPerSec float64
	if elapsed := time.Since(p.start).Seconds(); elapsed > 0 {
		bytesPerSec = float64(doneBytes) / elapsed
	}
	p.inFlightMu.Lock()
	inFlight := make([]string, 0, len(p.inFlight))
	for f := range p.inFlight {
		inFlight = append(inFlight, f)
	}
	p.inFlightMu.Unlock()
	sort.Strings(inFlight)
	p.onProgress(Progress{
		Phase:       "syncing",
		Current:     current,
		Total:       p.total,
		ETA:         p.eta.remaining(current, p.total),
		File:        file,
		BytesDone:   doneBytes,
		BytesTotal:  p.totalBytes,
		BytesPerSec: bytesPerSec,
		Uploaded:    int(p.uploaded.Load()),
		Duplicates:  int(p.duplicates.Load()),
		Errors:      int(p.failed.Load()),
		InFlight:    inFlight,
	})
}
