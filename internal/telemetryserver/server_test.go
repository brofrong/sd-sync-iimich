package telemetryserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

const testID = "0123456789abcdef0123456789abcdef"

func newServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	store, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	srv := &Server{Store: store, Password: "secret", Now: func() time.Time { return testNow }}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func post(t *testing.T, ts *httptest.Server, body string) int {
	t.Helper()
	resp, err := http.Post(ts.URL+"/v1/events", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func request(id, version string, events string) string {
	return fmt.Sprintf(`{"id":%q,"version":%q,"os":"darwin","osVersion":"15.1","arch":"arm64","install":"app","lang":"ru","events":[%s]}`,
		id, version, events)
}

func dashboard(t *testing.T, ts *httptest.Server, password string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	if password != "" {
		req.SetBasicAuth("admin", password)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestStoresValidEventsAndShowsThem(t *testing.T) {
	_, ts := newServer(t)
	at := testNow.Unix()
	events := fmt.Sprintf(`{"type":"launch","at":%d},
		{"type":"sync","at":%d,"data":{"result":"done","files":3,"uploaded":2,"duplicates":1,"bytes":2500000000,"stacks":1,
			"errorKinds":{"network":1},"path":"/Users/me/DCIM"}},
		{"type":"error","at":%d,"data":{"message":"sync failed"}}`, at, at, at)
	if code := post(t, ts, request(testID, "v0.7.0", events)); code != http.StatusNoContent {
		t.Fatalf("POST = %d, want 204", code)
	}

	code, page := dashboard(t, ts, "secret")
	if code != http.StatusOK {
		t.Fatalf("dashboard = %d, want 200", code)
	}
	for _, want := range []string{"v0.7.0", "darwin 15.1", "sync failed", "<code>done</code>", "2.5", "<code>network</code>"} {
		if !strings.Contains(page, want) {
			t.Errorf("dashboard lacks %q", want)
		}
	}
	if strings.Contains(page, "/Users/me") {
		t.Error("dashboard shows a field the sync event may not carry")
	}
}

func TestRefusesBadRequests(t *testing.T) {
	_, ts := newServer(t)
	launch := `{"type":"launch","at":1}`
	tests := map[string]string{
		"bad id":           request("nope", "v0.7.0", launch),
		"dev version":      request(testID, "dev", launch),
		"no events":        request(testID, "v0.7.0", ""),
		"unknown type":     request(testID, "v0.7.0", `{"type":"files","at":1}`),
		"empty error":      request(testID, "v0.7.0", `{"type":"error","at":1,"data":{}}`),
		"not JSON":         "hello",
		"too large":        request(testID, "v0.7.0", strings.Repeat(launch+",", 2000)+launch),
		"too many events":  request(testID, "v0.7.0", strings.Repeat(launch+",", maxEvents)+launch),
		"unknown system":   strings.Replace(request(testID, "v0.7.0", launch), `"darwin"`, `"plan9"`, 1),
		"unknown install":  strings.Replace(request(testID, "v0.7.0", launch), `"app"`, `"snap"`, 1),
		"html in language": strings.Replace(request(testID, "v0.7.0", launch), `"ru"`, `"<b>"`, 1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if code := post(t, ts, body); code != http.StatusBadRequest {
				t.Fatalf("POST = %d, want 400", code)
			}
		})
	}
}

func TestEventTimesFarOffUseTheServersClock(t *testing.T) {
	r := Request{ID: testID, Version: "v1.0.0", OS: "linux", Arch: "amd64", Install: "flatpak",
		Events: []Event{{Type: "launch", At: 1}, {Type: "launch", At: testNow.Unix() - 60}}}
	if err := validate(&r, testNow); err != nil {
		t.Fatal(err)
	}
	if r.Events[0].At != testNow.Unix() || r.Events[1].At != testNow.Unix()-60 {
		t.Fatalf("times = %d, %d", r.Events[0].At, r.Events[1].At)
	}
}

func TestErrorMessagesAreCleaned(t *testing.T) {
	data, err := cleanData("error", map[string]any{"message": "a\x00b\nc" + strings.Repeat("я", 100)})
	if err != nil {
		t.Fatal(err)
	}
	msg := data["message"].(string)
	if strings.ContainsAny(msg, "\x00\n") || len(msg) > 120 || !strings.HasPrefix(msg, "abc") {
		t.Fatalf("message = %q", msg)
	}
}

func TestRateLimit(t *testing.T) {
	_, ts := newServer(t)
	body := request(testID, "v0.7.0", `{"type":"launch","at":1}`)
	for i := range rateLimit {
		if code := post(t, ts, body); code != http.StatusNoContent {
			t.Fatalf("request %d = %d, want 204", i+1, code)
		}
	}
	if code := post(t, ts, body); code != http.StatusTooManyRequests {
		t.Fatalf("request over the limit = %d, want 429", code)
	}
}

func TestDashboardNeedsThePassword(t *testing.T) {
	srv, ts := newServer(t)
	for _, password := range []string{"", "wrong"} {
		if code, _ := dashboard(t, ts, password); code != http.StatusUnauthorized {
			t.Errorf("dashboard with password %q = %d, want 401", password, code)
		}
	}
	srv.Password = ""
	if code, _ := dashboard(t, ts, ""); code != http.StatusUnauthorized {
		t.Errorf("dashboard without a password set = %d, want 401", code)
	}
}

func TestActiveInstallsAndDays(t *testing.T) {
	srv, _ := newServer(t)
	ctx := context.Background()
	add := func(id string, daysAgo int, typ string) {
		at := testNow.Add(-time.Duration(daysAgo) * 24 * time.Hour).Unix()
		r := Request{ID: id, Version: "v1.0.0", OS: "linux", Arch: "amd64", Install: "appimage",
			Events: []Event{{Type: typ, At: at, Data: map[string]any{}}}}
		if err := srv.Store.Add(ctx, r, testNow); err != nil {
			t.Fatal(err)
		}
	}
	a, b, c := strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)
	add(a, 0, "launch")
	add(a, 0, "heartbeat")
	add(b, 3, "launch")
	add(c, 60, "launch")

	st, err := srv.Store.Stats(ctx, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if st.Installs != 3 || st.Active1 != 1 || st.Active7 != 2 || st.Active30 != 2 {
		t.Fatalf("installs %d, active %d/%d/%d; want 3, 1/2/2", st.Installs, st.Active1, st.Active7, st.Active30)
	}
	if len(st.Days) != 30 {
		t.Fatalf("days = %d, want 30", len(st.Days))
	}
	if today := st.Days[29]; today.Active != 1 || today.Launches != 1 {
		t.Fatalf("today = %+v, want 1 active, 1 launch", today)
	}

	if err := srv.Store.Prune(ctx, testNow.Add(retention-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if st, _ := srv.Store.Stats(ctx, testNow); st.Installs != 2 {
		t.Fatalf("installs after pruning = %d, want 2", st.Installs)
	}
}

func TestDownloads(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/releases" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `[{"tag_name":"v1.0.0","published_at":"2026-10-28T10:00:00Z","assets":[
			{"name":"SD-Sync-Immich.dmg","download_count":5},
			{"name":"SD-Sync-Immich.zip","download_count":2},
			{"name":"sd-sync-immich-darwin-arm64","download_count":1},
			{"name":"SD-Sync-Immich-Setup-x64.exe","download_count":3},
			{"name":"SD-Sync-Immich-x86_64.AppImage","download_count":4},
			{"name":"SD-Sync-Immich-x86_64.flatpak","download_count":6},
			{"name":"checksums.txt","download_count":100},
			{"name":"sd-sync-immich-vendor.tar.xz","download_count":7}]}]`)
	}))
	defer gh.Close()
	d := &Downloads{Repo: "o/r", APIURL: gh.URL}
	got, err := d.Releases(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := Release{Tag: "v1.0.0", Date: "2026-10-28", DMG: 5, MacOther: 3, Windows: 3, AppImage: 4, Flatpak: 6, Total: 21}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("Releases() = %+v, want %+v", got, want)
	}
}
