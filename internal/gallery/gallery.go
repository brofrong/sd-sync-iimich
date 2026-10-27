package gallery

import (
	"fmt"
	"math/rand/v2"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// Photo statuses, as the window shows them ("uploading" is added there from
// the sync progress).
const (
	// StatusInImmich is a photo behind the card's sync cursor or recorded
	// as uploaded.
	StatusInImmich = "in_immich"
	// StatusFailed is a photo the card's last finished sync failed.
	StatusFailed = "failed"
	// StatusWaiting is any other photo: it goes up with the next sync.
	StatusWaiting = "waiting"
)

const (
	// scanTTL is how long a card's scan is reused before it is redone.
	scanTTL = 60 * time.Second
	// maxReads is the most card reads (thumbnails, photos) at once, so the
	// gallery does not take the card away from the upload.
	maxReads = 3
	// maxThumbCache is how many bytes of thumbnails are kept in memory per
	// generation; past it, thumbnails are made again when asked for.
	maxThumbCache = 128 << 20
)

// Photo is one JPEG on the card.
type Photo struct {
	// Path is relative to the card's root, as in the sync progress.
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	TakenAt time.Time `json:"takenAt"`
	Size    int64     `json:"size"`
	// Status is StatusInImmich, StatusFailed or StatusWaiting.
	Status string `json:"status"`
	// Thumb and Src are the thumbnail's and the photo's URLs on the
	// window's asset server: "/gallery/thumb/<gen>/<i>", "/gallery/photo/<gen>/<i>".
	Thumb string `json:"thumb"`
	Src   string `json:"src"`
}

// List is what the gallery shows of a card.
type List struct {
	CardUUID string `json:"cardUuid"`
	CardName string `json:"cardName"`
	// Photos are newest first: by TakenAt, then by Path, both descending.
	Photos []Photo `json:"photos"`
	// Other counts the media found that are not JPEG (RAW, video).
	Other int `json:"other"`
}

// photoFile is one scanned JPEG, as the handler needs it.
type photoFile struct {
	abs   string
	rel   string
	name  string
	mtime time.Time
	size  int64
}

// scanKey identifies what was scanned: a card at a mount point.
type scanKey struct {
	uuid  string
	mount string
}

// Gallery lists the photos on the mounted card and serves their thumbnails
// and files to the window by id. Ids are indexes into the photo list of one
// generation; a generation starts whenever a scan finds a different card or
// different files, so ids from an older list are refused. It is safe for
// concurrent use.
type Gallery struct {
	cfg *config.Store
	st  *state.DB
	// reads holds a slot for each card read in progress (maxReads).
	reads chan struct{}
	// now and thumbnail are time.Now and Thumbnail, replaced by tests.
	now       func() time.Time
	thumbnail func(path string) ([]byte, error)

	// scanMu lets one List scan at a time; mu is not held while scanning,
	// so images are served meanwhile.
	scanMu sync.Mutex

	mu        sync.Mutex
	loaded    bool // photos hold a scan of key
	key       scanKey
	scannedAt time.Time
	clears    int // Clear calls, to drop a scan that was running across one
	gen       uint64
	photos    []photoFile // newest first; never changed once set
	other     int
	thumbs    map[int][]byte
	thumbSize int
	warned    bool // a read error was logged in this generation
}

// New returns a Gallery reading the scan settings from cfg and the photos'
// sync state from st. Its generations start at a random number, so ids do
// not repeat those of an earlier launch, which the window may still have
// cached.
func New(cfg *config.Store, st *state.DB) *Gallery {
	return &Gallery{
		cfg: cfg,
		st:  st,
		// Below 2^52: plenty of room to count up, and exact as a float.
		gen:       rand.Uint64N(1 << 52),
		reads:     make(chan struct{}, maxReads),
		now:       time.Now,
		thumbnail: Thumbnail,
	}
}

// List returns the JPEG photos on the card vol, newest first, with their
// sync status. The card's scan is reused for scanTTL while the card (UUID
// and mount point) stays the same; statuses are read from the database on
// every call, so calling List often during a sync is cheap.
func (g *Gallery) List(vol volume.Info) (List, error) {
	gen, photos, other, err := g.scan(vol)
	if err != nil {
		return List{}, err
	}
	status, err := g.statuses(vol.UUID)
	if err != nil {
		return List{}, err
	}

	out := List{
		CardUUID: vol.UUID,
		CardName: vol.Name,
		Photos:   make([]Photo, len(photos)),
		Other:    other,
	}
	for i, f := range photos {
		out.Photos[i] = Photo{
			Path:    f.rel,
			Name:    f.name,
			TakenAt: f.mtime,
			Size:    f.size,
			Status:  status(f),
			Thumb:   fmt.Sprintf("/gallery/thumb/%d/%d", gen, i),
			Src:     fmt.Sprintf("/gallery/photo/%d/%d", gen, i),
		}
	}
	return out, nil
}

// Clear forgets the scan and the thumbnails (the card was removed): no id
// is served until the next List, which scans again.
func (g *Gallery) Clear() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.clears++
	g.loaded = false
	g.key = scanKey{}
	g.photos = nil
	g.other = 0
	g.thumbs = nil
	g.thumbSize = 0
}

// scan returns the generation, photos and non-JPEG count for vol, from the
// cached scan when it is fresh, else scanning the card.
func (g *Gallery) scan(vol volume.Info) (gen uint64, photos []photoFile, other int, err error) {
	g.scanMu.Lock()
	defer g.scanMu.Unlock()

	key := scanKey{uuid: vol.UUID, mount: vol.MountPoint}
	now := g.now()
	g.mu.Lock()
	if age := now.Sub(g.scannedAt); g.loaded && g.key == key && age >= 0 && age < scanTTL {
		defer g.mu.Unlock()
		return g.gen, g.photos, g.other, nil
	}
	clears := g.clears
	g.mu.Unlock()

	files, err := scanner.ScanVolume(vol.MountPoint, g.cfg.Get())
	if err != nil {
		return 0, nil, 0, err
	}
	photos, other = pickPhotos(files)

	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.loaded || g.key != key || !samePhotos(g.photos, photos) {
		g.gen++
		g.photos = photos
		g.thumbs = make(map[int][]byte)
		g.thumbSize = 0
		g.warned = false
	}
	g.loaded = true
	g.key = key
	g.other = other
	g.scannedAt = now
	if g.clears != clears {
		// The card went away while it was scanned: serve this scan, but
		// do not trust it for the next List.
		g.scannedAt = time.Time{}
	}
	return g.gen, g.photos, g.other, nil
}

// pickPhotos returns the JPEGs among files, newest first, and how many
// other files there were.
func pickPhotos(files []scanner.File) (photos []photoFile, other int) {
	photos = make([]photoFile, 0, len(files))
	for _, f := range files {
		switch strings.ToLower(path.Ext(f.RelativePath)) {
		case ".jpg", ".jpeg":
			photos = append(photos, photoFile{
				abs:   f.AbsolutePath,
				rel:   f.RelativePath,
				name:  path.Base(f.RelativePath),
				mtime: f.Mtime,
				size:  f.Size,
			})
		default:
			other++
		}
	}
	sort.Slice(photos, func(i, j int) bool {
		a, b := photos[i], photos[j]
		if !a.mtime.Equal(b.mtime) {
			return a.mtime.After(b.mtime)
		}
		return a.rel > b.rel
	})
	return photos, other
}

// samePhotos reports whether two scans found the same files.
func samePhotos(a, b []photoFile) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].abs != b[i].abs || a[i].size != b[i].size || !a[i].mtime.Equal(b[i].mtime) {
			return false
		}
	}
	return true
}

// statuses reads the card's sync state and returns the status of a photo:
// in Immich when the sync cursor has passed it or it is recorded as
// uploaded, failed when the card's last finished sync failed it, waiting
// otherwise. It only reads the database: with no saved cursor (a card's
// first sync, a forgotten card) the zero cursor covers nothing and the
// files recorded as uploaded say what is in Immich.
func (g *Gallery) statuses(uuid string) (func(photoFile) string, error) {
	cursor, _, err := g.st.SavedVolumeSync(uuid)
	if err != nil {
		return nil, fmt.Errorf("gallery: sync cursor: %w", err)
	}
	uploaded, err := g.st.UploadedPaths(uuid)
	if err != nil {
		return nil, fmt.Errorf("gallery: uploaded files: %w", err)
	}
	failed := make(map[string]struct{})
	run, err := g.st.LastFinishedRunFor(uuid)
	if err != nil {
		return nil, fmt.Errorf("gallery: last sync: %w", err)
	}
	if run != nil {
		failures, err := g.st.RunFailures(run.ID)
		if err != nil {
			return nil, fmt.Errorf("gallery: last sync failures: %w", err)
		}
		for _, f := range failures {
			failed[f.RelativePath] = struct{}{}
		}
	}

	return func(f photoFile) string {
		if cursor.Covers(f.mtime, f.rel) {
			return StatusInImmich
		}
		if _, ok := uploaded[f.rel]; ok {
			return StatusInImmich
		}
		if _, ok := failed[f.rel]; ok {
			return StatusFailed
		}
		return StatusWaiting
	}, nil
}
