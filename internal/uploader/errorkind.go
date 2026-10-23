package uploader

import (
	"errors"
	"net/http"
	"sync"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
)

// Error kind keys reported in Result.ErrorSummary. They match the
// "error_kind.*" translation catalog keys (without the prefix), so the GUI
// can show a localized breakdown without Go emitting any text of its own.
const (
	errorKindRejected   = "rejected"
	errorKindReadFailed = "read_failed"
	errorKindNetwork    = "network"
	errorKindServer     = "server"
	errorKindState      = "state"
)

// isAuthError reports whether err is a 401/403 response: the API key itself
// is wrong, so retrying or continuing to the next file would not help.
func isAuthError(err error) bool {
	var se *immich.StatusError
	if !errors.As(err, &se) {
		return false
	}
	return se.Code == http.StatusUnauthorized || se.Code == http.StatusForbidden
}

// errorKind classifies an upload failure for Result.ErrorSummary.
func errorKind(err error) string {
	var se *immich.StatusError
	if errors.As(err, &se) {
		if se.Code == http.StatusRequestTimeout || se.Code == http.StatusTooManyRequests || se.Code >= 500 {
			return errorKindServer
		}
		return errorKindRejected
	}
	return errorKindNetwork
}

// errorTally counts upload failures by kind across concurrent workers.
type errorTally struct {
	mu sync.Mutex
	m  map[string]int
}

func newErrorTally() *errorTally {
	return &errorTally{m: make(map[string]int)}
}

func (t *errorTally) add(kind string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.m[kind]++
}

func (t *errorTally) snapshot() map[string]int {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[string]int, len(t.m))
	for k, v := range t.m {
		out[k] = v
	}
	return out
}

// maxRecordedFailures bounds how many per-file failures a sync keeps in
// Result.Failures (a var, not a const, so tests can lower it). Result.Errors
// still counts every failure, capped or not.
var maxRecordedFailures = 1000

// failureCollector gathers per-file failures across concurrent workers, up
// to maxRecordedFailures of them.
type failureCollector struct {
	mu    sync.Mutex
	items []state.FileFailure
}

func newFailureCollector() *failureCollector {
	return &failureCollector{}
}

func (c *failureCollector) add(f state.FileFailure) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) >= maxRecordedFailures {
		return
	}
	c.items = append(c.items, f)
}

// list returns the collected failures (nil if there are none) in a copy
// safe to hand out after the collector may still be written to.
func (c *failureCollector) list() []state.FileFailure {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.items) == 0 {
		return nil
	}
	out := make([]state.FileFailure, len(c.items))
	copy(out, c.items)
	return out
}
