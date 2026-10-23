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

	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
)

// TestUploadAllCountsDuplicatesApart checks that files Immich already has
// are counted as duplicates, not uploads, in the result and in the live
// progress, next to uploads and errors.
func TestUploadAllCountsDuplicatesApart(t *testing.T) {
	withoutRetryDelays(t)

	// By file: DSC00000 is new, DSC00001 Immich already has, DSC00002 is
	// rejected, DSC00003 is new.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch {
		case bytes.Contains(body, []byte("DSC00001")):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"b","status":"duplicate"}`))
		case bytes.Contains(body, []byte("DSC00002")):
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"a","status":"created"}`))
		}
	}))
	defer srv.Close()

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dir := t.TempDir()
	var files []scanner.File
	for i := range 4 {
		name := fmt.Sprintf("DSC%05d.JPG", i)
		path := filepath.Join(dir, name)
		// Distinct contents: equal ones would share a checksum.
		if err := os.WriteFile(path, bytes.Repeat([]byte{byte('a' + i)}, 100), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, scanner.File{AbsolutePath: path, RelativePath: "DCIM/" + name, Size: 100, Mtime: time.Unix(int64(1000+i), 0)})
	}

	var mu sync.Mutex
	var last Progress
	onProgress := func(p Progress) {
		mu.Lock()
		defer mu.Unlock()
		if p.Current >= last.Current {
			last = p
		}
	}

	svc := &Service{State: st}
	session := newSyncSession(st, "vol", files)
	res, err := svc.uploadAll(context.Background(), "vol", immich.NewClient(srv.URL, "key"), 1, false, len(files)+5, 5, files,
		newSyncProgress(len(files), 400, onProgress), session, newSyncCheckpoint(st, session, 150))
	if err != nil {
		t.Fatalf("uploadAll() error = %v, want nil", err)
	}

	if res.Uploaded != 2 || res.Duplicates != 1 || res.Errors != 1 || res.Skipped != 5 {
		t.Fatalf("result = %+v, want 2 uploaded, 1 duplicate, 1 error, 5 skipped", res)
	}
	if res.Bytes != 200 {
		t.Fatalf("res.Bytes = %d, want 200 (the uploads only)", res.Bytes)
	}
	if last.Current != 4 || last.Uploaded != 2 || last.Duplicates != 1 || last.Errors != 1 {
		t.Fatalf("last progress = %+v, want 4 done: 2 uploaded, 1 duplicate, 1 error", last)
	}
}
