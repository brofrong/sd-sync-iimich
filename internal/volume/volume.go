// Package volume finds removable volumes (camera cards), watches them
// being mounted and unmounted, and ejects and erases them. Each system's
// files implement the same API: ListRemovable, GetInfo, FindByUUID,
// NewWatcher, Eject and Erase.
package volume

import "errors"

// Info describes a mounted volume.
type Info struct {
	Name       string
	MountPoint string
	// UUID identifies the volume's filesystem: it changes when the card
	// is formatted.
	UUID      string
	Removable bool
}

// EventType says whether a volume appeared or disappeared.
type EventType int

const (
	EventMounted EventType = iota
	EventUnmounted
)

// Event is a volume being mounted or unmounted.
type Event struct {
	Type EventType
	Info Info
}

// Watcher reports volumes being mounted and unmounted (see NewWatcher).
type Watcher struct {
	events chan Event
	done   chan struct{}
}

// Events delivers the watcher's events.
func (w *Watcher) Events() <-chan Event {
	return w.events
}

// Close stops the watcher.
func (w *Watcher) Close() {
	close(w.done)
}

// ErrErasedNotRemounted indicates the erase succeeded but the freshly
// formatted volume never reappeared with a new identity in time. Callers
// must treat this as "the card WAS erased" even though Erase could not
// confirm it or return the new volume's Info.
var ErrErasedNotRemounted = errors.New("card erased but its new volume was not found")

// ErrNeedsAdmin means the system would only erase the card with
// administrator rights (or a password prompt), which the app never asks
// for.
var ErrNeedsAdmin = errors.New("formatting the card needs administrator rights")
