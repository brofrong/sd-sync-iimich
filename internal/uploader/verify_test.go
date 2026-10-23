package uploader

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
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

	srv, requests := newVerifyServer(t, nil, nil)
	configureImmich(t, cfgStore, srv.URL, "VOL-UUID")

	res, err := svc.VerifyCard(context.Background(), volume.Info{UUID: "VOL-UUID", MountPoint: mount}, nil)
	if err != nil {
		t.Fatalf("VerifyCard() error = %v", err)
	}
	if res.Media != 0 {
		t.Fatalf("res.Media = %d, want 0", res.Media)
	}
	if res.MissingCount != 0 || len(res.Unsupported) != 0 {
		t.Fatalf("res = %+v, want no missing and no unsupported files", res)
	}
	if !res.Safe() {
		t.Fatalf("res.Safe() = false, want true")
	}
	if requests.Load() != 0 {
		t.Fatalf("server received %d requests, want 0 (camera junk must never be sent to Immich)", requests.Load())
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
