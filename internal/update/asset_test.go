package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
)

func TestAssetName(t *testing.T) {
	tests := []struct{ goos, goarch, want string }{
		// v0.3.0's updater downloads this name: it must never change. The
		// executable is universal, so it serves both architectures.
		{"darwin", "arm64", "sd-sync-immich"},
		{"darwin", "amd64", "sd-sync-immich"},
		{"windows", "amd64", "SD-Sync-Immich-Setup-x64.exe"},
		{"linux", "amd64", "SD-Sync-Immich-x86_64.AppImage"},
		// The installer and the AppImage are x86_64 only: another
		// architecture must not be offered them.
		{"windows", "arm64", ""},
		{"windows", "386", ""},
		{"linux", "arm64", ""},
		{"linux", "arm", ""},
		{"linux", "", ""},
		{"freebsd", "amd64", ""},
	}
	for _, tt := range tests {
		if got := AssetName(tt.goos, tt.goarch); got != tt.want {
			t.Errorf("AssetName(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
		}
	}
}

// allAssetsRelease serves a release with every system's files.
func allAssetsRelease(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"tag_name": "v1.2.0",
			"assets": [
				{"name":"SD-Sync-Immich.zip","browser_download_url":"https://github.com/brofrong/sd-sync-iimich/releases/download/v1.2.0/SD-Sync-Immich.zip"},
				{"name":"SD-Sync-Immich-Setup-x64.exe","browser_download_url":"https://github.com/brofrong/sd-sync-iimich/releases/download/v1.2.0/SD-Sync-Immich-Setup-x64.exe"},
				{"name":"sd-sync-immich","browser_download_url":"https://github.com/brofrong/sd-sync-iimich/releases/download/v1.2.0/sd-sync-immich"},
				{"name":"SD-Sync-Immich-x86_64.AppImage","browser_download_url":"https://github.com/brofrong/sd-sync-iimich/releases/download/v1.2.0/SD-Sync-Immich-x86_64.AppImage"},
				{"name":"checksums.txt","browser_download_url":"https://github.com/brofrong/sd-sync-iimich/releases/download/v1.2.0/checksums.txt"}
			]
		}`))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestCheckPicksPlatformAsset(t *testing.T) {
	server := allAssetsRelease(t)
	for _, goos := range []string{"", "darwin", "windows", "linux"} {
		goarch := "amd64"
		want := AssetName(goos, goarch)
		if goos == "" {
			goarch = ""
			want = AssetName(runtime.GOOS, runtime.GOARCH)
			if want == "" {
				// This machine's system has no update file (e.g. an arm64
				// Linux runner): TestCheckReportsNoUpdatesForOtherArchitectures
				// covers that.
				continue
			}
		}
		client := NewClient("v1.1.0")
		client.APIURL = server.URL
		client.goos = goos
		client.goarch = goarch
		release, err := client.Check(context.Background())
		if err != nil {
			t.Fatalf("goos %q: Check(): %v", goos, err)
		}
		if release.Binary.Name != want {
			t.Errorf("goos %q: Binary = %q, want %q", goos, release.Binary.Name, want)
		}
		if wantURL := "https://github.com/brofrong/sd-sync-iimich/releases/download/v1.2.0/" + want; release.Binary.URL != wantURL {
			t.Errorf("goos %q: Binary URL = %q, want %q", goos, release.Binary.URL, wantURL)
		}
		if release.Checksums.Name != "checksums.txt" {
			t.Errorf("goos %q: Checksums = %q", goos, release.Checksums.Name)
		}
	}
}

func TestCheckRejectsReleaseWithoutPlatformAsset(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A Mac-only release, as before Windows and Linux builds existed.
		_, _ = w.Write([]byte(`{
			"tag_name":"v1.2.0",
			"assets":[
				{"name":"sd-sync-immich","browser_download_url":"https://github.com/brofrong/sd-sync-iimich/releases/download/v1.2.0/sd-sync-immich"},
				{"name":"checksums.txt","browser_download_url":"https://github.com/brofrong/sd-sync-iimich/releases/download/v1.2.0/checksums.txt"}
			]
		}`))
	}))
	defer server.Close()
	for _, goos := range []string{"windows", "linux", "freebsd"} {
		client := NewClient("v1.1.0")
		client.APIURL = server.URL
		client.goos = goos
		client.goarch = "amd64"
		if release, err := client.Check(context.Background()); err == nil {
			t.Errorf("goos %q: Check() = %#v, want an error (no file for this system)", goos, release)
		}
	}
}

// TestCheckReportsNoUpdatesForOtherArchitectures: a Windows or Linux build
// for another architecture than amd64 is never offered the x86_64 file,
// even when the release has it; macOS gets its universal executable on
// either architecture.
func TestCheckReportsNoUpdatesForOtherArchitectures(t *testing.T) {
	server := allAssetsRelease(t)
	for _, tt := range []struct{ goos, goarch string }{
		{"linux", "arm64"},
		{"linux", "arm"},
		{"windows", "arm64"},
		{"windows", "386"},
	} {
		client := NewClient("v1.1.0")
		client.APIURL = server.URL
		client.goos, client.goarch = tt.goos, tt.goarch
		release, err := client.Check(context.Background())
		if err == nil || !strings.Contains(err.Error(), "no updates for this system") {
			t.Errorf("%s/%s: Check() = %#v, %v; want the \"no updates for this system\" error", tt.goos, tt.goarch, release, err)
		}
	}
	for _, goarch := range []string{"arm64", "amd64"} {
		client := NewClient("v1.1.0")
		client.APIURL = server.URL
		client.goos, client.goarch = "darwin", goarch
		release, err := client.Check(context.Background())
		if err != nil || release == nil || release.Binary.Name != "sd-sync-immich" {
			t.Errorf("darwin/%s: Check() = %#v, %v; want the sd-sync-immich executable", goarch, release, err)
		}
	}
}
