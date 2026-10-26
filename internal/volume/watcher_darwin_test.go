//go:build darwin

package volume

import (
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

// fakeMounts scripts listMountsFn and getInfoFn: set replaces what /Volumes
// holds (mount point → device), and info maps a mount point to its volume.
type fakeMounts struct {
	mu        sync.Mutex
	mounts    map[string]uint64
	info      map[string]Info
	infoCalls map[string]int
}

func newFakeMounts(t *testing.T) *fakeMounts {
	f := &fakeMounts{
		mounts:    map[string]uint64{},
		info:      map[string]Info{},
		infoCalls: map[string]int{},
	}
	origList, origInfo, origPoll := listMountsFn, getInfoFn, pollInterval
	t.Cleanup(func() { listMountsFn, getInfoFn, pollInterval = origList, origInfo, origPoll })
	listMountsFn = f.list
	getInfoFn = f.getInfo
	return f
}

func (f *fakeMounts) set(mounts map[string]uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mounts = mounts
}

func (f *fakeMounts) setInfo(mountPoint string, info Info) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.info[mountPoint] = info
}

func (f *fakeMounts) calls(mountPoint string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.infoCalls[mountPoint]
}

func (f *fakeMounts) list() (map[string]uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]uint64, len(f.mounts))
	for k, v := range f.mounts {
		out[k] = v
	}
	return out, nil
}

func (f *fakeMounts) getInfo(mountPoint string) (*Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.infoCalls[mountPoint]++
	info, ok := f.info[mountPoint]
	if !ok {
		return nil, errors.New("diskutil: not ready")
	}
	return &info, nil
}

func TestMountScannerReusesIdentifiedVolumes(t *testing.T) {
	f := newFakeMounts(t)
	card := Info{Name: "Card", UUID: "AAAA", MountPoint: "/Volumes/Card", Removable: true}
	f.setInfo(card.MountPoint, card)

	s := newMountScanner(getInfoFn)
	for range 3 {
		got := s.snapshot(map[string]uint64{card.MountPoint: 7})
		if !reflect.DeepEqual(got, []Info{card}) {
			t.Fatalf("snapshot() = %+v, want %+v", got, []Info{card})
		}
	}
	if n := f.calls(card.MountPoint); n != 1 {
		t.Fatalf("GetInfo calls = %d, want 1 (cached while the device stays the same)", n)
	}
}

func TestMountScannerReidentifiesNewDevice(t *testing.T) {
	f := newFakeMounts(t)
	old := Info{Name: "Card", UUID: "AAAA", MountPoint: "/Volumes/Card"}
	f.setInfo(old.MountPoint, old)

	s := newMountScanner(getInfoFn)
	s.snapshot(map[string]uint64{old.MountPoint: 7})

	swapped := Info{Name: "Card", UUID: "BBBB", MountPoint: "/Volumes/Card"}
	f.setInfo(old.MountPoint, swapped)
	got := s.snapshot(map[string]uint64{old.MountPoint: 8})
	if !reflect.DeepEqual(got, []Info{swapped}) {
		t.Fatalf("snapshot() after a new device = %+v, want %+v", got, []Info{swapped})
	}
}

func TestMountScannerGivesUpOnUnidentifiableMount(t *testing.T) {
	f := newFakeMounts(t)
	const share = "/Volumes/share"

	s := newMountScanner(getInfoFn)
	for range maxInfoTries + 5 {
		if got := s.snapshot(map[string]uint64{share: 9}); len(got) != 0 {
			t.Fatalf("snapshot() = %+v, want nothing for a mount diskutil can't identify", got)
		}
	}
	if n := f.calls(share); n != maxInfoTries {
		t.Fatalf("GetInfo calls = %d, want %d", n, maxInfoTries)
	}

	// Mounted again (a new device): identified afresh.
	vol := Info{Name: "share", UUID: "CCCC", MountPoint: share}
	f.setInfo(share, vol)
	if got := s.snapshot(map[string]uint64{share: 10}); !reflect.DeepEqual(got, []Info{vol}) {
		t.Fatalf("snapshot() after remount = %+v, want %+v", got, []Info{vol})
	}
}

func TestMountScannerRetriesWhileMountSettles(t *testing.T) {
	f := newFakeMounts(t)
	card := Info{Name: "Card", UUID: "AAAA", MountPoint: "/Volumes/Card"}

	s := newMountScanner(getInfoFn)
	if got := s.snapshot(map[string]uint64{card.MountPoint: 7}); len(got) != 0 {
		t.Fatalf("snapshot() = %+v, want nothing before diskutil knows the volume", got)
	}
	f.setInfo(card.MountPoint, card)
	if got := s.snapshot(map[string]uint64{card.MountPoint: 7}); !reflect.DeepEqual(got, []Info{card}) {
		t.Fatalf("snapshot() = %+v, want %+v", got, []Info{card})
	}
}

func nextEvent(t *testing.T, w *Watcher) Event {
	t.Helper()
	select {
	case ev := <-w.Events():
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("no volume event")
		return Event{}
	}
}

func noEvent(t *testing.T, w *Watcher) {
	t.Helper()
	select {
	case ev := <-w.Events():
		t.Fatalf("unexpected volume event %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
}

// The card is ejected and inserted again at the same mount point: both
// show up as events (the bug: the second insertion was never reported).
func TestWatcherReportsReinsertedCard(t *testing.T) {
	f := newFakeMounts(t)
	pollInterval = 5 * time.Millisecond
	card := Info{Name: "Brofrong", UUID: "E811", MountPoint: "/Volumes/Brofrong", Removable: true}
	f.setInfo(card.MountPoint, card)
	f.set(map[string]uint64{card.MountPoint: 20})

	w, err := NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	noEvent(t, w) // already mounted at start: no event

	f.set(map[string]uint64{})
	if ev := nextEvent(t, w); ev.Type != EventUnmounted || ev.Info != card {
		t.Fatalf("event = %+v, want unmounted %+v", ev, card)
	}

	for range 2 {
		f.set(map[string]uint64{card.MountPoint: 21})
		if ev := nextEvent(t, w); ev.Type != EventMounted || ev.Info != card {
			t.Fatalf("event = %+v, want mounted %+v", ev, card)
		}
		f.set(map[string]uint64{})
		if ev := nextEvent(t, w); ev.Type != EventUnmounted || ev.Info != card {
			t.Fatalf("event = %+v, want unmounted %+v", ev, card)
		}
	}
	noEvent(t, w)
}
