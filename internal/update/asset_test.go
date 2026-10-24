package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

func TestAssetName(t *testing.T) {
	tests := []struct{ goos, want string }{
		// v0.3.0's updater downloads this name: it must never change.
		{"darwin", "sd-sync-immich"},
		{"windows", "SD-Sync-Immich-Setup-x64.exe"},
		{"linux", "SD-Sync-Immich-x86_64.AppImage"},
		{"freebsd", ""},
	}
	for _, tt := range tests {
		if got := AssetName(tt.goos); got != tt.want {
			t.Errorf("AssetName(%q) = %q, want %q", tt.goos, got, tt.want)
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
		want := AssetName(goos)
		if goos == "" {
			want = AssetName(runtime.GOOS)
		}
		client := NewClient("v1.1.0")
		client.APIURL = server.URL
		client.goos = goos
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
		if release, err := client.Check(context.Background()); err == nil {
			t.Errorf("goos %q: Check() = %#v, want an error (no file for this system)", goos, release)
		}
	}
}
