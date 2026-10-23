package gui

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
)

type recorder struct {
	mu  sync.Mutex
	got []app.Status
}

func newRecorder() *recorder {
	return &recorder{}
}

func (r *recorder) emit(s app.Status) {
	r.mu.Lock()
	r.got = append(r.got, s)
	r.mu.Unlock()
}

func (r *recorder) statuses() []app.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]app.Status(nil), r.got...)
}

func syncing(current int) app.Status {
	return app.Status{
		Code:     app.CodeSyncing,
		Params:   map[string]string{},
		Progress: &app.Progress{Current: current, Total: 10, File: "IMG_" + strconv.Itoa(current) + ".JPG"},
	}
}

func TestThrottleCoalescesProgress(t *testing.T) {
	r := newRecorder()
	th := newThrottle(100*time.Millisecond, r.emit)

	for i := 1; i <= 10; i++ {
		th.Push(syncing(i))
		time.Sleep(5 * time.Millisecond)
	}

	// Wait well past the throttle interval so a pending update is flushed.
	time.Sleep(250 * time.Millisecond)

	got := r.statuses()
	if len(got) == 0 || len(got) > 2 {
		t.Fatalf("emit called %d times, want 1..2", len(got))
	}
	last := got[len(got)-1]
	if last.Progress == nil || last.Progress.Current != 10 {
		t.Fatalf("last emitted progress = %+v, want Current 10", last.Progress)
	}
}

func TestThrottlePassesCodeChangeImmediately(t *testing.T) {
	r := newRecorder()
	th := newThrottle(30*time.Millisecond, r.emit)

	th.Push(syncing(1))
	th.Push(syncing(2)) // same code inside the interval: held back
	th.Push(app.Status{Code: app.CodeDone, Params: map[string]string{"uploaded": "2"}})

	got := r.statuses()
	if len(got) != 2 {
		t.Fatalf("emit called %d times synchronously, want 2 (first status and code change): %+v", len(got), got)
	}
	if got[0].Code != app.CodeSyncing || got[1].Code != app.CodeDone {
		t.Fatalf("emitted codes = %s, %s; want syncing, done", got[0].Code, got[1].Code)
	}

	// The held-back progress is superseded by the code change and must not
	// be emitted after it.
	time.Sleep(100 * time.Millisecond)
	got = r.statuses()
	if len(got) != 2 {
		t.Fatalf("emit called %d times, want 2; last = %s", len(got), got[len(got)-1].Code)
	}
}
