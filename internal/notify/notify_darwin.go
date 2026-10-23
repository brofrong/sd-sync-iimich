//go:build darwin

// Package notify shows desktop notifications: native ones with buttons
// through the Wails notifications service when the app may use them (see
// Center), else plain ones through osascript (see Show).
package notify

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// DefaultAction is the action ID a response carries when the user clicks
// the notification itself rather than one of its buttons.
const DefaultAction = notifications.DefaultActionIdentifier

// Message is one notification, with its texts already translated.
type Message struct {
	// ID identifies the notification: a new one with the same ID replaces
	// it in Notification Center.
	ID    string
	Title string
	Body  string
	// Actions are its buttons (shown only by native notifications).
	Actions []Action
	// Data comes back with the user's response.
	Data map[string]string
}

// Action is a notification button: ID comes back in the response, Title
// is its (translated) label.
type Action struct {
	ID    string
	Title string
}

// Show displays a macOS Notification Center alert with the given (already
// translated) title and body. It uses osascript instead of
// UNUserNotificationCenter because a binary outside an app bundle (and a
// LaunchAgent process) has no bundle proxy, which that API requires; it
// has no buttons, and clicking it does not reach the app.
func Show(title, body string) {
	script := fmt.Sprintf(`display notification %q with title %q sound name "default"`, body, title)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		logx.Warn("notify: failed to show notification", "err", err, "output", strings.TrimSpace(string(out)))
	}
}

// showFallback shows a notification without native notifications. Tests
// replace it so they never show one.
var showFallback = Show
