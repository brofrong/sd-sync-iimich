package uploader

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

// sha1Hex returns the hex SHA-1 of content, matching what VerifyCard sends
// as the checksum for a file with this content.
func sha1Hex(content []byte) string {
	sum := sha1.Sum(content)
	return hex.EncodeToString(sum[:])
}

// newVerifyService builds a Service wired to a fresh config store and state
// DB (both backed by t.TempDir()) and returns an empty directory to use as
// the fake card's mount point.
func newVerifyService(t *testing.T) (*Service, *config.Store, string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfgStore, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("state.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Service{Config: cfgStore, State: st}, cfgStore, t.TempDir()
}

// writeCardFile creates a file at rel (slash-separated) under mount with the
// given content, creating parent directories as needed.
func writeCardFile(t *testing.T, mount, rel string, content []byte) {
	t.Helper()
	path := filepath.Join(mount, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// bulkCheckRequest is the subset of the POST body VerifyCard sends that the
// fake servers below need to answer.
type bulkCheckRequest struct {
	Assets []struct {
		ID       string `json:"id"`
		Checksum string `json:"checksum"`
	} `json:"assets"`
}

// newVerifyServer starts a fake Immich server for /assets/bulk-upload-check
// that answers by checksum: checksums in duplicate are reported as
// "reject"/"duplicate" (trashed if also listed in trashed); everything else
// is "accept" (i.e. missing). requests counts how many POSTs it received.
func newVerifyServer(t *testing.T, duplicate, trashed map[string]bool) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body bulkCheckRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode bulk-upload-check body: %v", err)
		}
		type result struct {
			ID        string `json:"id"`
			Action    string `json:"action"`
			Reason    string `json:"reason,omitempty"`
			AssetID   string `json:"assetId,omitempty"`
			IsTrashed bool   `json:"isTrashed"`
		}
		results := make([]result, 0, len(body.Assets))
		for _, a := range body.Assets {
			if duplicate[a.Checksum] {
				results = append(results, result{
					ID: a.ID, Action: "reject", Reason: "duplicate",
					AssetID: "asset-" + a.ID, IsTrashed: trashed[a.Checksum],
				})
				continue
			}
			results = append(results, result{ID: a.ID, Action: "accept"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

func configureImmich(t *testing.T, cfgStore *config.Store, url, volumeUUID string) {
	t.Helper()
	if err := cfgStore.Update(func(c *config.Config) {
		c.Immich = config.ImmichConfig{URL: url, APIKey: "key"}
		c.Watch = config.WatchConfig{VolumeUUID: volumeUUID, VolumeName: "CARD"}
	}); err != nil {
		t.Fatalf("cfgStore.Update: %v", err)
	}
}

func TestVerifyCardAllConfirmed(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	contents := [][]byte{[]byte("one"), []byte("two"), []byte("three")}
	names := []string{"DCIM/A.JPG", "DCIM/B.JPG", "DCIM/C.JPG"}
	for i, rel := range names {
		writeCardFile(t, mount, rel, contents[i])
	}

	duplicate := map[string]bool{}
	for _, c := range contents {
		duplicate[sha1Hex(c)] = true
	}
	srv, _ := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	var progressCalls [][2]int
	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, func(done, total int) {
		progressCalls = append(progressCalls, [2]int{done, total})
	})
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if !res.Safe() {
		t.Fatalf("res.Safe() = false, want true (res = %+v)", res)
	}
	if res.Media != 3 {
		t.Fatalf("res.Media = %d, want 3", res.Media)
	}
	if len(progressCalls) == 0 || progressCalls[len(progressCalls)-1] != [2]int{3, 3} {
		t.Fatalf("progress calls = %v, want the last one to be (3, 3)", progressCalls)
	}
}

func TestVerifyCardMissing(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))
	writeCardFile(t, mount, "DCIM/B.JPG", []byte("never-uploaded"))

	duplicate := map[string]bool{sha1Hex([]byte("confirmed")): true}
	srv, _ := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.MissingCount != 1 {
		t.Fatalf("res.MissingCount = %d, want 1 (res = %+v)", res.MissingCount, res)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "DCIM/B.JPG" {
		t.Fatalf("res.Missing = %v, want [DCIM/B.JPG]", res.Missing)
	}
	if res.Safe() {
		t.Fatalf("res.Safe() = true, want false")
	}
}

func TestVerifyCardTrashedIsMissing(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "DCIM/A.JPG", []byte("trashed-on-server"))

	sum := sha1Hex([]byte("trashed-on-server"))
	duplicate := map[string]bool{sum: true}
	trashed := map[string]bool{sum: true}
	srv, _ := newVerifyServer(t, duplicate, trashed)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.MissingCount != 1 {
		t.Fatalf("res.MissingCount = %d, want 1 (a trashed match must not count as confirmed)", res.MissingCount)
	}
	if res.Safe() {
		t.Fatalf("res.Safe() = true, want false")
	}
}

func TestVerifyCardSameContentTwice(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	content := []byte("identical bytes")
	writeCardFile(t, mount, "DCIM/A.JPG", content)
	writeCardFile(t, mount, "DCIM/B.JPG", content)

	duplicate := map[string]bool{sha1Hex(content): true}
	srv, _ := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if !res.Safe() {
		t.Fatalf("res.Safe() = false, want true (res = %+v)", res)
	}
	if res.Media != 2 {
		t.Fatalf("res.Media = %d, want 2", res.Media)
	}
}

func TestVerifyCardUnsupported(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))
	writeCardFile(t, mount, "PRIVATE/OTHER/A.HIF", []byte("outside scan paths"))

	duplicate := map[string]bool{sha1Hex([]byte("confirmed")): true}
	srv, _ := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.Unsupported["hif"] != 1 {
		t.Fatalf("res.Unsupported[hif] = %d, want 1 (res = %+v)", res.Unsupported["hif"], res)
	}
	if res.Safe() {
		t.Fatalf("res.Safe() = true, want false (an unsupported file must block formatting)")
	}
}

func TestVerifyCardIgnoresCameraJunk(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "PRIVATE/M4ROOT/CLIP/C0001M01.XML", []byte("<xml/>"))
	writeCardFile(t, mount, "DCIM/100SONY/._DSC00001.JPG", []byte("apple double junk"))
	writeCardFile(t, mount, "AVF_INFO/AVIN0001.BNP", []byte("sony junk"))
	// A real, confirmable photo alongside the junk: Safe() now requires
	// Media > 0, so a card with only junk and nothing to confirm can never
	// be reported safe (see TestVerifyCardEmptyCardNotSafe below).
	writeCardFile(t, mount, "DCIM/100SONY/DSC00002.JPG", []byte("real photo"))

	duplicate := map[string]bool{sha1Hex([]byte("real photo")): true}
	srv, requests := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.Media != 1 {
		t.Fatalf("res.Media = %d, want 1 (only the real photo)", res.Media)
	}
	if res.MissingCount != 0 || len(res.Unsupported) != 0 {
		t.Fatalf("res = %+v, want no missing and no unsupported files", res)
	}
	if !res.Safe() {
		t.Fatalf("res.Safe() = false, want true")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("server received %d requests, want exactly 1 (only the real photo, never the camera junk)", got)
	}
}

// TestVerifyCardEmptyCardNotSafe checks that a card with nothing to confirm
// (empty, or holding only camera junk the gate ignores) is never reported
// safe to format: Safe() requires Media > 0.
func TestVerifyCardEmptyCardNotSafe(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	srv, requests := newVerifyServer(t, nil, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.Media != 0 {
		t.Fatalf("res.Media = %d, want 0 (empty card)", res.Media)
	}
	if res.Safe() {
		t.Fatalf("res.Safe() = true, want false (an empty/never-synced card must never be reported safe to format)")
	}
	if requests.Load() != 0 {
		t.Fatalf("server received %d requests, want 0 (nothing to check)", requests.Load())
	}
}

// TestVerifyCardUnknownFileBlocks checks that a file this gate has no
// classification for at all (a document, not camera media or a known
// service format) blocks the card from being reported safe.
func TestVerifyCardUnknownFileBlocks(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "DCIM/notes.pdf", []byte("pdf"))
	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))

	duplicate := map[string]bool{sha1Hex([]byte("confirmed")): true}
	srv, _ := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.Unsupported["pdf"] != 1 {
		t.Fatalf("res.Unsupported[pdf] = %d, want 1 (res = %+v)", res.Unsupported["pdf"], res)
	}
	if res.Safe() {
		t.Fatalf("res.Safe() = true, want false")
	}
}

// TestVerifyCardNoExtensionBlocks checks that an extension-less file is
// counted under the "(none)" key rather than silently ignored.
func TestVerifyCardNoExtensionBlocks(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "DCIM/README", []byte("no extension"))
	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))

	duplicate := map[string]bool{sha1Hex([]byte("confirmed")): true}
	srv, _ := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.Unsupported["(none)"] != 1 {
		t.Fatalf(`res.Unsupported["(none)"] = %d, want 1 (res = %+v)`, res.Unsupported["(none)"], res)
	}
	if res.Safe() {
		t.Fatalf("res.Safe() = true, want false")
	}
}

// TestVerifyCardIgnoresSonyThumbnailsAndProxies checks that Sony's
// thumbnail and proxy directories are disregarded entirely (never sent to
// Immich, never counted as unsupported) while the CLIP original they
// shadow is confirmed normally.
func TestVerifyCardIgnoresSonyThumbnailsAndProxies(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "PRIVATE/M4ROOT/CLIP/C0001.MP4", []byte("original clip"))
	writeCardFile(t, mount, "PRIVATE/M4ROOT/THMBNL/C0001T01.JPG", []byte("thumbnail"))
	writeCardFile(t, mount, "PRIVATE/M4ROOT/SUB/C0001S03.MP4", []byte("proxy"))

	duplicate := map[string]bool{sha1Hex([]byte("original clip")): true}
	srv, requests := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.Media != 1 {
		t.Fatalf("res.Media = %d, want 1 (only the CLIP original, not its thumbnail/proxy)", res.Media)
	}
	if !res.Safe() {
		t.Fatalf("res.Safe() = false, want true (res = %+v)", res)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("server received %d requests, want 1 (thumbnails/proxies must never be sent)", got)
	}
}

// TestVerifyCardExcludedMediaBlocks checks that a media file the user's own
// Scan.ExcludePatterns would exclude from upload is treated as Unsupported,
// not silently ignored: the scanner never uploads it, so a format must not
// treat it as already safe.
func TestVerifyCardExcludedMediaBlocks(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	if err := cfgStore.Update(func(c *config.Config) {
		c.Scan.ExcludePatterns = []string{"*.mov"}
	}); err != nil {
		t.Fatalf("cfgStore.Update: %v", err)
	}

	writeCardFile(t, mount, "DCIM/c0005.mov", []byte("excluded by user"))
	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))

	duplicate := map[string]bool{sha1Hex([]byte("confirmed")): true}
	srv, _ := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.Unsupported["mov"] != 1 {
		t.Fatalf("res.Unsupported[mov] = %d, want 1 (res = %+v)", res.Unsupported["mov"], res)
	}
	if res.Safe() {
		t.Fatalf("res.Safe() = true, want false (an excluded media file must block formatting)")
	}
}

// TestVerifyCardUnreadableDirFails checks that an unreadable directory
// fails the whole verification closed, rather than silently treating the
// files inside it as nonexistent.
func TestVerifyCardUnreadableDirFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root ignores directory permissions")
	}
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "DCIM/100SONY/DSC00001.JPG", []byte("locked away"))
	sub := filepath.Join(mount, "DCIM", "100SONY")
	if err := os.Chmod(sub, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o755) })

	srv, _ := newVerifyServer(t, nil, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	_, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err == nil {
		t.Fatal("VerifyCard() error = nil, want an error for an unreadable directory")
	}
}

// TestVerifyCardMissingMountFails checks that a mount point that doesn't
// exist fails verification with an error instead of reporting an
// (incorrectly) empty, "safe-looking" result.
func TestVerifyCardMissingMountFails(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	srv, _ := newVerifyServer(t, nil, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	missing := filepath.Join(mount, "does-not-exist")
	_, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: missing}, nil)
	if err == nil {
		t.Fatal("VerifyCard() error = nil, want an error for a missing mount point")
	}
}

// TestVerifyCardIdMissingFromResponse checks that an id the server's
// response simply omits (as opposed to answering with "accept") still
// counts as not confirmed.
func TestVerifyCardIdMissingFromResponse(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))
	writeCardFile(t, mount, "DCIM/B.JPG", []byte("dropped"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body bulkCheckRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode bulk-upload-check body: %v", err)
		}
		type result struct {
			ID     string `json:"id"`
			Action string `json:"action"`
			Reason string `json:"reason,omitempty"`
		}
		var results []result
		for _, a := range body.Assets {
			if a.ID == "DCIM/B.JPG" {
				continue // the server's response simply omits this id
			}
			results = append(results, result{ID: a.ID, Action: "reject", Reason: "duplicate"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": results})
	}))
	t.Cleanup(srv.Close)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.MissingCount != 1 {
		t.Fatalf("res.MissingCount = %d, want 1 (an id missing from the response must count as missing)", res.MissingCount)
	}
	if len(res.Missing) != 1 || res.Missing[0] != "DCIM/B.JPG" {
		t.Fatalf("res.Missing = %v, want [DCIM/B.JPG]", res.Missing)
	}
}

// TestVerifyCardDuplicateIDWorstWins checks that when the server answers
// more than once for the same id, a single confirming entry cannot paper
// over another, non-confirming one for the same id — the worst answer wins.
func TestVerifyCardDuplicateIDWorstWins(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed-and-not"))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body bulkCheckRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode bulk-upload-check body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		// Two results for the same id, non-confirming FIRST and confirming
		// LAST: a naive "last entry wins" merge would (wrongly) call this
		// confirmed, so this ordering is what actually distinguishes
		// "worst wins" from "last wins".
		_ = json.NewEncoder(w).Encode(map[string]any{"results": []map[string]any{
			{"id": "DCIM/A.JPG", "action": "accept"},
			{"id": "DCIM/A.JPG", "action": "reject", "reason": "duplicate", "isTrashed": false},
		}})
	}))
	t.Cleanup(srv.Close)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.MissingCount != 1 {
		t.Fatalf("res.MissingCount = %d, want 1 (a non-confirming duplicate answer must win)", res.MissingCount)
	}
}

// TestVerifyCardHashFailureIsMissing checks that a file that becomes
// unreadable after being listed (so hashing fails) still counts toward
// Media, but as not confirmed rather than being silently dropped.
func TestVerifyCardHashFailureIsMissing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root ignores file permissions")
	}
	svc, cfgStore, mount := newVerifyService(t)

	writeCardFile(t, mount, "DCIM/A.JPG", []byte("will become unreadable"))
	path := filepath.Join(mount, "DCIM", "A.JPG")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })

	srv, _ := newVerifyServer(t, nil, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.Media != 1 {
		t.Fatalf("res.Media = %d, want 1 (counted even though its hash failed)", res.Media)
	}
	if res.MissingCount != 1 {
		t.Fatalf("res.MissingCount = %d, want 1 (an unreadable file must count as missing, not silently skipped)", res.MissingCount)
	}
}

// TestVerifyCardCancelled checks that a cancelled context stops
// verification and surfaces ctx.Err(), rather than returning a result.
func TestVerifyCardCancelled(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	writeCardFile(t, mount, "DCIM/A.JPG", []byte("whatever"))
	configureImmich(t, cfgStore, "http://127.0.0.1:0", "VOL-UUID")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.VerifyCard(ctx, volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyCard() error = %v, want context.Canceled", err)
	}
}

func TestVerifyCardBatches(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)

	const n = 450
	duplicate := make(map[string]bool, n)
	for i := range n {
		content := []byte(fmt.Sprintf("file-%d", i))
		writeCardFile(t, mount, fmt.Sprintf("DCIM/DSC%05d.JPG", i), content)
		duplicate[sha1Hex(content)] = true
	}

	srv, requests := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.Media != n {
		t.Fatalf("res.Media = %d, want %d", res.Media, n)
	}
	if !res.Safe() {
		t.Fatalf("res.Safe() = false, want true (res = %+v)", res)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("server received %d requests, want 3 (450 files batched by 200)", got)
	}
}

// verifyOnly runs VerifyCard against a fake server that confirms exactly
// the given contents and returns the result plus the request counter.
func verifyOnly(t *testing.T, svc *Service, cfgStore *config.Store, mount string, confirmed ...string) (VerifyResult, *atomic.Int64) {
	t.Helper()
	duplicate := map[string]bool{}
	for _, c := range confirmed {
		duplicate[sha1Hex([]byte(c))] = true
	}
	srv, requests := newVerifyServer(t, duplicate, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")
	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	return res, requests
}

// TestVerifyCardNestedSystemDirNameIsNotIgnored checks that a directory
// merely named like a macOS system folder (".Trashes") below the card root
// is not bookkeeping: media inside it counts and must be confirmed.
func TestVerifyCardNestedSystemDirNameIsNotIgnored(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	writeCardFile(t, mount, "DCIM/100MSDCF/.Trashes/X.ARW", []byte("nested raw"))
	writeCardFile(t, mount, "DCIM/100MSDCF/.Spotlight-V100/Y.JPG", []byte("nested jpg"))
	writeCardFile(t, mount, "DCIM/100MSDCF/A.JPG", []byte("confirmed"))

	res, _ := verifyOnly(t, svc, cfgStore, mount, "confirmed")
	if res.Media != 3 {
		t.Fatalf("res.Media = %d, want 3 (files under nested system-looking dirs count) (res = %+v)", res.Media, res)
	}
	if res.MissingCount != 2 {
		t.Fatalf("res.MissingCount = %d, want 2 (res = %+v)", res.MissingCount, res)
	}
	if res.Safe() {
		t.Fatalf("res.Safe() = true, want false")
	}
}

// TestVerifyCardNestedSystemDirOutsideScanIsUnsupported checks that a
// nested ".Trashes" outside the scan paths is reported Unsupported rather
// than silently ignored.
func TestVerifyCardNestedSystemDirOutsideScanIsUnsupported(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	writeCardFile(t, mount, "OTHER/.Trashes/X.ARW", []byte("outside"))
	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))

	res, _ := verifyOnly(t, svc, cfgStore, mount, "confirmed")
	if res.Unsupported["arw"] != 1 {
		t.Fatalf("res.Unsupported[arw] = %d, want 1 (res = %+v)", res.Unsupported["arw"], res)
	}
	if res.Safe() {
		t.Fatalf("res.Safe() = true, want false")
	}
}

// TestVerifyCardIgnoresRootSystemEntries checks that macOS/Windows
// bookkeeping at the card root (and .DS_Store / AppleDouble files at any
// depth) never reaches the check.
func TestVerifyCardIgnoresRootSystemEntries(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	writeCardFile(t, mount, ".Trashes/501/X.ARW", []byte("trashed raw"))
	writeCardFile(t, mount, ".Spotlight-V100/Store-V2/store.db", []byte("spotlight"))
	writeCardFile(t, mount, ".fseventsd/fseventsd-uuid", []byte("fsevents"))
	writeCardFile(t, mount, ".TemporaryItems/folders.501/tmp", []byte("tmp"))
	writeCardFile(t, mount, "System Volume Information/IndexerVolumeGuid", []byte("win"))
	writeCardFile(t, mount, ".DS_Store", []byte("ds root"))
	writeCardFile(t, mount, "DCIM/100SONY/.DS_Store", []byte("ds nested"))
	writeCardFile(t, mount, "DCIM/100SONY/._DSC00001.JPG", []byte("apple double"))
	writeCardFile(t, mount, "DCIM/100SONY/DSC00001.JPG", []byte("confirmed"))

	res, requests := verifyOnly(t, svc, cfgStore, mount, "confirmed")
	if res.Media != 1 || res.MissingCount != 0 || len(res.Unsupported) != 0 {
		t.Fatalf("res = %+v, want exactly 1 confirmed media file and nothing else", res)
	}
	if !res.Safe() {
		t.Fatalf("res.Safe() = false, want true")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("server received %d requests, want 1", got)
	}
}

// TestVerifyCardIgnoresXDROOTDerivatives checks the XDCAM thumbnail/proxy
// dirs at the card root are ignored like Sony's M4ROOT ones.
func TestVerifyCardIgnoresXDROOTDerivatives(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	writeCardFile(t, mount, "XDROOT/Clip/C0001.MXF", []byte("original"))
	writeCardFile(t, mount, "XDROOT/Sub/C0001S01.MXF", []byte("proxy"))
	writeCardFile(t, mount, "XDROOT/Thmbnl/C0001T01.JPG", []byte("thumb"))
	if err := cfgStore.Update(func(c *config.Config) {
		c.Scan.Extensions = append(c.Scan.Extensions, "mxf")
	}); err != nil {
		t.Fatal(err)
	}

	res, _ := verifyOnly(t, svc, cfgStore, mount, "original")
	if res.Media != 1 || !res.Safe() {
		t.Fatalf("res = %+v, want only the original counted and safe", res)
	}
}

// TestVerifyCardDerivativeDirLookalikeIsNotIgnored checks that the
// thumbnail/proxy dir match is by whole path segment: "SUBX" is not "SUB".
func TestVerifyCardDerivativeDirLookalikeIsNotIgnored(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	writeCardFile(t, mount, "PRIVATE/M4ROOT/SUBX/A.MP4", []byte("lookalike"))
	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))

	res, _ := verifyOnly(t, svc, cfgStore, mount, "confirmed")
	if res.Unsupported["mp4"] != 1 {
		t.Fatalf("res.Unsupported[mp4] = %d, want 1 (res = %+v)", res.Unsupported["mp4"], res)
	}
	if res.Safe() {
		t.Fatalf("res.Safe() = true, want false")
	}
}

// TestVerifyCardSlashPrefixedScanPath checks a configured scan path
// written as "/DCIM" is treated as the card's DCIM folder.
func TestVerifyCardSlashPrefixedScanPath(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	if err := cfgStore.Update(func(c *config.Config) {
		c.Scan.Paths = []string{"/DCIM"}
		c.Scan.AutoPaths = false
	}); err != nil {
		t.Fatal(err)
	}
	writeCardFile(t, mount, "DCIM/100SONY/A.JPG", []byte("confirmed"))

	res, _ := verifyOnly(t, svc, cfgStore, mount, "confirmed")
	if res.Media != 1 || !res.Safe() {
		t.Fatalf("res = %+v, want the DCIM photo counted and safe", res)
	}
}

// TestVerifyCardIgnoresCameraDatabases checks camera database/service
// files (Sony SONYCARD.IND, AVCHD thumbnail index, Nikon .DSC, Canon .MRK)
// and root-level Finder volume files don't block a format.
func TestVerifyCardIgnoresCameraDatabases(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	writeCardFile(t, mount, "PRIVATE/SONY/SONYCARD.IND", []byte("ind"))
	writeCardFile(t, mount, "PRIVATE/AVCHD/AVCHDTN/THUMB.TDT", []byte("tdt"))
	writeCardFile(t, mount, "PRIVATE/AVCHD/AVCHDTN/THUMB.TID", []byte("tid"))
	writeCardFile(t, mount, "NIKON001.DSC", []byte("dsc"))
	writeCardFile(t, mount, "MISC/AUTPRINT.MRK", []byte("mrk"))
	writeCardFile(t, mount, ".VolumeIcon.icns", []byte("icon"))
	writeCardFile(t, mount, ".apdisk", []byte("apdisk"))
	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))

	res, requests := verifyOnly(t, svc, cfgStore, mount, "confirmed")
	if res.Media != 1 || res.MissingCount != 0 || len(res.Unsupported) != 0 {
		t.Fatalf("res = %+v, want exactly 1 confirmed media file and nothing else", res)
	}
	if !res.Safe() {
		t.Fatalf("res.Safe() = false, want true")
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("server received %d requests, want 1", got)
	}
}

// TestVerifyCardNestedVolumeFilesBlock checks .VolumeIcon.icns/.apdisk are
// only Finder bookkeeping at the card root; elsewhere they are unknown files.
func TestVerifyCardNestedVolumeFilesBlock(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	writeCardFile(t, mount, "DCIM/.apdisk", []byte("apdisk"))
	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))

	res, _ := verifyOnly(t, svc, cfgStore, mount, "confirmed")
	if res.Unsupported["apdisk"] != 1 {
		t.Fatalf("res.Unsupported[apdisk] = %d, want 1 (res = %+v)", res.Unsupported["apdisk"], res)
	}
}

// cancelAfterCtx is a context whose Err() reports nil for the first n calls
// and context.Canceled afterwards: it lets a test cancel VerifyCard after
// its up-front check, in the middle of the card walk.
type cancelAfterCtx struct {
	context.Context
	calls atomic.Int64
	n     int64
}

func (c *cancelAfterCtx) Err() error {
	if c.calls.Add(1) > c.n {
		return context.Canceled
	}
	return nil
}

// TestVerifyCardCancelledMidWalk checks a cancellation that arrives while
// the card is being walked stops verification with the context error and
// never reaches Immich.
func TestVerifyCardCancelledMidWalk(t *testing.T) {
	svc, cfgStore, mount := newVerifyService(t)
	for i := range 5 {
		writeCardFile(t, mount, fmt.Sprintf("DCIM/DSC%05d.JPG", i), []byte(fmt.Sprintf("f%d", i)))
	}
	srv, requests := newVerifyServer(t, nil, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	ctx := &cancelAfterCtx{Context: context.Background(), n: 2}
	_, err := svc.VerifyCard(ctx, volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyCard() error = %v, want context.Canceled", err)
	}
	if got := ctx.calls.Load(); got < 3 {
		t.Fatalf("ctx.Err() called %d times, want the walk itself to observe the cancellation", got)
	}
	if requests.Load() != 0 {
		t.Fatalf("server received %d requests, want 0", requests.Load())
	}
}

// TestVerifyCardUnreadableRootTrashesSkipped checks the root ".Trashes"
// (macOS often creates it unreadable) is skipped without being read, so it
// neither fails verification nor hides anything the gate must check.
func TestVerifyCardUnreadableRootTrashesSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root ignores directory permissions")
	}
	svc, cfgStore, mount := newVerifyService(t)
	writeCardFile(t, mount, ".Trashes/501/X.ARW", []byte("trashed"))
	writeCardFile(t, mount, "DCIM/A.JPG", []byte("confirmed"))
	trashes := filepath.Join(mount, ".Trashes")
	if err := os.Chmod(trashes, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(trashes, 0o755) })

	res, _ := verifyOnly(t, svc, cfgStore, mount, "confirmed")
	if !res.Safe() {
		t.Fatalf("res.Safe() = false, want true (res = %+v)", res)
	}
}
