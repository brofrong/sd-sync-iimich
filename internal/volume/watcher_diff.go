package volume

import "sort"

// volumeKey identifies a volume snapshot by mount point and UUID: either
// changing means the card was swapped even if the other stayed the same
// (e.g. reformatted in place, or the same card reader getting a new drive
// letter or, on Linux, a new auto-mount path).
func volumeKey(info Info) string {
	return info.MountPoint + "|" + info.UUID
}

// diffVolumes compares two snapshots of the removable volumes and returns
// the events that explain the difference: every volume present in prev but
// missing from cur unmounted, and every volume present in cur but missing
// from prev mounted. Events are ordered by key (unmounted before mounted)
// so the result is deterministic regardless of map iteration order.
//
// This is shared (no build tag) by every platform's watcher: each polls
// the mounted volumes on an interval rather than subscribing to OS mount
// events (Windows and Linux list the removable volumes, darwin everything
// in /Volumes).
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
