//go:build darwin

package volume

import (
	"path/filepath"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/fsnotify/fsnotify"
)

type EventType int

const (
	EventMounted EventType = iota
	EventUnmounted
)

type Event struct {
	Type EventType
	Info Info
}

type Watcher struct {
	events chan Event
	done   chan struct{}
}

func NewWatcher() (*Watcher, error) {
	w := &Watcher{
		events: make(chan Event, 16),
		done:   make(chan struct{}),
	}
	go w.run()
	return w, nil
}

func (w *Watcher) Events() <-chan Event {
	return w.events
}

func (w *Watcher) Close() {
	close(w.done)
}

func (w *Watcher) run() {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		logx.Error("volume watcher init failed", "err", err)
		return
	}
	defer fw.Close()

	if err := fw.Add(volumesRoot); err != nil {
		logx.Error("volume watcher cannot watch path", "path", volumesRoot, "err", err)
		return
	}

	known := make(map[string]Info)
	w.seedKnown(known)

	debounce := make(map[string]*time.Timer)

	for {
		select {
		case <-w.done:
			return
		case ev, ok := <-fw.Events:
			if !ok {
				return
			}
			w.handleFSEvent(ev, fw, known, debounce)
		case err, ok := <-fw.Errors:
			if !ok {
				return
			}
			logx.Warn("volume watcher error", "err", err)
		}
	}
}

func (w *Watcher) seedKnown(known map[string]Info) {
	volumes, err := ListMounted()
	if err != nil {
		return
	}
	for _, v := range volumes {
		known[v.MountPoint] = v
	}
}

func (w *Watcher) handleFSEvent(ev fsnotify.Event, fw *fsnotify.Watcher, known map[string]Info, debounce map[string]*time.Timer) {
	name := ev.Name
	if filepath.Dir(name) != volumesRoot {
		return
	}

	switch {
	case ev.Has(fsnotify.Create):
		if t, ok := debounce[name]; ok {
			t.Stop()
		}
		debounce[name] = time.AfterFunc(4*time.Second, func() {
			w.checkMount(name, fw, known)
			delete(debounce, name)
		})
	case ev.Has(fsnotify.Remove):
		if info, ok := known[name]; ok {
			delete(known, name)
			w.events <- Event{Type: EventUnmounted, Info: info}
		}
	}
}

func (w *Watcher) checkMount(mountPoint string, fw *fsnotify.Watcher, known map[string]Info) {
	info, err := GetInfo(mountPoint)
	if err != nil {
		return
	}
	if _, ok := known[mountPoint]; ok {
		return
	}
	known[mountPoint] = *info
	_ = fw.Add(mountPoint)
	w.events <- Event{Type: EventMounted, Info: *info}
}
