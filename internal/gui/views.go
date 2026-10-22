package gui

import (
	"syscall"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/update"
)

// StateView is everything the window needs to render, fetched once with
// Service.State and pushed again with the "state" event when it changes.
type StateView struct {
	Status app.Status `json:"status"`
	// Language is the resolved UI language (a tag from i18n.Supported).
	Language string       `json:"language"`
	Server   ServerView   `json:"server"`
	Card     CardView     `json:"card"`
	Settings SettingsView `json:"settings"`
	Version  string       `json:"version"`
	// Update is the available update, or nil when none is known.
	Update *UpdateView `json:"update"`
}

// ServerView is the configured Immich server. The API key itself is never
// sent to the window.
type ServerView struct {
	URL    string `json:"url"`
	HasKey bool   `json:"hasKey"`
}

// CardView is a removable volume (or the selected card while it is not
// mounted). SizeBytes is 0 when the card is not mounted.
type CardView struct {
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	MountPoint string `json:"mountPoint"`
	Mounted    bool   `json:"mounted"`
	Selected   bool   `json:"selected"`
	SizeBytes  int64  `json:"sizeBytes"`
}

// SettingsView holds the editable settings. Language is the preference
// ("" follows the system language).
type SettingsView struct {
	Language    string `json:"language"`
	Concurrency int    `json:"concurrency"`
	AutoPaths   bool   `json:"autoPaths"`
}

// ServerCheckView is the result of Service.TestServer. Code is "" when OK,
// otherwise one of "auth_failed", "server_unreachable", "server_error" or
// "invalid_url"; Params fill the matching translated message.
type ServerCheckView struct {
	OK      bool              `json:"ok"`
	Version string            `json:"version"`
	Code    string            `json:"code"`
	Params  map[string]string `json:"params"`
}

// UpdateView is an available application update.
type UpdateView struct {
	Version string `json:"version"`
	Notes   string `json:"notes"`
	URL     string `json:"url"`
}

func updateView(r *update.Release) *UpdateView {
	if r == nil {
		return nil
	}
	return &UpdateView{Version: r.Version, Notes: r.Notes, URL: r.HTMLURL}
}

// volumeSize returns the total size of the file system mounted at
// mountPoint, or 0 if it cannot be determined.
func volumeSize(mountPoint string) int64 {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(mountPoint, &fs); err != nil {
		return 0
	}
	return int64(fs.Blocks) * int64(fs.Bsize)
}
