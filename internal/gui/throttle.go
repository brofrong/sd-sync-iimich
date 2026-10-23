package gui

import (
	"sync"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
)

// throttle rate-limits status updates sent to the window. A status whose
// Code differs from the last one emitted goes out immediately (and drops
// any held-back update); repeated statuses with the same Code (progress
// ticks) are emitted at most once per interval, always with the latest
// value.
type throttle struct {
	every time.Duration
	emit  func(app.Status)

	mu       sync.Mutex
	started  bool
	lastCode app.Code
	lastAt   time.Time
	pending  *app.Status
	timer    *time.Timer
	// gen identifies the current timer, so a timer that fired just as it
	// was being replaced does not flush the newer pending status early.
	gen uint64
}

func newThrottle(every time.Duration, emit func(app.Status)) *throttle {
	return &throttle{every: every, emit: emit}
}

// Push records s and emits it now or after the rest of the interval.
func (t *throttle) Push(s app.Status) {
	t.mu.Lock()
	defer t.mu.Unlock()

	wait := t.every - time.Since(t.lastAt)
	if !t.started || s.Code != t.lastCode || (wait <= 0 && t.pending == nil) {
		t.emitLocked(s)
		return
	}
	t.pending = &s
	if t.timer == nil {
		gen := t.gen
		t.timer = time.AfterFunc(max(wait, 0), func() { t.flush(gen) })
	}
}

// Now emits s at once, dropping any held-back update.
func (t *throttle) Now(s app.Status) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.emitLocked(s)
}

func (t *throttle) flush(gen uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if gen != t.gen || t.pending == nil {
		return
	}
	t.emitLocked(*t.pending)
}

// emitLocked sends s and resets the pending state. Emitting under the lock
// keeps updates in order between Push and a timer flush.
func (t *throttle) emitLocked(s app.Status) {
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	t.gen++
	t.pending = nil
	t.started = true
	t.lastCode = s.Code
	t.lastAt = time.Now()
	t.emit(s)
}
