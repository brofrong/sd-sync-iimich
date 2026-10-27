package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	_ "modernc.org/sqlite"
)

type FileRecord struct {
	VolumeUUID   string
	RelativePath string
	Size         int64
	Mtime        time.Time
	SHA1         string
	UploadedAt   *time.Time
}

type DB struct {
	db *sql.DB
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(max(runtime.NumCPU(), 4))
	s := &DB{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *DB) Close() error {
	return s.db.Close()
}

func (s *DB) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS files (
			volume_uuid TEXT NOT NULL,
			relative_path TEXT NOT NULL,
			size INTEGER NOT NULL,
			mtime INTEGER NOT NULL,
			sha1 TEXT NOT NULL DEFAULT '',
			uploaded_at INTEGER,
			PRIMARY KEY (volume_uuid, relative_path)
		);
		CREATE INDEX IF NOT EXISTS idx_files_sha1 ON files(sha1);

		CREATE TABLE IF NOT EXISTS volume_sync (
			volume_uuid TEXT PRIMARY KEY,
			last_synced_mtime INTEGER NOT NULL DEFAULT 0,
			last_synced_path TEXT NOT NULL DEFAULT ''
		);

		CREATE TABLE IF NOT EXISTS sync_runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at INTEGER NOT NULL,
			finished_at INTEGER,
			volume_uuid TEXT NOT NULL DEFAULT '',
			volume_name TEXT NOT NULL DEFAULT '',
			result_code TEXT NOT NULL DEFAULT '',
			result_params TEXT NOT NULL DEFAULT '{}',
			uploaded INTEGER NOT NULL DEFAULT 0,
			skipped INTEGER NOT NULL DEFAULT 0,
			errors INTEGER NOT NULL DEFAULT 0,
			duplicates INTEGER NOT NULL DEFAULT 0,
			bytes INTEGER NOT NULL DEFAULT 0,
			error_summary TEXT NOT NULL DEFAULT '{}'
		);
		CREATE INDEX IF NOT EXISTS idx_sync_runs_started_at ON sync_runs(started_at);

		CREATE TABLE IF NOT EXISTS sync_run_failures (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			relative_path TEXT NOT NULL,
			kind TEXT NOT NULL,
			detail TEXT NOT NULL DEFAULT ''
		);
		CREATE INDEX IF NOT EXISTS idx_sync_run_failures_run_id ON sync_run_failures(run_id);
	`)
	if err != nil {
		return err
	}
	if err := s.addColumnIfMissing("sync_runs", "duplicates", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	if err := s.addColumnIfMissing("sync_runs", "failures_recorded", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	return s.markSyncedBehindCursor()
}

// addColumnIfMissing adds a column to a table created by an older version.
func (s *DB) addColumnIfMissing(table, column, decl string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := s.db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl))
	return err
}

func (s *DB) Get(volumeUUID, relativePath string) (*FileRecord, error) {
	row := s.db.QueryRow(`
		SELECT volume_uuid, relative_path, size, mtime, sha1, uploaded_at
		FROM files WHERE volume_uuid = ? AND relative_path = ?
	`, volumeUUID, relativePath)

	var rec FileRecord
	var mtimeUnix int64
	var uploadedAt sql.NullInt64
	if err := row.Scan(&rec.VolumeUUID, &rec.RelativePath, &rec.Size, &mtimeUnix, &rec.SHA1, &uploadedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	rec.Mtime = time.Unix(mtimeUnix, 0)
	if uploadedAt.Valid {
		t := time.Unix(uploadedAt.Int64, 0)
		rec.UploadedAt = &t
	}
	return &rec, nil
}

func (s *DB) Upsert(rec FileRecord) error {
	var uploadedAt *int64
	if rec.UploadedAt != nil {
		v := rec.UploadedAt.Unix()
		uploadedAt = &v
	}
	_, err := s.db.Exec(`
		INSERT INTO files (volume_uuid, relative_path, size, mtime, sha1, uploaded_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(volume_uuid, relative_path) DO UPDATE SET
			size = excluded.size,
			mtime = excluded.mtime,
			sha1 = excluded.sha1,
			uploaded_at = excluded.uploaded_at
	`, rec.VolumeUUID, rec.RelativePath, rec.Size, rec.Mtime.Unix(), rec.SHA1, uploadedAt)
	return err
}

func (s *DB) MarkHashed(volumeUUID, relativePath, sha1 string, size int64, mtime time.Time) error {
	_, err := s.db.Exec(`
		INSERT INTO files (volume_uuid, relative_path, size, mtime, sha1, uploaded_at)
		VALUES (?, ?, ?, ?, ?, NULL)
		ON CONFLICT(volume_uuid, relative_path) DO UPDATE SET
			size = excluded.size,
			mtime = excluded.mtime,
			sha1 = excluded.sha1
	`, volumeUUID, relativePath, size, mtime.Unix(), sha1)
	return err
}

// MarkUploaded records that a hashed file is in Immich (uploaded or already
// there), keeping its hash.
func (s *DB) MarkUploaded(volumeUUID, relativePath string, at time.Time) error {
	_, err := s.db.Exec(`
		UPDATE files SET uploaded_at = ?
		WHERE volume_uuid = ? AND relative_path = ?
	`, at.Unix(), volumeUUID, relativePath)
	return err
}

// markSyncedBehindCursor marks the hashed files a volume's cursor has passed
// as uploaded. Versions before MarkUploaded saved only the hash and the
// cursor; the cursor only passes files that are in Immich.
func (s *DB) markSyncedBehindCursor() error {
	_, err := s.db.Exec(`
		UPDATE files SET uploaded_at = ?
		WHERE uploaded_at IS NULL AND sha1 != '' AND EXISTS (
			SELECT 1 FROM volume_sync v
			WHERE v.volume_uuid = files.volume_uuid AND v.last_synced_mtime > 0 AND (
				files.mtime < v.last_synced_mtime OR
				(files.mtime = v.last_synced_mtime AND files.relative_path <= v.last_synced_path)
			)
		)
	`, time.Now().Unix())
	return err
}

// Checkpoint flushes the WAL so the sync watermark survives abrupt process termination.
func (s *DB) Checkpoint() error {
	_, err := s.db.Exec("PRAGMA wal_checkpoint(FULL)")
	return err
}

// VolumeSyncCursor is the last contiguous synced file (by mtime, then path) for a volume.
type VolumeSyncCursor struct {
	Mtime        time.Time
	RelativePath string
}

// GetVolumeSync returns the volume's sync cursor. A volume with no saved
// cursor gets one rebuilt from its newest file recorded as uploaded, and
// that cursor is saved: only the sync may call it.
func (s *DB) GetVolumeSync(volumeUUID string) (VolumeSyncCursor, error) {
	cur, found, err := s.SavedVolumeSync(volumeUUID)
	if err != nil {
		return VolumeSyncCursor{}, err
	}
	if found {
		return cur, nil
	}
	return s.backfillVolumeSync(volumeUUID)
}

// SavedVolumeSync returns the volume's saved sync cursor, and whether there
// is one, without writing anything: with no saved cursor it returns the zero
// cursor (which covers nothing) and found false. Readers outside the sync,
// such as the gallery, use it so they never save a cursor in the middle of
// a sync.
func (s *DB) SavedVolumeSync(volumeUUID string) (cur VolumeSyncCursor, found bool, err error) {
	row := s.db.QueryRow(`
		SELECT last_synced_mtime, last_synced_path
		FROM volume_sync WHERE volume_uuid = ?
	`, volumeUUID)

	var mtimeUnix int64
	err = row.Scan(&mtimeUnix, &cur.RelativePath)
	if err == sql.ErrNoRows {
		return VolumeSyncCursor{}, false, nil
	}
	if err != nil {
		return VolumeSyncCursor{}, false, err
	}
	if mtimeUnix > 0 {
		cur.Mtime = time.Unix(mtimeUnix, 0)
	}
	return cur, true, nil
}

func (s *DB) backfillVolumeSync(volumeUUID string) (VolumeSyncCursor, error) {
	row := s.db.QueryRow(`
		SELECT mtime, relative_path
		FROM files
		WHERE volume_uuid = ? AND uploaded_at IS NOT NULL
		ORDER BY mtime DESC, relative_path DESC
		LIMIT 1
	`, volumeUUID)

	var mtimeUnix int64
	var path string
	if err := row.Scan(&mtimeUnix, &path); err != nil {
		if err == sql.ErrNoRows {
			return VolumeSyncCursor{}, nil
		}
		return VolumeSyncCursor{}, err
	}

	_, err := s.db.Exec(`
		INSERT INTO volume_sync (volume_uuid, last_synced_mtime, last_synced_path)
		VALUES (?, ?, ?)
	`, volumeUUID, mtimeUnix, path)
	if err != nil {
		return VolumeSyncCursor{}, err
	}
	return VolumeSyncCursor{Mtime: time.Unix(mtimeUnix, 0), RelativePath: path}, nil
}

// EnsureVolumeSync saves an empty watermark for a volume that has none, so
// GetVolumeSync no longer rebuilds one from the files recorded as uploaded.
func (s *DB) EnsureVolumeSync(volumeUUID string) error {
	_, err := s.db.Exec(`
		INSERT INTO volume_sync (volume_uuid, last_synced_mtime, last_synced_path)
		VALUES (?, 0, '')
		ON CONFLICT(volume_uuid) DO NOTHING
	`, volumeUUID)
	return err
}

// SetVolumeSync saves the sync watermark when it moves forward in file order.
func (s *DB) SetVolumeSync(volumeUUID string, mtime time.Time, relativePath string) error {
	cur, found, err := s.SavedVolumeSync(volumeUUID)
	if err != nil {
		return err
	}
	if found && !isAfterWatermark(mtime, relativePath, cur) {
		return nil
	}

	_, err = s.db.Exec(`
		INSERT INTO volume_sync (volume_uuid, last_synced_mtime, last_synced_path)
		VALUES (?, ?, ?)
		ON CONFLICT(volume_uuid) DO UPDATE SET
			last_synced_mtime = excluded.last_synced_mtime,
			last_synced_path = excluded.last_synced_path
	`, volumeUUID, mtime.Unix(), relativePath)
	return err
}

func isAfterWatermark(mtime time.Time, path string, cur VolumeSyncCursor) bool {
	if cur.Mtime.IsZero() {
		return true
	}
	if mtime.After(cur.Mtime) {
		return true
	}
	if mtime.Equal(cur.Mtime) && path > cur.RelativePath {
		return true
	}
	return false
}

// Covers reports whether the cursor's watermark has passed a file with the
// given mtime and relative path, in the oldest-first (mtime, then path)
// order sync watermarks advance in. The zero cursor covers nothing.
func (c VolumeSyncCursor) Covers(mtime time.Time, relativePath string) bool {
	return !isAfterWatermark(mtime, relativePath, c)
}

// UploadedPaths returns the relative paths of a volume's files recorded as
// uploaded (in Immich), for quick membership checks.
func (s *DB) UploadedPaths(volumeUUID string) (map[string]struct{}, error) {
	rows, err := s.db.Query(`
		SELECT relative_path FROM files
		WHERE volume_uuid = ? AND uploaded_at IS NOT NULL
	`, volumeUUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	paths := make(map[string]struct{})
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		paths[path] = struct{}{}
	}
	return paths, rows.Err()
}
