//go:build windows

package volume

import (
	"sort"
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

// volumeKey identifies a volume snapshot by mount point and UUID: either
// changing means the card was swapped even if the other stayed the same
// (e.g. reformatted in place, or the same card reader getting a new drive
// letter).
func volumeKey(info Info) string {
	return info.MountPoint + "|" + info.UUID
}

// diffVolumes compares two snapshots of the removable volumes and returns
// the events that explain the difference: every volume present in prev but
// missing from cur unmounted, and every volume present in cur but missing
// from prev mounted. Events are ordered by key (unmounted before mounted)
// so the result is deterministic regardless of map iteration order.
func diffVolumes(prev, cur []Info) []Event {
	prevByKey := make(map[string]Info, len(prev))
	for _, v := range prev {
		prevByKey[volumeKey(v)] = v
	}
	curByKey := make(map[string]Info, len(cur))
	for _, v := range cur {
		curByKey[volumeKey(v)] = v
	}

	var events []Event
	for _, k := range sortedKeys(prevByKey) {
		if _, ok := curByKey[k]; !ok {
			events = append(events, Event{Type: EventUnmounted, Info: prevByKey[k]})
		}
	}
	for _, k := range sortedKeys(curByKey) {
		if _, ok := prevByKey[k]; !ok {
			events = append(events, Event{Type: EventMounted, Info: curByKey[k]})
		}
	}
	return events
}

func sortedKeys(m map[string]Info) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
