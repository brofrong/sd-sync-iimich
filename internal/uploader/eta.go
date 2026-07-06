package uploader

import "time"

// etaTracker estimates remaining time from completed work.
type etaTracker struct {
	start time.Time
}

func newETATracker() *etaTracker {
	return &etaTracker{start: time.Now()}
}

func (e *etaTracker) remaining(done, total int) time.Duration {
	if done <= 0 || total <= 0 || done >= total {
		return 0
	}
	elapsed := time.Since(e.start)
	perItem := elapsed / time.Duration(done)
	return perItem * time.Duration(total-done)
}
