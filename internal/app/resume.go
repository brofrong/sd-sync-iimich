package app

import (
	"context"
	"sync"
	"time"
)

// resumeIntervals are the delays between server checks while waiting to
// resume a sync; the last one repeats.
var resumeIntervals = []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}

// resumePingTimeout bounds a single server check.
const resumePingTimeout = 15 * time.Second

// resumer waits for the Immich server to come back after a sync stopped
// because it was unreachable, then starts the sync again. It pings the
// server on the resumeIntervals schedule; once a ping succeeds it calls
// start if canStart allows it, and stops waiting either way.
//
// start must not call Stop: Stop waits for an in-progress start to return.
type resumer struct {
	ping     func(context.Context) error
	canStart func() bool
	start    func()

	mu sync.Mutex
	// cancel is non-nil while waiting for the server; it stops the current
	// waiting goroutine, whose exit closes done.
	cancel context.CancelFunc
	done   chan struct{}
	// starting is the done channel of a goroutine that has stopped waiting
	// and is calling canStart/start, so Stop can wait for it to return.
	starting chan struct{}
}

func newResumer(ping func(context.Context) error, canStart func() bool, start func()) *resumer {
	return &resumer{ping: ping, canStart: canStart, start: start}
}

// Start begins waiting for the server unless already waiting.
func (r *resumer) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	r.cancel, r.done = cancel, done
	go r.wait(ctx, append([]time.Duration(nil), resumeIntervals...), done)
}

// Stop cancels waiting and returns once the goroutine has exited, so start
// is never called after Stop returns (unless Start is called again).
func (r *resumer) Stop() {
	r.mu.Lock()
	cancel, done, starting := r.cancel, r.done, r.starting
	r.cancel, r.done = nil, nil
	r.mu.Unlock()

	if cancel != nil {
		cancel()
		<-done
	}
	if starting != nil {
		<-starting
	}
}

// Waiting reports whether the resumer is waiting for the server.
func (r *resumer) Waiting() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cancel != nil
}

func (r *resumer) wait(ctx context.Context, intervals []time.Duration, done chan struct{}) {
	defer close(done)

	for i := 0; ; i++ {
		timer := time.NewTimer(intervals[min(i, len(intervals)-1)])
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		pingCtx, cancelPing := context.WithTimeout(ctx, resumePingTimeout)
		err := r.ping(pingCtx)
		cancelPing()
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return
		}
	}

	// The server is back: stop waiting, unless Stop got here first.
	r.mu.Lock()
	if ctx.Err() != nil {
		r.mu.Unlock()
		return
	}
	r.cancel()
	r.cancel, r.done = nil, nil
	r.starting = done
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		if r.starting == done {
			r.starting = nil
		}
		r.mu.Unlock()
	}()
	if r.canStart() {
		r.start()
	}
}

// shouldResume reports whether a sync that finished with code should be
// resumed automatically once the server is back: only when the server was
// unreachable or failing and the card is still mounted.
func shouldResume(code Code, cardMounted bool) bool {
	if !cardMounted {
		return false
	}
	return code == CodeServerUnreachable || code == CodeServerError
}
