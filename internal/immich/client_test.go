package immich

import (
	"context"
	"encoding/json"
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

func TestDetail(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"dns error", &net.DNSError{Err: "no such host", Name: "immich.example"}, "no such host"},
		{"op error", &net.OpError{Op: "dial", Err: errors.New("connection refused")}, "connection refused"},
		{"status error", &StatusError{Code: 502, Message: "bad gateway"}, "bad gateway"},
		{"generic error", errors.New("boom"), "boom"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Detail(tt.err); got != tt.want {
				t.Errorf("Detail(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}

func TestCheckReturnsServerVersion(t *testing.T) {
	var gotAPIKey string
	c := newTLSClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/server/version":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"major":2,"minor":3,"patch":1}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/assets/bulk-upload-check":
			gotAPIKey = r.Header.Get("x-api-key")
			var body struct {
				Assets []struct {
					ID       string `json:"id"`
					Checksum string `json:"checksum"`
				} `json:"assets"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("bulk-upload-check body: %v", err)
			}
			// Immich v3.3.4 fails an empty list with 500 "Failed to check
			// bulk upload".
			if len(body.Assets) == 0 {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"message":"Failed to check bulk upload"}`)
				return
			}
			if len(body.Assets) != 1 || body.Assets[0].ID == "" || len(body.Assets[0].Checksum) != 40 {
				t.Errorf("bulk-upload-check assets = %+v, want one probe with a SHA-1 hex checksum", body.Assets)
			}
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, `{"results":[{"id":%q,"action":"accept"}]}`, body.Assets[0].ID)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	info, err := c.Check(context.Background())
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if info.Version != "v2.3.1" {
		t.Fatalf("Version = %q, want v2.3.1", info.Version)
	}
	if gotAPIKey != "key" {
		t.Fatalf("bulk-upload-check x-api-key = %q, want %q", gotAPIKey, "key")
	}
}

func TestCheckRejectsBadKey(t *testing.T) {
	c := newTLSClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/server/version":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"major":2,"minor":3,"patch":1}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/assets/bulk-upload-check":
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"Invalid API key"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	_, err := c.Check(context.Background())
	var se *StatusError
	if !errors.As(err, &se) || se.Code != http.StatusUnauthorized {
		t.Fatalf("Check() error = %v, want *StatusError with 401", err)
	}
}

func TestPingUsesContext(t *testing.T) {
	c := newTLSClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := c.Ping(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ping() error = %v, want context.Canceled", err)
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

// endless writes spaces until the client goes away.
func endless(w http.ResponseWriter) {
	chunk := make([]byte, 32<<10)
	for i := range chunk {
		chunk[i] = ' '
	}
	for {
		if _, err := w.Write(chunk); err != nil {
			return
		}
	}
}

func TestCheckBoundsResponseBodies(t *testing.T) {
	for _, path := range []string{"/api/server/version", "/api/assets/bulk-upload-check"} {
		t.Run(path, func(t *testing.T) {
			c := newTLSClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == path {
					w.WriteHeader(http.StatusBadGateway)
					endless(w)
					return
				}
				fmt.Fprint(w, `{"major":2,"minor":3,"patch":1}`)
			})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, err := c.Check(ctx)
			if err == nil || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
				t.Fatalf("Check() error = %v; want a prompt error after reading a bounded body", err)
			}
		})
	}
}
