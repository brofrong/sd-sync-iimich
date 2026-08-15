package update

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallVerifiesChecksumAndReplacesBinary(t *testing.T) {
	newBinary := []byte("new executable")
	sum := sha256.Sum256(newBinary)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app":
			_, _ = w.Write(newBinary)
		case "/checksums":
			_, _ = fmt.Fprintf(w, "%x  sd-sync-immich\n", sum)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	destination := filepath.Join(dir, "sd-sync-immich")
	if err := os.WriteFile(destination, []byte("old executable"), 0o755); err != nil {
		t.Fatal(err)
	}

	client := NewClient("v1.0.0")
	client.allowLocalDownloads = true
	release := &Release{
		Version:   "v1.1.0",
		Binary:    Asset{Name: "sd-sync-immich", URL: server.URL + "/app"},
		Checksums: Asset{Name: "checksums.txt", URL: server.URL + "/checksums"},
	}
	if err := client.Install(context.Background(), release, destination); err != nil {
		t.Fatalf("Install(): %v", err)
	}

	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBinary) {
		t.Fatalf("installed binary = %q, want %q", got, newBinary)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %o, want 755", info.Mode().Perm())
	}
}

func TestInstallPreservesExistingBinaryOnChecksumMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app":
			_, _ = w.Write([]byte("tampered"))
		case "/checksums":
			_, _ = w.Write([]byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  sd-sync-immich\n"))
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	destination := filepath.Join(dir, "sd-sync-immich")
	if err := os.WriteFile(destination, []byte("old executable"), 0o755); err != nil {
		t.Fatal(err)
	}

	client := NewClient("v1.0.0")
	client.allowLocalDownloads = true
	release := &Release{
		Binary:    Asset{Name: "sd-sync-immich", URL: server.URL + "/app"},
		Checksums: Asset{Name: "checksums.txt", URL: server.URL + "/checksums"},
	}
	if err := client.Install(context.Background(), release, destination); err == nil {
		t.Fatal("Install() accepted a checksum mismatch")
	}
	got, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old executable" {
		t.Fatalf("existing binary changed to %q", got)
	}
}

func TestParseChecksumFindsExactAsset(t *testing.T) {
	input := []byte(
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  sd-sync-immich-darwin-arm64\n" +
			"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  sd-sync-immich\n",
	)
	got, err := parseChecksum(input, "sd-sync-immich")
	if err != nil {
		t.Fatalf("parseChecksum(): %v", err)
	}
	want := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if got != want {
		t.Fatalf("parseChecksum() = %q, want %q", got, want)
	}
}
