package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAPIURL = "https://api.github.com/repos/brofrong/sd-sync-iimich/releases/latest"
	maxAPIBytes   = 2 << 20
)

type Asset struct {
	Name string
	URL  string
}

type Release struct {
	Version   string
	Name      string
	Notes     string
	HTMLURL   string
	Binary    Asset
	Checksums Asset
}

type Client struct {
	CurrentVersion string
	APIURL         string
	HTTPClient     *http.Client

	allowLocalDownloads bool
}

func NewClient(currentVersion string) *Client {
	return &Client{
		CurrentVersion: currentVersion,
		APIURL:         defaultAPIURL,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return fmt.Errorf("too many redirects")
				}
				if !isTrustedGitHubHost(req.URL.Hostname()) || req.URL.Scheme != "https" {
					return fmt.Errorf("unsafe redirect to %s", req.URL.Redacted())
				}
				return nil
			},
		},
	}
}

func (c *Client) Check(ctx context.Context) (*Release, error) {
	if _, err := parseVersion(c.CurrentVersion); err != nil {
		return nil, fmt.Errorf("current version %q is not a release version: %w", c.CurrentVersion, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.APIURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "sd-sync-immich/"+c.CurrentVersion)

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("check GitHub Releases: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("GitHub Releases returned HTTP %d", resp.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
		Name    string `json:"name"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	decoder := json.NewDecoder(io.LimitReader(resp.Body, maxAPIBytes))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode GitHub response: %w", err)
	}

	comparison, err := CompareVersions(c.CurrentVersion, payload.TagName)
	if err != nil {
		return nil, fmt.Errorf("release version: %w", err)
	}
	if comparison >= 0 {
		return nil, nil
	}

	release := &Release{
		Version: payload.TagName,
		Name:    payload.Name,
		Notes:   payload.Body,
		HTMLURL: payload.HTMLURL,
	}
	for _, asset := range payload.Assets {
		switch asset.Name {
		case "sd-sync-immich":
			release.Binary = Asset{Name: asset.Name, URL: asset.URL}
		case "checksums.txt":
			release.Checksums = Asset{Name: asset.Name, URL: asset.URL}
		}
	}
	if release.Binary.URL == "" || release.Checksums.URL == "" {
		return nil, fmt.Errorf("release %s does not contain sd-sync-immich and checksums.txt", release.Version)
	}
	if err := validateDownloadURL(release.Binary.URL, c.allowLocalDownloads); err != nil {
		return nil, fmt.Errorf("binary URL: %w", err)
	}
	if err := validateDownloadURL(release.Checksums.URL, c.allowLocalDownloads); err != nil {
		return nil, fmt.Errorf("URL checksums.txt: %w", err)
	}
	return release, nil
}

func CompareVersions(a, b string) (int, error) {
	left, err := parseVersion(a)
	if err != nil {
		return 0, err
	}
	right, err := parseVersion(b)
	if err != nil {
		return 0, err
	}
	for i := range left {
		switch {
		case left[i] < right[i]:
			return -1, nil
		case left[i] > right[i]:
			return 1, nil
		}
	}
	return 0, nil
}

func parseVersion(value string) ([3]int, error) {
	var version [3]int
	value = strings.TrimPrefix(strings.TrimSpace(value), "v")
	parts := strings.Split(value, ".")
	if len(parts) != len(version) {
		return version, fmt.Errorf("expected a version like v1.2.3")
	}
	for i, part := range parts {
		if part == "" || strings.ContainsAny(part, "+-") {
			return version, fmt.Errorf("unsupported version %q", value)
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return version, fmt.Errorf("unsupported version %q", value)
		}
		version[i] = n
	}
	return version, nil
}

func validateDownloadURL(rawURL string, allowLocal bool) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if allowLocal && (parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1" || parsed.Hostname() == "localhost") {
		return nil
	}
	if parsed.Scheme != "https" || parsed.Hostname() != "github.com" {
		return fmt.Errorf("only HTTPS downloads from github.com are allowed")
	}
	return nil
}

func isTrustedGitHubHost(host string) bool {
	return host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com")
}
