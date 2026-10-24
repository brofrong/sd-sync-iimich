//go:build linux

package gui

import (
	"errors"
	"testing"
)

// TestTrayAvailableReadsWatcherOwner checks trayAvailable against a fake
// session bus instead of a real one (there may be none in the sandboxed
// container these tests run in).
func TestTrayAvailableReadsWatcherOwner(t *testing.T) {
	prev := sessionBusNameHasOwnerFn
	t.Cleanup(func() { sessionBusNameHasOwnerFn = prev })

	tests := []struct {
		name string
		has  bool
		err  error
		want bool
	}{
		{"a tray host owns the name", true, nil, true},
		{"nobody owns the name (GNOME without an extension)", false, nil, false},
		{"the session bus can't be reached", false, errors.New("no bus"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionBusNameHasOwnerFn = func(name string) (bool, error) {
				if name != statusNotifierWatcherName {
					t.Fatalf("checked name = %q, want %q", name, statusNotifierWatcherName)
				}
				return tt.has, tt.err
			}
			if got := trayAvailable(); got != tt.want {
				t.Errorf("trayAvailable() = %v, want %v", got, tt.want)
			}
		})
	}
}
