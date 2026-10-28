package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		current string
		latest  string
		want    int
	}{
		{"v1.2.3", "v1.2.4", -1},
		{"1.3.0", "v1.2.9", 1},
		{"v2.0.0", "2.0.0", 0},
	}
	for _, tt := range tests {
		got, err := CompareVersions(tt.current, tt.latest)
		if err != nil {
			t.Fatalf("CompareVersions(%q, %q): %v", tt.current, tt.latest, err)
		}
		if got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestCompareVersionsRejectsInvalidVersion(t *testing.T) {
	if _, err := CompareVersions("dev", "v1.0.0"); err == nil {
		t.Fatal("CompareVersions() accepted a development version")
	}
}

func TestCheckReturnsNewReleaseWithRequiredAssets(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"tag_name": "v1.2.0",
			"name": "Version 1.2.0",
			"body": "Changes",
			"html_url": "https://example.test/releases/v1.2.0",
			"assets": [
				{"name":"sd-sync-immich","browser_download_url":"https://github.com/brofrong/sd-sync-immich/releases/download/v1.2.0/sd-sync-immich"},
				{"name":"checksums.txt","browser_download_url":"https://github.com/brofrong/sd-sync-immich/releases/download/v1.2.0/checksums.txt"}
			]
		}`))
	}))
	defer server.Close()

	client := NewClient("v1.1.0")
	client.APIURL = server.URL
	// The release has the Mac file.
	client.goos = "darwin"

	release, err := client.Check(context.Background())
	if err != nil {
		t.Fatalf("Check(): %v", err)
	}
	if release == nil || release.Version != "v1.2.0" {
		t.Fatalf("Check() = %#v, want v1.2.0 release", release)
	}
	if release.Binary.Name != "sd-sync-immich" || release.Checksums.Name != "checksums.txt" {
		t.Fatalf("Check() selected wrong assets: %#v", release)
	}
}

func TestCheckReturnsNilWhenCurrentVersionIsLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"tag_name":"v1.2.0",
			"assets":[
				{"name":"sd-sync-immich","browser_download_url":"https://example.test/app"},
				{"name":"checksums.txt","browser_download_url":"https://example.test/checksums"}
			]
		}`))
	}))
	defer server.Close()

	client := NewClient("v1.2.0")
	client.APIURL = server.URL
	release, err := client.Check(context.Background())
	if err != nil {
		t.Fatalf("Check(): %v", err)
	}
	if release != nil {
		t.Fatalf("Check() = %#v, want nil", release)
	}
}

func TestCheckRejectsReleaseWithoutChecksums(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"tag_name":"v1.2.0",
			"assets":[{"name":"sd-sync-immich","browser_download_url":"https://example.test/app"}]
		}`))
	}))
	defer server.Close()

	client := NewClient("v1.1.0")
	client.APIURL = server.URL
	// The release has the Mac file.
	client.goos = "darwin"
	if _, err := client.Check(context.Background()); err == nil {
		t.Fatal("Check() accepted a release without checksums.txt")
	}
}

func TestCheckRejectsAssetsFromUntrustedHost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"tag_name":"v1.2.0",
			"assets":[
				{"name":"sd-sync-immich","browser_download_url":"https://evil.example/app"},
				{"name":"checksums.txt","browser_download_url":"https://evil.example/checksums"}
			]
		}`))
	}))
	defer server.Close()

	client := NewClient("v1.1.0")
	client.APIURL = server.URL
	// The release has the Mac file.
	client.goos = "darwin"
	if _, err := client.Check(context.Background()); err == nil {
		t.Fatal("Check() accepted assets from an untrusted host")
	}
}

// TestCheckFallsBackToTheFormerName: before the repository is renamed, its
// new name answers 404 and the former one has the releases.
func TestCheckFallsBackToTheFormerName(t *testing.T) {
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path)
		if r.URL.Path == "/new" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"tag_name": "v1.1.0", "assets": []}`))
	}))
	defer server.Close()

	client := NewClient("v1.1.0")
	client.APIURL = server.URL + "/new"
	client.FallbackAPIURL = server.URL + "/former"

	release, err := client.Check(context.Background())
	if err != nil || release != nil {
		t.Fatalf("Check() = %v, %v; want no update and no error", release, err)
	}
	if len(asked) != 2 || asked[0] != "/new" || asked[1] != "/former" {
		t.Fatalf("asked %v, want [/new /former]", asked)
	}
}

func TestRedirectsMayGoToTheGitHubAPI(t *testing.T) {
	for host, want := range map[string]bool{
		"api.github.com":                       true,
		"github.com":                           true,
		"release-assets.githubusercontent.com": true,
		"api.github.com.evil.example":          false,
		"example.com":                          false,
	} {
		if got := isTrustedGitHubHost(host); got != want {
			t.Errorf("isTrustedGitHubHost(%q) = %v, want %v", host, got, want)
		}
	}
}
