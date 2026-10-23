//go:build darwin

package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// fakeBackend stands in for the Wails notifications service.
type fakeBackend struct {
	startErr   error
	granted    bool
	authErr    error
	sendErr    error
	mu         sync.Mutex
	categories []string
	sent       []Message
	removed    []string
	respond    func(string, map[string]string)
}

func (b *fakeBackend) start(context.Context, application.ServiceOptions) error { return b.startErr }
func (b *fakeBackend) authorize() (bool, error)                                { return b.granted, b.authErr }

func (b *fakeBackend) registerCategory(id string, _ []Action) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.categories = append(b.categories, id)
	return nil
}

func (b *fakeBackend) send(m Message, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sendErr != nil {
		return b.sendErr
	}
	b.sent = append(b.sent, m)
	return nil
}

func (b *fakeBackend) onResponse(fn func(string, map[string]string)) { b.respond = fn }

func (b *fakeBackend) remove(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.removed = append(b.removed, id)
}

// fallbackRecorder replaces showFallback for the test, so osascript never
// runs, and returns the titles it was asked to show.
func fallbackRecorder(t *testing.T) func() []string {
	t.Helper()
	var mu sync.Mutex
	var shown []string
	prev := showFallback
	showFallback = func(title, _ string) {
		mu.Lock()
		defer mu.Unlock()
		shown = append(shown, title)
	}
	t.Cleanup(func() { showFallback = prev })
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), shown...)
	}
}

// started starts c and waits until it decided how to show notifications.
func started(t *testing.T, c *Center) {
	t.Helper()
	c.startup(context.Background(), application.ServiceOptions{})
	select {
	case <-c.decided:
	case <-time.After(5 * time.Second):
		t.Fatal("no decision about native notifications")
	}
}

var withButton = Message{ID: "x", Title: "T", Body: "B", Actions: []Action{{ID: "retry", Title: "Retry"}}}

func TestCenterUsesNativeWhenAllowed(t *testing.T) {
	shown := fallbackRecorder(t)
	b := &fakeBackend{granted: true}
	c := newCenter(b, nil)
	started(t, c)

	c.deliver(withButton)
	c.deliver(withButton)
	c.deliver(Message{ID: "y", Title: "plain"})
	if len(b.sent) != 3 || len(shown()) != 0 {
		t.Fatalf("native %d, osascript %v; want 3 native", len(b.sent), shown())
	}
	if len(b.categories) != 1 {
		t.Errorf("categories registered: %v, want one", b.categories)
	}
}

func TestCenterFallsBack(t *testing.T) {
	tests := []struct {
		name string
		b    *fakeBackend
	}{
		{"no bundle identifier", &fakeBackend{startErr: errors.New("notifications require a valid bundle identifier"), granted: true}},
		{"denied", &fakeBackend{granted: false}},
		{"authorization error", &fakeBackend{authErr: errors.New("boom")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shown := fallbackRecorder(t)
			c := newCenter(tt.b, nil)
			started(t, c)
			c.deliver(withButton)
			if len(tt.b.sent) != 0 || len(shown()) != 1 {
				t.Fatalf("native %d, osascript %v; want osascript only", len(tt.b.sent), shown())
			}
		})
	}

	t.Run("no service", func(t *testing.T) {
		shown := fallbackRecorder(t)
		c := newCenter(nil, nil)
		c.startup(context.Background(), application.ServiceOptions{})
		c.deliver(withButton)
		if len(shown()) != 1 {
			t.Fatalf("osascript %v; want one", shown())
		}
	})
}

func TestCenterSendFailureSwitchesToFallbackForTheSession(t *testing.T) {
	shown := fallbackRecorder(t)
	b := &fakeBackend{granted: true, sendErr: errors.New("failed")}
	c := newCenter(b, nil)
	started(t, c)

	c.deliver(withButton)
	b.sendErr = nil
	c.deliver(withButton)
	if len(b.sent) != 0 || len(shown()) != 2 {
		t.Fatalf("native %d, osascript %v; want both through osascript", len(b.sent), shown())
	}
}

func TestCenterForwardsResponses(t *testing.T) {
	got := make(chan string, 1)
	b := &fakeBackend{}
	newCenter(b, func(action string, data map[string]string) { got <- action + " " + data["uuid"] })
	b.respond("select_card", map[string]string{"uuid": "U"})
	select {
	case s := <-got:
		if s != "select_card U" {
			t.Fatalf("response = %q", s)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("response not forwarded")
	}
}

func TestCenterRemove(t *testing.T) {
	fallbackRecorder(t)
	b := &fakeBackend{granted: true}
	c := newCenter(b, nil)
	started(t, c)
	c.process(item{remove: "sd-sync-immich.format_pending"})
	if len(b.removed) != 1 || b.removed[0] != "sd-sync-immich.format_pending" {
		t.Fatalf("removed = %v, want the countdown's notification", b.removed)
	}

	// osascript notifications can't be removed: nothing to do.
	b = &fakeBackend{granted: false}
	c = newCenter(b, nil)
	started(t, c)
	c.process(item{remove: "x"})
	if len(b.removed) != 0 {
		t.Fatalf("removed = %v with osascript notifications, want nothing", b.removed)
	}
}
