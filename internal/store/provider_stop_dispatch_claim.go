package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// GetProviderStopDispatchClaim returns the durable claim currently fencing a
// Stop dispatch. A missing claim is distinct from a claim held by another call.
func (s *Store) GetProviderStopDispatchClaim(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, operationID string,
) (string, bool, error) {
	var claimID, storedJobID, storedExecutionID, storedSessionID string
	err := s.db.QueryRowContext(ctx,
		`SELECT claim_id, job_id, role_execution_id, session_id
			 FROM provider_stop_dispatch_claims WHERE operation_id = ?`,
		operationID,
	).Scan(&claimID, &storedJobID, &storedExecutionID, &storedSessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read Provider Stop dispatch claim: %w", err)
	}
	if storedJobID != jobID || storedExecutionID != roleExecutionID || storedSessionID != sessionID {
		return "", false, fmt.Errorf("%w: Provider Stop dispatch claim identity mismatch", ErrIntegrity)
	}
	return claimID, true, nil
}

// ClaimProviderStopDispatch acquires or compare-and-swaps a durable Stop
// dispatch claim. An empty previousClaimID permits only first insertion. A
// non-empty previousClaimID permits takeover only of the exact claim observed
// before a verified Provider NOT_FOUND lookup.
func (s *Store) ClaimProviderStopDispatch(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, operationID, previousClaimID, claimID string,
) (bool, error) {
	if jobID == "" || roleExecutionID == "" || sessionID == "" ||
		operationID == "" || claimID == "" || claimID == previousClaimID {
		return false, errors.New("Provider Stop dispatch claim identity is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin Provider Stop dispatch claim: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Round(0).Format(time.RFC3339Nano)
	var result sql.Result
	if previousClaimID == "" {
		result, err = tx.ExecContext(ctx,
			`INSERT INTO provider_stop_dispatch_claims
				(operation_id, job_id, role_execution_id, session_id, claim_id, updated_at)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT(operation_id) DO NOTHING`,
			jobID, roleExecutionID, sessionID, operationID, claimID, now,
		)
	} else {
		result, err = tx.ExecContext(ctx,
			`UPDATE provider_stop_dispatch_claims
				SET claim_id = ?, updated_at = ?
				WHERE operation_id = ? AND job_id = ? AND role_execution_id = ?
				AND session_id = ? AND claim_id = ?`,
			claimID, now, operationID, jobID, roleExecutionID, sessionID, previousClaimID,
		)
	}
	if err != nil {
		return false, fmt.Errorf("write Provider Stop dispatch claim: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("check Provider Stop dispatch claim: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit Provider Stop dispatch claim: %w", err)
	}
	return affected == 1, nil
}

// ReleaseProviderStopDispatchClaim releases only the claim still owned by the
// caller. A stale caller cannot release a newer recovery attempt's claim.
func (s *Store) ReleaseProviderStopDispatchClaim(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, operationID, claimID string,
) error {
	if jobID == "" || roleExecutionID == "" || sessionID == "" || operationID == "" || claimID == "" {
		return errors.New("Provider Stop dispatch claim identity is required")
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM provider_stop_dispatch_claims
			 WHERE operation_id = ? AND job_id = ? AND role_execution_id = ?
			 AND session_id = ? AND claim_id = ?`,
		operationID, jobID, roleExecutionID, sessionID, claimID,
	)
	if err != nil {
		return fmt.Errorf("release Provider Stop dispatch claim: %w", err)
	}
	return nil
}
