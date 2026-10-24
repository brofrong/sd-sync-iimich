//go:build darwin

// On macOS the system is kept awake by running `caffeinate -i -w <pid>`
// tied to this process's lifetime.

package power

import (
	"os"
	"os/exec"
	"strconv"
	"sync"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// startCaffeinate starts `caffeinate -i -w <pid>` and returns a function
// that stops it. It is a variable so tests can substitute a fake
// implementation instead of spawning a real caffeinate process.
var startCaffeinate = func(pid int) (stop func(), err error) {
	cmd := exec.Command("caffeinate", "-i", "-w", strconv.Itoa(pid))
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}, nil
}

// KeepAwake prevents the system from sleeping until the returned release
// function is called. release is safe to call more than once (only the
// first call has any effect), and safe to call even when caffeinate failed
// to start — a start failure is only logged.
func KeepAwake() (release func()) {
	stop, err := startCaffeinate(os.Getpid())
	if err != nil || stop == nil {
		logx.Warn("power: caffeinate failed to start", "err", err)
		return func() {}
	}

	var once sync.Once
	return func() {
		once.Do(stop)
	}
}
