package uploader

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/scanner"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/state"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/testenv"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/volume"
)

func TestRawJPEGGroups(t *testing.T) {
	var files []scanner.File
	for _, p := range []string{
		"DCIM/100/A.ARW", "DCIM/100/A.JPG", // a pair, the JPEG on top
		"DCIM/100/B.JPG",                   // a JPEG alone
		"DCIM/100/C.ARW",                   // a RAW alone
		"DCIM/100/D.CR3", "DCIM/100/D.HIF", // RAW+HEIF
		"DCIM/100/E.HEIC", "DCIM/100/E.JPG", // no RAW
		"DCIM/100/F.ARW", "DCIM/100/F.MP4", // a video is not part of a shot
		"DCIM/100/g.jpg", "DCIM/100/G.NEF", // names compared case-insensitively
		"DCIM/100/H.JPG", "DCIM/101/H.ARW", // other folders
		"DCIM/100/I.DNG", "DCIM/100/I.HEIC", "DCIM/100/I.JPG", // the JPEG before the HEIF
	} {
		files = append(files, scanner.File{RelativePath: p})
	}

	var got [][]string
	for _, g := range rawJPEGGroups(files) {
		var paths []string
		for _, f := range g {
			paths = append(paths, f.RelativePath)
		}
		got = append(got, paths)
	}
	want := [][]string{
		{"DCIM/100/A.JPG", "DCIM/100/A.ARW"},
		{"DCIM/100/D.HIF", "DCIM/100/D.CR3"},
		{"DCIM/100/I.JPG", "DCIM/100/I.HEIC", "DCIM/100/I.DNG"},
		{"DCIM/100/g.jpg", "DCIM/100/G.NEF"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("rawJPEGGroups() = %v, want %v", got, want)
	}
}

// stackServer is a fake Immich: uploads get the id "id-<file name>" (or
// fail for the names in reject), bulk-upload-check knows the files in
// known (checksum → asset id), and stack requests are recorded or answered
// with stackStatus when it is set.
type stackServer struct {
	mu          sync.Mutex
	reject      map[string]bool
	known       map[string]string
	stackStatus int
	stacks      [][]string
}

func (s *stackServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch r.URL.Path {
	case "/api/assets":
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		name := r.FormValue("filename")
		if s.reject[name] {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"message":"Unsupported file type"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":"id-%s","status":"created"}`, name)
	case "/api/assets/bulk-upload-check":
		var body struct {
			Assets []struct{ ID, Checksum string } `json:"assets"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		var results []string
		for _, a := range body.Assets {
			if id, ok := s.known[a.Checksum]; ok {
				results = append(results, fmt.Sprintf(`{"id":%q,"action":"reject","reason":"duplicate","assetId":%q}`, a.ID, id))
			} else {
				results = append(results, fmt.Sprintf(`{"id":%q,"action":"accept"}`, a.ID))
			}
		}
		fmt.Fprintf(w, `{"results":[%s]}`, strings.Join(results, ","))
	case "/api/stacks":
		if s.stackStatus != 0 {
			w.WriteHeader(s.stackStatus)
			fmt.Fprint(w, `{"message":"Missing required permission: stack.create"}`)
			return
		}
		var body struct {
			AssetIDs []string `json:"assetIds"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.stacks = append(s.stacks, body.AssetIDs)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":"stack"}`)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *stackServer) gotStacks() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stacks
}

// stackCard returns an uploader for a card with the named files in DCIM
// (mtimes in the order given) and its fake server.
func stackCard(t *testing.T, srv *stackServer, stack bool, names ...string) (*Service, volume.Info) {
	t.Helper()
	withoutRetryDelays(t)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	testenv.Home(t)
	cfgStore, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfgStore.Update(func(c *config.Config) {
		c.Immich = config.ImmichConfig{URL: ts.URL, APIKey: "key"}
		c.Watch = config.WatchConfig{VolumeUUID: "VOL", VolumeName: "CARD"}
		c.Upload.Concurrency = 1
		c.Upload.StackRawJPEG = stack
	}); err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	mount := t.TempDir()
	dcim := filepath.Join(mount, "DCIM")
	if err := os.MkdirAll(dcim, 0o755); err != nil {
		t.Fatal(err)
	}
	for i, name := range names {
		p := filepath.Join(dcim, name)
		if err := os.WriteFile(p, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		mtime := time.Unix(int64(1000+i), 0)
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	return &Service{Config: cfgStore, State: st}, volume.Info{UUID: "VOL", Name: "CARD", MountPoint: mount}
}

func TestSyncStacksRawJPEGPairs(t *testing.T) {
	srv := &stackServer{}
	svc, vol := stackCard(t, srv, true, "A.ARW", "A.JPG", "B.JPG")

	res, err := svc.Sync(context.Background(), vol, SyncOptions{}, nil)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	want := [][]string{{"id-A.JPG", "id-A.ARW"}}
	if got := srv.gotStacks(); !reflect.DeepEqual(got, want) {
		t.Fatalf("stacks = %v, want %v", got, want)
	}
	if res.Uploaded != 3 || res.Stacks != 1 {
		t.Fatalf("Uploaded = %d, Stacks = %d; want 3 and 1", res.Uploaded, res.Stacks)
	}
}

func TestSyncFindsTheOtherHalfInImmich(t *testing.T) {
	sha, err := scanner.ComputeSHA1(writeTemp(t, "A.JPG"))
	if err != nil {
		t.Fatal(err)
	}
	srv := &stackServer{known: map[string]string{sha: "earlier-jpg"}}
	svc, vol := stackCard(t, srv, true, "A.ARW", "A.JPG")

	// A retry of the RAW alone: the JPEG was uploaded by an earlier sync.
	res, err := svc.Sync(context.Background(), vol, SyncOptions{Only: []string{"DCIM/A.ARW"}}, nil)
	if err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	want := [][]string{{"earlier-jpg", "id-A.ARW"}}
	if got := srv.gotStacks(); !reflect.DeepEqual(got, want) || res.Stacks != 1 {
		t.Fatalf("stacks = %v (Stacks = %d), want %v", got, res.Stacks, want)
	}
}

func TestSyncDoesNotStackWithoutBothHalves(t *testing.T) {
	tests := []struct {
		name  string
		srv   *stackServer
		stack bool
	}{
		{name: "the RAW failed", srv: &stackServer{reject: map[string]bool{"A.ARW": true}}, stack: true},
		{name: "turned off", srv: &stackServer{}, stack: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, vol := stackCard(t, tt.srv, tt.stack, "A.ARW", "A.JPG")
			res, _ := svc.Sync(context.Background(), vol, SyncOptions{}, nil)
			if got := tt.srv.gotStacks(); len(got) != 0 || res.Stacks != 0 {
				t.Fatalf("stacks = %v (Stacks = %d), want none", got, res.Stacks)
			}
		})
	}
}

func TestSyncWithKeyThatCannotStack(t *testing.T) {
	srv := &stackServer{stackStatus: http.StatusForbidden}
	svc, vol := stackCard(t, srv, true, "A.ARW", "A.JPG", "B.ARW", "B.JPG")

	res, err := svc.Sync(context.Background(), vol, SyncOptions{}, nil)
	if err != nil {
		t.Fatalf("Sync() error = %v, want the upload to succeed without stacks", err)
	}
	if res.Uploaded != 4 || res.Errors != 0 || res.Stacks != 0 {
		t.Fatalf("Uploaded = %d, Errors = %d, Stacks = %d; want 4, 0, 0", res.Uploaded, res.Errors, res.Stacks)
	}
}

// writeTemp writes content to a temporary file and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}
