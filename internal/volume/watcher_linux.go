//go:build linux

package volume

import "time"

// pollInterval is how often the watcher re-lists removable volumes. A
// package var so tests can shrink it.
var pollInterval = 2 * time.Second

// listRemovableFn is ListRemovable behind a seam so the watcher's polling
// goroutine can be driven by tests without a real /proc/self/mountinfo.
var listRemovableFn = ListRemovable

// NewWatcher starts polling /proc/self/mountinfo (via ListRemovable) for
// removable volumes being mounted and unmounted.
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
