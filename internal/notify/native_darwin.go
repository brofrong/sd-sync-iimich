//go:build darwin

package notify

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

const (
	// queueSize bounds the notifications waiting to be shown; more are
	// dropped (with a log line) rather than blocking the caller.
	queueSize = 32
	// decideTimeout bounds how long notifications wait for the decision
	// whether native ones can be used: the authorization prompt waits for
	// the user (Wails gives up after 3 minutes), and the service may never
	// start. After it, osascript is used for the session.
	decideTimeout = 4 * time.Minute
	// categoryPrefix starts the IDs of the categories (sets of buttons)
	// registered for native notifications.
	categoryPrefix = "sd-sync-immich."
)

// backend is what Center needs from the native notifications service.
type backend interface {
	// start starts the service; it fails without a bundle identifier.
	start(ctx context.Context, opts application.ServiceOptions) error
	// authorize asks the user for permission (once; later calls return the
	// stored answer). It may wait for the user.
	authorize() (bool, error)
	registerCategory(id string, actions []Action) error
	send(m Message, categoryID string) error
	// onResponse registers fn for the user's responses.
	onResponse(fn func(actionID string, data map[string]string))
	// remove removes the delivered notification with id, if any.
	remove(id string)
}

// item is one job for the Center's goroutine: show msg, or, when remove is
// set, remove the notification with that ID.
type item struct {
	msg    Message
	remove string
}

// Center shows notifications: natively, with buttons, once the app is
// allowed to; through osascript, without buttons, when native ones are not
// available (no bundle identifier, as in a development build; permission
// denied; any error), for the rest of the session. Send never blocks: the
// notifications are shown in order by a goroutine of their own.
type Center struct {
	backend    backend // nil: osascript only
	onResponse func(actionID string, data map[string]string)

	queue chan item

	decideOnce sync.Once
	decided    chan struct{}

	mu         sync.Mutex
	native     bool
	categories map[string]string // registered category ID -> its button titles
}

// NewCenter returns a Center using svc (nil: osascript only). onResponse
// receives the action ID (DefaultAction for a click on the notification)
// and the Data of each native notification the user responds to; it runs
// on a goroutine of its own. Register Service() with the application: it
// starts svc and asks for permission.
func NewCenter(svc *notifications.NotificationService, onResponse func(actionID string, data map[string]string)) *Center {
	var b backend
	if svc != nil {
		b = &wailsBackend{svc: svc}
	}
	c := newCenter(b, onResponse)
	go c.run()
	return c
}

// newCenter returns a Center that is not running yet (tests drive it).
func newCenter(b backend, onResponse func(string, map[string]string)) *Center {
	c := &Center{
		backend:    b,
		onResponse: onResponse,
		queue:      make(chan item, queueSize),
		decided:    make(chan struct{}),
		categories: map[string]string{},
	}
	if b == nil {
		c.decide(false, "no native notifications service")
		return c
	}
	b.onResponse(func(actionID string, data map[string]string) {
		if c.onResponse != nil {
			go c.onResponse(actionID, data)
		}
	})
	return c
}

// Service is the Wails service to register with the application. Its
// startup starts the native notifications service and asks for permission
// in the background; it never fails the application's startup.
func (c *Center) Service() application.Service {
	return application.NewService(&centerService{c: c})
}

// Send queues m to be shown. It never blocks; when the queue is full, m is
// dropped.
func (c *Center) Send(m Message) {
	c.enqueue(item{msg: m})
}

// Remove removes the notification with id from Notification Center (after
// the notifications sent before it are shown). osascript notifications
// can't be removed: for them it does nothing.
func (c *Center) Remove(id string) {
	c.enqueue(item{remove: id})
}

func (c *Center) enqueue(it item) {
	select {
	case c.queue <- it:
	default:
		logx.Warn("notify: too many notifications waiting, dropping one", "id", it.msg.ID, "remove", it.remove)
	}
}

// startup starts the native service and, unless that fails, asks for
// permission in the background.
func (c *Center) startup(ctx context.Context, opts application.ServiceOptions) {
	if c.backend == nil {
		return
	}
	if err := c.backend.start(ctx, opts); err != nil {
		c.decide(false, "native notifications unavailable: "+err.Error())
		return
	}
	go func() {
		granted, err := c.backend.authorize()
		switch {
		case err != nil:
			c.decide(false, "notification authorization failed: "+err.Error())
		case !granted:
			c.decide(false, "notifications not allowed by the user")
		default:
			c.decide(true, "")
		}
	}()
}

// decide records, once, whether native notifications are used.
func (c *Center) decide(native bool, reason string) {
	c.decideOnce.Do(func() {
		c.mu.Lock()
		c.native = native
		c.mu.Unlock()
		if native {
			logx.Info("notify: using native notifications")
		} else {
			logx.Info("notify: using osascript notifications", "reason", reason)
		}
		close(c.decided)
	})
}

// run shows the queued notifications once it is decided how.
func (c *Center) run() {
	select {
	case <-c.decided:
	case <-time.After(decideTimeout):
		c.decide(false, "no answer about native notifications in time")
	}
	for it := range c.queue {
		c.process(it)
	}
}

func (c *Center) process(it item) {
	if it.remove == "" {
		c.deliver(it.msg)
		return
	}
	if c.usesNative() {
		c.backend.remove(it.remove)
	}
}

func (c *Center) usesNative() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.native
}

// deliver shows m natively if it can, else through osascript. Any native
// failure switches to osascript for the rest of the session.
func (c *Center) deliver(m Message) {
	if c.usesNative() {
		err := c.deliverNative(m)
		if err == nil {
			return
		}
		logx.Warn("notify: native notification failed, using osascript from now on", "id", m.ID, "err", err)
		c.mu.Lock()
		c.native = false
		c.mu.Unlock()
	}
	showFallback(m.Title, m.Body)
}

func (c *Center) deliverNative(m Message) error {
	categoryID, err := c.category(m.Actions)
	if err != nil {
		return err
	}
	return c.backend.send(m, categoryID)
}

// category returns the ID of the category with actions ("" for none),
// registering it first unless it already is with the same titles.
func (c *Center) category(actions []Action) (string, error) {
	if len(actions) == 0 {
		return "", nil
	}
	ids := make([]string, len(actions))
	titles := make([]string, len(actions))
	for i, a := range actions {
		ids[i] = a.ID
		titles[i] = a.Title
	}
	id := categoryPrefix + strings.Join(ids, "+")
	sig := strings.Join(titles, "\x00")

	c.mu.Lock()
	registered, ok := c.categories[id]
	c.mu.Unlock()
	if ok && registered == sig {
		return id, nil
	}
	if err := c.backend.registerCategory(id, actions); err != nil {
		return "", fmt.Errorf("register notification category %s: %w", id, err)
	}
	c.mu.Lock()
	c.categories[id] = sig
	c.mu.Unlock()
	return id, nil
}

// centerService is the Wails service of a Center. It has no methods the
// window could call.
type centerService struct {
	c *Center
}

func (s *centerService) ServiceName() string { return "sd-sync-immich/notify" }

func (s *centerService) ServiceStartup(ctx context.Context, opts application.ServiceOptions) error {
	s.c.startup(ctx, opts)
	return nil
}

// wailsBackend is the backend of the Wails notifications service.
type wailsBackend struct {
	svc *notifications.NotificationService
}

func (b *wailsBackend) start(ctx context.Context, opts application.ServiceOptions) error {
	return b.svc.ServiceStartup(ctx, opts)
}

func (b *wailsBackend) authorize() (bool, error) {
	return b.svc.RequestNotificationAuthorization()
}

func (b *wailsBackend) registerCategory(id string, actions []Action) error {
	na := make([]notifications.NotificationAction, len(actions))
	for i, a := range actions {
		na[i] = notifications.NotificationAction{ID: a.ID, Title: a.Title}
	}
	return b.svc.RegisterNotificationCategory(notifications.NotificationCategory{ID: id, Actions: na})
}

func (b *wailsBackend) send(m Message, categoryID string) error {
	data := make(map[string]any, len(m.Data))
	for k, v := range m.Data {
		data[k] = v
	}
	opts := notifications.NotificationOptions{
		ID:         m.ID,
		Title:      m.Title,
		Body:       m.Body,
		CategoryID: categoryID,
		Data:       data,
	}
	if categoryID == "" {
		return b.svc.SendNotification(opts)
	}
	return b.svc.SendNotificationWithActions(opts)
}

func (b *wailsBackend) remove(id string) {
	if err := b.svc.RemoveDeliveredNotification(id); err != nil {
		logx.Warn("notify: failed to remove a notification", "id", id, "err", err)
	}
}

func (b *wailsBackend) onResponse(fn func(actionID string, data map[string]string)) {
	b.svc.OnNotificationResponse(func(r notifications.NotificationResult) {
		if r.Error != nil {
			logx.Warn("notify: bad notification response", "err", r.Error)
			return
		}
		data := make(map[string]string, len(r.Response.UserInfo))
		for k, v := range r.Response.UserInfo {
			if s, ok := v.(string); ok {
				data[k] = s
			}
		}
		logx.Info("notify: notification response", "action", r.Response.ActionIdentifier, "id", r.Response.ID)
		fn(r.Response.ActionIdentifier, data)
	})
}
