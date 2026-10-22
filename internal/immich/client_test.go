package immich

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestFile(t *testing.T, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "DSC00001.JPG")
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// newTLSClient points a Client at an HTTP/2-capable TLS test server.
func newTLSClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, "key")
	c.HTTP.Transport.(*http.Transport).TLSClientConfig.RootCAs = srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs
	return c
}

func TestUploadUsesHTTP1EvenWhenServerOffersHTTP2(t *testing.T) {
	protos := make(chan string, 1)
	c := newTLSClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		protos <- r.Proto
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"a","status":"created"}`)
	})

	resp, err := c.Upload(context.Background(), newTestFile(t, 1024), "", time.Now(), "")
	if err != nil {
		t.Fatalf("Upload() error = %v", err)
	}
	if resp.Status != "created" {
		t.Fatalf("status = %q, want created", resp.Status)
	}
	if got := <-protos; got != "HTTP/1.1" {
		t.Fatalf("request proto = %s, want HTTP/1.1", got)
	}
}

func TestUploadReturnsStatusErrorForHTTPFailures(t *testing.T) {
	c := newTLSClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, `{"message":"upstream down"}`)
	})

	_, err := c.Upload(context.Background(), newTestFile(t, 1024), "", time.Now(), "")
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusBadGateway {
		t.Fatalf("Upload() error = %v, want *StatusError with 502", err)
	}
}

func TestUploadFailsWhenServerStopsReadingBody(t *testing.T) {
	release := make(chan struct{})
	c := newTLSClient(t, func(w http.ResponseWriter, r *http.Request) {
		<-release // never read the body, so the upload stalls
	})
	defer close(release)
	c.StallTimeout = 300 * time.Millisecond

	done := make(chan error, 1)
	go func() {
		_, err := c.Upload(context.Background(), newTestFile(t, 64<<20), "", time.Now(), "")
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, ErrUploadStalled) {
			t.Fatalf("Upload() error = %v, want ErrUploadStalled", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Upload() did not give up on a stalled connection")
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"server error", &StatusError{Code: 502}, true},
		{"rate limited", &StatusError{Code: 429}, true},
		{"rejected file", &StatusError{Code: 400}, false},
		{"unauthorized", &StatusError{Code: 401}, false},
		{"network", &net.OpError{Op: "dial", Err: errors.New("no such host")}, true},
		{"stalled", ErrUploadStalled, true},
		{"cancelled", context.Canceled, false},
		{"missing file", os.ErrNotExist, false},
	}
	for _, tt := range tests {
		if got := IsRetryable(tt.err); got != tt.want {
			t.Errorf("IsRetryable(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestUploadReturnsDuplicateWhenServerAnswersBeforeReadingBody(t *testing.T) {
	c := newTLSClient(t, func(w http.ResponseWriter, r *http.Request) {
		// Immich short-circuits known checksums without consuming the upload.
		w.Header().Set("Connection", "close")
		fmt.Fprint(w, `{"id":"a","status":"duplicate"}`)
	})

	done := make(chan error, 1)
	var resp *UploadResponse
	go func() {
		var err error
		resp, err = c.Upload(context.Background(), newTestFile(t, 64<<20), "abc", time.Now(), "")
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Upload() error = %v", err)
		}
		if resp.Status != "duplicate" {
			t.Fatalf("status = %q, want duplicate", resp.Status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Upload() hung after an early duplicate response")
	}
}
