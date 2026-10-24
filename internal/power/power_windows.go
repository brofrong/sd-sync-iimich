//go:build windows

// On Windows the system is kept awake by SetThreadExecutionState, held on
// one OS thread for as long as the hold lasts.

package power

import (
	"runtime"
	"sync"

	"golang.org/x/sys/windows"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// SetThreadExecutionState flags (winbase.h).
const (
	esContinuous     uint32 = 0x80000000
	esSystemRequired uint32 = 0x00000001
)

var procSetThreadExecutionState = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetThreadExecutionState")

// setThreadExecutionStateFn calls SetThreadExecutionState(flags) on the
// calling thread and returns the previous state, 0 on failure. It is a
// variable so tests can substitute a fake instead of changing this
// machine's power state.
var setThreadExecutionStateFn = func(flags uint32) uint32 {
	r, _, _ := procSetThreadExecutionState.Call(uintptr(flags))
	return uint32(r)
}

// KeepAwake prevents the system from sleeping until the returned release
// function is called. The execution state belongs to the thread that sets
// it, so a dedicated goroutine locked to its OS thread sets
// ES_CONTINUOUS|ES_SYSTEM_REQUIRED, waits for release, and clears it on the
// same thread. release is safe to call more than once (only the first call
// has any effect) and waits until the hold is cleared; a failure to set it
// is only logged, and release then does nothing.
func KeepAwake() (release func()) {
	started := make(chan bool, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Not unlocked on failure: a goroutine that exits while locked
		// takes its thread with it, and any execution state along.
		runtime.LockOSThread()
		if setThreadExecutionStateFn(esContinuous|esSystemRequired) == 0 {
			started <- false
			return
		}
		started <- true
		<-stop
		if setThreadExecutionStateFn(esContinuous) == 0 {
			logx.Warn("power: SetThreadExecutionState failed to clear; dropping its thread")
			return
		}
		runtime.UnlockOSThread()
	}()

	if !<-started {
		logx.Warn("power: SetThreadExecutionState failed; the system may sleep during the sync")
		return func() {}
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}
