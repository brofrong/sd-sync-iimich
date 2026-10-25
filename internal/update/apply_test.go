package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeRelease serves asset name with content and a checksums.txt that
// lists sum for it (the real SHA-256 of content when sum is ""). It
// returns the release and the number of requests served.
func fakeRelease(t *testing.T, name string, content []byte, sum string) (*Client, *Release, *atomic.Int32) {
	t.Helper()
	if sum == "" {
		sum = fmt.Sprintf("%x", sha256.Sum256(content))
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/asset":
			_, _ = w.Write(content)
		case "/checksums":
			_, _ = fmt.Fprintf(w, "%s  %s\n", sum, name)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client := NewClient("v1.0.0")
	client.allowLocalDownloads = true
	return client, &Release{
		Version:   "v1.1.0",
		Binary:    Asset{Name: name, URL: server.URL + "/asset"},
		Checksums: Asset{Name: "checksums.txt", URL: server.URL + "/checksums"},
	}, &requests
}

const badSum = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// recordStart is a start seam that records what it was asked to run:
// calls holds each program and its arguments, procs the whole process.
type recordStart struct {
	calls [][]string
	procs []process
	err   error
}

func (r *recordStart) start(p process) error {
	r.calls = append(r.calls, append([]string{p.name}, p.args...))
	r.procs = append(r.procs, p)
	return r.err
}

func TestWindowsUpdateRunsInstallerSilently(t *testing.T) {
	setup := []byte("MZ new installer")
	client, rel, _ := fakeRelease(t, AssetName("windows", "amd64"), setup, "")
	dir := t.TempDir()
	stale := filepath.Join(dir, "SD-Sync-Immich-Setup-123.exe")
	if err := os.WriteFile(stale, []byte("old installer"), 0o644); err != nil {
		t.Fatal(err)
	}
	var rec recordStart
	if err := client.runInstaller(context.Background(), rel, dir, rec.start); err != nil {
		t.Fatalf("runInstaller(): %v", err)
	}
	if len(rec.calls) != 1 {
		t.Fatalf("started %q, want one installer", rec.calls)
	}
	call := rec.calls[0]
	if len(call) != 3 || call[1] != "/S" || call[2] != "/RELAUNCH" {
		t.Fatalf("started %q, want <setup.exe> /S /RELAUNCH", call)
	}
	if filepath.Dir(call[0]) != dir || filepath.Ext(call[0]) != ".exe" {
		t.Fatalf("installer path %q, want an .exe in %s", call[0], dir)
	}
	if rec.procs[0].dir != dir {
		t.Fatalf("installer runs in %q, want %s", rec.procs[0].dir, dir)
	}
	got, err := os.ReadFile(call[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(setup) {
		t.Fatalf("installer content = %q, want the downloaded %q", got, setup)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installer left from an earlier update was not removed: %v", err)
	}
}

func TestWindowsUpdateNeverStartsInstallerWithBadChecksum(t *testing.T) {
	client, rel, _ := fakeRelease(t, AssetName("windows", "amd64"), []byte("MZ tampered"), badSum)
	dir := t.TempDir()
	var rec recordStart
	if err := client.runInstaller(context.Background(), rel, dir, rec.start); err == nil {
		t.Fatal("runInstaller() accepted a checksum mismatch")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("started %q despite the checksum mismatch", rec.calls)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("files written despite the checksum mismatch: %v", entries)
	}
}

func TestWindowsUpdateRemovesInstallerThatDidNotStart(t *testing.T) {
	client, rel, _ := fakeRelease(t, AssetName("windows", "amd64"), []byte("MZ installer"), "")
	dir := t.TempDir()
	rec := recordStart{err: errors.New("access denied")}
	if err := client.runInstaller(context.Background(), rel, dir, rec.start); err == nil {
		t.Fatal("runInstaller() succeeded although the installer did not start")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("installer left behind after a failed start: %v", entries)
	}
}

// oldAppImage writes the running AppImage into a fresh directory and
// returns its path, with symlinks resolved (macOS' /var is one).
func oldAppImage(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SD-Sync-Immich-x86_64.AppImage")
	if err := os.WriteFile(path, []byte("old AppImage"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// assertOnlyFile fails unless dir holds just path, with content.
func assertOnlyFile(t *testing.T, path, content string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("%s = %q, want %q", path, got, content)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%s holds %v, want only %s", filepath.Dir(path), entries, filepath.Base(path))
	}
}

func TestLinuxUpdateWithoutAppImage(t *testing.T) {
	client, rel, requests := fakeRelease(t, AssetName("linux", "amd64"), []byte("new AppImage"), "")
	var rec recordStart
	err := client.replaceAppImage(context.Background(), rel, "", "", 42, rec.start)
	if !errors.Is(err, ErrManualUpdate) {
		t.Fatalf("replaceAppImage() error = %v, want ErrManualUpdate", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("%d downloads without an AppImage to replace, want none", n)
	}
	if len(rec.calls) != 0 {
		t.Fatalf("started %q without an AppImage", rec.calls)
	}
}

func TestLinuxUpdateWithMissingAppImage(t *testing.T) {
	client, rel, requests := fakeRelease(t, AssetName("linux", "amd64"), []byte("new AppImage"), "")
	missing := filepath.Join(t.TempDir(), "gone.AppImage")
	var rec recordStart
	err := client.replaceAppImage(context.Background(), rel, missing, "", 42, rec.start)
	if !errors.Is(err, ErrManualUpdate) {
		t.Fatalf("replaceAppImage() error = %v, want ErrManualUpdate", err)
	}
	if n := requests.Load(); n != 0 || len(rec.calls) != 0 {
		t.Fatalf("downloads = %d, started %q; want nothing", n, rec.calls)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing AppImage was created: %v", err)
	}
}

func TestLinuxUpdateReplacesAppImage(t *testing.T) {
	newImage := []byte("new AppImage")
	client, rel, _ := fakeRelease(t, AssetName("linux", "amd64"), newImage, "")
	appimage := oldAppImage(t)
	var rec recordStart
	if err := client.replaceAppImage(context.Background(), rel, appimage, "", 4242, rec.start); err != nil {
		t.Fatalf("replaceAppImage(): %v", err)
	}
	assertOnlyFile(t, appimage, string(newImage))
	info, err := os.Stat(appimage)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o755 {
		t.Fatalf("AppImage mode = %o, want 755", info.Mode().Perm())
	}
	if len(rec.calls) != 1 {
		t.Fatalf("started %q, want one relaunch", rec.calls)
	}
	// The new AppImage starts once this process (4242) has exited, so two
	// copies never sync at once.
	call := rec.calls[0]
	if len(call) != 8 || call[0] != "/bin/sh" || call[2] != relaunchScript || call[4] != strconv.Itoa(4242) || call[5] != appimage {
		t.Fatalf("relaunch = %q, want /bin/sh -c <script> sh <pid> <AppImage> <tries> <log>", call)
	}
	if call[6] != strconv.Itoa(relaunchTries) {
		t.Fatalf("relaunch waits %s times, want %d", call[6], relaunchTries)
	}
}

func TestLinuxUpdateRelaunchesWithCleanEnvironment(t *testing.T) {
	client, rel, _ := fakeRelease(t, AssetName("linux", "amd64"), []byte("new AppImage"), "")
	appimage := oldAppImage(t)
	appdir := "/tmp/.mount_SDSyncOld"
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows' home, as os.UserHomeDir reads it
	t.Setenv("APPIMAGE", appimage)
	t.Setenv("APPDIR", appdir)
	t.Setenv("ARGV0", "SD-Sync-Immich-x86_64.AppImage")
	t.Setenv("OWD", "/home/u")
	t.Setenv("LD_LIBRARY_PATH", appdir+"/usr/lib:/usr/local/lib")
	t.Setenv("GDK_PIXBUF_MODULE_FILE", appdir+"/usr/lib/gdk-pixbuf/loaders.cache")
	t.Setenv("SD_SYNC_KEEP", "kept")
	t.Setenv("PATH", appdir+"/usr/bin:/usr/bin:/bin")
	var rec recordStart
	if err := client.replaceAppImage(context.Background(), rel, appimage, appdir, 1, rec.start); err != nil {
		t.Fatalf("replaceAppImage(): %v", err)
	}
	if len(rec.procs) != 1 {
		t.Fatalf("started %q, want one relaunch", rec.calls)
	}
	p := rec.procs[0]
	if p.dir != home {
		t.Errorf("relaunch dir = %q, want $HOME %q", p.dir, home)
	}
	if p.env == nil {
		t.Fatal("relaunch inherits the whole environment, want a cleaned copy")
	}
	kept, path := false, ""
	for _, kv := range p.env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
		name, value, _ := strings.Cut(kv, "=")
		switch {
		case name == "APPIMAGE" || name == "APPDIR" || name == "ARGV0" || name == "OWD":
			t.Errorf("relaunch environment keeps %s", kv)
		case strings.Contains(value, appdir):
			t.Errorf("relaunch environment keeps %s, which points into the old AppImage", kv)
		case kv == "SD_SYNC_KEEP=kept":
			kept = true
		}
	}
	if !kept {
		t.Error("relaunch environment dropped an unrelated variable")
	}
	// The relaunch script needs sleep and date from PATH.
	if path != "/usr/bin:/bin" {
		t.Errorf("relaunch PATH = %q, want the old one without the AppImage's entry", path)
	}
}

func TestRelaunchScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /bin/sh")
	}
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	fake := filepath.Join(dir, "fake.AppImage")
	// A stand-in for the new AppImage: it records its arguments.
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\" > \""+started+"\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "app.log")
	// The script exits non-zero when it gives up; only its effects count.
	run := func(pid int, tries int) {
		_ = exec.Command("/bin/sh", "-c", relaunchScript, "sh", strconv.Itoa(pid), fake, strconv.Itoa(tries), logPath).Run()
	}

	// The old process never exits: give up, log it, start nothing.
	run(os.Getpid(), 2)
	if _, err := os.Stat(started); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("new version started while the old one still runs: %v", err)
	}
	if log, err := os.ReadFile(logPath); err != nil || !strings.Contains(string(log), "level=WARN") {
		t.Fatalf("no warning logged on timeout: %q, %v", log, err)
	}

	// The old process is gone: the new version starts with "run".
	sleeper := exec.Command("/bin/sh", "-c", "exit 0")
	if err := sleeper.Run(); err != nil {
		t.Fatal(err)
	}
	run(sleeper.Process.Pid, 2)
	if got, err := os.ReadFile(started); err != nil || strings.TrimSpace(string(got)) != "run" {
		t.Fatalf("new version started with %q, %v; want \"run\"", got, err)
	}
}

func TestLinuxUpdateFollowsSymlinkToAppImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("AppImages and their symlinks are Linux-only")
	}
	newImage := []byte("new AppImage")
	client, rel, _ := fakeRelease(t, AssetName("linux", "amd64"), newImage, "")
	appimage := oldAppImage(t)
	link := filepath.Join(t.TempDir(), "sd-sync-immich")
	if err := os.Symlink(appimage, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	var rec recordStart
	if err := client.replaceAppImage(context.Background(), rel, link, "", 1, rec.start); err != nil {
		t.Fatalf("replaceAppImage(): %v", err)
	}
	if target, err := os.Readlink(link); err != nil || target != appimage {
		t.Fatalf("symlink replaced: target %q, %v", target, err)
	}
	assertOnlyFile(t, appimage, string(newImage))
}

func TestLinuxUpdateKeepsAppImageOnChecksumMismatch(t *testing.T) {
	client, rel, _ := fakeRelease(t, AssetName("linux", "amd64"), []byte("tampered"), badSum)
	appimage := oldAppImage(t)
	var rec recordStart
	if err := client.replaceAppImage(context.Background(), rel, appimage, "", 1, rec.start); err == nil {
		t.Fatal("replaceAppImage() accepted a checksum mismatch")
	}
	assertOnlyFile(t, appimage, "old AppImage")
	if len(rec.calls) != 0 {
		t.Fatalf("started %q despite the checksum mismatch", rec.calls)
	}
}

func TestLinuxUpdateKeepsAppImageInReadOnlyDirectory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions do not apply")
	}
	client, rel, _ := fakeRelease(t, AssetName("linux", "amd64"), []byte("new AppImage"), "")
	appimage := oldAppImage(t)
	dir := filepath.Dir(appimage)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	var rec recordStart
	if err := client.replaceAppImage(context.Background(), rel, appimage, "", 1, rec.start); err == nil {
		t.Fatal("replaceAppImage() succeeded in a read-only directory")
	}
	assertOnlyFile(t, appimage, "old AppImage")
	if len(rec.calls) != 0 {
		t.Fatalf("started %q although nothing was replaced", rec.calls)
	}
}
