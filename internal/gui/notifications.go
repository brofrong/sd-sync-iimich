package gui

import (
	"sync"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/i18n"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/notify"
)

// Notification kinds: each can be turned off in Settings (see
// config.NotifyConfig).
const (
	kindDone      = "done"
	kindErrors    = "errors"
	kindOtherCard = "other_card"
	kindServer    = "server"
	kindCard      = "card"
)

// notifyID prefixes the IDs of the app's notifications.
const notifyID = "sd-sync-immich."

// codeKinds maps each status code that is notified to its kind. Others
// (scanning, syncing, verifying, formatting, nothing_new, waiting_for_card,
// no_card_selected, which flashes while a formatted card is followed, ...)
// are only shown in the window and tray. done and ejected are not notified
// either: a successful sync is told once by app.NoticeSyncDone, whatever
// followed it.
var codeKinds = map[app.Code]string{
	app.CodeDoneWithErrors:           kindErrors,
	app.CodeOtherCardMounted:         kindOtherCard,
	app.CodeServerUnreachable:        kindServer,
	app.CodeAuthFailed:               kindServer,
	app.CodeServerError:              kindServer,
	app.CodeFormatPending:            kindCard,
	app.CodeFormatted:                kindCard,
	app.CodeFormatSkippedMissing:     kindCard,
	app.CodeFormatSkippedUnsupported: kindCard,
	app.CodeFormatFailed:             kindCard,
}

// codeActions is the button of a notified code, if it has one.
var codeActions = map[app.Code]app.Action{
	app.CodeDoneWithErrors:    app.ActionOpenHistory,
	app.CodeOtherCardMounted:  app.ActionSelectCard,
	app.CodeServerUnreachable: app.ActionRetry,
	app.CodeAuthFailed:        app.ActionOpenServer,
	app.CodeServerError:       app.ActionRetry,
	app.CodeFormatPending:     app.ActionCancelFormat,
}

// resetCodes end what one card insertion notifies: the card left or was
// ejected, or a sync succeeded.
var resetCodes = map[app.Code]bool{
	app.CodeWaitingForCard: true,
	app.CodeDone:           true,
	app.CodeNothingNew:     true,
	app.CodeEjected:        true,
}

// notificationKind returns the kind of notification for a status code, and
// false when the code is not notified.
func notificationKind(code app.Code) (kind string, ok bool) {
	kind, ok = codeKinds[code]
	return kind, ok
}

// noticeKind returns the kind of notification for a notice, "" for one
// that is not notified.
func noticeKind(k app.NoticeKind) string {
	switch k {
	case app.NoticeSyncDone:
		return kindDone
	case app.NoticeCardFollowed, app.NoticeEjectFailed:
		return kindCard
	}
	return ""
}

// kindEnabled reports whether notifications of kind are on.
func kindEnabled(kind string, n config.NotifyConfig) bool {
	if !n.Enabled {
		return false
	}
	switch kind {
	case kindDone:
		return n.Done
	case kindErrors:
		return n.Errors
	case kindOtherCard:
		return n.OtherCard
	case kindServer:
		return n.Server
	case kindCard:
		return n.Card
	}
	return false
}

// notifyGate lets each status code be notified once until it is reset (see
// resetCodes), so automatic retries that keep failing, or a countdown that
// only changes its seconds, notify once. Server problems share one gate:
// one Server notification, whichever problem follows which. It lives in
// memory only: a restart resets it too.
type notifyGate struct {
	mu   sync.Mutex
	sent map[string]bool
}

// gateKey is what code is gated by: its kind for server problems, else the
// code itself.
func gateKey(code app.Code, kind string) string {
	if kind == kindServer {
		return kindServer
	}
	return string(code)
}

// reset allows everything again.
func (g *notifyGate) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	clear(g.sent)
}

// forget allows key again.
func (g *notifyGate) forget(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.sent, key)
}

// allow reports whether key may be notified now, and if so marks it.
func (g *notifyGate) allow(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sent == nil {
		g.sent = map[string]bool{}
	}
	if g.sent[key] {
		return false
	}
	g.sent[key] = true
	return true
}

// sender shows and removes notifications (notify.Center; tests record them
// instead).
type sender interface {
	Send(notify.Message)
	Remove(id string)
}

// notifier decides which statuses and notices become notifications, and
// builds them.
type notifier struct {
	out  sender
	gate notifyGate

	// countdownShown is set while the format_pending notification is shown
	// and the countdown is on: it is removed when the countdown ends.
	mu             sync.Mutex
	countdownShown bool
}

func newNotifier(out sender) *notifier {
	return &notifier{out: out}
}

// status notifies st if its code is notified, enabled in cfg, and not
// notified yet for this card (see notifyGate). c translates the texts.
func (n *notifier) status(st app.Status, cfg config.Config, c *i18n.Catalog) {
	if st.Code != app.CodeFormatPending {
		n.endCountdown()
	}
	switch {
	case resetCodes[st.Code]:
		n.gate.reset()
	case st.Code == app.CodeDoneWithErrors:
		// The server answered: a server problem after it is news.
		n.gate.forget(kindServer)
	}
	kind, ok := notificationKind(st.Code)
	if !ok || !kindEnabled(kind, cfg.Notifications) || !n.gate.allow(gateKey(st.Code, kind)) {
		return
	}
	code := string(st.Code)
	m := notify.Message{
		ID:    notifyID + code,
		Title: c.T("notify."+code+".title", st.Params),
		Body:  c.T("notify."+code+".body", st.Params),
		Data:  map[string]string{"code": code},
	}
	if a, ok := codeActions[st.Code]; ok {
		m.Actions = []notify.Action{{ID: string(a), Title: c.T("action."+string(a), nil)}}
	}
	if st.Code == app.CodeOtherCardMounted {
		m.Data["uuid"] = st.Params["mounted_uuid"]
	}
	logx.Info("notify: showing notification", "code", code, "kind", kind)
	n.out.Send(m)
	if st.Code == app.CodeFormatPending {
		n.mu.Lock()
		n.countdownShown = true
		n.mu.Unlock()
	}
}

// endCountdown removes the format_pending notification once the countdown
// is over (formatting, cancelled, card pulled out): its "Cancel" no longer
// does anything.
func (n *notifier) endCountdown() {
	n.mu.Lock()
	shown := n.countdownShown
	n.countdownShown = false
	n.mu.Unlock()
	if shown {
		n.out.Remove(notifyID + string(app.CodeFormatPending))
	}
}

// notice notifies no if its kind is enabled in cfg. Notices are one-off
// events, so they are not gated; sync_done, a success, resets the gate.
func (n *notifier) notice(no app.Notice, cfg config.Config, c *i18n.Catalog) {
	if no.Kind == app.NoticeSyncDone {
		n.gate.reset()
	}
	kind := noticeKind(no.Kind)
	if kind == "" || !kindEnabled(kind, cfg.Notifications) {
		return
	}
	k := string(no.Kind)
	// The texts of a notice are its own, except sync_done's: "done", or
	// "ejected" when the card can be pulled out.
	texts := k
	if no.Kind == app.NoticeSyncDone {
		texts = "done"
		if no.Params["ejected"] == "true" {
			texts = "ejected"
		}
	}
	logx.Info("notify: showing notification", "notice", k, "kind", kind)
	n.out.Send(notify.Message{
		ID:    notifyID + texts,
		Title: c.T("notify."+texts+".title", no.Params),
		Body:  c.T("notify."+texts+".body", no.Params),
		Data:  map[string]string{"notice": k},
	})
}

// responseSection returns the window section to show for the user's
// response to a notification (its action and Data), and false when the
// response is not about the window: clicking a notification opens the
// history for errors and the status otherwise; "Details" opens the history
// and "Open Server Settings" (a rejected API key) the Server section.
func responseSection(action string, data map[string]string) (string, bool) {
	switch action {
	case notify.DefaultAction:
		if data["code"] == string(app.CodeDoneWithErrors) {
			return "history", true
		}
		return "status", true
	case string(app.ActionOpenHistory):
		return "history", true
	case string(app.ActionOpenServer):
		return "server", true
	}
	return "", false
}

// responder is what a response to a notification can do.
type responder interface {
	showSection(section string)
	selectCard(uuid string) error
	retry()
	cancelFormat()
}

// handleNotificationResponse acts on the user's response to a notification
// (its action and Data) through r.
func handleNotificationResponse(r responder, action string, data map[string]string) {
	if section, ok := responseSection(action, data); ok {
		r.showSection(section)
		return
	}
	switch app.Action(action) {
	case app.ActionSelectCard:
		if err := r.selectCard(data["uuid"]); err != nil {
			// The card is likely gone by now: let the user pick one.
			logx.Warn("notify: failed to select the card", "uuid", data["uuid"], "err", err)
			r.showSection("cards")
		}
	case app.ActionRetry:
		r.retry()
	case app.ActionCancelFormat:
		r.cancelFormat()
	default:
		logx.Debug("notify: ignoring notification response", "action", action)
	}
}

// onNotificationResponse acts on the user's response to a notification.
func (s *Service) onNotificationResponse(action string, data map[string]string) {
	handleNotificationResponse(serviceResponder{s}, action, data)
}

// serviceResponder is the responder of a Service.
type serviceResponder struct{ s *Service }

func (r serviceResponder) showSection(section string) {
	r.s.shell.showWindow()
	r.s.shell.emit(eventNavigate, section)
}
func (r serviceResponder) selectCard(uuid string) error { return r.s.SelectCard(uuid) }
func (r serviceResponder) retry()                       { r.s.app.Retry() }
func (r serviceResponder) cancelFormat()                { r.s.app.CancelFormat() }

// onNotice receives every notice from the App.
func (s *Service) onNotice(no app.Notice) {
	s.notifier.notice(no, s.cfg.Get(), s.catalog())
}
