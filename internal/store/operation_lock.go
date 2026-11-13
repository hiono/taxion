package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

// OperationLease uses a separate SQLite write reservation so workspace
// transactions remain available. Process exit releases the reservation.
type OperationLease struct {
	store *Store
	close func() error
	mu    sync.Mutex
}

func (s *Store) LockOperation() (*OperationLease, error) {
	var sequence int
	var name, path string
	if err := s.db.QueryRow("PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		return nil, err
	}
	if path == "" {
		if !s.operationMu.TryLock() {
			return nil, fmt.Errorf("another operation owns this workspace")
		}
		return &OperationLease{store: s, close: func() error { s.operationMu.Unlock(); return nil }}, nil
	}
	lock, err := sql.Open("sqlite", path+".operation-lock")
	if err != nil {
		return nil, err
	}
	connection, err := lock.Conn(context.Background())
	if err != nil {
		return nil, errors.Join(err, lock.Close())
	}
	if _, err := connection.ExecContext(context.Background(), "BEGIN EXCLUSIVE"); err != nil {
		return nil, errors.Join(fmt.Errorf("another operation owns this workspace: %w", err), connection.Close(), lock.Close())
	}
	return &OperationLease{store: s, close: func() error {
		_, err := connection.ExecContext(context.Background(), "ROLLBACK")
		return errors.Join(err, connection.Close(), lock.Close())
	}}, nil
}

func (l *OperationLease) Claim(request Operation, resume bool) (Operation, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.close == nil {
		return Operation{}, fmt.Errorf("operation lease is closed")
	}
	return l.store.claimOperation(request, resume)
}

func (l *OperationLease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.close == nil {
		return fmt.Errorf("operation lease is already closed")
	}
	close := l.close
	l.close = nil
	return close()
}
