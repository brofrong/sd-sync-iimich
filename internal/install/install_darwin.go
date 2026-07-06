//go:build darwin

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const (
	label    = "com.sd-sync-immich"
	appName  = "sd-sync-immich"
	plistTpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Binary}}</string>
		<string>run</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>{{.LogDir}}/stdout.log</string>
	<key>StandardErrorPath</key>
	<string>{{.LogDir}}/stderr.log</string>
</dict>
</plist>
`
)

type plistData struct {
	Label   string
	Binary  string
	LogDir  string
}

func Install(binaryPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	destDir := filepath.Join(home, "Applications")
	destBin := filepath.Join(destDir, appName)
	logDir := filepath.Join(home, "Library", "Logs", "sd-sync-immich")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}

	src, err := os.ReadFile(binaryPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(destBin, src, 0o755); err != nil {
		return err
	}

	tmpl, err := template.New("plist").Parse(plistTpl)
	if err != nil {
		return err
	}
	f, err := os.Create(plistPath)
	if err != nil {
		return err
	}
	if err := tmpl.Execute(f, plistData{Label: label, Binary: destBin, LogDir: logDir}); err != nil {
		f.Close()
		return err
	}
	f.Close()

	uid := os.Getuid()
	domain := fmt.Sprintf("gui/%d", uid)
	_ = exec.Command("launchctl", "bootout", domain, plistPath).Run()
	if out, err := exec.Command("launchctl", "bootstrap", domain, plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s: %w", string(out), err)
	}
	return nil
}

func Uninstall() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	destBin := filepath.Join(home, "Applications", appName)

	uid := os.Getuid()
	domain := fmt.Sprintf("gui/%d", uid)
	_ = exec.Command("launchctl", "bootout", domain, plistPath).Run()
	_ = os.Remove(plistPath)
	_ = os.Remove(destBin)
	return nil
}
