package gui

import (
	"time"

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
	// UpdateCheckedAt is when the last update check succeeded, nil before
	// the first one.
	UpdateCheckedAt *time.Time `json:"updateCheckedAt"`
}

// ServerView is the configured Immich server. The API key itself is never
// sent to the window.
type ServerView struct {
	URL    string `json:"url"`
	HasKey bool   `json:"hasKey"`
}

// CardView is a removable volume, the selected card or a card the app
// remembers (has sync state for), mounted or not. SizeBytes is 0 when the
// card is not mounted. Name is "" for a remembered card whose name was never
// recorded. Uploaded is the number of its files recorded as uploaded.
type CardView struct {
	UUID       string `json:"uuid"`
	Name       string `json:"name"`
	MountPoint string `json:"mountPoint"`
	Mounted    bool   `json:"mounted"`
	Selected   bool   `json:"selected"`
	SizeBytes  int64  `json:"sizeBytes"`
	Remembered bool   `json:"remembered"`
	Uploaded   int    `json:"uploaded"`
}

// SettingsView holds the editable settings. Language is the preference
// ("" follows the system language). Concurrency is the number of parallel
// uploads, 0 when each sync picks it automatically. AutoEject and
// AutoFormat are what happens to the card after a sync; StackRawJPEG
// stacks RAW+JPEG pairs in Immich; Notifications are the notification
// switches.
//
// Autostart (the LaunchAgent is installed) is changed with
// Service.SetAutostart, not SaveSettings, which ignores it and
// the autostart fields. AutostartAvailable is false when the app does not
// run from its bundle (a development build) or runs translocated
// (AutostartTranslocated: opened in place from a quarantined download, so
// it must be moved to Applications first): the switch is then disabled.
// Autostart is true only when the LaunchAgent runs this very executable.
//
// AutoFormatNeedsAdmin (read only, ignored by SaveSettings) is true when
// the system would only format a card with administrator rights (Windows
// without elevation, see volume.EraseNeedsAdmin): auto-format then never
// formats, and the window says so under its switch.
type SettingsView struct {
	Language              string `json:"language"`
	Concurrency           int    `json:"concurrency"`
	AutoPaths             bool   `json:"autoPaths"`
	StackRawJPEG          bool   `json:"stackRawJpeg"`
	Autostart             bool   `json:"autostart"`
	AutostartAvailable    bool   `json:"autostartAvailable"`
	AutostartTranslocated bool   `json:"autostartTranslocated"`

	AutoEject            bool              `json:"autoEject"`
	AutoFormat           bool              `json:"autoFormat"`
	AutoFormatNeedsAdmin bool              `json:"autoFormatNeedsAdmin"`
	Notifications        NotificationsView `json:"notifications"`
}

// NotificationsView holds the notification switches: Enabled turns all of
// them off; the others are the kinds (see config.NotifyConfig).
type NotificationsView struct {
	Enabled   bool `json:"enabled"`
	Done      bool `json:"done"`
	Errors    bool `json:"errors"`
	OtherCard bool `json:"otherCard"`
	Server    bool `json:"server"`
	Card      bool `json:"card"`
}

// ServerCheckView is the result of Service.TestServer. Code is "" when OK,
// otherwise one of "auth_failed", "key_forbidden" (the key lacks a
// permission), "server_unreachable", "server_error", "invalid_url",
// "key_required" (a new host was tested without typing its API key) or
// "key_missing" (no key typed or saved); Params fill the matching
// translated message. NoStack is set when the check passed but the key
// may not create stacks while stacking RAW+JPEG pairs is on.
type ServerCheckView struct {
	OK      bool              `json:"ok"`
	Version string            `json:"version"`
	Code    string            `json:"code"`
	Params  map[string]string `json:"params"`
	NoStack bool              `json:"noStack"`
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

// AppInfoView describes this copy of the app for the sidebar footer (see
// Service.AppInfo). OS is "macOS", "Windows" or "Linux"; OSVersion is its
// version ("" if unknown): the macOS version, the Windows version and
// build, or the Linux distribution's name. Beta is true on Windows and
// Linux. RepoURL and IssueURL (a new issue filled in with these versions)
// are the links Service.OpenURL opens.
type AppInfoView struct {
	Version   string `json:"version"`
	OS        string `json:"os"`
	OSVersion string `json:"osVersion"`
	Beta      bool   `json:"beta"`
	RepoURL   string `json:"repoURL"`
	IssueURL  string `json:"issueURL"`
	// Flatpak is true when the app runs as a Flatpak, which Flatpak itself
	// updates: the app then never checks for updates.
	Flatpak bool `json:"flatpak"`
}
