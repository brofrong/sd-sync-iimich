//go:build darwin

// Package install installs the app bundle into ~/Applications, manages the
// LaunchAgent that starts it at login, and migrates v0.3.0 installs (a bare
// binary at ~/Applications/sd-sync-immich) to the bundle.
package install

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"text/template"

	"howett.net/plist"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/bundle"
)

const (
	label = bundle.ID
	// legacyName is the bare binary v0.3.0 installed into ~/Applications.
	legacyName = "sd-sync-immich"

	// KeepAlive restarts the app only after a non-zero exit: "Quit" exits
	// with 0 and stays quit until the next login, while an update exits
	// with 75 to be started again in its new version.
	plistTpl = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{xml .Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{xml .Binary}}</string>
		<string>run</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>StandardOutPath</key>
	<string>{{xml .LogDir}}/stdout.log</string>
	<key>StandardErrorPath</key>
	<string>{{xml .LogDir}}/stderr.log</string>
</dict>
</plist>
`
)

var agentTemplate = template.Must(template.New("plist").Funcs(template.FuncMap{
	"xml": func(s string) (string, error) {
		var b strings.Builder
		err := xml.EscapeText(&b, []byte(s))
		return b.String(), err
	},
}).Parse(plistTpl))

// Seams for tests: no test may run launchctl or codesign for real.
var (
	// runCommand runs a command and returns its combined output.
	runCommand = func(name string, args ...string) ([]byte, error) {
		return exec.Command(name, args...).CombinedOutput()
	}
	// spawnDetached starts a command in its own session and does not wait
	// for it, so it outlives this process.
	spawnDetached = func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		return cmd.Process.Release()
	}
	createBundle = bundle.Create
)

// paths are the locations Install manages, under the user's home.
type paths struct {
	apps   string // ~/Applications
	bundle string // ~/Applications/SD Sync Immich.app
	legacy string // ~/Applications/sd-sync-immich (v0.3.0)
	plist  string // ~/Library/LaunchAgents/com.sd-sync-immich.plist
	logDir string // ~/Library/Logs/sd-sync-immich
}

func userPaths() (paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return paths{}, err
	}
	apps := filepath.Join(home, "Applications")
	return paths{
		apps:   apps,
		bundle: filepath.Join(apps, bundle.Name),
		legacy: filepath.Join(apps, legacyName),
		plist:  filepath.Join(home, "Library", "LaunchAgents", label+".plist"),
		logDir: filepath.Join(home, "Library", "Logs", "sd-sync-immich"),
	}, nil
}

// bundleExecutable is the executable inside the bundle at bundlePath.
func bundleExecutable(bundlePath string) string {
	return filepath.Join(bundlePath, "Contents", "MacOS", bundle.Executable)
}

func domain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

// underLaunchd reports whether this process is the LaunchAgent's job
// (launchd sets XPC_SERVICE_NAME to the job's label).
func underLaunchd() bool {
	return os.Getenv("XPC_SERVICE_NAME") == label
}

// Install builds the app bundle from binaryPath in ~/Applications, points
// the LaunchAgent at it, removes a v0.3.0 bare binary and (re)starts the
// agent. Under launchd the restart happens a second after this process
// exits (see reloadAgent): the caller must then exit with code 0.
func Install(binaryPath, version string) (bundlePath string, err error) {
	p, err := userPaths()
	if err != nil {
		return "", err
	}
	if err := createBundle(binaryPath, p.bundle, version); err != nil {
		return "", fmt.Errorf("create %s: %w", p.bundle, err)
	}
	if err := writeAgent(p, bundleExecutable(p.bundle)); err != nil {
		return "", err
	}
	// The LaunchAgent no longer runs it; a leftover copy would only
	// confuse (and v0.3.0's would be started by launchd again until the
	// agent is reloaded).
	if err := os.Remove(p.legacy); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("remove %s: %w", p.legacy, err)
	}
	if err := reloadAgent(p.plist); err != nil {
		return "", err
	}
	return p.bundle, nil
}

// writeAgent writes the LaunchAgent plist that runs binary.
func writeAgent(p paths, binary string) error {
	for _, dir := range []string{p.logDir, filepath.Dir(p.plist)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var b strings.Builder
	if err := agentTemplate.Execute(&b, map[string]string{"Label": label, "Binary": binary, "LogDir": p.logDir}); err != nil {
		return fmt.Errorf("render LaunchAgent: %w", err)
	}
	tmp := p.plist + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write LaunchAgent: %w", err)
	}
	if err := os.Rename(tmp, p.plist); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write LaunchAgent: %w", err)
	}
	return nil
}

// reloadAgent makes launchd load the LaunchAgent at plistPath again,
// starting the app it runs. When this process is the agent's job, bootout
// would kill it before bootstrap runs, so both are left to a detached
// shell that waits for this process to exit first.
func reloadAgent(plistPath string) error {
	if underLaunchd() {
		// Paths go in as positional parameters: never interpolated into
		// the script (the bundle path has spaces).
		err := spawnDetached("/bin/sh", "-c",
			`sleep 1; launchctl bootout "$1" "$2"; launchctl bootstrap "$1" "$2"`,
			"sh", domain(), plistPath)
		if err != nil {
			return fmt.Errorf("schedule LaunchAgent reload: %w", err)
		}
		return nil
	}
	// Fails when the agent is not loaded, which is fine.
	_, _ = runCommand("launchctl", "bootout", domain(), plistPath)
	if out, err := runCommand("launchctl", "bootstrap", domain(), plistPath); err != nil {
		return fmt.Errorf("launchctl bootstrap: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// Uninstall stops and removes the LaunchAgent, the app bundle and a v0.3.0
// bare binary.
func Uninstall() error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	_, _ = runCommand("launchctl", "bootout", domain(), p.plist)
	var errs []error
	for _, path := range []string{p.plist, p.legacy} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := os.RemoveAll(p.bundle); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// MigrateIfNeeded moves a v0.3.0 install to the app bundle. It acts only
// when executable is outside a bundle and is the program the LaunchAgent
// runs, i.e. v0.3.0's updater replaced its bare binary with this version:
// it then installs the bundle and points the agent at it (see Install).
// When it reports true the caller must exit with code 0 and let the
// agent start the bundle.
func MigrateIfNeeded(executable, version string) (migrated bool, err error) {
	if _, ok := bundle.Containing(executable); ok {
		return false, nil
	}
	p, err := userPaths()
	if err != nil {
		return false, err
	}
	program, err := agentProgram(p.plist)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !samePath(program, executable) {
		return false, nil
	}
	if _, err := Install(executable, version); err != nil {
		return false, fmt.Errorf("migrate to %s: %w", bundle.Name, err)
	}
	return true, nil
}

// agentProgram returns the program the LaunchAgent at plistPath runs.
func agentProgram(plistPath string) (string, error) {
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return "", err
	}
	var agent struct {
		Program          string
		ProgramArguments []string
	}
	if _, err := plist.Unmarshal(data, &agent); err != nil {
		return "", fmt.Errorf("parse %s: %w", plistPath, err)
	}
	if agent.Program != "" {
		return agent.Program, nil
	}
	if len(agent.ProgramArguments) > 0 {
		return agent.ProgramArguments[0], nil
	}
	return "", nil
}

// samePath reports whether a and b name the same file, following
// symlinks.
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// AutostartEnabled reports whether the LaunchAgent is installed.
func AutostartEnabled() bool {
	p, err := userPaths()
	if err != nil {
		return false
	}
	_, err = os.Stat(p.plist)
	return err == nil
}

// SetAutostart installs (enabled) or removes the LaunchAgent that starts
// bundleBinary at login. The bundle itself is never touched.
//
// Enabling only writes the plist: launchd loads it at the next login.
// Loading it now would start a second copy of the running app (RunAtLoad),
// or fail if this process already is the agent's job. Disabling removes
// the plist and, unless this process is the agent's job (bootout would
// kill it), unloads the agent.
func SetAutostart(enabled bool, bundleBinary string) error {
	p, err := userPaths()
	if err != nil {
		return err
	}
	if enabled {
		return writeAgent(p, bundleBinary)
	}
	if !underLaunchd() {
		// Fails when the agent is not loaded, which is fine.
		_, _ = runCommand("launchctl", "bootout", domain(), p.plist)
	}
	if err := os.Remove(p.plist); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove LaunchAgent: %w", err)
	}
	return nil
}

// UnderLaunchd reports whether this process was started by the
// LaunchAgent, which restarts it after a non-zero exit.
func UnderLaunchd() bool {
	return underLaunchd()
}

// OpenWhenExited opens the app bundle at bundlePath once this process has
// exited. An app that launchd did not start (opened from Finder) uses it
// to restart after an update.
func OpenWhenExited(bundlePath string) error {
	// Paths go in as positional parameters, never into the script.
	err := spawnDetached("/bin/sh", "-c",
		`while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; exec /usr/bin/open "$2"`,
		"sh", strconv.Itoa(os.Getpid()), bundlePath)
	if err != nil {
		return fmt.Errorf("schedule restart: %w", err)
	}
	return nil
}
