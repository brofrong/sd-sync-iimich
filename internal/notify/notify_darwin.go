//go:build darwin

package notify

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

// Show displays a macOS Notification Center alert with the given (already
// translated) title and body. It uses osascript instead of
// UNUserNotificationCenter because a binary outside an app bundle (and a
// LaunchAgent process) has no bundle proxy, which that API requires.
func Show(title, body string) {
	script := fmt.Sprintf(`display notification %q with title %q sound name "default"`, body, title)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		logx.Warn("notify: failed to show notification", "err", err, "output", strings.TrimSpace(string(out)))
	}
}
