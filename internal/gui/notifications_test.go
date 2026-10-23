package gui

import (
	"strings"
	"sync"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/i18n"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/notify"
)

// fakeSender records the notifications it is asked to show, instead of
// showing them.
type fakeSender struct {
	mu   sync.Mutex
	sent []notify.Message
}

func (f *fakeSender) Send(m notify.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
}

func (f *fakeSender) messages() []notify.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notify.Message(nil), f.sent...)
}

// count returns how many notifications were sent for code (or notice kind).
func (f *fakeSender) count(code string) int {
	n := 0
	for _, m := range f.messages() {
		if m.Data["code"] == code || m.Data["notice"] == code {
			n++
		}
	}
	return n
}

func englishCatalog(t *testing.T) *i18n.Catalog {
	t.Helper()
	c, err := i18n.Load("en")
	if err != nil {
		t.Fatalf("load en: %v", err)
	}
	return c
}

// notifyConfig is the default config (every notification on, auto-eject
// on, auto-format off) changed by edit.
func notifyConfig(edit func(*config.Config)) config.Config {
	cfg := config.Default()
	if edit != nil {
		edit(&cfg)
	}
	return cfg
}

func statusOf(code app.Code, params map[string]string) app.Status {
	if params == nil {
		params = map[string]string{}
	}
	return app.Status{Code: code, Params: params}
}

func TestNotificationKind(t *testing.T) {
	plain := notifyConfig(func(c *config.Config) { c.Card.AutoEject = false; c.Card.AutoFormat = false })
	eject := notifyConfig(func(c *config.Config) { c.Card.AutoEject = true; c.Card.AutoFormat = false })
	format := notifyConfig(func(c *config.Config) { c.Card.AutoEject = false; c.Card.AutoFormat = true })
	both := notifyConfig(func(c *config.Config) { c.Card.AutoEject = true; c.Card.AutoFormat = true })

	tests := []struct {
		code   app.Code
		cfg    config.Config
		want   string
		wantOK bool
	}{
		// "done" is told by the ejected card, or by a format phase, when
		// either follows; else by done itself.
		{app.CodeDone, plain, kindDone, true},
		{app.CodeDone, eject, "", false},
		{app.CodeDone, format, "", false},
		{app.CodeDone, both, "", false},
		{app.CodeEjected, plain, kindDone, true},
		{app.CodeEjected, both, kindDone, true},
		{app.CodeDoneWithErrors, plain, kindErrors, true},
		{app.CodeDoneWithErrors, both, kindErrors, true},
		{app.CodeOtherCardMounted, plain, kindOtherCard, true},
		{app.CodeServerUnreachable, plain, kindServer, true},
		{app.CodeAuthFailed, plain, kindServer, true},
		{app.CodeServerError, plain, kindServer, true},
		{app.CodeFormatPending, both, kindCard, true},
		{app.CodeFormatted, both, kindCard, true},
		{app.CodeFormatSkippedMissing, both, kindCard, true},
		{app.CodeFormatSkippedUnsupported, both, kindCard, true},
		{app.CodeFormatFailed, both, kindCard, true},
		// Passing states, and ones the user caused or sees in the window.
		{app.CodeNoCardSelected, plain, "", false},
		{app.CodeNotConfigured, plain, "", false},
		{app.CodeWaitingForCard, plain, "", false},
		{app.CodeScanning, plain, "", false},
		{app.CodeSyncing, plain, "", false},
		{app.CodeNothingNew, plain, "", false},
		{app.CodeVerifying, both, "", false},
		{app.CodeFormatting, both, "", false},
		{app.CodeCancelled, plain, "", false},
		{app.CodeCardRemoved, plain, "", false},
	}
	for _, tt := range tests {
		kind, ok := notificationKind(tt.code, tt.cfg)
		if kind != tt.want || ok != tt.wantOK {
			t.Errorf("notificationKind(%s, eject=%v format=%v) = %q, %v; want %q, %v",
				tt.code, tt.cfg.Card.AutoEject, tt.cfg.Card.AutoFormat, kind, ok, tt.want, tt.wantOK)
		}
	}

	for _, k := range []app.NoticeKind{app.NoticeCardFollowed, app.NoticeEjectFailed} {
		if got := noticeKind(k); got != kindCard {
			t.Errorf("noticeKind(%s) = %q, want %q", k, got, kindCard)
		}
	}
	if got := noticeKind("unknown"); got != "" {
		t.Errorf("noticeKind(unknown) = %q, want \"\"", got)
	}
}

func TestNotificationsDisabled(t *testing.T) {
	c := englishCatalog(t)
	unreachable := statusOf(app.CodeServerUnreachable, map[string]string{"host": "immich.example", "detail": "timeout"})
	followed := app.Notice{Kind: app.NoticeCardFollowed, Params: map[string]string{"name": "Brofrong"}}

	tests := []struct {
		name string
		edit func(*config.Config)
		want int
	}{
		{"all on", nil, 1},
		{"all off", func(c *config.Config) { c.Notifications.Enabled = false }, 0},
		{"kind off", func(c *config.Config) { c.Notifications.Server = false; c.Notifications.Card = false }, 0},
		{"other kinds off", func(c *config.Config) {
			c.Notifications.Done = false
			c.Notifications.Errors = false
			c.Notifications.OtherCard = false
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := notifyConfig(tt.edit)
			f := &fakeSender{}
			n := newNotifier(f)
			n.status(unreachable, cfg, c)
			n.notice(followed, cfg, c)
			if got := f.count(string(app.CodeServerUnreachable)); got != tt.want {
				t.Errorf("server_unreachable notifications = %d, want %d", got, tt.want)
			}
			if got := f.count(string(app.NoticeCardFollowed)); got != tt.want {
				t.Errorf("card_followed notifications = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNotifyGateOncePerCard(t *testing.T) {
	c := englishCatalog(t)
	cfg := notifyConfig(func(c *config.Config) { c.Card.AutoEject = false })
	f := &fakeSender{}
	n := newNotifier(f)
	unreachable := statusOf(app.CodeServerUnreachable, map[string]string{"host": "immich.example", "detail": "timeout"})

	// Automatic retries keep failing: one notification.
	n.status(unreachable, cfg, c)
	n.status(statusOf(app.CodeScanning, nil), cfg, c)
	n.status(unreachable, cfg, c)
	if got := f.count(string(app.CodeServerUnreachable)); got != 1 {
		t.Fatalf("after two failures: %d server notifications, want 1", got)
	}

	// A successful sync resets the gate.
	n.status(statusOf(app.CodeDone, map[string]string{"uploaded": "3"}), cfg, c)
	n.status(unreachable, cfg, c)
	if got := f.count(string(app.CodeServerUnreachable)); got != 2 {
		t.Fatalf("after done: %d server notifications, want 2", got)
	}

	// So do the card leaving and an up-to-date card, even when they are
	// not notified themselves.
	n.status(unreachable, cfg, c)
	n.status(statusOf(app.CodeWaitingForCard, map[string]string{"name": "Card"}), cfg, c)
	n.status(unreachable, cfg, c)
	n.status(statusOf(app.CodeNothingNew, nil), cfg, c)
	n.status(unreachable, cfg, c)
	if got := f.count(string(app.CodeServerUnreachable)); got != 4 {
		t.Fatalf("after waiting_for_card and nothing_new: %d server notifications, want 4", got)
	}

	// Disabled notifications still reset it.
	off := notifyConfig(func(c *config.Config) { c.Notifications.Enabled = false })
	n.status(statusOf(app.CodeEjected, map[string]string{"uploaded": "1"}), off, c)
	n.status(unreachable, cfg, c)
	if got := f.count(string(app.CodeServerUnreachable)); got != 5 {
		t.Fatalf("after ejected: %d server notifications, want 5", got)
	}
}

func TestFormatPendingNotifiedOnce(t *testing.T) {
	c := englishCatalog(t)
	cfg := notifyConfig(func(c *config.Config) { c.Card.AutoFormat = true })
	f := &fakeSender{}
	n := newNotifier(f)

	for _, s := range []string{"30", "29", "28"} {
		n.status(statusOf(app.CodeFormatPending, map[string]string{"seconds": s, "name": "Brofrong"}), cfg, c)
	}
	msgs := f.messages()
	if len(msgs) != 1 {
		t.Fatalf("sent %d notifications for one countdown, want 1: %+v", len(msgs), msgs)
	}
	m := msgs[0]
	if len(m.Actions) != 1 || m.Actions[0].ID != string(app.ActionCancelFormat) {
		t.Errorf("actions = %+v, want one %q", m.Actions, app.ActionCancelFormat)
	}
	if !strings.Contains(m.Body, "Brofrong") || !strings.Contains(m.Body, "30") {
		t.Errorf("body = %q, want the card name and the seconds", m.Body)
	}
}

func TestNotificationActions(t *testing.T) {
	c := englishCatalog(t)
	cfg := notifyConfig(func(c *config.Config) { c.Card.AutoEject = false })

	tests := []struct {
		st     app.Status
		action app.Action // "" for none
	}{
		{statusOf(app.CodeOtherCardMounted, map[string]string{
			"selected_name": "Brofrong", "mounted_name": "Other", "mounted_uuid": "UUID-2",
		}), app.ActionSelectCard},
		{statusOf(app.CodeDoneWithErrors, map[string]string{"uploaded": "3", "errors": "2"}), app.ActionOpenHistory},
		{statusOf(app.CodeServerUnreachable, map[string]string{"host": "h", "detail": "d"}), app.ActionRetry},
		{statusOf(app.CodeAuthFailed, nil), app.ActionRetry},
		{statusOf(app.CodeServerError, map[string]string{"status": "500", "detail": "d"}), app.ActionRetry},
		{statusOf(app.CodeFormatPending, map[string]string{"seconds": "30", "name": "Brofrong"}), app.ActionCancelFormat},
		{statusOf(app.CodeDone, map[string]string{"uploaded": "3"}), ""},
		{statusOf(app.CodeFormatted, map[string]string{"name": "Brofrong"}), ""},
	}
	for _, tt := range tests {
		f := &fakeSender{}
		newNotifier(f).status(tt.st, cfg, c)
		msgs := f.messages()
		if len(msgs) != 1 {
			t.Fatalf("%s: sent %d notifications, want 1", tt.st.Code, len(msgs))
		}
		m := msgs[0]
		if m.Data["code"] != string(tt.st.Code) {
			t.Errorf("%s: Data[code] = %q", tt.st.Code, m.Data["code"])
		}
		if m.ID == "" || m.Title == "" || strings.HasPrefix(m.Title, "notify.") || strings.HasPrefix(m.Body, "notify.") {
			t.Errorf("%s: id %q, title %q, body %q: want translated texts", tt.st.Code, m.ID, m.Title, m.Body)
		}
		if tt.action == "" {
			if len(m.Actions) != 0 {
				t.Errorf("%s: actions = %+v, want none", tt.st.Code, m.Actions)
			}
			continue
		}
		if len(m.Actions) != 1 || m.Actions[0].ID != string(tt.action) {
			t.Fatalf("%s: actions = %+v, want one %q", tt.st.Code, m.Actions, tt.action)
		}
		if want := c.T("action."+string(tt.action), nil); m.Actions[0].Title != want {
			t.Errorf("%s: action title = %q, want %q", tt.st.Code, m.Actions[0].Title, want)
		}
	}

	f := &fakeSender{}
	newNotifier(f).status(tests[0].st, cfg, c)
	if got := f.messages()[0].Data["uuid"]; got != "UUID-2" {
		t.Errorf("other_card_mounted Data[uuid] = %q, want the mounted card's UUID-2", got)
	}

	f = &fakeSender{}
	newNotifier(f).notice(app.Notice{Kind: app.NoticeEjectFailed, Params: map[string]string{"name": "Brofrong", "detail": "busy"}}, cfg, c)
	m := f.messages()
	if len(m) != 1 || len(m[0].Actions) != 0 || m[0].Data["notice"] != string(app.NoticeEjectFailed) {
		t.Fatalf("eject_failed notice: %+v", m)
	}
	if !strings.Contains(m[0].Body, "Brofrong") || !strings.Contains(m[0].Body, "busy") {
		t.Errorf("eject_failed body = %q, want the name and the detail", m[0].Body)
	}
}

func TestNotificationResponseSection(t *testing.T) {
	tests := []struct {
		action  string
		data    map[string]string
		section string
		ok      bool
	}{
		{notify.DefaultAction, map[string]string{"code": string(app.CodeDoneWithErrors)}, "history", true},
		{notify.DefaultAction, map[string]string{"code": string(app.CodeServerUnreachable)}, "status", true},
		{notify.DefaultAction, map[string]string{"notice": string(app.NoticeEjectFailed)}, "status", true},
		{notify.DefaultAction, nil, "status", true},
		{string(app.ActionOpenHistory), map[string]string{"code": string(app.CodeDoneWithErrors)}, "history", true},
		{string(app.ActionRetry), nil, "", false},
		{string(app.ActionSelectCard), map[string]string{"uuid": "U"}, "", false},
		{string(app.ActionCancelFormat), nil, "", false},
	}
	for _, tt := range tests {
		section, ok := responseSection(tt.action, tt.data)
		if section != tt.section || ok != tt.ok {
			t.Errorf("responseSection(%q, %v) = %q, %v; want %q, %v", tt.action, tt.data, section, ok, tt.section, tt.ok)
		}
	}
}

// TestNotificationTextsExist checks that every notified code and notice
// has its title and body in every catalog.
func TestNotificationTextsExist(t *testing.T) {
	var keys []string
	for code := range codeKinds {
		keys = append(keys, "notify."+string(code)+".title", "notify."+string(code)+".body")
	}
	for _, k := range []app.NoticeKind{app.NoticeCardFollowed, app.NoticeEjectFailed} {
		keys = append(keys, "notify."+string(k)+".title", "notify."+string(k)+".body")
	}
	for _, lang := range i18n.Supported {
		c, err := i18n.Load(lang)
		if err != nil {
			t.Fatalf("load %s: %v", lang, err)
		}
		for _, key := range keys {
			if c.T(key, nil) == key {
				t.Errorf("%s: no %q", lang, key)
			}
		}
	}
}
