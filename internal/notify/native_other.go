//go:build !darwin

package notify

import "github.com/dmitiigorshkov/sd-sync-immich/internal/logx"

// showFallback logs the notification instead of showing it. macOS falls
// back to osascript (notify_darwin.go) when native notifications are
// unavailable; Windows and Linux have no equivalent, so a native failure
// (the Wails notifications service did not start, or the user denied
// permission) means notifications are only logged for the rest of the
// session.
var showFallback = func(title, body string) {
	logx.Info("notify: showing a notification without native notifications (log only)", "title", title, "body", body)
}
