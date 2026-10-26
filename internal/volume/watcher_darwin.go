//go:build darwin

package volume

import (
	"os"
	"path/filepath"
	"sort"
	"syscall"
	"time"
)

// pollInterval is how often the watcher re-lists /Volumes. A package var
// so tests can shrink it.
var pollInterval = 2 * time.Second

// maxInfoTries is how many times a mount diskutil can't identify (e.g. a
// volume still settling, or a network share) is asked about before the
// watcher stops asking until something else is mounted there.
const maxInfoTries = 3

// listMountsFn and getInfoFn are seams so tests can drive the watcher
// without real volumes.
var (
	listMountsFn = listMounts
	getInfoFn    = GetInfo
)

// NewWatcher starts polling /Volumes for volumes being mounted and
// unmounted.
//
// It polls rather than watching /Volumes with kqueue (fsnotify): kqueue
// never reports a volume's mount point going away on unmount, so a card
// ejected and inserted again at the same mount point looked already
// mounted and its sync never started.
func NewWatcher() (*Watcher, error) {
	w := &Watcher{
		events: make(chan Event, 16),
		done:   make(chan struct{}),
	}
	// The seams are read here, not in the goroutine, which may outlive a
	// test that restores them.
	go w.run(listMountsFn, newMountScanner(getInfoFn), pollInterval)
	return w, nil
}

func (w *Watcher) run(list func() (map[string]uint64, error), s *mountScanner, interval time.Duration) {
	var prev []Info
	if mounts, err := list(); err == nil {
		prev = s.snapshot(mounts)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.done:
			return
		case <-ticker.C:
			mounts, err := list()
			if err != nil {
				continue
			}
			cur := s.snapshot(mounts)
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

// listMounts returns the volumes mounted in /Volumes: mount point →
// device. Entries that are not mount points (a folder left behind, the
// "Macintosh HD" symlink) are skipped.
func listMounts() (map[string]uint64, error) {
	root, err := os.Stat(volumesRoot)
	if err != nil {
		return nil, err
	}
	rootDev, ok := deviceOf(root)
	if !ok {
		return nil, nil
	}
	entries, err := os.ReadDir(volumesRoot)
	if err != nil {
		return nil, err
	}
	mounts := make(map[string]uint64)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		mountPoint := filepath.Join(volumesRoot, e.Name())
		fi, err := os.Stat(mountPoint)
		if err != nil {
			continue
		}
		if dev, ok := deviceOf(fi); ok && dev != rootDev {
			mounts[mountPoint] = dev
		}
	}
	return mounts, nil
}

func deviceOf(fi os.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

// mountScanner turns listings of /Volumes into volume snapshots for
// diffVolumes. It runs diskutil (GetInfo) only for mounts it has not
// identified yet: a mount point keeps its Info while its device stays the
// same.
type mountScanner struct {
	getInfo func(mountPoint string) (*Info, error)
	known   map[string]identified
	failed  map[string]failedInfo
}

type identified struct {
	dev  uint64
	info Info
}

type failedInfo struct {
	dev   uint64
	tries int
}

func newMountScanner(getInfo func(mountPoint string) (*Info, error)) *mountScanner {
	return &mountScanner{
		getInfo: getInfo,
		known:   make(map[string]identified),
		failed:  make(map[string]failedInfo),
	}
}

// snapshot returns the identified volumes among mounts (mount point →
// device), sorted by mount point.
func (s *mountScanner) snapshot(mounts map[string]uint64) []Info {
	for mp := range s.known {
		if _, ok := mounts[mp]; !ok {
			delete(s.known, mp)
		}
	}
	for mp := range s.failed {
		if _, ok := mounts[mp]; !ok {
			delete(s.failed, mp)
		}
	}

	var out []Info
	for mp, dev := range mounts {
		if k, ok := s.known[mp]; ok && k.dev == dev {
			out = append(out, k.info)
			continue
		}
		f := s.failed[mp]
		if f.dev != dev {
			f = failedInfo{dev: dev}
		}
		if f.tries >= maxInfoTries {
			continue
		}
		info, err := s.getInfo(mp)
		if err != nil {
			f.tries++
			s.failed[mp] = f
			delete(s.known, mp)
			continue
		}
		delete(s.failed, mp)
		s.known[mp] = identified{dev: dev, info: *info}
		out = append(out, *info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].MountPoint < out[j].MountPoint })
	return out
}
