package uploader

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// TestUploadAllRecordsFailures checks that a rejected upload is recorded as
// a per-file failure with its relative path, kind and a non-empty detail.
func TestUploadAllRecordsFailures(t *testing.T) {
	withoutRetryDelays(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dir := t.TempDir()
	name := "DSC00001.JPG"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
		t.Fatal(err)
	}
	files := []scanner.File{{
		AbsolutePath: path,
		RelativePath: "DCIM/" + name,
		Size:         int64(len(name)),
		Mtime:        time.Unix(1000, 0),
	}}

	svc := &Service{State: st}
	session := newSyncSession(st, "vol", files)
	res, err := svc.uploadAll(context.Background(), "vol", immich.NewClient(srv.URL, "key"), 1, false, len(files), 0, files,
		newSyncProgress(len(files), 0, nil), session, newSyncCheckpoint(st, session, 150))
	if err != nil {
		t.Fatalf("uploadAll() error = %v, want nil", err)
	}

	if len(res.Failures) != 1 {
		t.Fatalf("len(res.Failures) = %d, want 1 (failures = %+v)", len(res.Failures), res.Failures)
	}
	f := res.Failures[0]
	if f.RelativePath != "DCIM/DSC00001.JPG" || f.Kind != "rejected" || f.Detail == "" {
		t.Fatalf("res.Failures[0] = %+v, want {DCIM/DSC00001.JPG, rejected, <non-empty detail>}", f)
	}
}

// TestFailuresAreCapped checks that Result.Failures never grows past
// maxRecordedFailures, while Result.Errors still counts every failure.
func TestFailuresAreCapped(t *testing.T) {
	withoutRetryDelays(t)
	saved := maxRecordedFailures
	maxRecordedFailures = 2
	t.Cleanup(func() { maxRecordedFailures = saved })

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dir := t.TempDir()
	var files []scanner.File
	for i := range 3 {
		name := fmt.Sprintf("DSC%05d.JPG", i)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, scanner.File{AbsolutePath: path, RelativePath: "DCIM/" + name, Size: int64(len(name)), Mtime: time.Unix(int64(1000+i), 0)})
	}

	svc := &Service{State: st}
	session := newSyncSession(st, "vol", files)
	res, err := svc.uploadAll(context.Background(), "vol", immich.NewClient(srv.URL, "key"), 1, false, len(files), 0, files,
		newSyncProgress(len(files), 0, nil), session, newSyncCheckpoint(st, session, 150))
	if err != nil {
		t.Fatalf("uploadAll() error = %v, want nil", err)
	}

	if len(res.Failures) != 2 {
		t.Fatalf("len(res.Failures) = %d, want 2 (capped)", len(res.Failures))
	}
	if res.Errors != 3 {
		t.Fatalf("res.Errors = %d, want 3", res.Errors)
	}
}

// TestSyncOnlyUploadsListedFiles checks that Sync with SyncOptions.Only set
// uploads exactly the listed files, ignoring the cursor watermark, and
// counts the rest as Skipped.
func TestSyncOnlyUploadsListedFiles(t *testing.T) {
	withoutRetryDelays(t)

	var mu sync.Mutex
	var uploadedNames []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		switch {
		case bytes.Contains(body, []byte("A.JPG")):
			uploadedNames = append(uploadedNames, "A.JPG")
		case bytes.Contains(body, []byte("B.JPG")):
			uploadedNames = append(uploadedNames, "B.JPG")
		case bytes.Contains(body, []byte("C.JPG")):
			uploadedNames = append(uploadedNames, "C.JPG")
		}
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"a","status":"created"}`))
	}))
	defer srv.Close()

	t.Setenv("HOME", t.TempDir())
	cfgStore, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfgStore.Update(func(c *config.Config) {
		c.Immich = config.ImmichConfig{URL: srv.URL, APIKey: "key"}
		c.Watch = config.WatchConfig{VolumeUUID: "VOL-UUID", VolumeName: "CARD"}
	}); err != nil {
		t.Fatalf("cfgStore.Update: %v", err)
	}

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mount := t.TempDir()
	dcim := filepath.Join(mount, "DCIM")
	if err := os.MkdirAll(dcim, 0o755); err != nil {
		t.Fatal(err)
	}
	names := []string{"A.JPG", "B.JPG", "C.JPG"}
	var latestMtime time.Time
	var latestPath string
	for i, name := range names {
		path := filepath.Join(dcim, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		mtime := time.Unix(int64(1000+i), 0)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		latestMtime = mtime
		latestPath = "DCIM/" + name
	}

	// The cursor is already past all three files, as if a previous full sync
	// had uploaded them (and later marked one of them failed).
	if err := st.SetVolumeSync("VOL-UUID", latestMtime, latestPath); err != nil {
		t.Fatalf("SetVolumeSync: %v", err)
	}

	vol := volume.Info{UUID: "VOL-UUID", Name: "CARD", MountPoint: mount}
	svc := &Service{Config: cfgStore, State: st}

	res, err := svc.Sync(context.Background(), vol, SyncOptions{Only: []string{"DCIM/B.JPG"}}, nil)
	if err != nil {
		t.Fatalf("Sync() error = %v, want nil", err)
	}

	mu.Lock()
	got := append([]string(nil), uploadedNames...)
	mu.Unlock()
	if len(got) != 1 || got[0] != "B.JPG" {
		t.Fatalf("server saw uploads %v, want exactly [B.JPG]", got)
	}
	if res.Uploaded != 1 {
		t.Fatalf("res.Uploaded = %d, want 1", res.Uploaded)
	}
	if res.Skipped != 2 {
		t.Fatalf("res.Skipped = %d, want 2", res.Skipped)
	}
}

// TestSyncOnlyNeverMovesCursor checks that a retry sync (SyncOptions.Only)
// leaves the volume cursor exactly where it was even when the retried file
// is newer than the cursor: moving it forward would skip files between the
// cursor and the retried one that were never uploaded (the failure list a
// retry comes from is capped). The retried file is still recorded in the
// files table as usual (its hash), and counted as uploaded.
func TestSyncOnlyNeverMovesCursor(t *testing.T) {
	withoutRetryDelays(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"a","status":"created"}`))
	}))
	defer srv.Close()

	t.Setenv("HOME", t.TempDir())
	cfgStore, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if err := cfgStore.Update(func(c *config.Config) {
		c.Immich = config.ImmichConfig{URL: srv.URL, APIKey: "key"}
		c.Watch = config.WatchConfig{VolumeUUID: "VOL-UUID", VolumeName: "CARD"}
	}); err != nil {
		t.Fatalf("cfgStore.Update: %v", err)
	}
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mount := t.TempDir()
	dcim := filepath.Join(mount, "DCIM")
	if err := os.MkdirAll(dcim, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"A.JPG", "B.JPG", "C.JPG"} {
		path := filepath.Join(dcim, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		mtime := time.Unix(int64(1000+i), 0)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	// Only A is behind the cursor; B was never uploaded; C failed and is
	// retried.
	if err := st.SetVolumeSync("VOL-UUID", time.Unix(1000, 0), "DCIM/A.JPG"); err != nil {
		t.Fatalf("SetVolumeSync: %v", err)
	}
	before, err := st.GetVolumeSync("VOL-UUID")
	if err != nil {
		t.Fatal(err)
	}

	vol := volume.Info{UUID: "VOL-UUID", Name: "CARD", MountPoint: mount}
	svc := &Service{Config: cfgStore, State: st}
	res, err := svc.Sync(context.Background(), vol, SyncOptions{Only: []string{"DCIM/C.JPG"}}, nil)
	if err != nil {
		t.Fatalf("Sync() error = %v, want nil", err)
	}
	if res.Uploaded != 1 {
		t.Fatalf("res.Uploaded = %d, want 1", res.Uploaded)
	}

	after, err := st.GetVolumeSync("VOL-UUID")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Mtime.Equal(before.Mtime) || after.RelativePath != before.RelativePath {
		t.Fatalf("cursor after retry = %+v, want unchanged %+v (B.JPG was never uploaded)", after, before)
	}

	rec, err := st.Get("VOL-UUID", "DCIM/C.JPG")
	if err != nil {
		t.Fatal(err)
	}
	if rec == nil || rec.SHA1 == "" {
		t.Fatalf("files record for the retried file = %+v, want it recorded with its hash", rec)
	}
}
