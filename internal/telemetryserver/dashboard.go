package telemetryserver

import (
	_ "embed"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"time"
)

//go:embed dashboard.html
var dashboardHTML string

var dashboardTmpl = template.Must(template.New("dashboard").Funcs(template.FuncMap{
	"gb": func(b int64) string { return fmt.Sprintf("%.1f", float64(b)/1e9) },
	// dict passes several values to a nested template.
	"dict": func(kv ...any) map[string]any {
		m := map[string]any{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i].(string)] = kv[i+1]
		}
		return m
	},
	"pct": func(n, total int) int {
		if total == 0 {
			return 0
		}
		return n * 100 / total
	},
}).Parse(dashboardHTML))

// dashboardData is what dashboard.html renders.
type dashboardData struct {
	Stats
	Releases       []Release
	Downloads      Release // all releases summed
	DownloadsError string
	Generated      string
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Basic realm="SD Sync Immich statistics", charset="UTF-8"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	now := s.now()
	stats, err := s.Store.Stats(r.Context(), now)
	if err != nil {
		slog.Error("dashboard stats", "err", err)
		http.Error(w, "cannot read the statistics", http.StatusInternalServerError)
		return
	}
	data := dashboardData{Stats: stats, Generated: now.UTC().Format(time.RFC1123)}
	if s.Downloads != nil {
		releases, err := s.Downloads.Releases(r.Context())
		if err != nil {
			data.DownloadsError = err.Error()
		}
		data.Releases = releases
		for _, rel := range releases {
			data.Downloads.DMG += rel.DMG
			data.Downloads.MacOther += rel.MacOther
			data.Downloads.Windows += rel.Windows
			data.Downloads.AppImage += rel.AppImage
			data.Downloads.Flatpak += rel.Flatpak
			data.Downloads.Total += rel.Total
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := dashboardTmpl.Execute(w, data); err != nil {
		slog.Error("dashboard render", "err", err)
	}
}
