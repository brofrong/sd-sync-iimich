package update

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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

// recordStart is a start seam that records what it was asked to run.
type recordStart struct {
	calls [][]string
	err   error
}

func (r *recordStart) start(name string, args ...string) error {
	r.calls = append(r.calls, append([]string{name}, args...))
	return r.err
}

func TestWindowsUpdateRunsInstallerSilently(t *testing.T) {
	setup := []byte("MZ new installer")
	client, rel, _ := fakeRelease(t, AssetName("windows"), setup, "")
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
	client, rel, _ := fakeRelease(t, AssetName("windows"), []byte("MZ tampered"), badSum)
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
	client, rel, _ := fakeRelease(t, AssetName("windows"), []byte("MZ installer"), "")
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
	client, rel, requests := fakeRelease(t, AssetName("linux"), []byte("new AppImage"), "")
	var rec recordStart
	err := client.replaceAppImage(context.Background(), rel, "", 42, rec.start)
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
	client, rel, requests := fakeRelease(t, AssetName("linux"), []byte("new AppImage"), "")
	missing := filepath.Join(t.TempDir(), "gone.AppImage")
	var rec recordStart
	err := client.replaceAppImage(context.Background(), rel, missing, 42, rec.start)
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
	client, rel, _ := fakeRelease(t, AssetName("linux"), newImage, "")
	appimage := oldAppImage(t)
	var rec recordStart
	if err := client.replaceAppImage(context.Background(), rel, appimage, 4242, rec.start); err != nil {
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
	if call[0] != "/bin/sh" || call[len(call)-2] != strconv.Itoa(4242) || call[len(call)-1] != appimage {
		t.Fatalf("relaunch = %q, want /bin/sh ... <pid> <AppImage>", call)
	}
}

func TestLinuxUpdateFollowsSymlinkToAppImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("AppImages and their symlinks are Linux-only")
	}
	newImage := []byte("new AppImage")
	client, rel, _ := fakeRelease(t, AssetName("linux"), newImage, "")
	appimage := oldAppImage(t)
	link := filepath.Join(t.TempDir(), "sd-sync-immich")
	if err := os.Symlink(appimage, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	var rec recordStart
	if err := client.replaceAppImage(context.Background(), rel, link, 1, rec.start); err != nil {
		t.Fatalf("replaceAppImage(): %v", err)
	}
	if target, err := os.Readlink(link); err != nil || target != appimage {
		t.Fatalf("symlink replaced: target %q, %v", target, err)
	}
	assertOnlyFile(t, appimage, string(newImage))
}

func TestLinuxUpdateKeepsAppImageOnChecksumMismatch(t *testing.T) {
	client, rel, _ := fakeRelease(t, AssetName("linux"), []byte("tampered"), badSum)
	appimage := oldAppImage(t)
	var rec recordStart
	if err := client.replaceAppImage(context.Background(), rel, appimage, 1, rec.start); err == nil {
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
	client, rel, _ := fakeRelease(t, AssetName("linux"), []byte("new AppImage"), "")
	appimage := oldAppImage(t)
	dir := filepath.Dir(appimage)
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	var rec recordStart
	if err := client.replaceAppImage(context.Background(), rel, appimage, 1, rec.start); err == nil {
		t.Fatal("replaceAppImage() succeeded in a read-only directory")
	}
	assertOnlyFile(t, appimage, "old AppImage")
	if len(rec.calls) != 0 {
		t.Fatalf("started %q although nothing was replaced", rec.calls)
	}
}
