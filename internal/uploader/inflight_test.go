package uploader

import (
	"reflect"
	"testing"
)

// TestProgressReportsInFlight checks that started() adds a file to the
// in-flight set (reported sorted in Progress.InFlight) and done() removes it
// before the next progress report.
func TestProgressReportsInFlight(t *testing.T) {
	var last Progress
	onProgress := func(p Progress) {
		last = p
	}

	prog := newSyncProgress(2, 200, onProgress)

	prog.started("a")
	prog.started("b")
	if !reflect.DeepEqual(last.InFlight, []string{"a", "b"}) {
		t.Fatalf("InFlight after started(a), started(b) = %v, want [a b]", last.InFlight)
	}

	prog.done(100, "a", outcomeUploaded)
	if !reflect.DeepEqual(last.InFlight, []string{"b"}) {
		t.Fatalf("InFlight after done(a) = %v, want [b]", last.InFlight)
	}
	if last.Current != 1 {
		t.Fatalf("Current after done(a) = %d, want 1", last.Current)
	}
}
