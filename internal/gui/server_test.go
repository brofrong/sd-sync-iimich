package gui

import (
	"fmt"
	"net"
	"reflect"
	"testing"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/config"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
)

func TestNormalizeServer(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		key     string
		wantURL string
		wantKey string
		wantErr bool
	}{
		{
			name:    "trims spaces, trailing slash and newline in key",
			url:     " https://immich.example/ ",
			key:     " key\n",
			wantURL: "https://immich.example",
			wantKey: "key",
		},
		{
			name:    "drops /api suffix",
			url:     "https://immich.example/api",
			key:     "k",
			wantURL: "https://immich.example",
			wantKey: "k",
		},
		{
			name:    "drops /api/ suffix",
			url:     "http://10.0.0.5:2283/api/",
			key:     "k",
			wantURL: "http://10.0.0.5:2283",
			wantKey: "k",
		},
		{
			name:    "keeps a sub-path",
			url:     "https://example.com/immich/",
			key:     "k",
			wantURL: "https://example.com/immich",
			wantKey: "k",
		},
		{name: "rejects missing scheme", url: "immich.example", key: "k", wantErr: true},
		{name: "rejects other schemes", url: "ftp://x", key: "k", wantErr: true},
		{name: "rejects empty URL", url: "  ", key: "k", wantErr: true},
		{name: "rejects missing host", url: "https://", key: "k", wantErr: true},
		{
			name:    "allows an empty key",
			url:     "https://x",
			key:     "",
			wantURL: "https://x",
			wantKey: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotKey, err := normalizeServer(tt.url, tt.key)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalizeServer(%q, %q) = (%q, %q, nil), want error", tt.url, tt.key, gotURL, gotKey)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeServer(%q, %q) error: %v", tt.url, tt.key, err)
			}
			if gotURL != tt.wantURL || gotKey != tt.wantKey {
				t.Fatalf("normalizeServer(%q, %q) = (%q, %q), want (%q, %q)", tt.url, tt.key, gotURL, gotKey, tt.wantURL, tt.wantKey)
			}
		})
	}
}

func TestServerCheckResult(t *testing.T) {
	dnsErr := &net.DNSError{Err: "no such host", Name: "immich.example"}
	tests := []struct {
		name       string
		info       immich.ServerInfo
		err        error
		wantOK     bool
		wantCode   string
		wantParams map[string]string
	}{
		{name: "ok", info: immich.ServerInfo{Version: "v1.2.3"}, wantOK: true, wantParams: map[string]string{}},
		{name: "401", err: &immich.StatusError{Code: 401, Message: "Unauthorized"}, wantCode: "auth_failed", wantParams: map[string]string{}},
		{name: "403", err: &immich.StatusError{Code: 403, Message: "Forbidden"}, wantCode: "auth_failed", wantParams: map[string]string{}},
		{
			name: "500", err: &immich.StatusError{Code: 500, Message: "boom"}, wantCode: "server_error",
			wantParams: map[string]string{"status": "500", "detail": "boom"},
		},
		{
			name: "dns", err: fmt.Errorf("get: %w", dnsErr), wantCode: "server_unreachable",
			wantParams: map[string]string{"host": "immich.example", "detail": "no such host"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := serverCheckResult(tt.info, tt.err, "immich.example")
			if got.OK != tt.wantOK || got.Code != tt.wantCode {
				t.Fatalf("got OK=%v Code=%q, want OK=%v Code=%q", got.OK, got.Code, tt.wantOK, tt.wantCode)
			}
			if tt.wantOK && got.Version != tt.info.Version {
				t.Fatalf("Version = %q, want %q", got.Version, tt.info.Version)
			}
			if !reflect.DeepEqual(got.Params, tt.wantParams) {
				t.Fatalf("Params = %v, want %v", got.Params, tt.wantParams)
			}
		})
	}
}

func TestKeyForServer(t *testing.T) {
	const saved = "https://immich.example"
	tests := []struct {
		name               string
		url, key           string
		savedURL, savedKey string
		wantKey            string
		wantOK             bool
	}{
		{name: "typed key wins", url: "https://other.example", key: "typed", savedURL: saved, savedKey: "old", wantKey: "typed", wantOK: true},
		{name: "same host uses the saved key", url: "https://immich.example", savedURL: saved, savedKey: "old", wantKey: "old", wantOK: true},
		{name: "host compared case-insensitively", url: "https://IMMICH.example", savedURL: saved, savedKey: "old", wantKey: "old", wantOK: true},
		{name: "another host never gets the saved key", url: "https://other.example", savedURL: saved, savedKey: "old", wantOK: false},
		{name: "another port is another server", url: "https://immich.example:8443", savedURL: saved, savedKey: "old", wantOK: false},
		{name: "no saved key to leak", url: "https://other.example", savedURL: saved, wantKey: "", wantOK: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, ok := keyForServer(tt.url, tt.key, tt.savedURL, tt.savedKey)
			if key != tt.wantKey || ok != tt.wantOK {
				t.Fatalf("keyForServer = %q, %v; want %q, %v", key, ok, tt.wantKey, tt.wantOK)
			}
		})
	}
}

// savedServer returns a Service whose config (under a temporary HOME) has
// url and key saved.
func savedServer(t *testing.T, url, key string) *Service {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Update(func(c *config.Config) { c.Immich.URL, c.Immich.APIKey = url, key }); err != nil {
		t.Fatal(err)
	}
	return &Service{cfg: cfg}
}

func TestTestServerKeepsSavedKeyFromNewHost(t *testing.T) {
	s := savedServer(t, "https://immich.example", "secret")
	got := s.TestServer("https://other.example", "")
	if got.Code != checkKeyRequired || got.OK {
		t.Fatalf("TestServer(new host, no key) = %+v, want code %q", got, checkKeyRequired)
	}
}

func TestSaveServerRefusesNewHostWithoutKey(t *testing.T) {
	s := savedServer(t, "https://immich.example", "secret")
	err := s.SaveServer("https://other.example", "")
	if err == nil || err.Error() != checkKeyRequired {
		t.Fatalf("SaveServer(new host, no key) error = %v, want %q", err, checkKeyRequired)
	}
	if got := s.cfg.Get().Immich; got.URL != "https://immich.example" || got.APIKey != "secret" {
		t.Fatalf("saved server = %q (key %q), want it unchanged", got.URL, got.APIKey)
	}
}
