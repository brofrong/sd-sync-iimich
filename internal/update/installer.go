package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxChecksumBytes = 1 << 20
	maxBinaryBytes   = 512 << 20
)

func (c *Client) Install(ctx context.Context, release *Release, destination string) error {
	if release == nil {
		return fmt.Errorf("no release given")
	}
	checksums, err := c.download(ctx, release.Checksums.URL, maxChecksumBytes)
	if err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}
	expectedHex, err := parseChecksum(checksums, release.Binary.Name)
	if err != nil {
		return err
	}
	expected, err := hex.DecodeString(expectedHex)
	if err != nil {
		return fmt.Errorf("invalid SHA-256 for %s: %w", release.Binary.Name, err)
	}

	binary, err := c.download(ctx, release.Binary.URL, maxBinaryBytes)
	if err != nil {
		return fmt.Errorf("download %s: %w", release.Binary.Name, err)
	}
	actual := sha256.Sum256(binary)
	if !equalBytes(actual[:], expected) {
		return fmt.Errorf("SHA-256 of the downloaded file does not match checksums.txt")
	}

	dir := filepath.Dir(destination)
	tmp, err := os.CreateTemp(dir, ".sd-sync-immich-update-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return fmt.Errorf("write update: %w", err)
	}
	if err := tmp.Chmod(0o755); err != nil {
		tmp.Close()
		return fmt.Errorf("set update permissions: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync update: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close update: %w", err)
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return fmt.Errorf("install update: %w", err)
	}
	return nil
}

func (c *Client) download(ctx context.Context, url string, maxBytes int64) ([]byte, error) {
	if url == "" {
		return nil, fmt.Errorf("empty URL")
	}
	if err := validateDownloadURL(url, c.allowLocalDownloads); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "sd-sync-immich/"+c.CurrentVersion)

	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return nil, fmt.Errorf("file exceeds the %d byte limit", maxBytes)
	}
	content, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maxBytes {
		return nil, fmt.Errorf("file exceeds the %d byte limit", maxBytes)
	}
	return content, nil
}

func parseChecksum(content []byte, assetName string) (string, error) {
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		if name != assetName {
			continue
		}
		sum := strings.ToLower(fields[0])
		if len(sum) != sha256.Size*2 {
			return "", fmt.Errorf("invalid SHA-256 for %s", assetName)
		}
		if _, err := hex.DecodeString(sum); err != nil {
			return "", fmt.Errorf("invalid SHA-256 for %s", assetName)
		}
		return sum, nil
	}
	return "", fmt.Errorf("checksums.txt has no SHA-256 for %s", assetName)
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var different byte
	for i := range a {
		different |= a[i] ^ b[i]
	}
	return different == 0
}
