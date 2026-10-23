package uploader

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// Automatic upload concurrency (config: concurrency 0). A sync starts with
// the number of parallel uploads the previous one settled on (or
// config.DefaultConcurrency) and measures its throughput window by window:
// it adds tuneStep workers while that makes the sync at least tuneGain
// faster, goes back when it doesn't and halves the workers as soon as
// uploads hit network or server errors.
const (
	tuneStep        = 2
	tuneGain        = 1.10
	tuneHoldWindows = 4 // windows to stay put before probing again
)

// tuneWindow is the shortest measurement window; one also lasts until as
// many files finished as there are workers, so big videos give fair samples.
var tuneWindow = 15 * time.Second

// tuner decides the number of parallel uploads from one window to the next.
type tuner struct {
	n, limit int
	base     float64 // throughput at the kept worker count
	prev     int     // the kept count while probing n; 0 when not probing
	hold     int     // windows left before the next probe
}

func newTuner(start, limit int) *tuner {
	return &tuner{n: min(max(start, 1), limit), limit: limit}
}

// next takes the throughput (bytes/s) of the window just measured at t.n and
// whether uploads hit network or server errors in it, and returns the
// worker count for the next window.
func (t *tuner) next(throughput float64, congested bool) int {
	if congested {
		t.n = max(1, t.n/2)
		t.prev, t.base = 0, 0
		t.hold = tuneHoldWindows
		return t.n
	}
	if t.prev != 0 { // judge the probe
		if throughput > t.base*tuneGain {
			t.base, t.prev = throughput, 0
			t.probe()
		} else {
			t.n, t.prev = t.prev, 0
			t.hold = tuneHoldWindows
		}
		return t.n
	}
	if t.hold > 0 {
		t.hold--
		return t.n
	}
	t.base = throughput
	t.probe()
	return t.n
}

// probe tries tuneStep more workers, or holds at the maximum.
func (t *tuner) probe() {
	if t.n >= t.limit {
		t.hold = tuneHoldWindows
		return
	}
	t.prev = t.n
	t.n = min(t.n+tuneStep, t.limit)
}

// kept is the worker count the tuner settled on: an unjudged probe doesn't
// count.
func (t *tuner) kept() int {
	if t.prev != 0 {
		return t.prev
	}
	return t.n
}

// limiter bounds how many uploads run at once; the bound can change while
// they run.
type limiter struct {
	ctx    context.Context
	mu     sync.Mutex
	cond   *sync.Cond
	limit  int
	active int
}

func newLimiter(ctx context.Context, limit int) *limiter {
	l := &limiter{ctx: ctx, limit: limit}
	l.cond = sync.NewCond(&l.mu)
	context.AfterFunc(ctx, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.cond.Broadcast()
	})
	return l
}

// acquire waits for a free slot; it returns false once ctx is done.
func (l *limiter) acquire() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.active >= l.limit && l.ctx.Err() == nil {
		l.cond.Wait()
	}
	if l.ctx.Err() != nil {
		return false
	}
	l.active++
	return true
}

func (l *limiter) release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.active--
	l.cond.Broadcast()
}

func (l *limiter) setLimit(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = n
	l.cond.Broadcast()
}

// autotune measures the sync window by window and sets the limiter to the
// tuner's choice until stop is closed or ctx is done. A window lasts at
// least tuneWindow and until as many files finished as uploads run, unless
// uploads hit network or server errors, which ends it right away.
func autotune(ctx context.Context, stop <-chan struct{}, tn *tuner, lim *limiter, prog *syncProgress, congestion *atomic.Int64) {
	ticker := time.NewTicker(tuneWindow)
	defer ticker.Stop()

	start := time.Now()
	bytes0, files0, errors0 := prog.doneBytes.Load(), prog.doneCount.Load(), congestion.Load()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
		}
		congested := congestion.Load() > errors0
		if !congested && prog.doneCount.Load()-files0 < int64(tn.n) {
			continue // too few files finished for a fair sample
		}
		throughput := float64(prog.doneBytes.Load()-bytes0) / time.Since(start).Seconds()
		from := tn.n
		if to := tn.next(throughput, congested); to != from {
			logx.Info("sync: parallel uploads",
				"from", from,
				"to", to,
				"mb_per_sec", fmt.Sprintf("%.1f", throughput/1e6),
				"network_errors", congested,
			)
			lim.setLimit(to)
		}
		start = time.Now()
		bytes0, files0, errors0 = prog.doneBytes.Load(), prog.doneCount.Load(), congestion.Load()
	}
}
