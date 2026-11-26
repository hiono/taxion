package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

const addJobQuestionAuthorizationCommandType = "JOB_QUESTION_AUTHORIZATION"

// GetJobQuestionAuthorizationCommand returns the persisted grant and receipt for a command.
// It lets the Controller replay a committed request before resolving external context.
func (s *Store) GetJobQuestionAuthorizationCommand(
	ctx context.Context,
	jobID, commandID string,
) (CommandReceipt, domain.JobQuestionAuthorization, bool, error) {
	if ctx == nil || jobID == "" || commandID == "" {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, errors.New("Job and command identities are required")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, fmt.Errorf("begin question authorization receipt lookup: %w", err)
	}
	defer tx.Rollback()

	var commandType, storedJobID, payloadHash, receiptJSON, createdAtText string
	var resultingRevision int64
	err = tx.QueryRowContext(ctx,
		"SELECT command_type, job_id, payload_sha256, receipt_json, created_at, resulting_revision FROM job_commands WHERE command_id = ?",
		commandID,
	).Scan(&commandType, &storedJobID, &payloadHash, &receiptJSON, &createdAtText, &resultingRevision)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.Commit(); err != nil {
			return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, fmt.Errorf("commit question authorization receipt lookup: %w", err)
		}
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, nil
	}
	if err != nil {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, fmt.Errorf("read question authorization receipt: %w", err)
	}
	if commandType != addJobQuestionAuthorizationCommandType || storedJobID != jobID || resultingRevision <= 0 {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, ErrIdempotencyConflict
	}
	var receipt CommandReceipt
	if err := json.Unmarshal([]byte(receiptJSON), &receipt); err != nil {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, fmt.Errorf("%w: decode question authorization receipt: %v", ErrIntegrity, err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, createdAtText)
	if err != nil {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, fmt.Errorf("%w: invalid question authorization receipt time: %v", ErrIntegrity, err)
	}
	if receipt.CommandID != commandID || receipt.JobID != jobID || receipt.Revision != uint64(resultingRevision) ||
		receipt.PayloadSHA256 != payloadHash || !receipt.CreatedAt.Equal(createdAt) {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, fmt.Errorf("%w: question authorization receipt metadata mismatch", ErrIntegrity)
	}
	job, err := readJobAggregateTx(ctx, tx, jobID)
	if err != nil {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, err
	}
	var authorization domain.JobQuestionAuthorization
	found := false
	for _, candidate := range job.JobQuestionAuthorizations {
		if candidate.CommandID == commandID {
			authorization, found = candidate, true
			break
		}
	}
	if !found {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, fmt.Errorf("%w: question authorization fact is missing", ErrIntegrity)
	}
	if err := authorization.Validate(job); err != nil {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, fmt.Errorf("%w: persisted question authorization is invalid: %v", ErrIntegrity, err)
	}
	if receipt.Revision != authorization.CreatedRevision ||
		!receipt.CreatedAt.Equal(authorization.CreatedAt) {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, fmt.Errorf("%w: question authorization fact does not match its command receipt", ErrIntegrity)
	}
	if err := tx.Commit(); err != nil {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, false, fmt.Errorf("commit question authorization receipt lookup: %w", err)
	}
	return receipt, authorization, true, nil
}

func (s *Store) AddJobQuestionAuthorization(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	request domain.JobQuestionAuthorizationRequest,
) (CommandReceipt, domain.JobQuestionAuthorization, error) {
	receipt, authorization, err := persistPlanningFact(
		ctx, s, jobID, expectedRevision, request.CommandID, addJobQuestionAuthorizationCommandType, request,
		func(job domain.Job, now time.Time) (domain.JobQuestionAuthorization, error) {
			return domain.NewJobQuestionAuthorization(job, request, now)
		},
		func(job *domain.Job, authorization domain.JobQuestionAuthorization) {
			job.JobQuestionAuthorizations = append(job.JobQuestionAuthorizations, authorization)
		},
		func(job domain.Job) (domain.JobQuestionAuthorization, bool) {
			for _, authorization := range job.JobQuestionAuthorizations {
				if authorization.CommandID == request.CommandID {
					return authorization, true
				}
			}
			return domain.JobQuestionAuthorization{}, false
		},
	)
	if err != nil {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, err
	}
	if receipt.Revision != authorization.CreatedRevision ||
		!receipt.CreatedAt.Equal(authorization.CreatedAt) {
		return CommandReceipt{}, domain.JobQuestionAuthorization{}, fmt.Errorf("%w: question authorization fact does not match its command receipt", ErrIntegrity)
	}
	return receipt, authorization, nil
}
