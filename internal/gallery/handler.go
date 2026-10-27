package gallery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

const (
	// routePrefix starts every URL the gallery serves.
	routePrefix = "/gallery/"
	// cacheControl lets the window keep images: an id never names another
	// file within its generation.
	cacheControl = "private, max-age=3600"
)

// Middleware serves "/gallery/thumb/<gen>/<i>" (a JPEG thumbnail) and
// "/gallery/photo/<gen>/<i>" (the photo file, with Range support) for the
// ids of the current List, and passes every path outside "/gallery/" to
// next. Anything else under "/gallery/" — unknown or old ids, non-numeric
// ids, extra segments such as ".." — is 404 without touching the disk; so is
// a photo that can no longer be read (the card was removed).
func (g *Gallery) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest, ok := strings.CutPrefix(r.URL.Path, routePrefix)
		if !ok {
			next.ServeHTTP(w, r)
			return
		}
		thumb, gen, i, ok := parseRoute(rest)
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		if thumb {
			g.serveThumb(w, r, gen, i)
		} else {
			g.servePhoto(w, r, gen, i)
		}
	})
}

// parseRoute splits "thumb/<gen>/<i>" or "photo/<gen>/<i>"; gen and i are
// plain decimal numbers.
func parseRoute(rest string) (thumb bool, gen uint64, i int, ok bool) {
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return false, 0, 0, false
	}
	switch parts[0] {
	case "thumb":
		thumb = true
	case "photo":
	default:
		return false, 0, 0, false
	}
	gen, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return false, 0, 0, false
	}
	n, err := strconv.ParseUint(parts[2], 10, 31)
	if err != nil {
		return false, 0, 0, false
	}
	return thumb, gen, int(n), true
}

// lookup returns photo i of generation gen and its cached thumbnail (nil
// when not made yet); ok is false for an unknown id.
func (g *Gallery) lookup(gen uint64, i int) (f photoFile, thumb []byte, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.loaded || gen != g.gen || i >= len(g.photos) {
		return photoFile{}, nil, false
	}
	return g.photos[i], g.thumbs[i], true
}

func (g *Gallery) serveThumb(w http.ResponseWriter, r *http.Request, gen uint64, i int) {
	f, data, ok := g.lookup(gen, i)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if data == nil {
		if !g.acquire(r.Context()) {
			http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			return
		}
		var err error
		data, err = g.makeThumb(gen, i, f)
		g.release()
		if err != nil {
			g.readFailed(gen, f, err)
			http.NotFound(w, r)
			return
		}
	}
	g.writeImage(w, r, f, bytes.NewReader(data))
}

// makeThumb returns photo i's thumbnail, made by another request while this
// one waited for a read slot or made now and cached.
func (g *Gallery) makeThumb(gen uint64, i int, f photoFile) ([]byte, error) {
	if _, data, ok := g.lookup(gen, i); ok && data != nil {
		return data, nil
	}
	fi, err := os.Stat(f.abs)
	if err != nil {
		return nil, err
	}
	if !sameFile(fi, f) {
		return nil, errChanged
	}
	data, err := g.thumbnail(f.abs)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	if g.loaded && gen == g.gen && g.thumbSize+len(data) <= maxThumbCache {
		if _, dup := g.thumbs[i]; !dup {
			g.thumbs[i] = data
			g.thumbSize += len(data)
		}
	}
	g.mu.Unlock()
	return data, nil
}

func (g *Gallery) servePhoto(w http.ResponseWriter, r *http.Request, gen uint64, i int) {
	f, _, ok := g.lookup(gen, i)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !g.acquire(r.Context()) {
		http.Error(w, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
		return
	}
	defer g.release()

	file, err := os.Open(f.abs)
	if err != nil {
		g.readFailed(gen, f, err)
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	fi, err := file.Stat()
	if err == nil && !sameFile(fi, f) {
		err = errChanged
	}
	if err != nil {
		g.readFailed(gen, f, err)
		http.NotFound(w, r)
		return
	}
	g.writeImage(w, r, f, file)
}

// writeImage answers with the JPEG in content, honouring Range and
// conditional requests.
func (g *Gallery) writeImage(w http.ResponseWriter, r *http.Request, f photoFile, content io.ReadSeeker) {
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", cacheControl)
	http.ServeContent(w, r, f.name, f.mtime, content)
}

// errChanged reports a file that is no longer the one scanned: another card
// is mounted at the same place.
var errChanged = errors.New("file changed since the card was scanned")

// sameFile reports whether fi still describes the scanned file f.
func sameFile(fi os.FileInfo, f photoFile) bool {
	return fi.Mode().IsRegular() && fi.Size() == f.size && fi.ModTime().Equal(f.mtime)
}

// acquire takes a read slot, waiting for one unless the request is gone.
func (g *Gallery) acquire(ctx context.Context) bool {
	select {
	case g.reads <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (g *Gallery) release() { <-g.reads }

// readFailed logs the first read error of the current generation: the
// usual cause is a removed card, which fails every request after it.
func (g *Gallery) readFailed(gen uint64, f photoFile, err error) {
	g.mu.Lock()
	first := g.loaded && gen == g.gen && !g.warned
	if first {
		g.warned = true
	}
	g.mu.Unlock()
	if first {
		logx.Warn("gallery: cannot read photo", "path", f.rel, "err", err)
	}
}
