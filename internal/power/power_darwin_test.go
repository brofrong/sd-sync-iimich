//go:build darwin

package power

import (
	"errors"
	"os"
	"testing"
)

func TestKeepAwakeStartsAndStops(t *testing.T) {
	orig := startCaffeinate
	t.Cleanup(func() { startCaffeinate = orig })

	var gotPID int
	stopCalls := 0
	startCaffeinate = func(pid int) (func(), error) {
		gotPID = pid
		return func() { stopCalls++ }, nil
	}

	release := KeepAwake()

	if gotPID != os.Getpid() {
		t.Fatalf("startCaffeinate called with pid %d, want %d", gotPID, os.Getpid())
	}

	release()
	release()

	if stopCalls != 1 {
		t.Fatalf("stop called %d times, want exactly 1", stopCalls)
	}
}

func TestKeepAwakeStartFailure(t *testing.T) {
	orig := startCaffeinate
	t.Cleanup(func() { startCaffeinate = orig })

	startCaffeinate = func(pid int) (func(), error) {
		return nil, errors.New("caffeinate: not found")
	}

	release := KeepAwake()
	if release == nil {
		t.Fatal("KeepAwake() release = nil, want a safe no-op function")
	}

	// Must not panic, even when called more than once.
	release()
	release()
}
