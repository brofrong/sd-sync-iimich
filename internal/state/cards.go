package state

import "fmt"

// RememberedCard is a card the database keeps sync state for: which of its
// files were uploaded and where its sync stopped.
type RememberedCard struct {
	UUID string
	// Name is the volume name of the card's newest recorded sync run; ""
	// when it was only synced by versions that kept no history.
	Name string
	// Uploaded is the number of its files recorded as uploaded.
	Uploaded int
}

// RememberedCards lists the cards with sync state, ordered by UUID. Cards
// with only sync history are not listed: there is nothing to forget.
func (s *DB) RememberedCards() ([]RememberedCard, error) {
	rows, err := s.db.Query(`
		SELECT c.uuid,
			COALESCE((
				SELECT r.volume_name FROM sync_runs r
				WHERE r.volume_uuid = c.uuid AND r.volume_name != ''
				ORDER BY r.started_at DESC, r.id DESC LIMIT 1
			), ''),
			(SELECT COUNT(*) FROM files f WHERE f.volume_uuid = c.uuid AND f.uploaded_at IS NOT NULL)
		FROM (
			SELECT volume_uuid AS uuid FROM files
			UNION
			SELECT volume_uuid FROM volume_sync
		) c
		ORDER BY c.uuid
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cards []RememberedCard
	for rows.Next() {
		var c RememberedCard
		if err := rows.Scan(&c.UUID, &c.Name, &c.Uploaded); err != nil {
			return nil, err
		}
		cards = append(cards, c)
	}
	return cards, rows.Err()
}

// ForgetCard deletes the sync state of the card with uuid, so its next sync
// checks every file again (Immich recognizes the ones it already has). The
// sync history is kept.
func (s *DB) ForgetCard(uuid string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM files WHERE volume_uuid = ?`, uuid); err != nil {
		return fmt.Errorf("forget files: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM volume_sync WHERE volume_uuid = ?`, uuid); err != nil {
		return fmt.Errorf("forget sync cursor: %w", err)
	}
	return tx.Commit()
}
