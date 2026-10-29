package telemetryserver

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

const (
	// maxBody bounds a request: the app sends at most 50 small events.
	maxBody   = 16 << 10
	maxEvents = 50
	// maxClockSkew is how far an event's time may be from the server's
	// before the time it was received is used instead.
	maxClockSkew = 30 * 24 * time.Hour
	// rateLimit requests per rateWindow from one address.
	rateLimit  = 30
	rateWindow = time.Minute
)

var (
	idPattern      = regexp.MustCompile(`^[0-9a-f]{32}$`)
	versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
	codePattern    = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)
	langPattern    = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})?$`)

	systems  = []string{"darwin", "windows", "linux"}
	installs = []string{"app", "installer", "appimage", "flatpak", "binary"}

	// syncCounts are the numbers a sync event may carry.
	syncCounts = []string{"files", "uploaded", "duplicates", "skipped", "errors", "stacks", "bytes", "seconds"}
)

// Request is what the app sends (see package telemetry).
type Request struct {
	ID        string  `json:"id"`
	Version   string  `json:"version"`
	OS        string  `json:"os"`
	OSVersion string  `json:"osVersion"`
	Arch      string  `json:"arch"`
	Install   string  `json:"install"`
	Lang      string  `json:"lang"`
	Events    []Event `json:"events"`
}

// Event is one event of a Request; Data is kept only as validate leaves it.
type Event struct {
	Type string         `json:"type"`
	At   int64          `json:"at"`
	Data map[string]any `json:"data"`
}

// validate checks r and cleans its events' data down to the fields each
// type may carry, with times outside maxClockSkew of now replaced by now.
func validate(r *Request, now time.Time) error {
	switch {
	case !idPattern.MatchString(r.ID):
		return errors.New("bad id")
	case !versionPattern.MatchString(r.Version):
		return errors.New("bad version")
	case !slices.Contains(systems, r.OS):
		return errors.New("bad os")
	case !codePattern.MatchString(r.Arch):
		return errors.New("bad arch")
	case !slices.Contains(installs, r.Install):
		return errors.New("bad install")
	case r.Lang != "" && !langPattern.MatchString(r.Lang):
		return errors.New("bad lang")
	case len(r.Events) == 0 || len(r.Events) > maxEvents:
		return errors.New("bad number of events")
	}
	r.OSVersion = clean(r.OSVersion, 64)
	for i := range r.Events {
		e := &r.Events[i]
		if t := time.Unix(e.At, 0); t.Before(now.Add(-maxClockSkew)) || t.After(now.Add(maxClockSkew)) {
			e.At = now.Unix()
		}
		data, err := cleanData(e.Type, e.Data)
		if err != nil {
			return err
		}
		e.Data = data
	}
	return nil
}

// cleanData keeps the fields an event of type typ may carry.
func cleanData(typ string, in map[string]any) (map[string]any, error) {
	out := map[string]any{}
	switch typ {
	case "launch", "heartbeat":
	case "sync":
		if code, ok := in["result"].(string); ok && codePattern.MatchString(code) {
			out["result"] = code
		}
		for _, k := range syncCounts {
			if n, ok := count(in[k]); ok {
				out[k] = n
			}
		}
		if kinds, ok := in["errorKinds"].(map[string]any); ok {
			clean := map[string]int64{}
			for k, v := range kinds {
				if n, ok := count(v); ok && codePattern.MatchString(k) && len(clean) < 10 {
					clean[k] = n
				}
			}
			if len(clean) > 0 {
				out["errorKinds"] = clean
			}
		}
	case "error":
		msg, _ := in["message"].(string)
		if msg = clean(msg, 120); msg == "" {
			return nil, errors.New("error event without a message")
		}
		out["message"] = msg
	default:
		return nil, fmt.Errorf("unknown event type %q", typ)
	}
	return out, nil
}

// count reads a non-negative whole number from JSON.
func count(v any) (int64, bool) {
	f, ok := v.(float64)
	if !ok || f < 0 || f > 1<<53 || f != float64(int64(f)) {
		return 0, false
	}
	return int64(f), true
}

// clean keeps the printable characters of s, at most max bytes of them.
func clean(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return -1
	}, s)
	if len(s) > max {
		s = strings.ToValidUTF8(s[:max], "")
	}
	return strings.TrimSpace(s)
}

// Server is the HTTP side: POST /v1/events, GET /healthz and the
// dashboard at /.
type Server struct {
	Store *Store
	// Password protects the dashboard (user "admin"); empty turns the
	// dashboard off.
	Password string
	// Downloads, when set, counts the release downloads for the dashboard.
	Downloads *Downloads
	Now       func() time.Time

	limiter limiter
}

func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Handler returns the server's routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/events", s.events)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /{$}", s.dashboard)
	return mux
}

func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	now := s.now()
	if !s.limiter.allow(clientIP(r), now) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	var req Request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	if err := dec.Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := validate(&req, now); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.Store.Add(r.Context(), req, now); err != nil {
		slog.Error("store events", "err", err)
		http.Error(w, "cannot store", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// authorized checks the dashboard's basic auth.
func (s *Server) authorized(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	return ok && s.Password != "" &&
		subtle.ConstantTimeCompare([]byte(user), []byte("admin")) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(s.Password)) == 1
}

// clientIP is the address the request came from: the last entry of
// X-Forwarded-For, which the reverse proxy in front adds, or the
// connection's address.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[len(parts)-1])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiter allows rateLimit requests per rateWindow from one address,
// remembering addresses only for the current window.
type limiter struct {
	mu     sync.Mutex
	window time.Time
	counts map[string]int
}

func (l *limiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.window) >= rateWindow || l.counts == nil {
		l.window = now
		l.counts = map[string]int{}
	}
	l.counts[ip]++
	return l.counts[ip] <= rateLimit
}
