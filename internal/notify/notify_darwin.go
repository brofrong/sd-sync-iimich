//go:build darwin

package notify

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/logx"
)

const appTitle = "SD Sync Immich"

// Init is a no-op: osascript notifications do not require a separate permission handshake at startup.
func Init() {}

// SyncComplete shows a macOS Notification Center alert after a successful sync.
// Uses osascript instead of UNUserNotificationCenter because bare binaries and
// LaunchAgent processes do not have an app bundle (bundleProxyForCurrentProcess is nil).
func SyncComplete(uploaded int) {
	if uploaded <= 0 {
		return
	}

	body := uploadedMessage(uploaded)
	script := fmt.Sprintf(
		`display notification %q with title %q sound name "default"`,
		body,
		appTitle,
	)
	if out, err := exec.Command("osascript", "-e", script).CombinedOutput(); err != nil {
		logx.Warn("notify: failed to show notification", "err", err, "output", strings.TrimSpace(string(out)))
	}
}

func uploadedMessage(n int) string {
	mod100 := n % 100
	if mod100 >= 11 && mod100 <= 14 {
		return fmt.Sprintf("%d файлов успешно загружено", n)
	}
	switch n % 10 {
	case 1:
		return fmt.Sprintf("%d файл успешно загружен", n)
	case 2, 3, 4:
		return fmt.Sprintf("%d файла успешно загружено", n)
	default:
		return fmt.Sprintf("%d файлов успешно загружено", n)
	}
}
