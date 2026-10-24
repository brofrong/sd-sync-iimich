//go:build windows

package volume

import (
	"time"
)

// pollInterval is how often the watcher re-lists removable volumes. A
// package var so tests can shrink it.
var pollInterval = 2 * time.Second

// listRemovableFn is ListRemovable behind a seam so the watcher's polling
// goroutine can be driven by tests without real drives.
var listRemovableFn = ListRemovable

// NewWatcher starts polling for removable volumes being mounted and
// unmounted.
func NewWatcher() (*Watcher, error) {
	w := &Watcher{
		events: make(chan Event, 16),
		done:   make(chan struct{}),
	}
	go w.run()
	return w, nil
}

func (w *Watcher) run() {
	prev, _ := listRemovableFn()

	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			cur, err := listRemovableFn()
			if err != nil {
				continue
			}
			for _, ev := range diffVolumes(prev, cur) {
				select {
				case w.events <- ev:
				case <-w.done:
					return
				}
			}
			prev = cur
		}
	}
}

// volumeKey, diffVolumes and sortedKeys live in watcher_diff.go (no build
// tag): the snapshot-diffing logic itself has no Windows-specific code, so
// Linux's poll-based watcher (watcher_linux.go) reuses it instead of
// duplicating it.
