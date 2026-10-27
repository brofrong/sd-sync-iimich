package gui

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/app"
	"github.com/dmitiigorshkov/sd-sync-immich/internal/immich"
)

// serverCheckTimeout bounds the "Test connection" request.
const serverCheckTimeout = 20 * time.Second

// ServerCheckView codes that are not app status codes: checkInvalidURL for
// a URL that normalizeServer rejects, checkKeyRequired for a server other
// than the saved one tested or saved without typing its API key,
// checkKeyMissing when no key is typed and none is saved, and
// checkKeyForbidden when Immich takes the key but refuses it the upload
// permission (403).
const (
	checkInvalidURL   = "invalid_url"
	checkKeyRequired  = "key_required"
	checkKeyMissing   = "key_missing"
	checkKeyForbidden = "key_forbidden"
)

// keyForServer returns the API key to use with serverURL (normalized):
// apiKey when one was typed, otherwise the saved key, but only for the
// saved server's host, so the saved key is never sent to a server the
// user just typed in. ok is false when the saved key cannot be used.
func keyForServer(serverURL, apiKey, savedURL, savedKey string) (key string, ok bool) {
	if apiKey != "" {
		return apiKey, true
	}
	savedKey = strings.TrimSpace(savedKey)
	if savedKey == "" {
		return "", true
	}
	if saved, _, err := normalizeServer(savedURL, ""); err != nil || !strings.EqualFold(hostOf(saved), hostOf(serverURL)) {
		return "", false
	}
	return savedKey, true
}

// normalizeServer cleans up a server URL and API key typed or pasted by the
// user: surrounding whitespace (including a trailing newline in a pasted
// key), a trailing "/" and an "/api" suffix are removed. The URL must be an
// absolute http(s) URL with a host. An empty key is returned as is.
func normalizeServer(rawURL, apiKey string) (string, string, error) {
	key := strings.TrimSpace(apiKey)

	s := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	s = strings.TrimSuffix(s, "/api")
	s = strings.TrimRight(s, "/")
	if s == "" {
		return "", "", errors.New("server URL is empty")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", "", fmt.Errorf("invalid server URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", "", fmt.Errorf("server URL must start with http:// or https://")
	}
	if u.Host == "" {
		return "", "", errors.New("server URL has no host")
	}
	return s, key, nil
}

// checkServer runs immich.Client.Check against serverURL (already
// normalized) and reports the outcome in the form the window shows.
func checkServer(ctx context.Context, serverURL, apiKey string) ServerCheckView {
	ctx, cancel := context.WithTimeout(ctx, serverCheckTimeout)
	defer cancel()
	info, err := immich.NewClient(serverURL, apiKey).Check(ctx)
	return serverCheckResult(info, err, hostOf(serverURL))
}

// serverCheckResult maps the result of immich.Client.Check to a view:
// 401 → auth_failed and 403 → key_forbidden, both with Immich's own
// message (for a 403 it names the missing permission), any other HTTP
// status → server_error, anything else (DNS, connection, timeout, bad
// response) → server_unreachable.
func serverCheckResult(info immich.ServerInfo, err error, host string) ServerCheckView {
	if err == nil {
		return ServerCheckView{OK: true, Version: info.Version, Params: map[string]string{}, NoStack: !info.CanStack}
	}
	var se *immich.StatusError
	if errors.As(err, &se) {
		switch se.Code {
		case http.StatusUnauthorized:
			return ServerCheckView{Code: string(app.CodeAuthFailed), Params: map[string]string{"detail": se.Message}}
		case http.StatusForbidden:
			return ServerCheckView{Code: checkKeyForbidden, Params: map[string]string{"detail": se.Message}}
		}
		return ServerCheckView{Code: string(app.CodeServerError), Params: map[string]string{
			"status": strconv.Itoa(se.Code),
			"detail": immich.Detail(err),
		}}
	}
	return ServerCheckView{Code: string(app.CodeServerUnreachable), Params: map[string]string{
		"host":   host,
		"detail": immich.Detail(err),
	}}
}

// hostOf returns the host of a server URL, or the URL itself when it has
// none.
func hostOf(serverURL string) string {
	u, err := url.Parse(serverURL)
	if err != nil || u.Host == "" {
		return serverURL
	}
	return u.Host
}
