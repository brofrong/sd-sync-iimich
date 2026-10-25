package gui

import (
	"errors"
	"net/url"
	"runtime"
	"strings"
	"testing"
)

func TestAppInfoBeta(t *testing.T) {
	tests := []struct {
		goos string
		os   string
		beta bool
	}{
		{goos: "darwin", os: "macOS", beta: false},
		{goos: "windows", os: "Windows", beta: true},
		{goos: "linux", os: "Linux", beta: true},
	}
	for _, tt := range tests {
		info := appInfo(tt.goos, "v0.5.0", "1.2.3")
		if info.OS != tt.os || info.Beta != tt.beta {
			t.Errorf("appInfo(%q) OS/Beta = %q/%v, want %q/%v", tt.goos, info.OS, info.Beta, tt.os, tt.beta)
		}
		if info.Version != "v0.5.0" || info.OSVersion != "1.2.3" {
			t.Errorf("appInfo(%q) Version/OSVersion = %q/%q, want v0.5.0/1.2.3", tt.goos, info.Version, info.OSVersion)
		}
		if info.RepoURL != repoURL || info.IssueURL != issueURL(info) {
			t.Errorf("appInfo(%q) links = %q, %q; want the repository and issueURL(info)", tt.goos, info.RepoURL, info.IssueURL)
		}
	}
}

func TestServiceAppInfo(t *testing.T) {
	orig := systemVersion
	systemVersion = func() string { return "15.1" }
	t.Cleanup(func() { systemVersion = orig })

	got := (&Service{version: "v0.5.0"}).AppInfo()
	want := appInfo(runtime.GOOS, "v0.5.0", "15.1")
	if got != want {
		t.Fatalf("AppInfo() = %+v, want %+v", got, want)
	}
}

func TestIssueURL(t *testing.T) {
	info := AppInfoView{Version: "v0.5.0", OS: "Windows", OSVersion: "11 (10.0.22631)", Beta: true}
	raw := issueURL(info)

	prefix := "https://github.com/brofrong/sd-sync-iimich/issues/new?body="
	if !strings.HasPrefix(raw, prefix) {
		t.Fatalf("issueURL = %q, want prefix %q", raw, prefix)
	}
	if strings.ContainsAny(raw, " \n*") {
		t.Errorf("issueURL = %q is not URL-encoded", raw)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	want := "**Version:** v0.5.0\n**OS:** Windows 11 (10.0.22631)\n\n**What happened:**\n\n**Steps to reproduce:**\n"
	if got := u.Query().Get("body"); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	if len(u.Query()) != 1 {
		t.Errorf("query = %v, want only body", u.Query())
	}
	// Nothing about the user's setup: AppInfoView has no server address,
	// and the body holds the app and system versions only.
	if strings.Contains(strings.ToLower(raw), "immich.") || strings.Contains(raw, "http%3A") || strings.Contains(raw, "https%3A") {
		t.Errorf("issueURL = %q contains a server address", raw)
	}
	if !allowedURL(raw) {
		t.Errorf("OpenURL would refuse issueURL %q", raw)
	}

	// An unknown system version leaves no trailing space.
	u, err = url.Parse(issueURL(AppInfoView{Version: "v0.5.0", OS: "Linux"}))
	if err != nil {
		t.Fatal(err)
	}
	if body := u.Query().Get("body"); !strings.Contains(body, "**OS:** Linux\n") {
		t.Errorf("body without a system version = %q, want \"**OS:** Linux\\n\"", body)
	}
}

// recordOpenURL replaces desktop.OpenURL and returns the URLs it got.
func recordOpenURL(t *testing.T) *[]string {
	t.Helper()
	var got []string
	orig := openURL
	openURL = func(u string) error {
		got = append(got, u)
		return nil
	}
	t.Cleanup(func() { openURL = orig })
	return &got
}

func TestOpenURLAllowlist(t *testing.T) {
	issue := issueURL(AppInfoView{Version: "v0.5.0", OS: "Linux", OSVersion: "Ubuntu 24.04.1 LTS"})
	allowed := []string{
		"https://github.com/brofrong/sd-sync-iimich",
		"https://github.com/brofrong/sd-sync-iimich/issues/new",
		issue,
	}
	refused := []string{
		"",
		"https://example.com",
		"https://github.com/brofrong",
		"https://github.com/brofrong/sd-sync-iimich/",
		"https://github.com/brofrong/sd-sync-iimich/issues",
		"https://github.com/brofrong/sd-sync-iimich/issues/1",
		"https://github.com/brofrong/sd-sync-iimich/issues/new/../../../evil",
		"https://github.com/brofrong%2Fsd-sync-iimich",
		"https://github.com/brofrong/sd-sync-iimich?tab=readme",
		"https://github.com/brofrong/sd-sync-iimich#readme",
		"https://github.com/brofrong/sd-sync-iimich/issues/new?body=x&title=y",
		"https://github.com/brofrong/sd-sync-iimich/issues/new?title=y",
		"https://github.com/brofrong/sd-sync-iimich/issues/new?body=a b",
		"https://github.com/brofrong/sd-sync-iimich/issues/new?body=x#y",
		"http://github.com/brofrong/sd-sync-iimich",
		"https://GitHub.com/brofrong/sd-sync-iimich",
		"https://github.com.evil.example/brofrong/sd-sync-iimich",
		"https://evil@github.com/brofrong/sd-sync-iimich",
		"https://github.com:8443/brofrong/sd-sync-iimich",
		"file:///etc/passwd",
		`C:\Windows\System32\calc.exe`,
		"calc.exe",
		" https://github.com/brofrong/sd-sync-iimich",
	}

	opened := recordOpenURL(t)
	s := &Service{}
	for _, u := range allowed {
		if err := s.OpenURL(u); err != nil {
			t.Errorf("OpenURL(%q) = %v, want it opened", u, err)
		}
	}
	for _, u := range refused {
		if err := s.OpenURL(u); err == nil {
			t.Errorf("OpenURL(%q) succeeded, want it refused", u)
		}
	}
	if len(*opened) != len(allowed) {
		t.Fatalf("opened %q, want exactly %q", *opened, allowed)
	}
	for i, u := range allowed {
		if (*opened)[i] != u {
			t.Errorf("opened[%d] = %q, want %q", i, (*opened)[i], u)
		}
	}
}

func TestOpenURLReportsFailure(t *testing.T) {
	orig := openURL
	openURL = func(string) error { return errors.New("no browser") }
	t.Cleanup(func() { openURL = orig })
	if err := (&Service{}).OpenURL(repoURL); err == nil {
		t.Fatal("OpenURL succeeded although the browser could not be opened")
	}
}

func TestQuitExitsWithZero(t *testing.T) {
	var codes []int
	orig := quitApp
	quitApp = func(_ *shell, code int) { codes = append(codes, code) }
	t.Cleanup(func() { quitApp = orig })

	(&Service{shell: &shell{}}).Quit()
	if len(codes) != 1 || codes[0] != 0 {
		t.Fatalf("quit codes = %v, want [0]", codes)
	}
}
