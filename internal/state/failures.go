package state

// FileFailure is one file that could not be synced during a run, kept so the
// failure list can be shown after a run that had errors.
type FileFailure struct {
	RelativePath string `json:"relativePath"`
	Kind         string `json:"kind"`
	Detail       string `json:"detail"`
}

// RecordFailures stores the per-file failures for a run and marks the run's
// failure list as recorded, even when failures is empty: callers (and
// RunFailures) can then tell "recorded, zero failures" apart from "never
// recorded". Runs in one transaction.
func (s *DB) RecordFailures(runID int64, failures []FileFailure) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO sync_run_failures (run_id, relative_path, kind, detail)
		VALUES (?, ?, ?, ?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, f := range failures {
		if _, err := stmt.Exec(runID, f.RelativePath, f.Kind, f.Detail); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`UPDATE sync_runs SET failures_recorded = 1 WHERE id = ?`, runID); err != nil {
		return err
	}

	return tx.Commit()
}

// RunFailures returns the per-file failures recorded for a run, in the
// order they were inserted.
func (s *DB) RunFailures(runID int64) ([]FileFailure, error) {
	rows, err := s.db.Query(`
		SELECT relative_path, kind, detail
		FROM sync_run_failures
		WHERE run_id = ?
		ORDER BY id ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var failures []FileFailure
	for rows.Next() {
		var f FileFailure
		if err := rows.Scan(&f.RelativePath, &f.Kind, &f.Detail); err != nil {
			return nil, err
		}
		failures = append(failures, f)
	}
	return failures, rows.Err()
}
