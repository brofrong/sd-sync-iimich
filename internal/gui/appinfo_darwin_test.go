package gui

import (
	"errors"
	"testing"
)

func TestOSVersionOnMac(t *testing.T) {
	orig := swVers
	t.Cleanup(func() { swVers = orig })

	swVers = func() ([]byte, error) { return []byte("15.1.1\n"), nil }
	if got := osVersion(); got != "15.1.1" {
		t.Errorf("osVersion() = %q, want 15.1.1", got)
	}
	swVers = func() ([]byte, error) { return nil, errors.New("sw_vers: not found") }
	if got := osVersion(); got != "" {
		t.Errorf("osVersion() with a failing sw_vers = %q, want \"\"", got)
	}
}
