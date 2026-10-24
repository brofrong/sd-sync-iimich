//go:build linux

package power

import (
	"errors"
	"testing"
)

// fakeInhibitor stands in for logind's inhibitor lock descriptor.
type fakeInhibitor struct{ closes int }

func (f *fakeInhibitor) Close() error {
	f.closes++
	return nil
}

func TestKeepAwakeReleaseIdempotent(t *testing.T) {
	orig := inhibitSleepFn
	t.Cleanup(func() { inhibitSleepFn = orig })
	lock := &fakeInhibitor{}
	calls := 0
	inhibitSleepFn = func() (closer, error) {
		calls++
		return lock, nil
	}

	release := KeepAwake()
	if calls != 1 {
		t.Fatalf("inhibit calls = %d, want 1", calls)
	}
	release()
	release()

	if lock.closes != 1 {
		t.Fatalf("lock closed %d times, want exactly once", lock.closes)
	}
}

func TestKeepAwakeStartFailure(t *testing.T) {
	orig := inhibitSleepFn
	t.Cleanup(func() { inhibitSleepFn = orig })
	inhibitSleepFn = func() (closer, error) {
		return nil, errors.New("no system bus")
	}

	release := KeepAwake()
	if release == nil {
		t.Fatal("KeepAwake() release = nil, want a safe no-op function")
	}
	release()
	release()
}

func TestKeepAwakeNilLockDoesNotPanic(t *testing.T) {
	orig := inhibitSleepFn
	t.Cleanup(func() { inhibitSleepFn = orig })
	inhibitSleepFn = func() (closer, error) { return nil, nil }

	release := KeepAwake()
	release()
	release()
}

// TestInhibitArgs pins what logind is asked for: sleep and idle, blocked
// (not just delayed), under the app's name.
func TestInhibitArgs(t *testing.T) {
	want := []any{"sleep:idle", "SD Sync Immich", "Uploading photos", "block"}
	got := inhibitArgs()
	if len(got) != len(want) {
		t.Fatalf("inhibitArgs() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("inhibitArgs() = %v, want %v", got, want)
		}
	}
}
