// Package telemetry sends anonymous usage statistics and error reports,
// only when the user has allowed it (config.TelemetryConfig.Enabled, off
// by default) and only from release builds.
//
// What is sent, and nothing else: a random id made when the user allows
// it (a new one each time it is turned on again), the app's version, the
// system and its version, the CPU architecture, how the app is installed
// and the window's language; then events: the app started, it is still
// running (once a day), a sync's counts and outcome, and the messages of
// errors the app logs. Never file names, paths, card names, the server's
// address or the API key: sync events carry numbers and kinds, error
// events the log message alone, which is a constant in the code, without
// its details.
package telemetry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// Endpoint receives the events (see cmd/sd-sync-telemetry).
const Endpoint = "https://sd-sync-stats.brofrong.ru/v1/events"

const (
	// maxQueue bounds the events waiting to be sent; the oldest go first.
	maxQueue = 200
	// maxBatch is the most events one request carries, which keeps it
	// well under the server's limit.
	maxBatch = 50
	// flushDelay batches the events that come together (a sync's event
	// and the errors logged with it) into one request.
	flushDelay = 5 * time.Second
	// errorFlushEvery sends errors on their own at most this often.
	errorFlushEvery = 10 * time.Minute
	// heartbeatEvery is how often a running app says it still runs.
	heartbeatEvery = 24 * time.Hour
	// errorRepeatAfter drops an error message reported again sooner.
	errorRepeatAfter = time.Hour
	// maxMessage cuts long error messages.
	maxMessage = 120

	sendTimeout = 15 * time.Second
)

// Event types.
const (
	EventLaunch    = "launch"
	EventHeartbeat = "heartbeat"
	EventSync      = "sync"
	EventError     = "error"
)

// Info describes this copy of the app; it goes with every request.
type Info struct {
	Version   string `json:"version"`
	OS        string `json:"os"`
	OSVersion string `json:"osVersion,omitempty"`
	Arch      string `json:"arch"`
	// Install is how the app is installed: "app" (the macOS bundle),
	// "installer" (Windows), "appimage", "flatpak" or "binary".
	Install string `json:"install"`
	Lang    string `json:"lang,omitempty"`
}

// Event is one thing that happened. Data holds numbers and short codes
// only (see the package comment).
type Event struct {
	Type string         `json:"type"`
	At   int64          `json:"at"`
	Data map[string]any `json:"data,omitempty"`
}

// payload is a request's body.
type payload struct {
	ID string `json:"id"`
	Info
	Events []Event `json:"events"`
}

// Sync is a finished sync, as the sync event reports it.
type Sync struct {
	// Result is the final status code, such as "done" or "error_server".
	Result     string
	Files      int
	Uploaded   int
	Duplicates int
	Skipped    int
	Errors     int
	Stacks     int
	Bytes      int64
	Duration   time.Duration
	// ErrorKinds counts the failures by kind ("network", "rejected"…).
	ErrorKinds map[string]int
}

// Client queues events and sends them while the user allows it.
type Client struct {
	cfg      *config.Store
	info     Info
	endpoint string
	http     *http.Client
	now      func() time.Time
	// release reports whether this is a release build; development builds
	// never send anything.
	release bool

	mu         sync.Mutex
	queue      []Event
	lastErrors map[string]time.Time
	wake       chan struct{}
}

// New returns a client for the app described by info; release is whether
// this is a release build (see version.IsRelease).
func New(cfg *config.Store, info Info, release bool) *Client {
	return &Client{
		cfg:        cfg,
		info:       info,
		endpoint:   Endpoint,
		http:       &http.Client{Timeout: sendTimeout},
		now:        time.Now,
		release:    release,
		lastErrors: make(map[string]time.Time),
		wake:       make(chan struct{}, 1),
	}
}

// SetLanguage updates the window's language sent with the events.
func (c *Client) SetLanguage(lang string) {
	c.mu.Lock()
	c.info.Lang = lang
	c.mu.Unlock()
}

// id returns the install id when sending is allowed, "" otherwise.
func (c *Client) id() string {
	if c == nil || !c.release {
		return ""
	}
	t := c.cfg.Get().Telemetry
	if !t.Enabled {
		return ""
	}
	return t.ID
}

// Launch records that the app started.
func (c *Client) Launch() { c.add(Event{Type: EventLaunch}, true) }

// Synced records a finished sync.
func (c *Client) Synced(s Sync) {
	data := map[string]any{
		"result":     s.Result,
		"files":      s.Files,
		"uploaded":   s.Uploaded,
		"duplicates": s.Duplicates,
		"skipped":    s.Skipped,
		"errors":     s.Errors,
		"stacks":     s.Stacks,
		"bytes":      s.Bytes,
		"seconds":    int64(s.Duration.Round(time.Second) / time.Second),
	}
	if len(s.ErrorKinds) > 0 {
		data["errorKinds"] = s.ErrorKinds
	}
	c.add(Event{Type: EventSync, Data: data}, true)
}

// Error records an error message the app logged. The same message is
// recorded at most once an hour.
func (c *Client) Error(msg string) {
	if c.id() == "" {
		return
	}
	if len(msg) > maxMessage {
		msg = msg[:maxMessage]
	}
	now := c.now()
	c.mu.Lock()
	if last, ok := c.lastErrors[msg]; ok && now.Sub(last) < errorRepeatAfter {
		c.mu.Unlock()
		return
	}
	c.lastErrors[msg] = now
	c.mu.Unlock()
	c.add(Event{Type: EventError, Data: map[string]any{"message": msg}}, false)
}

func (c *Client) add(e Event, soon bool) {
	if c.id() == "" {
		return
	}
	e.At = c.now().Unix()
	c.mu.Lock()
	c.queue = append(c.queue, e)
	if over := len(c.queue) - maxQueue; over > 0 {
		c.queue = c.queue[over:]
	}
	c.mu.Unlock()
	if soon {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}

// Run sends the queued events until ctx is cancelled: a few seconds after
// a launch or sync event, every errorFlushEvery for errors, and a
// heartbeat every heartbeatEvery.
func (c *Client) Run(ctx context.Context) {
	flush := time.NewTicker(errorFlushEvery)
	defer flush.Stop()
	beat := time.NewTicker(heartbeatEvery)
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
			select {
			case <-ctx.Done():
				return
			case <-time.After(flushDelay):
			}
		case <-flush.C:
		case <-beat.C:
			c.add(Event{Type: EventHeartbeat}, false)
		}
		if err := c.Flush(ctx); err != nil && !errors.Is(err, context.Canceled) {
			// Not logged as an error: that would report itself.
			logx.Debug("telemetry: send failed", "err", err)
		}
	}
}

// Flush sends the queued events now. Events that could not be sent stay
// queued for the next try; when sending is no longer allowed they are
// dropped.
func (c *Client) Flush(ctx context.Context) error {
	id := c.id()
	c.mu.Lock()
	if id == "" {
		c.queue = nil
		c.mu.Unlock()
		return nil
	}
	n := min(len(c.queue), maxBatch)
	if n == 0 {
		c.mu.Unlock()
		return nil
	}
	batch := append([]Event(nil), c.queue[:n]...)
	body, err := json.Marshal(payload{ID: id, Info: c.info, Events: batch})
	c.mu.Unlock()
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	// A request the server refuses (4xx) would be refused again: drop it.
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("telemetry server answered %d", resp.StatusCode)
	}
	c.mu.Lock()
	c.queue = c.queue[min(n, len(c.queue)):]
	more := len(c.queue) > 0
	c.mu.Unlock()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("telemetry server answered %d", resp.StatusCode)
	}
	if more {
		return c.Flush(ctx)
	}
	return nil
}

// NewID returns a fresh random install id.
func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("telemetry: random id: %v", err))
	}
	return hex.EncodeToString(b)
}
