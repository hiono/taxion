package store

import (
	"fmt"
)

// InstallRestoreRecord records a new placement in an offline archive copy.
// Live workspaces use UpdateWorkspace, which keeps this receipt immutable.
func (s *Store) InstallRestoreRecord(record *RestoreRecord) error {
	if record == nil {
		return fmt.Errorf("restore requires a placement receipt")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := s.loadWorkspace(tx)
	if err != nil {
		return err
	}
	if state.Records.Restore != nil {
		state.Records.Restores = append(state.Records.Restores, *state.Records.Restore)
	}
	state.Records.Restore = record
	if err := state.Records.validate(); err != nil {
		return err
	}
	if err := saveWorkspaceDocuments(tx, state); err != nil {
		return err
	}
	return tx.Commit()
}
