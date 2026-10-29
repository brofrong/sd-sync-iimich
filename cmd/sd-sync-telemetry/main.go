// Command sd-sync-telemetry is the server the app sends its anonymous
// statistics to (see internal/telemetryserver). It is configured with
// environment variables:
//
//	LISTEN              address to listen on (default ":8080")
//	DB_PATH             the SQLite database (default "/data/telemetry.db")
//	DASHBOARD_PASSWORD  password of the dashboard's user "admin"; empty
//	                    turns the dashboard off
//	GITHUB_REPO         where the release downloads are counted
//	                    (default "brofrong/sd-sync-immich")
//
// "sd-sync-telemetry healthcheck" asks the running server's /healthz and
// exits 0 when it answers: the container's health check.
package main

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dmitiigorshkov/sd-sync-immich/internal/telemetryserver"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	addr := cmp.Or(os.Getenv("LISTEN"), ":8080")
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(addr))
	}
	dbPath := cmp.Or(os.Getenv("DB_PATH"), "/data/telemetry.db")

	store, err := telemetryserver.Open(dbPath)
	if err != nil {
		slog.Error("open database", "path", dbPath, "err", err)
		os.Exit(1)
	}
	defer store.Close()

	password := os.Getenv("DASHBOARD_PASSWORD")
	if password == "" {
		slog.Warn("DASHBOARD_PASSWORD is empty: the dashboard is off")
	}
	srv := &telemetryserver.Server{
		Store:     store,
		Password:  password,
		Downloads: &telemetryserver.Downloads{Repo: cmp.Or(os.Getenv("GITHUB_REPO"), "brofrong/sd-sync-immich")},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go prune(ctx, store)

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdown)
	}()
	slog.Info("listening", "addr", addr, "db", dbPath)
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}

// prune deletes old events at start and then daily.
func prune(ctx context.Context, store *telemetryserver.Store) {
	for {
		if err := store.Prune(ctx, time.Now()); err != nil && ctx.Err() == nil {
			slog.Error("prune", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

// healthcheck asks the server listening on addr whether it is up.
func healthcheck(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
	if err != nil {
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
