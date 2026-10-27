package gallery

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// nextStatus is what the handler behind the middleware answers.
const nextStatus = http.StatusTeapot

func serveMux(g *Gallery) http.Handler {
	return g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(nextStatus)
	}))
}

func get(h http.Handler, path string, header ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://wails.localhost"+path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// photoWithThumb returns a 1200×800 blue JPEG carrying a 160×106 red EXIF
// thumbnail.
func photoWithThumb(t *testing.T) []byte {
	t.Helper()
	thumb := encodeJPEG(t, 160, 106, red)
	return withExif(encodeJPEG(t, 1200, 800, blue), buildTIFF(binary.LittleEndian, 1, thumb))
}

func TestHandlerServesKnownIDs(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	data := photoWithThumb(t)
	putFile(t, vol, "DSC00001.JPG", data, base)
	h := serveMux(g)
	p := mustList(t, g, vol).Photos[0]

	w := get(h, p.Thumb)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", p.Thumb, w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("thumb Content-Type = %q, want image/jpeg", ct)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "private, max-age=3600" {
		t.Errorf("thumb Cache-Control = %q", cc)
	}
	img, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("thumb is not a JPEG: %v", err)
	}
	if got := img.Bounds().Dx(); got != 160 {
		t.Errorf("thumb width = %d, want the embedded 160", got)
	}

	w = get(h, p.Src)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200", p.Src, w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("photo Content-Type = %q, want image/jpeg", ct)
	}
	if !bytes.Equal(w.Body.Bytes(), data) {
		t.Error("photo body differs from the file")
	}

	w = get(h, p.Src, "Range", "bytes=2-11")
	if w.Code != http.StatusPartialContent {
		t.Fatalf("ranged GET = %d, want 206", w.Code)
	}
	if !bytes.Equal(w.Body.Bytes(), data[2:12]) {
		t.Errorf("ranged body = %x, want %x", w.Body.Bytes(), data[2:12])
	}

	// HEAD answers without a body.
	r := httptest.NewRequest(http.MethodHead, p.Src, nil)
	hw := httptest.NewRecorder()
	h.ServeHTTP(hw, r)
	if hw.Code != http.StatusOK || hw.Body.Len() != 0 {
		t.Errorf("HEAD = %d with %d bytes, want 200 and no body", hw.Code, hw.Body.Len())
	}
}

func TestHandlerCachesThumbnails(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	putFile(t, vol, "DSC00001.JPG", photoWithThumb(t), base)
	var calls atomic.Int32
	g.thumbnail = func(path string) ([]byte, error) {
		calls.Add(1)
		return Thumbnail(path)
	}
	h := serveMux(g)
	p := mustList(t, g, vol).Photos[0]

	first := get(h, p.Thumb)
	second := get(h, p.Thumb)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("GETs = %d, %d, want 200", first.Code, second.Code)
	}
	if !bytes.Equal(first.Body.Bytes(), second.Body.Bytes()) {
		t.Error("cached thumbnail differs")
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("thumbnail made %d times, want once", n)
	}
}

// TestHandlerNeverAnswersConditionally: Wails turns a 304 into a 500 on
// Windows (WebView2), so revalidating a cached image must get the image
// again, and the answers carry no validators to revalidate with.
func TestHandlerNeverAnswersConditionally(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	data := photoWithThumb(t)
	putFile(t, vol, "DSC00001.JPG", data, base)
	h := serveMux(g)
	p := mustList(t, g, vol).Photos[0]
	later := base.Add(time.Hour).UTC().Format(http.TimeFormat)
	earlier := base.Add(-time.Hour).UTC().Format(http.TimeFormat)

	for _, path := range []string{p.Thumb, p.Src} {
		w := get(h, path)
		if lm := w.Header().Get("Last-Modified"); lm != "" {
			t.Errorf("GET %s Last-Modified = %q, want none", path, lm)
		}
		if et := w.Header().Get("ETag"); et != "" {
			t.Errorf("GET %s ETag = %q, want none", path, et)
		}
		for _, hdr := range [][2]string{
			{"If-Modified-Since", later},
			{"If-None-Match", "*"},
			{"If-Unmodified-Since", earlier},
			{"If-Match", `"x"`},
		} {
			w := get(h, path, hdr[0], hdr[1])
			if w.Code != http.StatusOK || w.Body.Len() == 0 {
				t.Errorf("GET %s with %s: %s = %d with %d bytes, want 200 and the image", path, hdr[0], hdr[1], w.Code, w.Body.Len())
			}
		}
	}
	w := get(h, p.Src, "Range", "bytes=2-11", "If-Range", later)
	if w.Code != http.StatusPartialContent || !bytes.Equal(w.Body.Bytes(), data[2:12]) {
		t.Errorf("ranged GET with If-Range = %d, want 206 with the range", w.Code)
	}
}

// TestGenerationsDifferAcrossLaunches: the window's HTTP cache outlives the
// app, so a new Gallery must not reuse the ids of the last launch.
func TestGenerationsDifferAcrossLaunches(t *testing.T) {
	first, _ := newTestGallery(t)
	second, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	putFile(t, vol, "DSC00001.JPG", []byte("jpeg"), base)
	a := genOf(t, mustList(t, first, vol).Photos[0])
	b := genOf(t, mustList(t, second, vol).Photos[0])
	if a == b {
		t.Errorf("two Gallery instances both started at generation %d", a)
	}
}

func TestHandlerRejectsUnknownIDs(t *testing.T) {
	g, _ := newTestGallery(t)
	a := newCard(t, "CARD-A")
	putFile(t, a, "DSC00001.JPG", photoWithThumb(t), base)
	h := serveMux(g)
	old := mustList(t, g, a).Photos[0]

	b := newCard(t, "CARD-B")
	putFile(t, b, "DSC00002.JPG", photoWithThumb(t), base)
	cur := mustList(t, g, b).Photos[0]
	gen := genOf(t, cur)

	notFound := []string{
		old.Thumb, // an old generation
		old.Src,
		fmt.Sprintf("/gallery/thumb/%d/1", gen), // out of the list
		fmt.Sprintf("/gallery/photo/%d/99999999999999999999", gen),
		fmt.Sprintf("/gallery/thumb/%d/abc", gen),
		fmt.Sprintf("/gallery/photo/%d/-1", gen),
		fmt.Sprintf("/gallery/photo/%d/+0", gen),
		fmt.Sprintf("/gallery/photo/%d/0x0", gen),
		fmt.Sprintf("/gallery/photo/%d/", gen),
		fmt.Sprintf("/gallery/photo/%d/0/", gen),
		fmt.Sprintf("/gallery/photo/%d/0/extra", gen),
		fmt.Sprintf("/gallery/thumb/%d/../../../etc/passwd", gen),
		fmt.Sprintf("/gallery/thumb/%d/0/../0", gen),
		fmt.Sprintf("/gallery/thumb/%d/%%2e%%2e/0", gen),
		fmt.Sprintf("/gallery/video/%d/0", gen),
		"/gallery/thumb/x/0",
		"/gallery/thumb/0",
		"/gallery/",
	}
	for _, path := range notFound {
		if w := get(h, path); w.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, w.Code)
		}
	}
	// The ids the current list gave out still work.
	for _, path := range []string{cur.Thumb, cur.Src} {
		if w := get(h, path); w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, w.Code)
		}
	}
	for _, path := range []string{"/other", "/", "/index.html", "/gallery", "/galleryx/thumb/1/0"} {
		if w := get(h, path); w.Code != nextStatus {
			t.Errorf("GET %s = %d, want it passed on (%d)", path, w.Code, nextStatus)
		}
	}

	// After Clear nothing is served.
	g.Clear()
	for _, path := range []string{cur.Thumb, cur.Src} {
		if w := get(h, path); w.Code != http.StatusNotFound {
			t.Errorf("after Clear GET %s = %d, want 404", path, w.Code)
		}
	}
}

func TestHandlerRejectsOtherMethods(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	putFile(t, vol, "DSC00001.JPG", photoWithThumb(t), base)
	p := mustList(t, g, vol).Photos[0]

	r := httptest.NewRequest(http.MethodPost, p.Src, nil)
	w := httptest.NewRecorder()
	serveMux(g).ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", w.Code)
	}
}

func TestHandlerAfterCardRemoved(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	putFile(t, vol, "DSC00001.JPG", photoWithThumb(t), base)
	putFile(t, vol, "DSC00002.JPG", photoWithThumb(t), base.Add(time.Minute))
	h := serveMux(g)
	l := mustList(t, g, vol)

	if err := os.RemoveAll(filepath.Join(vol.MountPoint, "DCIM")); err != nil {
		t.Fatal(err)
	}
	for _, p := range l.Photos {
		for _, path := range []string{p.Thumb, p.Src} {
			if w := get(h, path); w.Code != http.StatusNotFound {
				t.Errorf("GET %s after removal = %d, want 404", path, w.Code)
			}
		}
	}
}

func TestHandlerRejectsReplacedFile(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	putFile(t, vol, "DSC00001.JPG", photoWithThumb(t), base)
	h := serveMux(g)
	p := mustList(t, g, vol).Photos[0]

	// Another card at the same mount point, before the window lists it.
	putFile(t, vol, "DSC00001.JPG", []byte("another card's photo"), base.Add(time.Hour))
	for _, path := range []string{p.Thumb, p.Src} {
		if w := get(h, path); w.Code != http.StatusNotFound {
			t.Errorf("GET %s of a replaced file = %d, want 404", path, w.Code)
		}
	}
}

func TestHandlerLimitsParallelReads(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	data := photoWithThumb(t)
	for i := 0; i < 12; i++ {
		putFile(t, vol, fmt.Sprintf("DSC%05d.JPG", i), data, base.Add(time.Duration(i)*time.Second))
	}
	var running, peak atomic.Int32
	g.thumbnail = func(path string) ([]byte, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
		return Thumbnail(path)
	}
	h := serveMux(g)
	l := mustList(t, g, vol)

	var wg sync.WaitGroup
	for _, p := range l.Photos {
		for _, path := range []string{p.Thumb, p.Src} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if w := get(h, path); w.Code != http.StatusOK {
					t.Errorf("GET %s = %d, want 200", path, w.Code)
				}
			}()
		}
	}
	// List runs alongside, rescanning as the clock moves.
	for i := 0; i < 5; i++ {
		advance(g, time.Minute)
		mustList(t, g, vol)
	}
	wg.Wait()
	if p := peak.Load(); p > maxReads {
		t.Errorf("%d thumbnails made at once, want at most %d", p, maxReads)
	}
}

func TestHandlerGivesUpWhenClientLeaves(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	putFile(t, vol, "DSC00001.JPG", photoWithThumb(t), base)
	h := serveMux(g)
	p := mustList(t, g, vol).Photos[0]

	// Every read slot is taken.
	for i := 0; i < maxReads; i++ {
		g.reads <- struct{}{}
	}
	r := httptest.NewRequest(http.MethodGet, p.Src, nil)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(w, r.WithContext(ctx))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request waited for a read slot after the client left")
	}
	if w.Code == http.StatusOK {
		t.Errorf("abandoned request = 200")
	}
}

func TestHandlerReleasesReadSlotOnPanic(t *testing.T) {
	g, _ := newTestGallery(t)
	vol := newCard(t, "CARD-A")
	putFile(t, vol, "DSC00001.JPG", photoWithThumb(t), base)
	g.thumbnail = func(string) ([]byte, error) { panic("thumbnail bug") }
	h := serveMux(g)
	p := mustList(t, g, vol).Photos[0]

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the thumbnail's panic did not reach the server")
			}
		}()
		get(h, p.Thumb)
	}()
	if n := len(g.reads); n != 0 {
		t.Errorf("%d read slots held after the panic, want 0", n)
	}
}
