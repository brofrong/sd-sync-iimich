package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SyncRun is one recorded sync attempt, from the moment the target volume
// was found mounted and a sync started, to the moment it finished (success,
// error, or cancellation). FinishedAt and the result/counter fields are
// zero/empty until FinishRun is called.
type SyncRun struct {
	ID           int64             `json:"id"`
	StartedAt    time.Time         `json:"startedAt"`
	FinishedAt   *time.Time        `json:"finishedAt"`
	VolumeUUID   string            `json:"volumeUUID"`
	VolumeName   string            `json:"volumeName"`
	ResultCode   string            `json:"resultCode"`
	ResultParams map[string]string `json:"resultParams"`
	Uploaded     int               `json:"uploaded"`
	Skipped      int               `json:"skipped"`
	Errors       int               `json:"errors"`
	Duplicates   int               `json:"duplicates"`
	Bytes        int64             `json:"bytes"`
	ErrorSummary map[string]int    `json:"errorSummary"`
	// FailuresRecorded reports whether RecordFailures has been called for
	// this run (even when the failure list was empty). RunFailures returns
	// no rows for a run where this is false.
	FailuresRecorded bool `json:"failuresRecorded"`
}

// StartRun records the start of a sync attempt and returns its ID, to be
// passed back to FinishRun once the attempt completes.
func (s *DB) StartRun(volumeUUID, volumeName string, at time.Time) (int64, error) {
	res, err := s.db.Exec(`
		INSERT INTO sync_runs (started_at, volume_uuid, volume_name)
		VALUES (?, ?, ?)
	`, at.Unix(), volumeUUID, volumeName)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishRun records the outcome of a sync attempt started with StartRun.
// run.ID selects the row; run.FinishedAt must be set.
func (s *DB) FinishRun(run SyncRun) error {
	if run.FinishedAt == nil {
		return fmt.Errorf("state: FinishRun requires FinishedAt")
	}
	resultParams, err := marshalJSONMap(run.ResultParams)
	if err != nil {
		return err
	}
	errorSummary, err := marshalJSONIntMap(run.ErrorSummary)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		UPDATE sync_runs SET
			finished_at = ?,
			result_code = ?,
			result_params = ?,
			uploaded = ?,
			skipped = ?,
			errors = ?,
			duplicates = ?,
			bytes = ?,
			error_summary = ?
		WHERE id = ?
	`, run.FinishedAt.Unix(), run.ResultCode, resultParams,
		run.Uploaded, run.Skipped, run.Errors, run.Duplicates, run.Bytes, errorSummary,
		run.ID)
	return err
}

// RecentRuns returns up to limit sync runs, newest (by start time) first.
func (s *DB) RecentRuns(limit int) ([]SyncRun, error) {
	rows, err := s.db.Query(`
		SELECT id, started_at, finished_at, volume_uuid, volume_name,
			result_code, result_params, uploaded, skipped, errors, duplicates, bytes, error_summary,
			failures_recorded
		FROM sync_runs
		ORDER BY started_at DESC, id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []SyncRun
	for rows.Next() {
		run, err := scanSyncRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

// LastFinishedRun returns the most recently started run that has finished,
// or nil if there is none (no runs at all, or the only runs are still in
// progress).
func (s *DB) LastFinishedRun() (*SyncRun, error) {
	row := s.db.QueryRow(`
		SELECT id, started_at, finished_at, volume_uuid, volume_name,
			result_code, result_params, uploaded, skipped, errors, duplicates, bytes, error_summary,
			failures_recorded
		FROM sync_runs
		WHERE finished_at IS NOT NULL
		ORDER BY started_at DESC, id DESC
		LIMIT 1
	`)
	run, err := scanSyncRun(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanSyncRun(r rowScanner) (SyncRun, error) {
	var run SyncRun
	var startedAtUnix int64
	var finishedAtUnix sql.NullInt64
	var resultParams, errorSummary string
	var failuresRecorded int
	if err := r.Scan(
		&run.ID, &startedAtUnix, &finishedAtUnix, &run.VolumeUUID, &run.VolumeName,
		&run.ResultCode, &resultParams, &run.Uploaded, &run.Skipped, &run.Errors,
		&run.Duplicates, &run.Bytes, &errorSummary, &failuresRecorded,
	); err != nil {
		return SyncRun{}, err
	}
	run.FailuresRecorded = failuresRecorded != 0

	run.StartedAt = time.Unix(startedAtUnix, 0)
	if finishedAtUnix.Valid {
		t := time.Unix(finishedAtUnix.Int64, 0)
		run.FinishedAt = &t
	}

	params, err := unmarshalJSONMap(resultParams)
	if err != nil {
		return SyncRun{}, err
	}
	run.ResultParams = params

	summary, err := unmarshalJSONIntMap(errorSummary)
	if err != nil {
		return SyncRun{}, err
	}
	run.ErrorSummary = summary

	return run, nil
}

func marshalJSONMap(m map[string]string) (string, error) {
	if m == nil {
		m = map[string]string{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func marshalJSONIntMap(m map[string]int) (string, error) {
	if m == nil {
		m = map[string]int{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func unmarshalJSONMap(s string) (map[string]string, error) {
	m := map[string]string{}
	if s == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

func unmarshalJSONIntMap(s string) (map[string]int, error) {
	m := map[string]int{}
	if s == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}
