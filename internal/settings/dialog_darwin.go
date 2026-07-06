//go:build darwin

package settings

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

func PromptURL(current string) (string, error) {
	script := fmt.Sprintf(
		`display dialog "Immich URL:" default answer %q with title "SD Sync Immich"`,
		escapeAppleScript(current),
	)
	return runDialog(script)
}

func PromptAPIKey() (string, error) {
	script := `display dialog "API Key:" default answer "" with hidden answer with title "SD Sync Immich"`
	return runDialog(script)
}

// PromptWorkers asks for worker count. 0 = auto (use CPU cores).
func PromptWorkers(kind string, current int) (int, error) {
	hint := fmt.Sprintf(
		"Количество воркеров (%s).\n0 = автоматически (по числу ядер CPU).\nДопустимо: 0–32.",
		kind,
	)
	script := fmt.Sprintf(
		`display dialog %q default answer %q with title "SD Sync Immich"`,
		escapeAppleScript(hint),
		strconv.Itoa(current),
	)
	val, err := runDialog(script)
	if err != nil || val == "" {
		return current, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return current, fmt.Errorf("некорректное число: %q", val)
	}
	if n < 0 || n > 32 {
		return current, fmt.Errorf("значение должно быть от 0 до 32")
	}
	return n, nil
}

func runDialog(script string) (string, error) {
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "", nil // user cancelled
		}
		return "", fmt.Errorf("osascript: %w", err)
	}
	text := strings.TrimSpace(string(out))
	for _, part := range strings.Split(text, ", ") {
		if strings.HasPrefix(part, "text returned:") {
			return strings.TrimPrefix(part, "text returned:"), nil
		}
	}
	return text, nil
}

func escapeAppleScript(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return s
}
