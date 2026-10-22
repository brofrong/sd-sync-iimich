package uploader

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
)

func withoutRetryDelays(t *testing.T) {
	t.Helper()
	saved := retryDelays
	retryDelays = []time.Duration{0, 0, 0}
	t.Cleanup(func() { retryDelays = saved })
}

func TestUploadWithRetryRetriesTransientErrors(t *testing.T) {
	withoutRetryDelays(t)
	attempts := 0
	resp, err := uploadWithRetry(context.Background(), func() (*immich.UploadResponse, error) {
		attempts++
		if attempts < 3 {
			return nil, &immich.StatusError{Code: http.StatusBadGateway}
		}
		return &immich.UploadResponse{Status: "created"}, nil
	})
	if err != nil || resp.Status != "created" {
		t.Fatalf("uploadWithRetry() = %v, %v; want created", resp, err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func TestUploadWithRetryDoesNotRetryRejectedFiles(t *testing.T) {
	withoutRetryDelays(t)
	attempts := 0
	_, err := uploadWithRetry(context.Background(), func() (*immich.UploadResponse, error) {
		attempts++
		return nil, &immich.StatusError{Code: http.StatusBadRequest}
	})
	if err == nil || attempts != 1 {
		t.Fatalf("attempts = %d, err = %v; want one failed attempt", attempts, err)
	}
}

func TestUploadWithRetryGivesUpAfterAllAttempts(t *testing.T) {
	withoutRetryDelays(t)
	attempts := 0
	_, err := uploadWithRetry(context.Background(), func() (*immich.UploadResponse, error) {
		attempts++
		return nil, immich.ErrUploadStalled
	})
	if !errors.Is(err, immich.ErrUploadStalled) {
		t.Fatalf("err = %v, want ErrUploadStalled", err)
	}
	if want := len(retryDelays) + 1; attempts != want {
		t.Fatalf("attempts = %d, want %d", attempts, want)
	}
}

func TestUploadWithRetryStopsWaitingWhenCancelled(t *testing.T) {
	saved := retryDelays
	retryDelays = []time.Duration{time.Hour}
	t.Cleanup(func() { retryDelays = saved })

	ctx, cancel := context.WithCancel(context.Background())
	_, err := uploadWithRetry(ctx, func() (*immich.UploadResponse, error) {
		cancel()
		return nil, immich.ErrUploadStalled
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestUploadAllStopsWhenServerIsUnreachable(t *testing.T) {
	withoutRetryDelays(t)

	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dir := t.TempDir()
	var files []scanner.File
	for i := range 5 {
		name := fmt.Sprintf("DSC%05d.JPG", i)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, scanner.File{AbsolutePath: path, RelativePath: "DCIM/" + name, Size: int64(len(name)), Mtime: time.Unix(int64(1000+i), 0)})
	}

	svc := &Service{State: st}
	session := newSyncSession(st, "vol", files)
	res, err := svc.uploadAll(context.Background(), "vol", immich.NewClient(srv.URL, "key"), 1, len(files), 0, files,
		newSyncProgress(len(files), 0, nil), session, newSyncCheckpoint(st, session, 150))

	if !errors.Is(err, ErrServerUnavailable) {
		t.Fatalf("uploadAll() error = %v, want ErrServerUnavailable", err)
	}
	if want := int64(len(retryDelays) + 1); requests.Load() != want {
		t.Fatalf("server got %d requests, want %d (only the first file)", requests.Load(), want)
	}
	if res.Uploaded != 0 || res.Errors != 1 {
		t.Fatalf("result = %+v, want 0 uploaded and 1 error", res)
	}
}

func TestUploadAllStopsOnRejectedAPIKey(t *testing.T) {
	withoutRetryDelays(t)

	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusUnauthorized)
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
	res, err := svc.uploadAll(context.Background(), "vol", immich.NewClient(srv.URL, "key"), 1, len(files), 0, files,
		newSyncProgress(len(files), 0, nil), session, newSyncCheckpoint(st, session, 150))

	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("uploadAll() error = %v, want ErrAuthFailed", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("server got %d requests, want 1 (stop after the first rejected key)", requests.Load())
	}
	if res.Errors != 1 {
		t.Fatalf("res.Errors = %d, want 1", res.Errors)
	}
}

func TestUploadAllReportsBytesAndErrorKinds(t *testing.T) {
	withoutRetryDelays(t)

	var seq atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if seq.Add(1) == 2 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"a","status":"created"}`))
	}))
	defer srv.Close()

	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	dir := t.TempDir()
	sizes := []int{100, 200, 300}
	var files []scanner.File
	var wantBytes int64
	for i, sz := range sizes {
		name := fmt.Sprintf("DSC%05d.JPG", i)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, bytes.Repeat([]byte("x"), sz), 0o600); err != nil {
			t.Fatal(err)
		}
		files = append(files, scanner.File{AbsolutePath: path, RelativePath: "DCIM/" + name, Size: int64(sz), Mtime: time.Unix(int64(1000+i), 0)})
		if i != 1 { // the second file is rejected by the server below
			wantBytes += int64(sz)
		}
	}

	svc := &Service{State: st}
	session := newSyncSession(st, "vol", files)
	res, err := svc.uploadAll(context.Background(), "vol", immich.NewClient(srv.URL, "key"), 1, len(files), 0, files,
		newSyncProgress(len(files), 600, nil), session, newSyncCheckpoint(st, session, 150))

	if err != nil {
		t.Fatalf("uploadAll() error = %v, want nil", err)
	}
	if res.Uploaded != 2 || res.Errors != 1 {
		t.Fatalf("result = %+v, want 2 uploaded and 1 error", res)
	}
	if res.Bytes != wantBytes {
		t.Fatalf("res.Bytes = %d, want %d", res.Bytes, wantBytes)
	}
	if res.ErrorSummary["rejected"] != 1 {
		t.Fatalf("res.ErrorSummary[%q] = %d, want 1 (ErrorSummary = %+v)", "rejected", res.ErrorSummary["rejected"], res.ErrorSummary)
	}
}
