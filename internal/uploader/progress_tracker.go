package uploader

import (
	"sync/atomic"
)

type syncProgress struct {
	total      int
	doneCount  atomic.Int64
	onProgress ProgressFunc
	eta        *etaTracker
}

func newSyncProgress(total int, onProgress ProgressFunc) *syncProgress {
	return &syncProgress{
		total:      total,
		onProgress: onProgress,
		eta:        newETATracker(),
	}
}

func (p *syncProgress) reportStart() {
	p.emit("syncing", 0)
}

func (p *syncProgress) done() {
	cur := int(p.doneCount.Add(1))
	p.emit("syncing", cur)
}

func (p *syncProgress) emit(phase string, current int) {
	if p.onProgress == nil || p.total == 0 {
		return
	}
	p.onProgress(Progress{
		Phase:   phase,
		Current: current,
		Total:   p.total,
		ETA:     p.eta.remaining(current, p.total),
	})
}
