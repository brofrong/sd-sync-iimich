package uploader

import (
	"sync/atomic"
	"time"
)

type syncProgress struct {
	total      int
	totalBytes int64
	doneCount  atomic.Int64
	doneBytes  atomic.Int64
	onProgress ProgressFunc
	eta        *etaTracker
	start      time.Time
}

func newSyncProgress(total int, totalBytes int64, onProgress ProgressFunc) *syncProgress {
	return &syncProgress{
		total:      total,
		totalBytes: totalBytes,
		onProgress: onProgress,
		eta:        newETATracker(),
		start:      time.Now(),
	}
}

func (p *syncProgress) reportStart() {
	p.emit(0, 0, "")
}

// done records one more processed file (successful or not) with its size,
// so the progress bar and byte-rate estimate advance through the whole batch.
func (p *syncProgress) done(size int64, file string) {
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
	p.onProgress(Progress{
		Phase:       "syncing",
		Current:     current,
		Total:       p.total,
		ETA:         p.eta.remaining(current, p.total),
		File:        file,
		BytesDone:   doneBytes,
		BytesTotal:  p.totalBytes,
		BytesPerSec: bytesPerSec,
	})
}
