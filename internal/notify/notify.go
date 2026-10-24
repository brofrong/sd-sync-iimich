// Package notify shows desktop notifications through a Center. On macOS it
// shows native ones with buttons through the Wails notifications service
// when the app may use them, else plain ones through osascript (see Show).
package notify

import "github.com/wailsapp/wails/v3/pkg/services/notifications"

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
