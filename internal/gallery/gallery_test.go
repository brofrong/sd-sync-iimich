package gallery

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/testenv"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// cardDir is where the fixtures put files on a card: a scan path of the
// default config.
const cardDir = "DCIM/100MSDCF"

// base is a whole second, as the database stores mtimes in seconds.
var base = time.Date(2026, 10, 1, 12, 0, 0, 0, time.Local)

// newTestGallery returns a Gallery over the default config and an empty
// database, all in temporary directories, with its clock at base.
func newTestGallery(t *testing.T) (*Gallery, *state.DB) {
	t.Helper()
	testenv.Home(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	g := New(cfg, st)
	clock := base
	g.now = func() time.Time { return clock }
	return g, st
}

// advance moves g's clock forward by d.
func advance(g *Gallery, d time.Duration) {
	now := g.now().Add(d)
	g.now = func() time.Time { return now }
}

// newCard returns a temporary directory acting as a mounted card.
func newCard(t *testing.T, uuid string) volume.Info {
	t.Helper()
	return volume.Info{Name: "CARD-" + uuid, MountPoint: t.TempDir(), UUID: uuid, Removable: true}
}

// putFile writes data to name in the card's cardDir with the given mtime
// and returns its relative path.
func putFile(t *testing.T, vol volume.Info, name string, data []byte, mtime time.Time) string {
	t.Helper()
	dir := filepath.Join(vol.MountPoint, filepath.FromSlash(cardDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	return cardDir + "/" + name
}

func paths(l List) []string {
	out := make([]string, len(l.Photos))
	for i, p := range l.Photos {
		out[i] = p.Path
	}
	return out
}

func mustList(t *testing.T, g *Gallery, vol volume.Info) List {
	t.Helper()
	l, err := g.List(vol)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	return l
}

func genOf(t *testing.T, p Photo) uint64 {
	t.Helper()
	var gen, i uint64
	if _, err := fmt.Sscanf(p.Thumb, "/gallery/thumb/%d/%d", &gen, &i); err != nil {
		t.Fatalf("Thumb %q: %v", p.Thumb, err)
	}
	return gen
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestListOnlyJPEGNewestFirst(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	jpg := []byte("jpeg bytes")
	p1 := putFile(t, vol, "DSC00001.JPG", jpg, base)
	p2 := putFile(t, vol, "DSC00002.jpg", []byte("longer jpeg bytes"), base.Add(time.Hour))
	p3 := putFile(t, vol, "DSC00003.JPEG", jpg, base.Add(2*time.Hour))
	// Same mtime as DSC00001: the later path comes first.
	p0 := putFile(t, vol, "DSC00009.JPG", jpg, base)
	putFile(t, vol, "DSC00004.ARW", []byte("raw"), base.Add(3*time.Hour))
	putFile(t, vol, "C0001.MP4", []byte("video"), base.Add(4*time.Hour))
	putFile(t, vol, "DSC00001.JPG.xmp", []byte("<xmp/>"), base)

	l := mustList(t, g, vol)

	if want := []string{p3, p2, p0, p1}; !equal(paths(l), want) {
		t.Fatalf("photos = %v, want %v", paths(l), want)
	}
	if l.Other != 2 {
		t.Errorf("Other = %d, want 2 (ARW and MP4)", l.Other)
	}
	if l.CardUUID != vol.UUID || l.CardName != vol.Name {
		t.Errorf("card = %q %q, want %q %q", l.CardUUID, l.CardName, vol.UUID, vol.Name)
	}
	p := l.Photos[1]
	if p.Name != "DSC00002.jpg" || p.Size != int64(len("longer jpeg bytes")) || !p.TakenAt.Equal(base.Add(time.Hour)) {
		t.Errorf("photo 1 = %+v", p)
	}
	gen := genOf(t, l.Photos[0])
	for i, p := range l.Photos {
		if want := fmt.Sprintf("/gallery/thumb/%d/%d", gen, i); p.Thumb != want {
			t.Errorf("photo %d Thumb = %q, want %q", i, p.Thumb, want)
		}
		if want := fmt.Sprintf("/gallery/photo/%d/%d", gen, i); p.Src != want {
			t.Errorf("photo %d Src = %q, want %q", i, p.Src, want)
		}
	}
}

func TestListEmptyCard(t *testing.T) {
	g, _ := newTestGallery(t)
	l := mustList(t, g, newCard(t, "CARD-A"))
	if l.Photos == nil || len(l.Photos) != 0 || l.Other != 0 {
		t.Fatalf("List = %+v, want an empty, non-nil photo list", l)
	}
}

func TestListStatuses(t *testing.T) {
	g, st := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	jpg := []byte("jpeg")
	behind := putFile(t, vol, "A.JPG", jpg, base)
	failed := putFile(t, vol, "B.JPG", jpg, base.Add(time.Minute))
	uploaded := putFile(t, vol, "C.JPG", jpg, base.Add(2*time.Minute))
	waiting := putFile(t, vol, "D.JPG", jpg, base.Add(3*time.Minute))
	retried := putFile(t, vol, "E.JPG", jpg, base.Add(4*time.Minute))
	oldFailure := putFile(t, vol, "F.JPG", jpg, base.Add(5*time.Minute))

	if err := st.SetVolumeSync(vol.UUID, base, behind); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{uploaded, retried} {
		if err := st.MarkHashed(vol.UUID, p, "sha-"+p, 4, base); err != nil {
			t.Fatal(err)
		}
		if err := st.MarkUploaded(vol.UUID, p, base); err != nil {
			t.Fatal(err)
		}
	}
	// An older finished run failed F; the last one failed B and E (E was
	// uploaded since, by a retry).
	finishRun(t, st, vol, base, oldFailure)
	finishRun(t, st, vol, base.Add(time.Hour), failed, retried)
	// Another card's failures and an unfinished run do not count.
	finishRun(t, st, newCard(t, "CARD-B"), base.Add(2*time.Hour), waiting)
	if _, err := st.StartRun(vol.UUID, vol.Name, base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{
		behind:     "in_immich",
		failed:     "failed",
		uploaded:   "in_immich",
		waiting:    "waiting",
		retried:    "in_immich",
		oldFailure: "waiting",
	}
	check := func(l List) {
		t.Helper()
		if len(l.Photos) != len(want) {
			t.Fatalf("got %d photos, want %d", len(l.Photos), len(want))
		}
		for _, p := range l.Photos {
			if p.Status != want[p.Path] {
				t.Errorf("%s status = %q, want %q", p.Path, p.Status, want[p.Path])
			}
		}
	}
	check(mustList(t, g, vol))

	// Statuses are read again on every call, even from a cached scan.
	if err := st.MarkHashed(vol.UUID, waiting, "sha", 4, base); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkUploaded(vol.UUID, waiting, base); err != nil {
		t.Fatal(err)
	}
	want[waiting] = "in_immich"
	check(mustList(t, g, vol))
}

// finishRun records a finished sync run of vol that failed the given paths.
func finishRun(t *testing.T, st *state.DB, vol volume.Info, at time.Time, failed ...string) {
	t.Helper()
	id, err := st.StartRun(vol.UUID, vol.Name, at)
	if err != nil {
		t.Fatal(err)
	}
	failures := make([]state.FileFailure, len(failed))
	for i, p := range failed {
		failures[i] = state.FileFailure{RelativePath: p, Kind: "upload_failed", Detail: "test"}
	}
	if err := st.RecordFailures(id, failures); err != nil {
		t.Fatal(err)
	}
	done := at.Add(time.Minute)
	if err := st.FinishRun(state.SyncRun{ID: id, StartedAt: at, FinishedAt: &done, VolumeUUID: vol.UUID, VolumeName: vol.Name, ResultCode: "done", Errors: len(failed)}); err != nil {
		t.Fatal(err)
	}
}

func TestListRescansAfterCardChange(t *testing.T) {
	g, _ := newTestGallery(t)
	a := newCard(t, "CARD-A")
	a1 := putFile(t, a, "A1.JPG", []byte("a1"), base)
	a2 := putFile(t, a, "A2.JPG", []byte("a2"), base.Add(time.Minute))
	b := newCard(t, "CARD-B")
	b1 := putFile(t, b, "B1.JPG", []byte("b1"), base)

	la := mustList(t, g, a)
	if want := []string{a2, a1}; !equal(paths(la), want) {
		t.Fatalf("card A photos = %v, want %v", paths(la), want)
	}
	lb := mustList(t, g, b)
	if want := []string{b1}; !equal(paths(lb), want) {
		t.Fatalf("card B photos = %v, want %v", paths(lb), want)
	}
	if lb.CardUUID != b.UUID {
		t.Errorf("CardUUID = %q, want %q", lb.CardUUID, b.UUID)
	}
	if genOf(t, lb.Photos[0]) == genOf(t, la.Photos[0]) {
		t.Error("a new card kept the old generation")
	}

	// The same card at another mount point is scanned there.
	moved := b
	moved.MountPoint = a.MountPoint
	if got := paths(mustList(t, g, moved)); !equal(got, []string{a2, a1}) {
		t.Fatalf("after remount photos = %v, want %v", got, []string{a2, a1})
	}
}

func TestListCachesScan(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	p1 := putFile(t, vol, "A1.JPG", []byte("a1"), base)
	first := mustList(t, g, vol)

	p2 := putFile(t, vol, "A2.JPG", []byte("a2"), base.Add(time.Minute))
	advance(g, 59*time.Second)
	if got := paths(mustList(t, g, vol)); !equal(got, []string{p1}) {
		t.Fatalf("within 60 s photos = %v, want the cached %v", got, []string{p1})
	}

	advance(g, time.Second)
	l := mustList(t, g, vol)
	if got := paths(l); !equal(got, []string{p2, p1}) {
		t.Fatalf("after 60 s photos = %v, want %v", got, []string{p2, p1})
	}
	if genOf(t, l.Photos[0]) == genOf(t, first.Photos[0]) {
		t.Error("a changed scan kept the old generation")
	}

	// A rescan that finds the same files keeps the generation, so the
	// window's images (and the thumbnail cache) stay valid.
	advance(g, time.Minute)
	again := mustList(t, g, vol)
	if genOf(t, again.Photos[0]) != genOf(t, l.Photos[0]) {
		t.Error("an unchanged rescan started a new generation")
	}
}

func TestClearForgetsScan(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	p1 := putFile(t, vol, "A1.JPG", []byte("a1"), base)
	first := mustList(t, g, vol)

	g.Clear()
	p2 := putFile(t, vol, "A2.JPG", []byte("a2"), base.Add(time.Minute))
	l := mustList(t, g, vol)
	if got := paths(l); !equal(got, []string{p2, p1}) {
		t.Fatalf("after Clear photos = %v, want a fresh scan %v", got, []string{p2, p1})
	}
	if genOf(t, l.Photos[0]) <= genOf(t, first.Photos[0]) {
		t.Error("the generation did not grow after Clear")
	}
}
