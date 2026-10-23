package uploader

import (
	"sync"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
)

// syncSession tracks uploads in memory and advances a contiguous watermark
// along the oldest-first file order.
type syncSession struct {
	state      *state.DB
	volumeUUID string
	order      []scanner.File
	completed  map[string]struct{}
	// keepCursor, set for a retry of selected files (SyncOptions.Only),
	// stops recordSuccess from saving the watermark: the session's order
	// then holds only the retried files, so its watermark says nothing
	// about the files between the saved cursor and them — moving the
	// cursor, in either direction, could skip files never uploaded.
	keepCursor bool
	mu         sync.Mutex
}

func newSyncSession(st *state.DB, volumeUUID string, order []scanner.File) *syncSession {
	return &syncSession{
		state:      st,
		volumeUUID: volumeUUID,
		order:      order,
		completed:  make(map[string]struct{}),
	}
}

func (s *syncSession) recordSuccess(f scanner.File) error {
	s.mu.Lock()
	s.completed[f.RelativePath] = struct{}{}
	cur := s.watermarkLocked()
	keep := s.keepCursor
	s.mu.Unlock()
	if keep {
		return nil
	}
	return s.state.SetVolumeSync(s.volumeUUID, cur.Mtime, cur.RelativePath)
}

func (s *syncSession) watermark() state.VolumeSyncCursor {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.watermarkLocked()
}

func (s *syncSession) watermarkLocked() state.VolumeSyncCursor {
	var cur state.VolumeSyncCursor
	for _, f := range s.order {
		if _, ok := s.completed[f.RelativePath]; !ok {
			break
		}
		cur = state.VolumeSyncCursor{Mtime: f.Mtime, RelativePath: f.RelativePath}
	}
	return cur
}

// isAfterWatermark reports whether f still needs syncing relative to the saved cursor.
func isAfterWatermark(f scanner.File, cur state.VolumeSyncCursor) bool {
	if cur.Mtime.IsZero() {
		return true
	}
	if f.Mtime.After(cur.Mtime) {
		return true
	}
	if f.Mtime.Equal(cur.Mtime) && f.RelativePath > cur.RelativePath {
		return true
	}
	return false
}
