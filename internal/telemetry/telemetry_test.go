package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/telemetryserver"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/testenv"
)

// server is a fake telemetry server that records the requests' bodies and
// answers status (200 when 0).
type server struct {
	mu     sync.Mutex
	status int
	bodies []payload
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var p payload
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodies = append(s.bodies, p)
	if s.status != 0 {
		w.WriteHeader(s.status)
	}
}

func (s *server) got() []payload {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]payload(nil), s.bodies...)
}

// newClient returns a client of a release build sending to srv, with the
// statistics turned on or off.
func newClient(t *testing.T, srv *server, enabled, release bool) (*Client, *config.Store) {
	t.Helper()
	testenv.Home(t)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) {
		c.Telemetry = config.TelemetryConfig{Enabled: enabled, Asked: true}
		if enabled {
			c.Telemetry.ID = "0123456789abcdef0123456789abcdef"
		}
	}); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	c := New(cfg, Info{Version: "v1.2.3", OS: "darwin", OSVersion: "15.1", Arch: "arm64", Install: "app", Lang: "ru"}, release)
	c.endpoint = ts.URL
	c.now = func() time.Time { return time.Unix(1700000000, 0) }
	return c, cfg
}

func TestSendsEventsWhenAllowed(t *testing.T) {
	srv := &server{}
	c, _ := newClient(t, srv, true, true)

	c.Launch()
	c.Synced(Sync{Result: "done", Files: 3, Uploaded: 2, Duplicates: 1, Bytes: 42, Duration: 90 * time.Second,
		ErrorKinds: map[string]int{"network": 1}})
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}

	got := srv.got()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	p := got[0]
	if p.ID != "0123456789abcdef0123456789abcdef" || p.Version != "v1.2.3" || p.OS != "darwin" || p.Install != "app" || p.Lang != "ru" {
		t.Fatalf("payload = %+v, want the install id and the app's info", p)
	}
	if len(p.Events) != 2 || p.Events[0].Type != EventLaunch || p.Events[1].Type != EventSync {
		t.Fatalf("events = %+v, want launch and sync", p.Events)
	}
	d := p.Events[1].Data
	if d["result"] != "done" || d["uploaded"] != float64(2) || d["seconds"] != float64(90) || p.Events[1].At != 1700000000 {
		t.Fatalf("sync event = %+v", p.Events[1])
	}
}

func TestSendsNothing(t *testing.T) {
	tests := []struct {
		name             string
		enabled, release bool
	}{
		{name: "not allowed", enabled: false, release: true},
		{name: "development build", enabled: true, release: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := &server{}
			c, _ := newClient(t, srv, tt.enabled, tt.release)
			c.Launch()
			c.Error("sync failed")
			if err := c.Flush(context.Background()); err != nil {
				t.Fatalf("Flush() error = %v", err)
			}
			if got := srv.got(); len(got) != 0 {
				t.Fatalf("requests = %+v, want none", got)
			}
		})
	}
}

func TestTurningOffDropsTheQueue(t *testing.T) {
	srv := &server{}
	c, cfg := newClient(t, srv, true, true)
	c.Launch()
	if err := cfg.Update(func(c *config.Config) { c.Telemetry = config.TelemetryConfig{Asked: true} }); err != nil {
		t.Fatal(err)
	}
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	if got := srv.got(); len(got) != 0 {
		t.Fatalf("requests = %+v, want none after turning off", got)
	}
}

func TestErrorsAreReportedOnceAnHourAndCut(t *testing.T) {
	srv := &server{}
	c, _ := newClient(t, srv, true, true)
	now := time.Unix(1700000000, 0)
	c.now = func() time.Time { return now }

	long := strings.Repeat("x", 300)
	c.Error(long)
	c.Error(long)
	now = now.Add(2 * time.Hour)
	c.Error(long)
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	events := srv.got()[0].Events
	if len(events) != 2 {
		t.Fatalf("error events = %d, want 2 (the repeat within an hour dropped)", len(events))
	}
	if msg := events[0].Data["message"].(string); len(msg) != maxMessage {
		t.Fatalf("message length = %d, want %d", len(msg), maxMessage)
	}
}

func TestFailedSendsAreKeptRefusedOnesDropped(t *testing.T) {
	srv := &server{status: http.StatusServiceUnavailable}
	c, _ := newClient(t, srv, true, true)
	c.Launch()
	if err := c.Flush(context.Background()); err == nil {
		t.Fatal("Flush() error = nil, want the 503")
	}
	srv.mu.Lock()
	srv.status = http.StatusBadRequest
	srv.mu.Unlock()
	if err := c.Flush(context.Background()); err == nil {
		t.Fatal("Flush() error = nil, want the 400")
	}
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("third Flush() error = %v, want nothing left to send", err)
	}
	if got := srv.got(); len(got) != 2 {
		t.Fatalf("requests = %d, want 2 (kept after 503, dropped after 400)", len(got))
	}
}

func TestQueueIsBoundedAndSentInBatches(t *testing.T) {
	srv := &server{}
	c, _ := newClient(t, srv, true, true)
	for range maxQueue + 10 {
		c.Launch()
	}
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() error = %v", err)
	}
	got := srv.got()
	total := 0
	for _, p := range got {
		if len(p.Events) > maxBatch {
			t.Fatalf("batch of %d events, want at most %d", len(p.Events), maxBatch)
		}
		total += len(p.Events)
	}
	if total != maxQueue {
		t.Fatalf("events sent = %d, want %d (the oldest dropped)", total, maxQueue)
	}
}

func TestNewID(t *testing.T) {
	a, b := NewID(), NewID()
	if len(a) != 32 || a == b {
		t.Fatalf("NewID() = %q, %q; want two different 32-character ids", a, b)
	}
}

// The server accepts what the app sends.
func TestServerAcceptsTheAppsEvents(t *testing.T) {
	testenv.Home(t)
	store, err := telemetryserver.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ts := httptest.NewServer((&telemetryserver.Server{Store: store, Password: "p"}).Handler())
	defer ts.Close()

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) {
		c.Telemetry = config.TelemetryConfig{Enabled: true, Asked: true, ID: NewID()}
	}); err != nil {
		t.Fatal(err)
	}
	c := New(cfg, Info{Version: "v1.2.3", OS: "linux", OSVersion: "Ubuntu 24.04.1 LTS", Arch: "amd64", Install: "flatpak", Lang: "pt-BR"}, true)
	c.endpoint = ts.URL + "/v1/events"
	c.Launch()
	c.Synced(Sync{Result: "done", Files: 1, Uploaded: 1, ErrorKinds: map[string]int{"rejected": 1}})
	c.Error("sync failed")
	if err := c.Flush(context.Background()); err != nil {
		t.Fatalf("Flush() to the real server error = %v", err)
	}
	st, err := store.Stats(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if st.Installs != 1 || st.Syncs.Syncs != 1 || len(st.Errors) != 1 {
		t.Fatalf("stats = %+v, want one install with a sync and an error", st)
	}
}
