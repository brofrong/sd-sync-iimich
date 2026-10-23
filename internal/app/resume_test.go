package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// fastResumeIntervals swaps resumeIntervals for millisecond delays for the
// duration of the test so resumer tests don't wait for real minutes.
func fastResumeIntervals(t *testing.T) {
	t.Helper()
	orig := resumeIntervals
	resumeIntervals = []time.Duration{time.Millisecond, 2 * time.Millisecond}
	t.Cleanup(func() { resumeIntervals = orig })
}

var errPingDown = errors.New("server down")

// waitFor polls cond until it is true or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestResumerStartsOnceAfterServerReturns(t *testing.T) {
	fastResumeIntervals(t)

	var pings, starts atomic.Int32
	started := make(chan struct{}, 1)
	r := newResumer(
		func(context.Context) error {
			if pings.Add(1) <= 2 {
				return errPingDown
			}
			return nil
		},
		func() bool { return true },
		func() {
			starts.Add(1)
			started <- struct{}{}
		},
	)
	t.Cleanup(r.Stop)

	r.Start()
	if !r.Waiting() {
		t.Fatal("Waiting() = false right after Start, want true")
	}

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for start")
	}
	r.Stop() // waits for the goroutine to exit

	if got := starts.Load(); got != 1 {
		t.Errorf("start called %d times, want 1", got)
	}
	if got := pings.Load(); got != 3 {
		t.Errorf("ping called %d times, want 3", got)
	}
	if r.Waiting() {
		t.Error("Waiting() = true after start, want false")
	}
}

func TestResumerStopPreventsStart(t *testing.T) {
	fastResumeIntervals(t)

	var pings, starts atomic.Int32
	r := newResumer(
		func(context.Context) error {
			pings.Add(1)
			return errPingDown
		},
		func() bool { return true },
		func() { starts.Add(1) },
	)

	r.Start()
	waitFor(t, "a few failed pings", func() bool { return pings.Load() >= 2 })
	r.Stop()

	if r.Waiting() {
		t.Error("Waiting() = true after Stop, want false")
	}
	// Stop waits for the goroutine, so no ping may happen afterwards.
	after := pings.Load()
	time.Sleep(20 * time.Millisecond)
	if got := pings.Load(); got != after {
		t.Errorf("ping called %d more times after Stop, want 0 (goroutine still running)", got-after)
	}
	if got := starts.Load(); got != 0 {
		t.Errorf("start called %d times, want 0", got)
	}
}

func TestResumerStopCancelsInFlightPing(t *testing.T) {
	fastResumeIntervals(t)

	inPing := make(chan struct{})
	var starts atomic.Int32
	r := newResumer(
		func(ctx context.Context) error {
			close(inPing)
			<-ctx.Done()
			return ctx.Err()
		},
		func() bool { return true },
		func() { starts.Add(1) },
	)

	r.Start()
	select {
	case <-inPing:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for ping")
	}
	stopped := make(chan struct{})
	go func() {
		r.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return while a ping was in flight")
	}
	if got := starts.Load(); got != 0 {
		t.Errorf("start called %d times, want 0", got)
	}
}

func TestResumerDoesNotStartWhileSyncRuns(t *testing.T) {
	fastResumeIntervals(t)

	var pings, starts, checks atomic.Int32
	r := newResumer(
		func(context.Context) error {
			pings.Add(1)
			return nil
		},
		func() bool {
			checks.Add(1)
			return false
		},
		func() { starts.Add(1) },
	)
	t.Cleanup(r.Stop)

	r.Start()
	waitFor(t, "waiting to end", func() bool { return !r.Waiting() })
	r.Stop() // waits for the goroutine to exit

	if got := starts.Load(); got != 0 {
		t.Errorf("start called %d times, want 0", got)
	}
	if got := checks.Load(); got != 1 {
		t.Errorf("canStart called %d times, want 1", got)
	}
	if got := pings.Load(); got != 1 {
		t.Errorf("ping called %d times, want 1 (waiting should end once the server is back)", got)
	}
}

func TestResumerStartIsIdempotent(t *testing.T) {
	fastResumeIntervals(t)

	var pings, starts atomic.Int32
	gate := make(chan struct{})
	r := newResumer(
		func(context.Context) error {
			pings.Add(1)
			<-gate
			return nil
		},
		func() bool { return true },
		func() { starts.Add(1) },
	)
	t.Cleanup(r.Stop)

	r.Start()
	r.Start()
	waitFor(t, "first ping", func() bool { return pings.Load() >= 1 })
	// A second waiting goroutine would reach its first ping within a couple
	// of milliseconds.
	time.Sleep(20 * time.Millisecond)
	close(gate)
	waitFor(t, "start", func() bool { return starts.Load() >= 1 })
	r.Stop()

	if got := pings.Load(); got != 1 {
		t.Errorf("ping called %d times, want 1 (one waiting goroutine)", got)
	}
	if got := starts.Load(); got != 1 {
		t.Errorf("start called %d times, want 1", got)
	}
}

func TestResumerCanRestartAfterStart(t *testing.T) {
	fastResumeIntervals(t)

	var starts atomic.Int32
	r := newResumer(
		func(context.Context) error { return nil },
		func() bool { return true },
		func() { starts.Add(1) },
	)
	t.Cleanup(r.Stop)

	r.Start()
	waitFor(t, "first start", func() bool { return starts.Load() == 1 })
	r.Start()
	waitFor(t, "second start", func() bool { return starts.Load() == 2 })
}

func TestShouldResume(t *testing.T) {
	tests := []struct {
		code    Code
		mounted bool
		want    bool
	}{
		{CodeServerUnreachable, true, true},
		{CodeServerError, true, true},
		{CodeServerUnreachable, false, false},
		{CodeServerError, false, false},
		{CodeCardRemoved, true, false},
		{CodeCardRemoved, false, false},
		{CodeAuthFailed, true, false},
		{CodeCancelled, true, false},
		{CodeFailed, true, false},
		{CodeDone, true, false},
	}
	for _, tt := range tests {
		if got := shouldResume(tt.code, tt.mounted); got != tt.want {
			t.Errorf("shouldResume(%q, %v) = %v, want %v", tt.code, tt.mounted, got, tt.want)
		}
	}
}
