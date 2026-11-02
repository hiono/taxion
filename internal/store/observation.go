package store

import (
	"context"
	"database/sql"
)

// ObserveWorkspace reuses a verified snapshot only while its transaction revision
// is unchanged. The callback must not mutate or retain the snapshot.
func (s *Store) ObserveWorkspace(observe func(Workspace) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision int64
	if err := tx.QueryRow(`SELECT revision FROM workspace_state WHERE id=1`).Scan(&revision); err != nil {
		return err
	}
	if s.observation == nil || s.observationRevision != revision {
		state, err := s.loadWorkspace(tx)
		if err != nil {
			return err
		}
		s.observation = &state
		s.observationRevision = revision
	}
	if err := observe(*s.observation); err != nil {
		return err
	}
	return tx.Commit()
}
