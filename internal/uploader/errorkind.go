package uploader

import (
	"errors"
	"net/http"
	"sync"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
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
