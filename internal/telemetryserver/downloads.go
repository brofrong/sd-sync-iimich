package telemetryserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// downloadsTTL is how long the GitHub numbers are reused.
const downloadsTTL = time.Hour

// Release is one release's downloads by file kind.
type Release struct {
	Tag      string
	Date     string
	DMG      int // the macOS disk image
	MacOther int // the zip and binaries the macOS updater downloads
	Windows  int
	AppImage int
	Flatpak  int
	Total    int
}

// Downloads reads the release download counts from GitHub's API.
type Downloads struct {
	// Repo is "owner/name".
	Repo string
	// APIURL is GitHub's API (a fake one in tests).
	APIURL string
	HTTP   *http.Client

	mu      sync.Mutex
	fetched time.Time
	cached  []Release
}

// Releases returns the releases, newest first, refreshed at most once an
// hour; on an error it returns the last numbers it had with the error.
func (d *Downloads) Releases(ctx context.Context) ([]Release, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cached != nil && time.Since(d.fetched) < downloadsTTL {
		return d.cached, nil
	}
	fresh, err := d.fetch(ctx)
	if err != nil {
		return d.cached, err
	}
	d.cached, d.fetched = fresh, time.Now()
	return fresh, nil
}

func (d *Downloads) fetch(ctx context.Context) ([]Release, error) {
	api := d.APIURL
	if api == "" {
		api = "https://api.github.com"
	}
	client := d.HTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/repos/"+d.Repo+"/releases?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub answered %d", resp.StatusCode)
	}
	var releases []struct {
		Tag       string    `json:"tag_name"`
		Published time.Time `json:"published_at"`
		Assets    []struct {
			Name      string `json:"name"`
			Downloads int    `json:"download_count"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	out := make([]Release, 0, len(releases))
	for _, r := range releases {
		rel := Release{Tag: r.Tag, Date: r.Published.Format("2006-01-02")}
		for _, a := range r.Assets {
			name := strings.ToLower(a.Name)
			switch {
			case strings.HasSuffix(name, ".dmg"):
				rel.DMG += a.Downloads
			case strings.HasSuffix(name, ".zip"), strings.HasPrefix(name, "sd-sync-immich") && !strings.Contains(name, "."):
				rel.MacOther += a.Downloads
			case strings.HasSuffix(name, ".exe"):
				rel.Windows += a.Downloads
			case strings.HasSuffix(name, ".appimage"):
				rel.AppImage += a.Downloads
			case strings.HasSuffix(name, ".flatpak"):
				rel.Flatpak += a.Downloads
			default:
				continue // checksums.txt, the vendored modules
			}
			rel.Total += a.Downloads
		}
		out = append(out, rel)
	}
	return out, nil
}
