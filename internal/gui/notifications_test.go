package gui

import (
	"errors"
	"runtime"
	"slices"
	"strconv"
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
	mu      sync.Mutex
	sent    []notify.Message
	removed []string
}

func (f *fakeSender) Send(m notify.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
}

func (f *fakeSender) Remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
}

func (f *fakeSender) removedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removed...)
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
	tests := []struct {
		code   app.Code
		want   string
		wantOK bool
	}{
		{app.CodeDoneWithErrors, kindErrors, true},
		{app.CodeOtherCardMounted, kindOtherCard, true},
		{app.CodeServerUnreachable, kindServer, true},
		{app.CodeAuthFailed, kindServer, true},
		{app.CodeServerError, kindServer, true},
		{app.CodeFormatPending, kindCard, true},
		{app.CodeFormatted, kindCard, true},
		{app.CodeFormatSkippedMissing, kindCard, true},
		{app.CodeFormatSkippedUnsupported, kindCard, true},
		{app.CodeFormatFailed, kindCard, true},
		{app.CodeFormatNeedsAdmin, kindCard, true},
		// A successful sync is told by the sync_done notice, once, whatever
		// followed it (eject, format, a failed eject).
		{app.CodeDone, "", false},
		{app.CodeEjected, "", false},
		// Passing states, and ones the user caused or sees in the window.
		{app.CodeNoCardSelected, "", false},
		{app.CodeNotConfigured, "", false},
		{app.CodeWaitingForCard, "", false},
		{app.CodeScanning, "", false},
		{app.CodeSyncing, "", false},
		{app.CodeNothingNew, "", false},
		{app.CodeVerifying, "", false},
		{app.CodeFormatting, "", false},
		{app.CodeCancelled, "", false},
		{app.CodeCardRemoved, "", false},
	}
	for _, tt := range tests {
		kind, ok := notificationKind(tt.code)
		if kind != tt.want || ok != tt.wantOK {
			t.Errorf("notificationKind(%s) = %q, %v; want %q, %v", tt.code, kind, ok, tt.want, tt.wantOK)
		}
	}

	notices := map[app.NoticeKind]string{
		app.NoticeSyncDone:     kindDone,
		app.NoticeCardFollowed: kindCard,
		app.NoticeEjectFailed:  kindCard,
		"unknown":              "",
	}
	for k, want := range notices {
		if got := noticeKind(k); got != want {
			t.Errorf("noticeKind(%s) = %q, want %q", k, got, want)
		}
	}
}

func syncDone(uploaded int, ejected bool) app.Notice {
	return app.Notice{Kind: app.NoticeSyncDone, Params: map[string]string{
		"uploaded": strconv.Itoa(uploaded),
		"ejected":  strconv.FormatBool(ejected),
	}}
}

func TestSyncDoneNotification(t *testing.T) {
	c := englishCatalog(t)
	cfg := notifyConfig(nil)

	tests := []struct {
		notice app.Notice
		key    string
	}{
		{syncDone(3, true), "notify.ejected"},
		{syncDone(3, false), "notify.done"},
	}
	for _, tt := range tests {
		f := &fakeSender{}
		newNotifier(f).notice(tt.notice, cfg, c)
		msgs := f.messages()
		if len(msgs) != 1 {
			t.Fatalf("%v: sent %d notifications, want 1", tt.notice.Params, len(msgs))
		}
		m := msgs[0]
		wantTitle := c.T(tt.key+".title", tt.notice.Params)
		wantBody := c.T(tt.key+".body", tt.notice.Params)
		if m.Title != wantTitle || m.Body != wantBody {
			t.Errorf("%v: %q / %q, want %q / %q", tt.notice.Params, m.Title, m.Body, wantTitle, wantBody)
		}
		if !strings.Contains(m.Body, "3") || len(m.Actions) != 0 {
			t.Errorf("%v: body %q, actions %+v; want the count and no buttons", tt.notice.Params, m.Body, m.Actions)
		}
	}

	// The status codes themselves are not notified.
	f := &fakeSender{}
	n := newNotifier(f)
	n.status(statusOf(app.CodeDone, map[string]string{"uploaded": "3"}), cfg, c)
	n.status(statusOf(app.CodeEjected, map[string]string{"uploaded": "3"}), cfg, c)
	if got := f.messages(); len(got) != 0 {
		t.Errorf("done/ejected statuses sent %+v, want nothing", got)
	}

	// Done turned off.
	f = &fakeSender{}
	off := notifyConfig(func(c *config.Config) { c.Notifications.Done = false })
	newNotifier(f).notice(syncDone(3, true), off, c)
	if got := f.messages(); len(got) != 0 {
		t.Errorf("with Done off sent %+v, want nothing", got)
	}
}

func TestNotificationsDisabled(t *testing.T) {
	c := englishCatalog(t)
	// One trigger per kind.
	triggers := map[string]func(*notifier, config.Config){
		kindDone: func(n *notifier, cfg config.Config) { n.notice(syncDone(2, false), cfg, c) },
		kindErrors: func(n *notifier, cfg config.Config) {
			n.status(statusOf(app.CodeDoneWithErrors, map[string]string{"uploaded": "1", "errors": "1"}), cfg, c)
		},
		kindOtherCard: func(n *notifier, cfg config.Config) {
			n.status(statusOf(app.CodeOtherCardMounted, map[string]string{"mounted_name": "X", "selected_name": "Y", "mounted_uuid": "U"}), cfg, c)
		},
		kindServer: func(n *notifier, cfg config.Config) {
			n.status(statusOf(app.CodeServerUnreachable, map[string]string{"host": "h", "detail": "d"}), cfg, c)
		},
		kindCard: func(n *notifier, cfg config.Config) {
			n.notice(app.Notice{Kind: app.NoticeCardFollowed, Params: map[string]string{"name": "Brofrong"}}, cfg, c)
		},
	}
	kinds := []string{kindDone, kindErrors, kindOtherCard, kindServer, kindCard}
	// sentKinds fires every trigger with cfg and returns the kinds that
	// produced a notification.
	sentKinds := func(cfg config.Config) map[string]bool {
		got := map[string]bool{}
		for _, kind := range kinds {
			f := &fakeSender{}
			triggers[kind](newNotifier(f), cfg)
			got[kind] = len(f.messages()) > 0
		}
		return got
	}

	for kind, sent := range sentKinds(notifyConfig(nil)) {
		if !sent {
			t.Errorf("all on: %s not notified", kind)
		}
	}
	for kind, sent := range sentKinds(notifyConfig(func(c *config.Config) { c.Notifications.Enabled = false })) {
		if sent {
			t.Errorf("all off: %s notified", kind)
		}
	}

	toggles := map[string]func(*config.NotifyConfig){
		kindDone:      func(n *config.NotifyConfig) { n.Done = false },
		kindErrors:    func(n *config.NotifyConfig) { n.Errors = false },
		kindOtherCard: func(n *config.NotifyConfig) { n.OtherCard = false },
		kindServer:    func(n *config.NotifyConfig) { n.Server = false },
		kindCard:      func(n *config.NotifyConfig) { n.Card = false },
	}
	for _, off := range kinds {
		t.Run(off+" off", func(t *testing.T) {
			got := sentKinds(notifyConfig(func(c *config.Config) { toggles[off](&c.Notifications) }))
			for _, kind := range kinds {
				if want := kind != off; got[kind] != want {
					t.Errorf("%s notified = %v, want %v", kind, got[kind], want)
				}
			}
		})
	}
}

func TestNotifyGateOncePerCard(t *testing.T) {
	c := englishCatalog(t)
	cfg := notifyConfig(nil)
	f := &fakeSender{}
	n := newNotifier(f)
	unreachable := statusOf(app.CodeServerUnreachable, map[string]string{"host": "immich.example", "detail": "timeout"})
	other := statusOf(app.CodeOtherCardMounted, map[string]string{"mounted_name": "X", "selected_name": "Y", "mounted_uuid": "U"})

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

	// So do the card leaving, an up-to-date card and the sync_done notice,
	// even when they are not notified themselves.
	n.status(unreachable, cfg, c)
	n.status(statusOf(app.CodeWaitingForCard, map[string]string{"name": "Card"}), cfg, c)
	n.status(unreachable, cfg, c)
	n.status(statusOf(app.CodeNothingNew, nil), cfg, c)
	n.status(unreachable, cfg, c)
	n.notice(syncDone(1, true), cfg, c)
	n.status(unreachable, cfg, c)
	if got := f.count(string(app.CodeServerUnreachable)); got != 5 {
		t.Fatalf("after waiting_for_card, nothing_new and sync_done: %d server notifications, want 5", got)
	}

	// Disabled notifications still reset it.
	off := notifyConfig(func(c *config.Config) { c.Notifications.Enabled = false })
	n.status(statusOf(app.CodeEjected, map[string]string{"uploaded": "1"}), off, c)
	n.status(unreachable, cfg, c)
	if got := f.count(string(app.CodeServerUnreachable)); got != 6 {
		t.Fatalf("after ejected: %d server notifications, want 6", got)
	}

	// Other codes are gated on their own.
	n.status(other, cfg, c)
	n.status(statusOf(app.CodeScanning, nil), cfg, c)
	n.status(other, cfg, c)
	if got := f.count(string(app.CodeOtherCardMounted)); got != 1 {
		t.Fatalf("%d other_card notifications, want 1", got)
	}
}

func TestServerGatedPerKind(t *testing.T) {
	c := englishCatalog(t)
	cfg := notifyConfig(nil)
	f := &fakeSender{}
	n := newNotifier(f)
	server := func() int {
		return f.count(string(app.CodeServerUnreachable)) + f.count(string(app.CodeAuthFailed)) + f.count(string(app.CodeServerError))
	}

	// One server problem after another: one notification.
	n.status(statusOf(app.CodeServerUnreachable, map[string]string{"host": "h", "detail": "d"}), cfg, c)
	n.status(statusOf(app.CodeServerError, map[string]string{"status": "502", "detail": "d"}), cfg, c)
	n.status(statusOf(app.CodeAuthFailed, nil), cfg, c)
	if got := server(); got != 1 {
		t.Fatalf("%d server notifications, want 1", got)
	}

	// A sync with errors got answers from the server: a new server problem
	// is news.
	n.status(statusOf(app.CodeDoneWithErrors, map[string]string{"uploaded": "1", "errors": "1"}), cfg, c)
	n.status(statusOf(app.CodeServerError, map[string]string{"status": "500", "detail": "d"}), cfg, c)
	if got := server(); got != 2 {
		t.Fatalf("after done_with_errors: %d server notifications, want 2", got)
	}
	// done_with_errors itself stays gated.
	n.status(statusOf(app.CodeDoneWithErrors, map[string]string{"uploaded": "1", "errors": "1"}), cfg, c)
	if got := f.count(string(app.CodeDoneWithErrors)); got != 1 {
		t.Fatalf("%d done_with_errors notifications, want 1", got)
	}
}

func TestFormatPendingRemovedWhenCountdownEnds(t *testing.T) {
	c := englishCatalog(t)
	cfg := notifyConfig(func(c *config.Config) { c.Card.AutoFormat = true })
	f := &fakeSender{}
	n := newNotifier(f)

	n.status(statusOf(app.CodeFormatPending, map[string]string{"seconds": "30", "name": "Brofrong"}), cfg, c)
	n.status(statusOf(app.CodeFormatPending, map[string]string{"seconds": "29", "name": "Brofrong"}), cfg, c)
	if got := f.removedIDs(); len(got) != 0 {
		t.Fatalf("removed %v during the countdown, want nothing", got)
	}
	n.status(statusOf(app.CodeFormatting, map[string]string{"name": "Brofrong"}), cfg, c)
	sent := f.messages()
	if got := f.removedIDs(); len(got) != 1 || got[0] != sent[0].ID {
		t.Fatalf("removed %v after the countdown, want [%s]", got, sent[0].ID)
	}
	// Only once.
	n.status(statusOf(app.CodeFormatted, map[string]string{"name": "Brofrong"}), cfg, c)
	if got := f.removedIDs(); len(got) != 1 {
		t.Fatalf("removed %v, want one removal", got)
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
	cfg := notifyConfig(nil)

	tests := []struct {
		st     app.Status
		action app.Action // "" for none
	}{
		{statusOf(app.CodeOtherCardMounted, map[string]string{
			"selected_name": "Brofrong", "mounted_name": "Other", "mounted_uuid": "UUID-2",
		}), app.ActionSelectCard},
		{statusOf(app.CodeDoneWithErrors, map[string]string{"uploaded": "3", "errors": "2"}), app.ActionOpenHistory},
		{statusOf(app.CodeServerUnreachable, map[string]string{"host": "h", "detail": "d"}), app.ActionRetry},
		// A rejected key needs the user, not a retry: open the server settings.
		{statusOf(app.CodeAuthFailed, nil), app.ActionOpenServer},
		{statusOf(app.CodeServerError, map[string]string{"status": "500", "detail": "d"}), app.ActionRetry},
		{statusOf(app.CodeFormatPending, map[string]string{"seconds": "30", "name": "Brofrong"}), app.ActionCancelFormat},
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
		{string(app.ActionOpenServer), map[string]string{"code": string(app.CodeAuthFailed)}, "server", true},
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
	// sync_done is told with the done or the ejected texts.
	for _, k := range []string{string(app.NoticeCardFollowed), string(app.NoticeEjectFailed), "done", "ejected"} {
		keys = append(keys, "notify."+k+".title", "notify."+k+".body")
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

// TestEjectFailedBodyPerSystem: the failed-eject notification names Finder
// on macOS only; Windows, Linux and any other system get the OS-neutral
// text, in every language. Codes without an OS wording keep their body.
func TestEjectFailedBodyPerSystem(t *testing.T) {
	tests := []struct{ goos, want string }{
		{"darwin", "notify.eject_failed.body_mac"},
		{"windows", "notify.eject_failed.body"},
		{"linux", "notify.eject_failed.body"},
		{"freebsd", "notify.eject_failed.body"},
	}
	for _, lang := range i18n.Supported {
		c, err := i18n.Load(lang)
		if err != nil {
			t.Fatalf("load %s: %v", lang, err)
		}
		for _, tt := range tests {
			if got := bodyKey(c, string(app.NoticeEjectFailed), tt.goos); got != tt.want {
				t.Errorf("%s: bodyKey(eject_failed, %s) = %q, want %q", lang, tt.goos, got, tt.want)
			}
			if got := bodyKey(c, "done", tt.goos); got != "notify.done.body" {
				t.Errorf("%s: bodyKey(done, %s) = %q, want notify.done.body", lang, tt.goos, got)
			}
		}
		mac, neutral := c.T("notify.eject_failed.body_mac", nil), c.T("notify.eject_failed.body", nil)
		if mac == neutral {
			t.Errorf("%s: the macOS and the neutral eject_failed bodies are the same %q", lang, mac)
		}
	}
	// English: Finder on the Mac, nowhere else.
	c := englishCatalog(t)
	if body := c.T(bodyKey(c, "eject_failed", "darwin"), nil); !strings.Contains(body, "Finder") {
		t.Errorf("macOS body %q, want it to name Finder", body)
	}
	for _, goos := range []string{"windows", "linux"} {
		if body := c.T(bodyKey(c, "eject_failed", goos), nil); strings.Contains(body, "Finder") {
			t.Errorf("%s body %q names Finder", goos, body)
		}
	}
}

// TestEjectFailedNotificationBody: the notifier sends this system's
// wording of the failed-eject body.
func TestEjectFailedNotificationBody(t *testing.T) {
	c := englishCatalog(t)
	f := &fakeSender{}
	params := map[string]string{"name": "CARD", "detail": "busy"}
	newNotifier(f).notice(app.Notice{Kind: app.NoticeEjectFailed, Params: params}, notifyConfig(nil), c)
	msgs := f.messages()
	if len(msgs) != 1 {
		t.Fatalf("sent %d notifications, want 1", len(msgs))
	}
	if want := c.T(bodyKey(c, "eject_failed", runtime.GOOS), params); msgs[0].Body != want {
		t.Errorf("body = %q, want %q", msgs[0].Body, want)
	}
	if hasFinder := strings.Contains(msgs[0].Body, "Finder"); hasFinder != (runtime.GOOS == "darwin") {
		t.Errorf("on %s the body %q names Finder: %v", runtime.GOOS, msgs[0].Body, hasFinder)
	}
}

// fakeResponder records what a notification response asked for.
type fakeResponder struct {
	selectErr error
	calls     []string
}

func (r *fakeResponder) showSection(section string) { r.calls = append(r.calls, "show "+section) }
func (r *fakeResponder) selectCard(uuid string) error {
	r.calls = append(r.calls, "select "+uuid)
	return r.selectErr
}
func (r *fakeResponder) retry()        { r.calls = append(r.calls, "retry") }
func (r *fakeResponder) cancelFormat() { r.calls = append(r.calls, "cancel_format") }

func TestHandleNotificationResponse(t *testing.T) {
	tests := []struct {
		name      string
		action    string
		data      map[string]string
		selectErr error
		want      []string
	}{
		{"click errors", notify.DefaultAction, map[string]string{"code": string(app.CodeDoneWithErrors)}, nil, []string{"show history"}},
		{"click other", notify.DefaultAction, map[string]string{"notice": string(app.NoticeSyncDone)}, nil, []string{"show status"}},
		{"details", string(app.ActionOpenHistory), nil, nil, []string{"show history"}},
		{"open server", string(app.ActionOpenServer), map[string]string{"code": string(app.CodeAuthFailed)}, nil, []string{"show server"}},
		{"retry", string(app.ActionRetry), nil, nil, []string{"retry"}},
		{"cancel format", string(app.ActionCancelFormat), nil, nil, []string{"cancel_format"}},
		{"select card", string(app.ActionSelectCard), map[string]string{"uuid": "U"}, nil, []string{"select U"}},
		// The card is gone by now: show the cards to choose from.
		{"select stale card", string(app.ActionSelectCard), map[string]string{"uuid": "U"}, errors.New("card U is not mounted"), []string{"select U", "show cards"}},
		{"unknown", "com.apple.UNNotificationDismissActionIdentifier", nil, nil, nil},
	}
	for _, tt := range tests {
		r := &fakeResponder{selectErr: tt.selectErr}
		handleNotificationResponse(r, tt.action, tt.data)
		if !slices.Equal(r.calls, tt.want) {
			t.Errorf("%s: calls = %v, want %v", tt.name, r.calls, tt.want)
		}
	}
}
