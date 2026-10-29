// Package telemetryserver receives the app's anonymous statistics (see
// package telemetry) and shows them on a password-protected page. It
// stores what the app sends and nothing about the sender: no IP address,
// which is only used, in memory, to limit the request rate.
package telemetryserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// retention is how long events are kept.
const retention = 400 * 24 * time.Hour

const schema = `
CREATE TABLE IF NOT EXISTS events (
	id         INTEGER PRIMARY KEY,
	received   INTEGER NOT NULL,
	at         INTEGER NOT NULL,
	install    TEXT NOT NULL,
	version    TEXT NOT NULL,
	os         TEXT NOT NULL,
	os_version TEXT NOT NULL,
	arch       TEXT NOT NULL,
	kind       TEXT NOT NULL,
	lang       TEXT NOT NULL,
	type       TEXT NOT NULL,
	data       TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS events_at ON events (at);
CREATE INDEX IF NOT EXISTS events_type_at ON events (type, at);
`

// Store keeps the events in SQLite.
type Store struct {
	db *sql.DB
}

// Open opens (creating it if needed) the database at path.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// Add stores a request's events, received at received.
func (s *Store) Add(ctx context.Context, r Request, received time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO events
		(received, at, install, version, os, os_version, arch, kind, lang, type, data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, e := range r.Events {
		data, err := json.Marshal(e.Data)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx, received.Unix(), e.At, r.ID, r.Version, r.OS, r.OSVersion,
			r.Arch, r.Install, r.Lang, e.Type, string(data)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// Prune deletes the events older than the retention period.
func (s *Store) Prune(ctx context.Context, now time.Time) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE at < ?`, now.Add(-retention).Unix())
	return err
}
