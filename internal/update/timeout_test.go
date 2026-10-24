package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// scaleTimeouts shrinks the update timeouts for the length of the test.
func scaleTimeouts(t *testing.T, api, header time.Duration) {
	t.Helper()
	origAPI, origHeader := apiTimeout, responseHeaderTimeout
	apiTimeout, responseHeaderTimeout = api, header
	t.Cleanup(func() { apiTimeout, responseHeaderTimeout = origAPI, origHeader })
}

// wait sleeps for d or until the request is cancelled.
func wait(r *http.Request, d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-r.Context().Done():
		return false
	}
}

// TestDownloadOutlastsAPITimeout: a large file on a slow link takes far
// longer than the GitHub API check may; only the caller's context limits
// it.
func TestDownloadOutlastsAPITimeout(t *testing.T) {
	scaleTimeouts(t, 50*time.Millisecond, 200*time.Millisecond)
	chunk := bytes.Repeat([]byte("x"), 1024)
	const chunks = 10
	sum := sha256.Sum256(bytes.Repeat(chunk, chunks))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/checksums" {
			_, _ = fmt.Fprintf(w, "%x  sd-sync-immich\n", sum)
			return
		}
		// Streams for ~400 ms, 8 times the API timeout, and twice the
		// header timeout: headers come at once.
		w.WriteHeader(http.StatusOK)
		for i := 0; i < chunks; i++ {
			_, _ = w.Write(chunk)
			w.(http.Flusher).Flush()
			if !wait(r, 40*time.Millisecond) {
				return
			}
		}
	}))
	defer server.Close()

	client := NewClient("v1.0.0")
	client.allowLocalDownloads = true
	rel := &Release{
		Binary:    Asset{Name: "sd-sync-immich", URL: server.URL + "/app"},
		Checksums: Asset{Name: "checksums.txt", URL: server.URL + "/checksums"},
	}
	dest := filepath.Join(t.TempDir(), "sd-sync-immich")
	if err := client.Install(context.Background(), rel, dest); err != nil {
		t.Fatalf("Install() of a slow download: %v", err)
	}
	if info, err := os.Stat(dest); err != nil || info.Size() != chunks*int64(len(chunk)) {
		t.Fatalf("installed %v, %v; want %d bytes", info, err, chunks*len(chunk))
	}
}

// TestDownloadFailsWhenHeadersStall: a server that never answers does not
// hold the update for the whole caller's context.
func TestDownloadFailsWhenHeadersStall(t *testing.T) {
	scaleTimeouts(t, time.Second, 100*time.Millisecond)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wait(r, 5*time.Second)
	}))
	defer server.Close()

	client := NewClient("v1.0.0")
	client.allowLocalDownloads = true
	rel := &Release{
		Binary:    Asset{Name: "sd-sync-immich", URL: server.URL + "/app"},
		Checksums: Asset{Name: "checksums.txt", URL: server.URL + "/checksums"},
	}
	start := time.Now()
	if err := client.Install(context.Background(), rel, filepath.Join(t.TempDir(), "x")); err == nil {
		t.Fatal("Install() succeeded against a stalled server")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Install() took %v against a stalled server, want the header timeout", elapsed)
	}
}

// TestCheckHasItsOwnTimeout: the GitHub API check stays short.
func TestCheckHasItsOwnTimeout(t *testing.T) {
	scaleTimeouts(t, 100*time.Millisecond, time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Headers at once, then a body that never ends.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"tag_name":`))
		w.(http.Flusher).Flush()
		wait(r, 5*time.Second)
	}))
	defer server.Close()

	client := NewClient("v1.0.0")
	client.APIURL = server.URL
	start := time.Now()
	if _, err := client.Check(context.Background()); err == nil {
		t.Fatal("Check() succeeded against a stalled server")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Check() took %v, want the API timeout", elapsed)
	}
}
