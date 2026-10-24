//go:build windows

package power

import (
	"sync"
	"testing"

	"golang.org/x/sys/windows"
)

// fakeExecutionState replaces setThreadExecutionStateFn for a test: it
// records each call's flags and OS thread, and answers ok (a non-zero
// previous state) or 0 (failure). The real call is never made.
type fakeExecutionState struct {
	mu      sync.Mutex
	flags   []uint32
	threads []uint32
	fail    bool
}

func withFakeExecutionState(t *testing.T) *fakeExecutionState {
	t.Helper()
	orig := setThreadExecutionStateFn
	t.Cleanup(func() { setThreadExecutionStateFn = orig })
	f := &fakeExecutionState{}
	setThreadExecutionStateFn = func(flags uint32) uint32 {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.flags = append(f.flags, flags)
		f.threads = append(f.threads, windows.GetCurrentThreadId())
		if f.fail {
			return 0
		}
		return esContinuous
	}
	return f
}

func (f *fakeExecutionState) calls() ([]uint32, []uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint32(nil), f.flags...), append([]uint32(nil), f.threads...)
}

func TestKeepAwakeReleaseIdempotent(t *testing.T) {
	f := withFakeExecutionState(t)

	release := KeepAwake()
	flags, _ := f.calls()
	if len(flags) != 1 || flags[0] != esContinuous|esSystemRequired {
		t.Fatalf("calls after KeepAwake = %#x, want one ES_CONTINUOUS|ES_SYSTEM_REQUIRED", flags)
	}

	release()
	release()

	flags, threads := f.calls()
	if len(flags) != 2 || flags[1] != esContinuous {
		t.Fatalf("calls after release = %#x, want the hold cleared with ES_CONTINUOUS exactly once", flags)
	}
	// The execution state belongs to a thread: it must be cleared on the
	// thread that set it.
	if threads[0] != threads[1] {
		t.Fatalf("set on thread %d, cleared on thread %d, want the same thread", threads[0], threads[1])
	}
}

func TestKeepAwakeStartFailure(t *testing.T) {
	f := withFakeExecutionState(t)
	f.fail = true

	release := KeepAwake()
	if release == nil {
		t.Fatal("KeepAwake() release = nil, want a safe no-op function")
	}
	release()
	release()

	if flags, _ := f.calls(); len(flags) != 1 {
		t.Fatalf("calls = %#x, want only the failed attempt", flags)
	}
}
