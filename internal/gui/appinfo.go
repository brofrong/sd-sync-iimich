package gui

import (
	"errors"
	"net/url"
	"runtime"
	"strings"
	"sync"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/desktop"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// The project's GitHub repository, and the page that opens a new issue.
// They are the only URLs OpenURL opens.
const (
	repoURL     = "https://github.com/brofrong/sd-sync-iimich"
	newIssueURL = repoURL + "/issues/new"
)

// osName is the name of the system goos (runtime.GOOS) the window shows.
func osName(goos string) string {
	switch goos {
	case "darwin":
		return "macOS"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	}
	return goos
}

// appInfo describes this copy of the app, version ver, on the system goos
// whose version is osVersion. The Windows and Linux builds are betas.
func appInfo(goos, ver, osVersion string) AppInfoView {
	info := AppInfoView{
		Version:   ver,
		OS:        osName(goos),
		OSVersion: osVersion,
		Beta:      goos != "darwin",
		RepoURL:   repoURL,
	}
	info.IssueURL = issueURL(info)
	return info
}

// issueURL opens a new GitHub issue whose body is filled in with the app's
// version and the system's name and version, and headings for the user to
// fill in. Nothing else about the user's setup (such as the server
// address) goes in it.
func issueURL(info AppInfoView) string {
	body := "**Version:** " + info.Version + "\n" +
		"**OS:** " + strings.TrimSpace(info.OS+" "+info.OSVersion) + "\n" +
		"\n" +
		"**What happened:**\n" +
		"\n" +
		"**Steps to reproduce:**\n"
	return newIssueURL + "?" + url.Values{"body": {body}}.Encode()
}

// allowedURL reports whether OpenURL may open raw: the repository, or the
// new-issue page with at most a body, written exactly as issueURL writes
// it. Anything else is refused, as the system opener (ShellExecute on
// Windows) would also run programs and open files.
func allowedURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.String() != raw {
		return false
	}
	if u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawPath != "" || u.Fragment != "" || u.ForceQuery {
		return false
	}
	switch u.Path {
	case "/brofrong/sd-sync-iimich":
		return u.RawQuery == ""
	case "/brofrong/sd-sync-iimich/issues/new":
		if u.RawQuery == "" {
			return true
		}
		q, err := url.ParseQuery(u.RawQuery)
		return err == nil && len(q) == 1 && len(q["body"]) == 1 && q.Encode() == u.RawQuery
	}
	return false
}

// AppInfo describes this copy of the app for the sidebar footer: its
// version, the system and whether it is a beta, and the GitHub links.
func (s *Service) AppInfo() AppInfoView {
	return appInfo(runtime.GOOS, s.version, systemVersion())
}

// OpenURL opens the repository or a new issue (see AppInfoView) in the
// default browser. Other URLs are refused.
func (s *Service) OpenURL(u string) error {
	if !allowedURL(u) {
		logx.Warn("gui: refused to open a URL that is not the project's GitHub page")
		return errors.New("only the project's GitHub pages can be opened")
	}
	if err := openURL(u); err != nil {
		logx.Warn("gui: failed to open URL", "err", err)
		return err
	}
	return nil
}

// Quit quits the app, as "Quit" in the tray menu does: the window's
// button for when there is no tray (GNOME without AppIndicator).
func (s *Service) Quit() {
	logx.Info("quit from the window")
	quitApp(s.shell, 0)
}

// Seams for tests. systemVersion is osVersion (per system: appinfo_*.go),
// asked once.
var (
	systemVersion = sync.OnceValue(osVersion)
	openURL       = desktop.OpenURL
	quitApp       = (*shell).quit
)
