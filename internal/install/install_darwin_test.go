//go:build darwin

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"howett.net/plist"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/bundle"
)

// recorder holds the external commands a test ran instead of the real ones.
type recorder struct {
	commands []string // launchctl and friends, run synchronously
	detached [][]string
	bundles  []string // bundle.Create destinations
}

// setup points HOME at a temporary directory and routes every external
// command through fakes, so no test touches the user's real LaunchAgent,
// ~/Applications or running app.
func setup(t *testing.T) (home string, rec *recorder) {
	t.Helper()
	home = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	t.Setenv("HOME", home)
	t.Setenv("XPC_SERVICE_NAME", "")

	rec = &recorder{}
	origRun, origSpawn, origCreate := runCommand, spawnDetached, createBundle
	runCommand = func(name string, args ...string) ([]byte, error) {
		rec.commands = append(rec.commands, strings.Join(append([]string{name}, args...), " "))
		return nil, nil
	}
	spawnDetached = func(name string, args ...string) error {
		rec.detached = append(rec.detached, append([]string{name}, args...))
		return nil
	}
	createBundle = func(binary, dest, version string) error {
		rec.bundles = append(rec.bundles, dest)
		data, err := os.ReadFile(binary)
		if err != nil {
			return err
		}
		exe := filepath.Join(dest, "Contents", "MacOS", bundle.Executable)
		if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
			return err
		}
		return os.WriteFile(exe, data, 0o755)
	}
	t.Cleanup(func() { runCommand, spawnDetached, createBundle = origRun, origSpawn, origCreate })
	return home, rec
}

type agentPlist struct {
	Label             string
	ProgramArguments  []string
	RunAtLoad         bool
	KeepAlive         map[string]bool
	StandardOutPath   string
	StandardErrorPath string
}

func readAgent(t *testing.T, home string) agentPlist {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", "com.sd-sync-immich.plist"))
	if err != nil {
		t.Fatalf("read LaunchAgent: %v", err)
	}
	var p agentPlist
	if _, err := plist.Unmarshal(data, &p); err != nil {
		t.Fatalf("parse LaunchAgent: %v\n%s", err, data)
	}
	return p
}

// writeLegacyAgent writes the LaunchAgent v0.3.0 installed, running program.
func writeLegacyAgent(t *testing.T, home, program string) string {
	t.Helper()
	path := filepath.Join(home, "Library", "LaunchAgents", "com.sd-sync-immich.plist")
	content := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.sd-sync-immich</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + program + `</string>
		<string>run</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
</dict>
</plist>
`
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestInstallWritesAgentPointingIntoBundle(t *testing.T) {
	home, rec := setup(t)
	bin := filepath.Join(t.TempDir(), "sd-sync-immich")
	writeFile(t, bin, "new binary")

	bundlePath, err := Install(bin, "v1.2.3")
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	wantBundle := filepath.Join(home, "Applications", "SD Sync Immich.app")
	if bundlePath != wantBundle {
		t.Fatalf("bundle = %q, want %q", bundlePath, wantBundle)
	}
	if !reflect.DeepEqual(rec.bundles, []string{wantBundle}) {
		t.Fatalf("bundles created = %q", rec.bundles)
	}

	raw, _ := os.ReadFile(filepath.Join(home, "Library", "LaunchAgents", "com.sd-sync-immich.plist"))
	for _, want := range []string{"/SD Sync Immich.app/Contents/MacOS/sd-sync-immich</string>", "<key>SuccessfulExit</key>"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("LaunchAgent lacks %q:\n%s", want, raw)
		}
	}
	agent := readAgent(t, home)
	wantArgs := []string{filepath.Join(wantBundle, "Contents", "MacOS", "sd-sync-immich"), "run"}
	if !reflect.DeepEqual(agent.ProgramArguments, wantArgs) {
		t.Errorf("ProgramArguments = %q, want %q", agent.ProgramArguments, wantArgs)
	}
	if agent.Label != "com.sd-sync-immich" || !agent.RunAtLoad {
		t.Errorf("Label = %q, RunAtLoad = %v", agent.Label, agent.RunAtLoad)
	}
	if !reflect.DeepEqual(agent.KeepAlive, map[string]bool{"SuccessfulExit": false}) {
		t.Errorf("KeepAlive = %v, want {SuccessfulExit: false}", agent.KeepAlive)
	}
	logDir := filepath.Join(home, "Library", "Logs", "sd-sync-immich")
	if agent.StandardOutPath != filepath.Join(logDir, "stdout.log") || agent.StandardErrorPath != filepath.Join(logDir, "stderr.log") {
		t.Errorf("log paths = %q, %q", agent.StandardOutPath, agent.StandardErrorPath)
	}
	if !exists(logDir) {
		t.Errorf("log directory not created")
	}

	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.sd-sync-immich.plist")
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	wantCommands := []string{
		"launchctl bootout " + domain + " " + plistPath,
		"launchctl bootstrap " + domain + " " + plistPath,
	}
	if !reflect.DeepEqual(rec.commands, wantCommands) {
		t.Errorf("commands = %q, want %q", rec.commands, wantCommands)
	}
	if len(rec.detached) != 0 {
		t.Errorf("detached commands = %q, want none outside launchd", rec.detached)
	}
}

func TestInstallEscapesPathsInAgent(t *testing.T) {
	home, _ := setup(t)
	weird := filepath.Join(home, "R&D <x>")
	t.Setenv("HOME", weird)
	bin := filepath.Join(t.TempDir(), "sd-sync-immich")
	writeFile(t, bin, "x")

	if _, err := Install(bin, "v1.2.3"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	agent := readAgent(t, weird)
	want := filepath.Join(weird, "Applications", "SD Sync Immich.app", "Contents", "MacOS", "sd-sync-immich")
	if agent.ProgramArguments[0] != want {
		t.Fatalf("program = %q, want %q", agent.ProgramArguments[0], want)
	}
}

func TestInstallRemovesLegacyBinary(t *testing.T) {
	home, _ := setup(t)
	legacy := filepath.Join(home, "Applications", "sd-sync-immich")
	writeFile(t, legacy, "v0.3.0")
	bin := filepath.Join(t.TempDir(), "sd-sync-immich")
	writeFile(t, bin, "new binary")

	if _, err := Install(bin, "v1.2.3"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if exists(legacy) {
		t.Fatalf("legacy binary %s still exists", legacy)
	}
}

func TestInstallUnderLaunchdReloadsAgentDetached(t *testing.T) {
	home, rec := setup(t)
	t.Setenv("XPC_SERVICE_NAME", "com.sd-sync-immich")
	bin := filepath.Join(t.TempDir(), "sd-sync-immich")
	writeFile(t, bin, "new binary")

	if _, err := Install(bin, "v1.2.3"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(rec.commands) != 0 {
		t.Fatalf("ran %q synchronously; bootout would kill this process", rec.commands)
	}
	if len(rec.detached) != 1 {
		t.Fatalf("detached = %q, want one reload", rec.detached)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.sd-sync-immich.plist")
	got := rec.detached[0]
	want := []string{
		"/bin/sh", "-c",
		`sleep 1; launchctl bootout "$1" "$2"; launchctl bootstrap "$1" "$2"`,
		"sh", fmt.Sprintf("gui/%d", os.Getuid()), plistPath,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("detached = %q, want %q", got, want)
	}
}

func TestMigrateIfNeededSkipsInsideBundle(t *testing.T) {
	home, rec := setup(t)
	exe := filepath.Join(home, "Applications", "SD Sync Immich.app", "Contents", "MacOS", "sd-sync-immich")
	writeFile(t, exe, "bundled")
	writeLegacyAgent(t, home, exe)

	migrated, err := MigrateIfNeeded(exe, "v1.2.3")
	if err != nil || migrated {
		t.Fatalf("MigrateIfNeeded = %v, %v; want false, nil", migrated, err)
	}
	if len(rec.bundles)+len(rec.commands)+len(rec.detached) != 0 {
		t.Fatalf("did something: %+v", rec)
	}
}

func TestMigrateIfNeededMigratesBareInstalledBinary(t *testing.T) {
	home, rec := setup(t)
	t.Setenv("XPC_SERVICE_NAME", "com.sd-sync-immich")
	legacy := filepath.Join(home, "Applications", "sd-sync-immich")
	writeFile(t, legacy, "v0.4.0 binary")
	writeLegacyAgent(t, home, legacy)

	migrated, err := MigrateIfNeeded(legacy, "v0.4.0")
	if err != nil || !migrated {
		t.Fatalf("MigrateIfNeeded = %v, %v; want true, nil", migrated, err)
	}
	bundlePath := filepath.Join(home, "Applications", "SD Sync Immich.app")
	exe := filepath.Join(bundlePath, "Contents", "MacOS", "sd-sync-immich")
	if data, err := os.ReadFile(exe); err != nil || string(data) != "v0.4.0 binary" {
		t.Fatalf("bundle executable = %q, %v", data, err)
	}
	agent := readAgent(t, home)
	if agent.ProgramArguments[0] != exe {
		t.Fatalf("LaunchAgent runs %q, want %q", agent.ProgramArguments[0], exe)
	}
	if !reflect.DeepEqual(agent.KeepAlive, map[string]bool{"SuccessfulExit": false}) {
		t.Fatalf("KeepAlive = %v", agent.KeepAlive)
	}
	if exists(legacy) {
		t.Fatalf("legacy binary still exists")
	}
	if len(rec.commands) != 0 || len(rec.detached) != 1 {
		t.Fatalf("commands = %q, detached = %q; want only the detached reload", rec.commands, rec.detached)
	}
}

func TestMigrateIfNeededIgnoresUnrelatedBinary(t *testing.T) {
	home, rec := setup(t)
	legacy := filepath.Join(home, "Applications", "sd-sync-immich")
	writeFile(t, legacy, "v0.3.0")
	plistPath := writeLegacyAgent(t, home, legacy)
	before, _ := os.ReadFile(plistPath)
	dev := filepath.Join(t.TempDir(), "sd-sync-immich")
	writeFile(t, dev, "dev build")

	migrated, err := MigrateIfNeeded(dev, "dev")
	if err != nil || migrated {
		t.Fatalf("MigrateIfNeeded = %v, %v; want false, nil", migrated, err)
	}
	after, _ := os.ReadFile(plistPath)
	if string(after) != string(before) {
		t.Fatalf("LaunchAgent changed")
	}
	if !exists(legacy) || len(rec.bundles)+len(rec.commands)+len(rec.detached) != 0 {
		t.Fatalf("did something: %+v", rec)
	}
}

func TestMigrateIfNeededWithoutAgent(t *testing.T) {
	_, rec := setup(t)
	bin := filepath.Join(t.TempDir(), "sd-sync-immich")
	writeFile(t, bin, "x")

	migrated, err := MigrateIfNeeded(bin, "v1.2.3")
	if err != nil || migrated {
		t.Fatalf("MigrateIfNeeded = %v, %v; want false, nil", migrated, err)
	}
	if len(rec.bundles) != 0 {
		t.Fatalf("created a bundle without a LaunchAgent")
	}
}

func TestMigrateIfNeededMatchesThroughSymlinks(t *testing.T) {
	home, _ := setup(t)
	legacy := filepath.Join(home, "Applications", "sd-sync-immich")
	writeFile(t, legacy, "x")
	writeLegacyAgent(t, home, legacy)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(legacy, link); err != nil {
		t.Fatal(err)
	}

	migrated, err := MigrateIfNeeded(link, "v1.2.3")
	if err != nil || !migrated {
		t.Fatalf("MigrateIfNeeded = %v, %v; want true, nil", migrated, err)
	}
}

func TestSetAutostartTogglesAgent(t *testing.T) {
	home, rec := setup(t)
	bin := filepath.Join(t.TempDir(), "sd-sync-immich")
	writeFile(t, bin, "x")
	bundlePath, err := Install(bin, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(bundlePath, "Contents", "MacOS", "sd-sync-immich")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.sd-sync-immich.plist")
	rec.commands = nil

	if err := SetAutostart(false, exe); err != nil {
		t.Fatalf("SetAutostart(false): %v", err)
	}
	if exists(plistPath) || AutostartEnabled() {
		t.Fatalf("LaunchAgent still exists after disabling")
	}
	if !exists(exe) {
		t.Fatalf("disabling autostart removed the bundle")
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if want := []string{"launchctl bootout " + domain + " " + plistPath}; !reflect.DeepEqual(rec.commands, want) {
		t.Fatalf("commands = %q, want %q", rec.commands, want)
	}
	rec.commands = nil

	if err := SetAutostart(true, exe); err != nil {
		t.Fatalf("SetAutostart(true): %v", err)
	}
	if !AutostartEnabled() {
		t.Fatalf("AutostartEnabled() = false after enabling")
	}
	if got := readAgent(t, home).ProgramArguments; !reflect.DeepEqual(got, []string{exe, "run"}) {
		t.Fatalf("ProgramArguments = %q", got)
	}
	// RunAtLoad would start a second copy of the running app right away;
	// launchd loads the agent at the next login instead.
	if len(rec.commands)+len(rec.detached) != 0 {
		t.Fatalf("enabling ran %q %q", rec.commands, rec.detached)
	}
}

func TestSetAutostartUnderLaunchdLeavesJobLoaded(t *testing.T) {
	home, rec := setup(t)
	t.Setenv("XPC_SERVICE_NAME", "com.sd-sync-immich")
	exe := filepath.Join(home, "Applications", "SD Sync Immich.app", "Contents", "MacOS", "sd-sync-immich")
	writeFile(t, exe, "x")
	if err := SetAutostart(true, exe); err != nil {
		t.Fatal(err)
	}

	if err := SetAutostart(false, exe); err != nil {
		t.Fatalf("SetAutostart(false): %v", err)
	}
	if AutostartEnabled() {
		t.Fatalf("LaunchAgent still exists")
	}
	// bootout would kill this very process.
	if len(rec.commands)+len(rec.detached) != 0 {
		t.Fatalf("ran %q %q under launchd", rec.commands, rec.detached)
	}
}

func TestUninstallRemovesBundleAgentAndLegacyBinary(t *testing.T) {
	home, rec := setup(t)
	legacy := filepath.Join(home, "Applications", "sd-sync-immich")
	bin := filepath.Join(t.TempDir(), "sd-sync-immich")
	writeFile(t, bin, "x")
	bundlePath, err := Install(bin, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, legacy, "v0.3.0")
	plistPath := filepath.Join(home, "Library", "LaunchAgents", "com.sd-sync-immich.plist")
	rec.commands = nil

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	for _, p := range []string{bundlePath, legacy, plistPath} {
		if exists(p) {
			t.Errorf("%s still exists", p)
		}
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if want := []string{"launchctl bootout " + domain + " " + plistPath}; !reflect.DeepEqual(rec.commands, want) {
		t.Fatalf("commands = %q, want %q", rec.commands, want)
	}
}

func TestOpenWhenExitedWaitsForThisProcess(t *testing.T) {
	_, rec := setup(t)
	if err := OpenWhenExited("/A/SD Sync Immich.app"); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{
		"/bin/sh", "-c",
		`while kill -0 "$1" 2>/dev/null; do sleep 0.2; done; exec /usr/bin/open "$2"`,
		"sh", strconv.Itoa(os.Getpid()), "/A/SD Sync Immich.app",
	}}
	if !reflect.DeepEqual(rec.detached, want) || len(rec.commands) != 0 {
		t.Fatalf("detached = %q, commands = %q; want %q", rec.detached, rec.commands, want)
	}
}

func TestUnderLaunchd(t *testing.T) {
	t.Setenv("XPC_SERVICE_NAME", "com.sd-sync-immich")
	if !UnderLaunchd() {
		t.Fatal("UnderLaunchd() = false for the agent's label")
	}
	t.Setenv("XPC_SERVICE_NAME", "application.com.sd-sync-immich.123.456")
	if UnderLaunchd() {
		t.Fatal("UnderLaunchd() = true for an app opened from Finder")
	}
}
