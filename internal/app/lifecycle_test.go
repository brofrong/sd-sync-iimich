package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// fakeImmich is an Immich server that accepts every upload except the
// file names in reject (answered 400) and remembers what was uploaded.
type fakeImmich struct {
	mu       sync.Mutex
	uploaded []string
	reject   map[string]bool
	// hold, when non-nil, keeps every upload waiting until it is closed
	// (or the request is cancelled); arrived gets a value per upload.
	hold    chan struct{}
	arrived chan struct{}
}

// useFakeImmich points a's config at a new fakeImmich.
func useFakeImmich(t *testing.T, a *App, reject ...string) *fakeImmich {
	t.Helper()
	return newFakeImmich(t, a, nil, reject...)
}

// useHeldImmich is useFakeImmich whose uploads hang until the test ends.
func useHeldImmich(t *testing.T, a *App) *fakeImmich {
	t.Helper()
	return newFakeImmich(t, a, make(chan struct{}))
}

func newFakeImmich(t *testing.T, a *App, hold chan struct{}, reject ...string) *fakeImmich {
	t.Helper()
	f := &fakeImmich{reject: map[string]bool{}, hold: hold, arrived: make(chan struct{}, 100)}
	for _, name := range reject {
		f.reject[name] = true
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/assets" {
			http.NotFound(w, r)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		name := r.FormValue("filename")
		select {
		case f.arrived <- struct{}{}:
		default:
		}
		if f.hold != nil {
			select {
			case <-f.hold:
			case <-r.Context().Done():
				return
			}
		}
		if f.reject[name] {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"message":"unsupported file"}`)
			return
		}
		f.mu.Lock()
		f.uploaded = append(f.uploaded, name)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"id-`+name+`","status":"created"}`)
	}))
	t.Cleanup(srv.Close)
	if hold != nil {
		t.Cleanup(func() { close(hold) }) // runs before srv.Close
	}
	if err := a.Config.Update(func(c *config.Config) { c.Immich.URL = srv.URL }); err != nil {
		t.Fatalf("Config.Update: %v", err)
	}
	return f
}

func (f *fakeImmich) uploads() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	got := slices.Clone(f.uploaded)
	sort.Strings(got)
	return got
}

// putPhotos creates DCIM/<name> files with distinct contents on the
// selected card of a.
func putPhotos(t *testing.T, a *App, names ...string) {
	t.Helper()
	dcim := filepath.Join(a.MountedTarget().MountPoint, "DCIM")
	if err := os.MkdirAll(dcim, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		path := filepath.Join(dcim, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		mtime := time.Unix(1_700_000_000+int64(i), 0)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
}

// statusLog records every published status code.
type statusLog struct {
	mu    sync.Mutex
	codes []Code
	ch    chan Status
}

func recordStatuses(a *App) *statusLog {
	l := &statusLog{ch: make(chan Status, 100)}
	a.OnStatus(func(s Status) {
		l.mu.Lock()
		l.codes = append(l.codes, s.Code)
		l.mu.Unlock()
		select {
		case l.ch <- s:
		default:
		}
	})
	return l
}

func (l *statusLog) all() []Code {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.codes)
}

// waitCode waits until a status with code is published.
func (l *statusLog) waitCode(t *testing.T, code Code) Status {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case s := <-l.ch:
			if s.Code == code {
				return s
			}
		case <-timeout:
			t.Fatalf("timed out waiting for status %q (got %v)", code, l.all())
		}
	}
}

// noticeLog records every Notice.
type noticeLog struct {
	mu      sync.Mutex
	notices []Notice
}

func recordNotices(a *App) *noticeLog {
	l := &noticeLog{}
	a.OnNotice(func(n Notice) {
		l.mu.Lock()
		l.notices = append(l.notices, n)
		l.mu.Unlock()
	})
	return l
}

func (l *noticeLog) all() []Notice {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.notices)
}

// others returns the notices other than sync_done, which every successful
// sync raises.
func (l *noticeLog) others() []Notice {
	var got []Notice
	for _, n := range l.all() {
		if n.Kind != NoticeSyncDone {
			got = append(got, n)
		}
	}
	return got
}

// ejectRecorder is a fake eject seam.
type ejectRecorder struct {
	mu    sync.Mutex
	calls []string
	err   error
	// during runs inside the fake eject, as the unmount event can arrive
	// before diskutil returns.
	during func(mountPoint string)
}

func fakeEject(a *App, err error) *ejectRecorder {
	r := &ejectRecorder{err: err}
	a.eject = func(mountPoint string) error {
		r.mu.Lock()
		r.calls = append(r.calls, mountPoint)
		during := r.during
		r.mu.Unlock()
		if during != nil {
			during(mountPoint)
		}
		return r.err
	}
	return r
}

func (r *ejectRecorder) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

func waitSyncFinished(t *testing.T, a *App) {
	t.Helper()
	waitFor(t, "sync to finish", func() bool { return !a.syncRunning() })
}

func TestSyncKeepsMacAwake(t *testing.T) {
	a := testApp(t)

	var mu sync.Mutex
	var events []string
	released := make(chan struct{})
	a.keepAwake = func() func() {
		mu.Lock()
		events = append(events, "awake")
		mu.Unlock()
		return func() {
			mu.Lock()
			events = append(events, "release")
			mu.Unlock()
			close(released)
		}
	}
	a.OnStatus(func(s Status) {
		mu.Lock()
		events = append(events, string(s.Code))
		mu.Unlock()
	})

	a.SyncNow()
	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for release")
	}
	waitSyncFinished(t, a)
	time.Sleep(20 * time.Millisecond) // nothing may follow the release

	mu.Lock()
	defer mu.Unlock()
	if len(events) < 3 || events[0] != "awake" {
		t.Fatalf("events = %v, want keepAwake first", events)
	}
	if events[len(events)-1] != "release" || events[len(events)-2] != string(CodeNothingNew) {
		t.Fatalf("events = %v, want release right after the final status", events)
	}
	if n := slices.Index(events, "release"); n != len(events)-1 {
		t.Fatalf("events = %v, want a single release at the end", events)
	}
}

func TestAutoEjectAfterDone(t *testing.T) {
	a := testApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG", "B.JPG")
	ej := fakeEject(a, nil)
	statuses := recordStatuses(a)
	mount := a.MountedTarget().MountPoint

	a.SyncNow()
	st := statuses.waitCode(t, CodeEjected)
	waitSyncFinished(t, a)

	if got := ej.all(); len(got) != 1 || got[0] != mount {
		t.Fatalf("eject calls = %v, want [%s]", got, mount)
	}
	if st.Params["uploaded"] != "2" {
		t.Errorf("ejected params = %v, want uploaded=2", st.Params)
	}
	if !slices.Equal(st.Actions, []Action{ActionOpenImmich}) {
		t.Errorf("ejected actions = %v, want [open_immich]", st.Actions)
	}
	if got := a.Status().Code; got != CodeEjected {
		t.Errorf("Status().Code = %q, want ejected", got)
	}
	// The run itself is recorded as done.
	runs, err := a.State.RecentRuns(10)
	if err != nil || len(runs) != 1 || runs[0].ResultCode != string(CodeDone) {
		t.Fatalf("runs = %+v, %v; want one done run", runs, err)
	}
}

func TestEjectedStatusNotReverted(t *testing.T) {
	a := testApp(t)
	a.revertAfter = 20 * time.Millisecond
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	fakeEject(a, nil)
	statuses := recordStatuses(a)

	a.SyncNow()
	statuses.waitCode(t, CodeEjected)
	waitSyncFinished(t, a)
	time.Sleep(10 * a.revertAfter)

	if got := a.Status().Code; got != CodeEjected {
		t.Fatalf("Status().Code = %q after the revert delay, want ejected kept (statuses %v)", got, statuses.all())
	}
}

// TestEjectedNotRevertedByEarlierSync: the revert timer of a sync that
// finished just before must not replace the ejected status of the next.
func TestEjectedNotRevertedByEarlierSync(t *testing.T) {
	a := testApp(t)
	a.revertAfter = 300 * time.Millisecond
	statuses := recordStatuses(a)

	a.SyncNow() // nothing_new: its revert is scheduled
	statuses.waitCode(t, CodeNothingNew)
	waitSyncFinished(t, a)

	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	fakeEject(a, nil)
	a.SyncNow()
	statuses.waitCode(t, CodeEjected)
	waitSyncFinished(t, a)
	time.Sleep(3 * a.revertAfter)

	if got := a.Status().Code; got != CodeEjected {
		t.Fatalf("Status().Code = %q, want ejected kept (statuses %v)", got, statuses.all())
	}
}

func TestDoneStillRevertsWithoutEject(t *testing.T) {
	a := testApp(t)
	a.revertAfter = 20 * time.Millisecond
	if err := a.Config.Update(func(c *config.Config) { c.Card.AutoEject = false }); err != nil {
		t.Fatal(err)
	}
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	statuses := recordStatuses(a)

	a.SyncNow()
	statuses.waitCode(t, CodeDone)
	waitFor(t, "done to revert to an idle status", func() bool { return a.Status().Code != CodeDone })
}

func TestNoAutoEjectAfterNothingNew(t *testing.T) {
	a := testApp(t)
	ej := fakeEject(a, nil)
	statuses := recordStatuses(a)

	a.SyncNow()
	statuses.waitCode(t, CodeNothingNew)
	waitSyncFinished(t, a)

	if got := ej.all(); len(got) != 0 {
		t.Fatalf("eject calls = %v after nothing_new, want none", got)
	}
}

func TestNoAutoEjectAfterErrors(t *testing.T) {
	a := testApp(t)
	useFakeImmich(t, a, "B.JPG")
	putPhotos(t, a, "A.JPG", "B.JPG")
	ej := fakeEject(a, nil)
	statuses := recordStatuses(a)

	a.SyncNow()
	statuses.waitCode(t, CodeDoneWithErrors)
	waitSyncFinished(t, a)

	if got := ej.all(); len(got) != 0 {
		t.Fatalf("eject calls = %v after done_with_errors, want none", got)
	}
}

func TestNoAutoEjectWhenDisabled(t *testing.T) {
	a := testApp(t)
	if err := a.Config.Update(func(c *config.Config) { c.Card.AutoEject = false }); err != nil {
		t.Fatal(err)
	}
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	ej := fakeEject(a, nil)
	statuses := recordStatuses(a)

	a.SyncNow()
	statuses.waitCode(t, CodeDone)
	waitSyncFinished(t, a)

	if got := ej.all(); len(got) != 0 {
		t.Fatalf("eject calls = %v with auto-eject off, want none", got)
	}
}

func TestAutoEjectFailureKeepsStatus(t *testing.T) {
	a := testApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	ej := fakeEject(a, errors.New("diskutil eject: resource busy"))
	notices := recordNotices(a)
	statuses := recordStatuses(a)

	a.SyncNow()
	statuses.waitCode(t, CodeDone)
	waitSyncFinished(t, a)
	time.Sleep(20 * time.Millisecond) // no retry may follow

	if got := ej.all(); len(got) != 1 {
		t.Fatalf("eject calls = %v, want exactly one (no retries)", got)
	}
	got := notices.others()
	if len(got) != 1 || got[0].Kind != NoticeEjectFailed {
		t.Fatalf("notices = %+v, want one eject_failed (and sync_done)", notices.all())
	}
	if got[0].Params["name"] != "CARD" || got[0].Params["detail"] == "" {
		t.Errorf("eject_failed params = %v, want name=CARD and a detail", got[0].Params)
	}
	if code := a.Status().Code; code != CodeDone {
		t.Errorf("Status().Code = %q, want done kept", code)
	}
	if slices.Contains(statuses.all(), CodeEjected) {
		t.Errorf("statuses = %v, want no ejected", statuses.all())
	}

	// The card was not ejected by the app: pulling it out now is a real
	// removal.
	a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: volume.Info{UUID: "VOL-UUID", Name: "CARD", MountPoint: ej.all()[0]}})
	if code := a.Status().Code; code == CodeDone {
		t.Errorf("Status().Code = %q after the card was pulled out, want an idle status", code)
	}
}

func TestOwnEjectUnmountKeepsStatus(t *testing.T) {
	a := testApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	vol := *a.MountedTarget()
	ej := fakeEject(a, nil)
	// The unmount event arrives while diskutil is still ejecting, i.e.
	// while the sync goroutine is still running.
	ej.during = func(mountPoint string) {
		a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: vol})
	}
	statuses := recordStatuses(a)

	a.SyncNow()
	statuses.waitCode(t, CodeEjected)
	waitSyncFinished(t, a)

	if mt := a.MountedTarget(); mt != nil {
		t.Errorf("MountedTarget() = %+v after the eject, want nil", mt)
	}
	if a.cardRemoved.Load() {
		t.Error("own eject was taken for the card being pulled out (sync cancelled)")
	}
	for _, c := range statuses.all() {
		if c == CodeWaitingForCard || c == CodeOtherCardMounted || c == CodeCardRemoved {
			t.Errorf("statuses = %v, want no idle/card_removed status after own eject", statuses.all())
			break
		}
	}
	if got := a.Status().Code; got != CodeEjected {
		t.Errorf("Status().Code = %q, want ejected", got)
	}

	a.mu.RLock()
	marker := a.ejecting
	a.mu.RUnlock()
	if marker != "" {
		t.Errorf("own-eject marker = %q after its unmount, want cleared", marker)
	}

	// A later unmount of the same mount point is not ours any more: it is
	// handled as usual (idle status).
	a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: vol})
	if got := a.Status().Code; got != CodeWaitingForCard {
		t.Errorf("Status().Code = %q after a later unmount, want waiting_for_card", got)
	}
}

// TestOtherMountReplacesEjected: another card plugged in after the eject
// shows other_card_mounted instead of ejected.
func TestOtherMountReplacesEjected(t *testing.T) {
	a := testApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	vol := *a.MountedTarget()
	ej := fakeEject(a, nil)
	ej.during = func(string) { a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: vol}) }
	statuses := recordStatuses(a)

	a.SyncNow()
	statuses.waitCode(t, CodeEjected)
	waitSyncFinished(t, a)

	other := volume.Info{UUID: "OTHER-UUID", Name: "USB", MountPoint: t.TempDir()}
	a.handleVolumeEvent(volume.Event{Type: volume.EventMounted, Info: other})

	st := a.Status()
	if st.Code != CodeOtherCardMounted || st.Params["mounted_uuid"] != "OTHER-UUID" {
		t.Fatalf("Status() = %q %v after another card was mounted, want other_card_mounted of OTHER-UUID", st.Code, st.Params)
	}
}

// TestStopBeforeEjectSkipsEject: StopSync between the done status and the
// eject means no eject.
func TestStopBeforeEjectSkipsEject(t *testing.T) {
	a := testApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	ej := fakeEject(a, nil)
	finished := make(chan struct{})
	a.OnStatus(func(s Status) {
		if s.Code == CodeDone {
			a.StopSync()
			close(finished)
		}
	})

	a.SyncNow()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for done")
	}
	waitSyncFinished(t, a)

	if got := ej.all(); len(got) != 0 {
		t.Fatalf("eject calls = %v after StopSync, want none", got)
	}
	if got := a.Status().Code; got != CodeDone {
		t.Errorf("Status().Code = %q, want done", got)
	}
}

// TestCardGoneBeforeEjectSkipsEject: the card is no longer the mounted
// target when the post-sync phase starts: no eject.
func TestCardGoneBeforeEjectSkipsEject(t *testing.T) {
	a := testApp(t)
	useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG")
	ej := fakeEject(a, nil)
	finished := make(chan struct{})
	a.OnStatus(func(s Status) {
		if s.Code == CodeDone {
			a.mu.Lock()
			a.mountedTarget = nil
			a.mu.Unlock()
			close(finished)
		}
	})

	a.SyncNow()
	select {
	case <-finished:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for done")
	}
	waitSyncFinished(t, a)

	if got := ej.all(); len(got) != 0 {
		t.Fatalf("eject calls = %v with the card gone, want none", got)
	}
}

func TestFollowCandidate(t *testing.T) {
	camera := func(mount string) bool { return mount != "/Volumes/NOTCAM" }
	cfg := config.Default()
	cfg.Immich = config.ImmichConfig{URL: "https://immich.example", APIKey: "key"}
	cfg.Watch = config.WatchConfig{VolumeUUID: "OLD-UUID", VolumeName: "Brofrong"}

	newCard := volume.Info{UUID: "NEW-UUID", Name: "BROFRONG", MountPoint: "/Volumes/BROFRONG"}
	tests := []struct {
		name    string
		cfg     func(config.Config) config.Config
		mounted []volume.Info
		want    bool
	}{
		{"one same-name camera card", nil, []volume.Info{newCard}, true},
		{"one same-name camera card among others", nil, []volume.Info{
			{UUID: "USB", Name: "USB", MountPoint: "/Volumes/USB"}, newCard,
		}, true},
		{"two same-name cards", nil, []volume.Info{
			newCard, {UUID: "NEW2", Name: "brofrong", MountPoint: "/Volumes/brofrong 1"},
		}, false},
		{"two same-name cards, only one a camera card", nil, []volume.Info{
			newCard, {UUID: "NEW2", Name: "Brofrong", MountPoint: "/Volumes/NOTCAM"},
		}, false},
		{"selected card is mounted", nil, []volume.Info{
			{UUID: "old-uuid", Name: "Brofrong", MountPoint: "/Volumes/Brofrong"}, newCard,
		}, false},
		{"not a camera card", nil, []volume.Info{{UUID: "NEW-UUID", Name: "Brofrong", MountPoint: "/Volumes/NOTCAM"}}, false},
		{"other name", nil, []volume.Info{{UUID: "NEW-UUID", Name: "Untitled", MountPoint: "/Volumes/Untitled"}}, false},
		{"nothing mounted", nil, nil, false},
		{"no card selected", func(c config.Config) config.Config { c.Watch = config.WatchConfig{}; return c }, []volume.Info{newCard}, false},
		{"server not configured", func(c config.Config) config.Config { c.Immich = config.ImmichConfig{}; return c }, []volume.Info{newCard}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := cfg
			if tt.cfg != nil {
				c = tt.cfg(c)
			}
			got, ok := followCandidate(c, tt.mounted, camera)
			if ok != tt.want {
				t.Fatalf("followCandidate ok = %v (%+v), want %v", ok, got, tt.want)
			}
			if ok && got.UUID != "NEW-UUID" {
				t.Errorf("candidate = %+v, want NEW-UUID", got)
			}
		})
	}
}

func TestIsCameraCardDir(t *testing.T) {
	empty := t.TempDir()
	dcim := t.TempDir()
	if err := os.Mkdir(filepath.Join(dcim, "DCIM"), 0o755); err != nil {
		t.Fatal(err)
	}
	sony := t.TempDir()
	if err := os.MkdirAll(filepath.Join(sony, "PRIVATE", "M4ROOT"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := t.TempDir()
	if err := os.WriteFile(filepath.Join(file, "DCIM"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for dir, want := range map[string]bool{empty: false, dcim: true, sony: true, file: false} {
		if got := isCameraCardDir(dir); got != want {
			t.Errorf("isCameraCardDir(%s) = %v, want %v", dir, got, want)
		}
	}
}

// formattedCard sets a up as if the selected card ("CARD", VOL-UUID, with
// remembered state) was formatted elsewhere: it is not mounted, and a
// camera card with the same name and a new UUID is.
func formattedCard(t *testing.T, a *App) volume.Info {
	t.Helper()
	rememberCard(t, a, "VOL-UUID")
	a.mu.Lock()
	a.mountedTarget = nil
	a.mu.Unlock()
	newCard := volume.Info{UUID: "NEW-UUID", Name: "card", MountPoint: t.TempDir()}
	a.listRemovable = func() ([]volume.Info, error) { return []volume.Info{newCard}, nil }
	a.isCameraCard = func(mount string) bool { return mount == newCard.MountPoint }
	return newCard
}

func checkFollowed(t *testing.T, a *App, newCard volume.Info, notices *noticeLog) {
	t.Helper()
	waitFor(t, "a run of the new card", func() bool {
		runs, err := a.State.RecentRuns(10)
		return err == nil && len(runs) > 0 && runs[0].FinishedAt != nil
	})
	waitSyncFinished(t, a)

	cfg := a.Config.Get()
	if cfg.Watch.VolumeUUID != "NEW-UUID" || cfg.Watch.VolumeName != "card" {
		t.Errorf("watch = %+v, want NEW-UUID/card", cfg.Watch)
	}
	if mt := a.MountedTarget(); mt == nil || mt.UUID != "NEW-UUID" {
		t.Errorf("MountedTarget() = %+v, want the new card", mt)
	}
	if got := rememberedUUIDs(t, a); slices.Contains(got, "VOL-UUID") {
		t.Errorf("remembered = %v, want the old card forgotten", got)
	}
	got := notices.all()
	if len(got) != 1 || got[0].Kind != NoticeCardFollowed || got[0].Params["name"] != "card" {
		t.Errorf("notices = %+v, want one card_followed name=card", got)
	}
	runs, err := a.State.RecentRuns(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].VolumeUUID != "NEW-UUID" {
		t.Errorf("runs = %+v, want exactly one run of NEW-UUID", runs)
	}
}

func TestFollowFormattedCardOnMount(t *testing.T) {
	a := testApp(t)
	newCard := formattedCard(t, a)
	notices := recordNotices(a)

	a.handleVolumeEvent(volume.Event{Type: volume.EventMounted, Info: newCard})

	checkFollowed(t, a, newCard, notices)
}

func TestFollowFormattedCardAtStartup(t *testing.T) {
	a := testApp(t)
	newCard := formattedCard(t, a)
	notices := recordNotices(a)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(stopped)
	}()
	checkFollowed(t, a, newCard, notices)
	cancel()
	<-stopped
}

func TestNoFollowWhenNotCameraCard(t *testing.T) {
	a := testApp(t)
	newCard := formattedCard(t, a)
	a.isCameraCard = func(string) bool { return false }
	notices := recordNotices(a)

	a.handleVolumeEvent(volume.Event{Type: volume.EventMounted, Info: newCard})

	if got := a.Config.Get().Watch.VolumeUUID; got != "VOL-UUID" {
		t.Errorf("selected = %q, want VOL-UUID kept", got)
	}
	if got := notices.all(); len(got) != 0 {
		t.Errorf("notices = %+v, want none", got)
	}
}

func TestNoFollowRememberedCard(t *testing.T) {
	a := testApp(t)
	newCard := formattedCard(t, a)
	rememberCard(t, a, "NEW-UUID") // synced before: not a fresh format
	notices := recordNotices(a)

	a.handleVolumeEvent(volume.Event{Type: volume.EventMounted, Info: newCard})

	if got := a.Config.Get().Watch.VolumeUUID; got != "VOL-UUID" {
		t.Errorf("selected = %q, want VOL-UUID kept", got)
	}
	if got := rememberedUUIDs(t, a); !slices.Contains(got, "VOL-UUID") {
		t.Errorf("remembered = %v, want VOL-UUID kept", got)
	}
	if got := notices.all(); len(got) != 0 {
		t.Errorf("notices = %+v, want none", got)
	}
	if got := a.Status().Code; got != CodeOtherCardMounted {
		t.Errorf("Status().Code = %q, want other_card_mounted", got)
	}
}

// TestPullFollowedCardAfterMissedEjectUnmount: the unmount of an earlier
// own eject never arrived, and the formatted card is mounted at the same
// mount point. Pulling it out mid-sync is a real removal.
func TestPullFollowedCardAfterMissedEjectUnmount(t *testing.T) {
	a := testApp(t)
	srv := useHeldImmich(t, a)
	newCard := formattedCard(t, a)
	if err := os.MkdirAll(filepath.Join(newCard.MountPoint, "DCIM"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(newCard.MountPoint, "DCIM", "A.JPG"), []byte("A"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.ejecting = newCard.MountPoint // the missed unmount of the old card
	a.mu.Unlock()
	statuses := recordStatuses(a)

	a.handleVolumeEvent(volume.Event{Type: volume.EventMounted, Info: newCard})
	select {
	case <-srv.arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the followed card's upload")
	}
	a.handleVolumeEvent(volume.Event{Type: volume.EventUnmounted, Info: newCard})

	statuses.waitCode(t, CodeCardRemoved)
	waitSyncFinished(t, a)
}

func TestFailuresRecordedWithRun(t *testing.T) {
	a := testApp(t)
	useFakeImmich(t, a, "B.JPG")
	putPhotos(t, a, "A.JPG", "B.JPG")
	statuses := recordStatuses(a)

	a.SyncNow()
	statuses.waitCode(t, CodeDoneWithErrors)
	waitSyncFinished(t, a)

	runs, err := a.State.RecentRuns(10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, %v; want one", runs, err)
	}
	if !runs[0].FailuresRecorded {
		t.Error("FailuresRecorded = false, want true")
	}
	failures, err := a.State.RunFailures(runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].RelativePath != "DCIM/B.JPG" || failures[0].Kind == "" {
		t.Errorf("failures = %+v, want DCIM/B.JPG", failures)
	}
}

func TestFailuresRecordedForCleanRun(t *testing.T) {
	a := testApp(t)
	statuses := recordStatuses(a)

	a.SyncNow()
	statuses.waitCode(t, CodeNothingNew)
	waitSyncFinished(t, a)

	runs, err := a.State.RecentRuns(10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("runs = %+v, %v; want one", runs, err)
	}
	if !runs[0].FailuresRecorded {
		t.Error("FailuresRecorded = false for a run without failures, want true (recorded, empty)")
	}
}

// recordedRun stores a finished run of card uuid with the given failures.
func recordedRun(t *testing.T, a *App, uuid string, failed ...string) int64 {
	t.Helper()
	id, err := a.State.StartRun(uuid, "CARD", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Now().Add(-time.Hour)
	if err := a.State.FinishRun(state.SyncRun{ID: id, FinishedAt: &finished, ResultCode: string(CodeDoneWithErrors), Errors: len(failed)}); err != nil {
		t.Fatal(err)
	}
	var failures []state.FileFailure
	for _, p := range failed {
		failures = append(failures, state.FileFailure{RelativePath: p, Kind: "rejected", Detail: "400"})
	}
	if err := a.State.RecordFailures(id, failures); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRetryFailuresSyncsOnlyThem(t *testing.T) {
	a := testApp(t)
	if err := a.Config.Update(func(c *config.Config) { c.Card.AutoEject = false }); err != nil {
		t.Fatal(err)
	}
	srv := useFakeImmich(t, a)
	putPhotos(t, a, "A.JPG", "B.JPG", "C.JPG")
	runID := recordedRun(t, a, "VOL-UUID", "DCIM/B.JPG", "DCIM/C.JPG")
	statuses := recordStatuses(a)

	if err := a.RetryFailures(runID); err != nil {
		t.Fatalf("RetryFailures: %v", err)
	}
	st := statuses.waitCode(t, CodeDone)
	waitSyncFinished(t, a)

	if got := srv.uploads(); !slices.Equal(got, []string{"B.JPG", "C.JPG"}) {
		t.Errorf("uploaded = %v, want [B.JPG C.JPG]", got)
	}
	if st.Params["uploaded"] != "2" {
		t.Errorf("done params = %v, want uploaded=2", st.Params)
	}
	runs, err := a.State.RecentRuns(10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs = %+v, %v; want the retry recorded as a new run", runs, err)
	}
}

func TestRetryFailuresUnavailable(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, a *App) int64
	}{
		{"card not mounted", func(t *testing.T, a *App) int64 {
			a.mu.Lock()
			a.mountedTarget = nil
			a.mu.Unlock()
			return recordedRun(t, a, "VOL-UUID", "DCIM/B.JPG")
		}},
		{"run of another card", func(t *testing.T, a *App) int64 {
			return recordedRun(t, a, "OTHER-UUID", "DCIM/B.JPG")
		}},
		{"sync running", func(t *testing.T, a *App) int64 {
			a.syncCancel = func() {}
			return recordedRun(t, a, "VOL-UUID", "DCIM/B.JPG")
		}},
		{"no failures", func(t *testing.T, a *App) int64 {
			return recordedRun(t, a, "VOL-UUID")
		}},
		{"failures not recorded", func(t *testing.T, a *App) int64 {
			id, err := a.State.StartRun("VOL-UUID", "CARD", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			return id
		}},
		{"unknown run", func(t *testing.T, a *App) int64 { return 12345 }},
		{"server not configured", func(t *testing.T, a *App) int64 {
			if err := a.Config.Update(func(c *config.Config) { c.Immich.APIKey = "" }); err != nil {
				t.Fatal(err)
			}
			return recordedRun(t, a, "VOL-UUID", "DCIM/B.JPG")
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := testApp(t)
			runID := tt.setup(t, a)
			before, err := a.State.RecentRuns(100)
			if err != nil {
				t.Fatal(err)
			}
			a.resumer.Start() // a pending auto-resume must survive

			if err := a.RetryFailures(runID); !errors.Is(err, ErrRetryUnavailable) {
				t.Fatalf("RetryFailures = %v, want ErrRetryUnavailable", err)
			}
			if !a.resumer.Waiting() {
				t.Error("RetryFailures stopped the pending auto-resume, want it kept")
			}
			time.Sleep(50 * time.Millisecond) // a started sync would have recorded its run by now
			after, err := a.State.RecentRuns(100)
			if err != nil {
				t.Fatal(err)
			}
			if len(after) != len(before) {
				t.Errorf("runs = %d after RetryFailures, want %d (no sync started)", len(after), len(before))
			}
		})
	}
}

func TestOnNoticeNotRegistered(t *testing.T) {
	a := testApp(t)
	a.notify(Notice{Kind: NoticeEjectFailed}) // must not panic
}

// syncDoneNotices returns the sync_done notices among got.
func syncDoneNotices(got []Notice) []Notice {
	var done []Notice
	for _, n := range got {
		if n.Kind == NoticeSyncDone {
			done = append(done, n)
		}
	}
	return done
}

func TestSyncDoneNotice(t *testing.T) {
	tests := []struct {
		name      string
		autoEject bool
		ejectErr  error
		reject    []string
		photos    []string
		final     Code
		want      int    // sync_done notices
		ejected   string // their "ejected" param
		uploaded  string
	}{
		{name: "ejected", autoEject: true, photos: []string{"A.JPG", "B.JPG"}, final: CodeEjected, want: 1, ejected: "true", uploaded: "2"},
		{name: "eject off", photos: []string{"A.JPG"}, final: CodeDone, want: 1, ejected: "false", uploaded: "1"},
		{name: "eject failed", autoEject: true, ejectErr: errors.New("resource busy"), photos: []string{"A.JPG"}, final: CodeDone, want: 1, ejected: "false", uploaded: "1"},
		{name: "nothing new", autoEject: true, final: CodeNothingNew},
		{name: "errors", autoEject: true, reject: []string{"B.JPG"}, photos: []string{"A.JPG", "B.JPG"}, final: CodeDoneWithErrors},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := testApp(t)
			if err := a.Config.Update(func(c *config.Config) { c.Card.AutoEject = tt.autoEject }); err != nil {
				t.Fatal(err)
			}
			if len(tt.photos) > 0 {
				useFakeImmich(t, a, tt.reject...)
				putPhotos(t, a, tt.photos...)
			}
			fakeEject(a, tt.ejectErr)
			notices := recordNotices(a)
			statuses := recordStatuses(a)

			a.SyncNow()
			statuses.waitCode(t, tt.final)
			waitSyncFinished(t, a)

			got := syncDoneNotices(notices.all())
			if len(got) != tt.want {
				t.Fatalf("sync_done notices = %+v (all %+v), want %d", got, notices.all(), tt.want)
			}
			if tt.want == 0 {
				return
			}
			if p := got[0].Params; p["ejected"] != tt.ejected || p["uploaded"] != tt.uploaded {
				t.Errorf("sync_done params = %v, want ejected=%s uploaded=%s", p, tt.ejected, tt.uploaded)
			}
		})
	}
}
