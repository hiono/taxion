package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
)

const (
	addProviderStopIntentCommandType       = "AddProviderStopIntent"
	addProviderStopObservationCommandType  = "AddProviderStopObservation"
	addProviderEventObservationCommandType = "AddProviderEventObservation"
	addProviderStartObservationCommandType = "AddProviderStartObservation"
	addPlanningTurnIntentCommandType       = "AddPlanningTurnIntent"
	addPlanningTurnObservationCommandType  = "AddPlanningTurnObservation"
	addPlanningEffectDecisionCommandType   = "AddPlanningEffectDecision"
	addPlanningResumeIntentCommandType     = "AddPlanningResumeIntent"
)

func (s *Store) AddPlanningTurnIntent(ctx context.Context, jobID string, expectedRevision uint64, roleExecutionID, providerSessionID, commandID, promptSHA256 string, reservedTokens uint64, principal auth.AuthenticatedPrincipal) (CommandReceipt, domain.PlanningTurnIntent, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.PlanningTurnIntent{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, RoleExecutionID, ProviderSessionID, CommandID, PromptSHA256 string
		ReservedTokens                                                     uint64
		PrincipalID                                                        string
	}{jobID, roleExecutionID, providerSessionID, commandID, promptSHA256, reservedTokens, principal.PrincipalID()}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addPlanningTurnIntentCommandType, request,
		func(job domain.Job, now time.Time) (domain.PlanningTurnIntent, error) {
			return domain.NewPlanningTurnIntent(job, roleExecutionID, principal.PrincipalID(), providerSessionID, commandID, promptSHA256, reservedTokens, now)
		},
		func(job *domain.Job, fact domain.PlanningTurnIntent) {
			job.PlanningTurnIntents = append(job.PlanningTurnIntents, fact)
		},
		func(job domain.Job) (domain.PlanningTurnIntent, bool) {
			for _, fact := range job.PlanningTurnIntents {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.PlanningTurnIntent{}, false
		})
}

func (s *Store) AddPlanningTurnObservation(ctx context.Context, jobID string, expectedRevision uint64, turnID, commandID, status string, tokensUsed uint64, evidenceSHA256 string, reconcilesUnknown bool, principal auth.AuthenticatedPrincipal) (CommandReceipt, domain.PlanningTurnObservation, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.PlanningTurnObservation{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, TurnID, CommandID, Status, EvidenceSHA256 string
		TokensUsed                                       uint64
		ReconcilesUnknown                                bool
		PrincipalID                                      string
	}{jobID, turnID, commandID, status, evidenceSHA256, tokensUsed, reconcilesUnknown, principal.PrincipalID()}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addPlanningTurnObservationCommandType, request,
		func(job domain.Job, now time.Time) (domain.PlanningTurnObservation, error) {
			return domain.NewPlanningTurnObservation(job, turnID, principal.PrincipalID(), commandID, status, tokensUsed, evidenceSHA256, reconcilesUnknown, now)
		},
		func(job *domain.Job, fact domain.PlanningTurnObservation) {
			job.PlanningTurnObservations = append(job.PlanningTurnObservations, fact)
		},
		func(job domain.Job) (domain.PlanningTurnObservation, bool) {
			for _, fact := range job.PlanningTurnObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.PlanningTurnObservation{}, false
		})
}

func (s *Store) AddPlanningEffectDecision(ctx context.Context, jobID string, expectedRevision uint64, roleExecutionID, commandID, effect, contextSHA256 string, principal auth.AuthenticatedPrincipal) (CommandReceipt, domain.PlanningEffectDecision, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.PlanningEffectDecision{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, RoleExecutionID, CommandID, Effect, ContextSHA256 string
		PrincipalID                                              string
	}{jobID, roleExecutionID, commandID, effect, contextSHA256, principal.PrincipalID()}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addPlanningEffectDecisionCommandType, request,
		func(job domain.Job, now time.Time) (domain.PlanningEffectDecision, error) {
			return domain.NewPlanningEffectDecision(job, roleExecutionID, principal.PrincipalID(), commandID, effect, contextSHA256, now)
		},
		func(job *domain.Job, fact domain.PlanningEffectDecision) {
			job.PlanningEffectDecisions = append(job.PlanningEffectDecisions, fact)
		},
		func(job domain.Job) (domain.PlanningEffectDecision, bool) {
			for _, fact := range job.PlanningEffectDecisions {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.PlanningEffectDecision{}, false
		})
}

func (s *Store) AddPlanningResumeIntent(ctx context.Context, jobID string, expectedRevision uint64, roleExecutionID, providerSessionID, commandID, capabilityEvidenceSHA256 string, reattachmentSupported bool, principal auth.AuthenticatedPrincipal) (CommandReceipt, domain.PlanningResumeIntent, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.PlanningResumeIntent{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, RoleExecutionID, ProviderSessionID, CommandID, CapabilityEvidenceSHA256 string
		ReattachmentSupported                                                          bool
		PrincipalID                                                                    string
	}{jobID, roleExecutionID, providerSessionID, commandID, capabilityEvidenceSHA256, reattachmentSupported, principal.PrincipalID()}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addPlanningResumeIntentCommandType, request,
		func(job domain.Job, now time.Time) (domain.PlanningResumeIntent, error) {
			return domain.NewPlanningResumeIntent(job, roleExecutionID, principal.PrincipalID(), providerSessionID, commandID, capabilityEvidenceSHA256, reattachmentSupported, now)
		},
		func(job *domain.Job, fact domain.PlanningResumeIntent) {
			job.PlanningResumeIntents = append(job.PlanningResumeIntents, fact)
		},
		func(job domain.Job) (domain.PlanningResumeIntent, bool) {
			for _, fact := range job.PlanningResumeIntents {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.PlanningResumeIntent{}, false
		})
}

func persistPlanningFact[T any](ctx context.Context, s *Store, jobID string, expectedRevision uint64, commandID, commandType string, request any,
	create func(domain.Job, time.Time) (T, error), appendFact func(*domain.Job, T), findFact func(domain.Job) (T, bool)) (CommandReceipt, T, error) {
	var zero T
	if jobID == "" || expectedRevision == 0 || commandID == "" {
		return CommandReceipt{}, zero, errors.New("Job, expected revision, and command identity are required")
	}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return CommandReceipt{}, zero, fmt.Errorf("encode planning lifecycle command: %w", err)
	}
	requestHash := digest(requestJSON)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandReceipt{}, zero, fmt.Errorf("begin planning lifecycle command: %w", err)
	}
	defer tx.Rollback()

	var existingType, existingJobID, existingHash, receiptJSON, createdAtText string
	var resultingRevision int64
	err = tx.QueryRowContext(ctx, "SELECT command_type, job_id, payload_sha256, receipt_json, created_at, resulting_revision FROM job_commands WHERE command_id = ?", commandID).
		Scan(&existingType, &existingJobID, &existingHash, &receiptJSON, &createdAtText, &resultingRevision)
	if err == nil {
		if existingType != commandType || existingJobID != jobID || existingHash != requestHash {
			return CommandReceipt{}, zero, ErrIdempotencyConflict
		}
		var receipt CommandReceipt
		if err := json.Unmarshal([]byte(receiptJSON), &receipt); err != nil {
			return CommandReceipt{}, zero, fmt.Errorf("%w: decode planning lifecycle receipt: %v", ErrIntegrity, err)
		}
		createdAt, err := time.Parse(time.RFC3339Nano, createdAtText)
		if err != nil {
			return CommandReceipt{}, zero, fmt.Errorf("%w: invalid planning lifecycle receipt time: %v", ErrIntegrity, err)
		}
		if receipt.CommandID != commandID || receipt.JobID != jobID || receipt.Revision != uint64(resultingRevision) || receipt.PayloadSHA256 != requestHash || !receipt.CreatedAt.Equal(createdAt) {
			return CommandReceipt{}, zero, fmt.Errorf("%w: planning lifecycle receipt metadata mismatch", ErrIntegrity)
		}
		job, err := readJobAggregateTx(ctx, tx, jobID)
		if err != nil {
			return CommandReceipt{}, zero, err
		}
		fact, found := findFact(job)
		if !found {
			return CommandReceipt{}, zero, fmt.Errorf("%w: persisted planning lifecycle fact is missing", ErrIntegrity)
		}
		if err := tx.Commit(); err != nil {
			return CommandReceipt{}, zero, fmt.Errorf("commit planning lifecycle retry: %w", err)
		}
		return receipt, fact, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommandReceipt{}, zero, fmt.Errorf("read planning lifecycle receipt: %w", err)
	}

	job, err := readJobAggregateTx(ctx, tx, jobID)
	if err != nil {
		return CommandReceipt{}, zero, err
	}
	if job.Revision != expectedRevision {
		return CommandReceipt{}, zero, ErrIdempotencyConflict
	}
	now := time.Now().UTC().Round(0)
	if now.Before(job.UpdatedAt) {
		return CommandReceipt{}, zero, errors.New("planning lifecycle command time precedes current Job revision")
	}
	fact, err := create(job, now)
	if err != nil {
		return CommandReceipt{}, zero, err
	}
	appendFact(&job, fact)
	if job.Revision >= uint64(^uint64(0)>>1) {
		return CommandReceipt{}, zero, errors.New("Job revision exhausted")
	}
	job.Revision++
	job.UpdatedAt = now
	if err := job.Validate(); err != nil {
		return CommandReceipt{}, zero, fmt.Errorf("validate Job with planning lifecycle fact: %w", err)
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return CommandReceipt{}, zero, fmt.Errorf("encode Job with planning lifecycle fact: %w", err)
	}
	aggregateHash := digest(payload)
	receipt := CommandReceipt{CommandID: commandID, JobID: jobID, Revision: job.Revision, PayloadSHA256: requestHash, CreatedAt: now}
	encodedReceipt, err := json.Marshal(receipt)
	if err != nil {
		return CommandReceipt{}, zero, fmt.Errorf("encode planning lifecycle receipt: %w", err)
	}
	result, err := tx.ExecContext(ctx, "UPDATE job_aggregates SET revision = ?, payload_json = ?, integrity_sha256 = ?, updated_at = ? WHERE job_id = ? AND revision = ?", job.Revision, string(payload), aggregateHash, now.Format(time.RFC3339Nano), jobID, expectedRevision)
	if err != nil {
		return CommandReceipt{}, zero, fmt.Errorf("update Job with planning lifecycle fact: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CommandReceipt{}, zero, fmt.Errorf("check planning lifecycle Job update: %w", err)
	}
	if affected != 1 {
		return CommandReceipt{}, zero, ErrIdempotencyConflict
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO job_commands (command_id, job_id, command_type, payload_sha256, resulting_revision, receipt_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)", commandID, jobID, commandType, requestHash, job.Revision, string(encodedReceipt), now.Format(time.RFC3339Nano))
	if err != nil {
		return CommandReceipt{}, zero, fmt.Errorf("insert planning lifecycle receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommandReceipt{}, zero, fmt.Errorf("commit planning lifecycle fact: %w", err)
	}
	return receipt, fact, nil
}

func readJobAggregateTx(ctx context.Context, tx *sql.Tx, jobID string) (domain.Job, error) {
	var revision int64
	var payload, expectedHash, createdAt, updatedAt string
	err := tx.QueryRowContext(ctx, "SELECT revision, payload_json, integrity_sha256, created_at, updated_at FROM job_aggregates WHERE job_id = ?", jobID).
		Scan(&revision, &payload, &expectedHash, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Job{}, ErrNotFound
	}
	if err != nil {
		return domain.Job{}, fmt.Errorf("read Job aggregate for planning lifecycle: %w", err)
	}
	return decodeJobAggregate(jobID, revision, payload, expectedHash, createdAt, updatedAt)
}

func (s *Store) AddPlanningBoundaryDecision(ctx context.Context, jobID string, expectedRevision uint64, roleExecutionID, sourceCommandID, boundary, reasonCode string, principal auth.AuthenticatedPrincipal) (CommandReceipt, domain.PlanningBoundaryDecision, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.PlanningBoundaryDecision{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	commandID := domain.PlanningBoundaryDecisionCommandID(sourceCommandID, boundary)
	request := struct {
		JobID, RoleExecutionID, SourceCommandID, Boundary, ReasonCode string
		PrincipalID                                                   string
	}{jobID, roleExecutionID, sourceCommandID, boundary, reasonCode, principal.PrincipalID()}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, "AddPlanningBoundaryDecision", request,
		func(job domain.Job, now time.Time) (domain.PlanningBoundaryDecision, error) {
			return domain.NewPlanningBoundaryDecision(job, roleExecutionID, principal.PrincipalID(), sourceCommandID, boundary, reasonCode, now)
		},
		func(job *domain.Job, fact domain.PlanningBoundaryDecision) {
			job.PlanningBoundaryDecisions = append(job.PlanningBoundaryDecisions, fact)
		},
		func(job domain.Job) (domain.PlanningBoundaryDecision, bool) {
			for _, fact := range job.PlanningBoundaryDecisions {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.PlanningBoundaryDecision{}, false
		})
}

func (s *Store) AddPlanningResumeObservation(ctx context.Context, jobID string, expectedRevision uint64, resumeID, commandID, status, evidenceSHA256 string, reconcilesUnknown bool, principal auth.AuthenticatedPrincipal) (CommandReceipt, domain.PlanningResumeObservation, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.PlanningResumeObservation{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, ResumeID, CommandID, Status, EvidenceSHA256 string
		ReconcilesUnknown                                  bool
		PrincipalID                                        string
	}{jobID, resumeID, commandID, status, evidenceSHA256, reconcilesUnknown, principal.PrincipalID()}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, "AddPlanningResumeObservation", request,
		func(job domain.Job, now time.Time) (domain.PlanningResumeObservation, error) {
			return domain.NewPlanningResumeObservation(job, resumeID, principal.PrincipalID(), commandID, status, evidenceSHA256, reconcilesUnknown, now)
		},
		func(job *domain.Job, fact domain.PlanningResumeObservation) {
			job.PlanningResumeObservations = append(job.PlanningResumeObservations, fact)
		},
		func(job domain.Job) (domain.PlanningResumeObservation, bool) {
			for _, fact := range job.PlanningResumeObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.PlanningResumeObservation{}, false
		})
}

func (s *Store) AddProviderStopIntent(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	roleExecutionID, sessionID, commandID string,
	reason domain.ProviderStopReason,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderStopIntent, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.ProviderStopIntent{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, RoleExecutionID, SessionID, CommandID string
		Reason                                       domain.ProviderStopReason
		PrincipalID                                  string
	}{jobID, roleExecutionID, sessionID, commandID, reason, principal.PrincipalID()}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addProviderStopIntentCommandType, request,
		func(job domain.Job, now time.Time) (domain.ProviderStopIntent, error) {
			return domain.NewProviderStopIntent(job, roleExecutionID, principal.PrincipalID(), sessionID, commandID, reason, now)
		},
		func(job *domain.Job, fact domain.ProviderStopIntent) {
			job.ProviderStopIntents = append(job.ProviderStopIntents, fact)
		},
		func(job domain.Job) (domain.ProviderStopIntent, bool) {
			for _, fact := range job.ProviderStopIntents {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.ProviderStopIntent{}, false
		})
}

func (s *Store) AddProviderStopObservation(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	stopIntentID, commandID string,
	stopped, quiescent bool,
	evidenceSHA256 string,
	reconcilesUnknown bool,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderStopObservation, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.ProviderStopObservation{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, StopIntentID, CommandID string
		Stopped, Quiescent             bool
		EvidenceSHA256                 string
		ReconcilesUnknown              bool
		PrincipalID                    string
	}{jobID, stopIntentID, commandID, stopped, quiescent, evidenceSHA256, reconcilesUnknown, principal.PrincipalID()}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addProviderStopObservationCommandType, request,
		func(job domain.Job, now time.Time) (domain.ProviderStopObservation, error) {
			return domain.NewProviderStopObservation(job, stopIntentID, principal.PrincipalID(), commandID,
				stopped, quiescent, evidenceSHA256, reconcilesUnknown, now)
		},
		func(job *domain.Job, fact domain.ProviderStopObservation) {
			job.ProviderStopObservations = append(job.ProviderStopObservations, fact)
		},
		func(job domain.Job) (domain.ProviderStopObservation, bool) {
			for _, fact := range job.ProviderStopObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.ProviderStopObservation{}, false
		})
}

func (s *Store) AddProviderEventObservation(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	roleExecutionID, operationID, turnID, sessionID, eventID, kind,
	previousCursor, cursor, evidenceSHA256 string,
	observedAt time.Time,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderEventObservation, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.ProviderEventObservation{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	commandID := domain.ProviderEventObservationCommandID(sessionID, eventID)
	request := struct {
		JobID, RoleExecutionID, OperationID, TurnID, SessionID, EventID, Kind string
		PreviousCursor, Cursor, EvidenceSHA256                                string
		ObservedAt                                                            time.Time
		PrincipalID                                                           string
	}{
		jobID, roleExecutionID, operationID, turnID, sessionID, eventID, kind,
		previousCursor, cursor, evidenceSHA256, observedAt.UTC().Round(0), principal.PrincipalID(),
	}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addProviderEventObservationCommandType, request,
		func(job domain.Job, _ time.Time) (domain.ProviderEventObservation, error) {
			return domain.NewProviderEventObservation(
				job, roleExecutionID, principal.PrincipalID(), operationID, turnID, sessionID,
				eventID, domain.ProviderEventKind(kind), previousCursor, cursor, evidenceSHA256,
				observedAt, commandID,
			)
		},
		func(job *domain.Job, fact domain.ProviderEventObservation) {
			job.ProviderEventObservations = append(job.ProviderEventObservations, fact)
		},
		func(job domain.Job) (domain.ProviderEventObservation, bool) {
			for _, fact := range job.ProviderEventObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.ProviderEventObservation{}, false
		})
}

func (s *Store) AddProviderStartObservation(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	operationID, commandID, status, providerSessionID, receiptID, evidenceSHA256 string,
	reconcilesUnknown bool,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderStartObservation, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.ProviderStartObservation{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, OperationID, CommandID, Status, ProviderSessionID, ReceiptID, EvidenceSHA256 string
		ReconcilesUnknown                                                                   bool
		PrincipalID                                                                         string
	}{
		jobID, operationID, commandID, status, providerSessionID, receiptID, evidenceSHA256,
		reconcilesUnknown, principal.PrincipalID(),
	}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addProviderStartObservationCommandType, request,
		func(job domain.Job, now time.Time) (domain.ProviderStartObservation, error) {
			return domain.NewProviderStartObservation(
				job, operationID, principal.PrincipalID(), commandID, status,
				providerSessionID, receiptID, evidenceSHA256, reconcilesUnknown, now,
			)
		},
		func(job *domain.Job, fact domain.ProviderStartObservation) {
			if job.ProviderLifecycleEnforcedFromRevision == 0 {
				job.ProviderLifecycleEnforcedFromRevision = fact.CreatedRevision
			}
			job.ProviderStartObservations = append(job.ProviderStartObservations, fact)
		},
		func(job domain.Job) (domain.ProviderStartObservation, bool) {
			for _, fact := range job.ProviderStartObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.ProviderStartObservation{}, false
		})
}

func (s *Store) AddTaskProviderStartObservation(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	operationID, commandID, status, providerSessionID, receiptID, evidenceSHA256 string,
	reconcilesUnknown bool,
) (CommandReceipt, domain.ProviderStartObservation, error) {
	request := struct {
		JobID              string
		OperationID        string
		CommandID          string
		Status             string
		ProviderSessionID  string
		ReceiptID          string
		EvidenceSHA256     string
		ReconcilesUnknown bool
	}{
		JobID: jobID, OperationID: operationID, CommandID: commandID, Status: status,
		ProviderSessionID: providerSessionID, ReceiptID: receiptID, EvidenceSHA256: evidenceSHA256,
		ReconcilesUnknown: reconcilesUnknown,
	}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addProviderStartObservationCommandType, request,
		func(job domain.Job, now time.Time) (domain.ProviderStartObservation, error) {
			return domain.NewProviderStartObservation(
				job, operationID, "", commandID, status,
				providerSessionID, receiptID, evidenceSHA256, reconcilesUnknown, now,
			)
		},
		func(job *domain.Job, fact domain.ProviderStartObservation) {
			if job.ProviderLifecycleEnforcedFromRevision == 0 {
				job.ProviderLifecycleEnforcedFromRevision = fact.CreatedRevision
			}
			job.ProviderStartObservations = append(job.ProviderStartObservations, fact)
		},
		func(job domain.Job) (domain.ProviderStartObservation, bool) {
			for _, fact := range job.ProviderStartObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.ProviderStartObservation{}, false
		})
}

func (s *Store) AddProviderResultObservation(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	roleExecutionID, operationID, turnID, sessionID, content, contentSHA256 string,
	final bool,
	evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderResultObservation, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.ProviderResultObservation{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	commandID := domain.ProviderResultObservationCommandID(operationID, turnID, contentSHA256, evidenceSHA256)
	request := struct {
		JobID, RoleExecutionID, OperationID, TurnID, SessionID string
		Content, ContentSHA256                                 string
		Final                                                  bool
		EvidenceSHA256, PrincipalID                            string
	}{
		jobID, roleExecutionID, operationID, turnID, sessionID,
		content, contentSHA256, final, evidenceSHA256, principal.PrincipalID(),
	}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addProviderResultObservationCommandType, request,
		func(job domain.Job, now time.Time) (domain.ProviderResultObservation, error) {
			return domain.NewProviderResultObservation(
				job, roleExecutionID, principal.PrincipalID(), operationID, turnID, sessionID,
				content, contentSHA256, final, evidenceSHA256, now,
			)
		},
		func(job *domain.Job, fact domain.ProviderResultObservation) {
			job.ProviderResultObservations = append(job.ProviderResultObservations, fact)
		},
		func(job domain.Job) (domain.ProviderResultObservation, bool) {
			for _, fact := range job.ProviderResultObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.ProviderResultObservation{}, false
		})
}

func (s *Store) AddProviderUsageObservation(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	roleExecutionID, operationID, turnID, sessionID, usageID string,
	tokens uint64,
	complete bool,
	evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderUsageObservation, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.ProviderUsageObservation{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	commandID := domain.ProviderUsageObservationCommandID(operationID, turnID, sessionID, usageID, tokens, complete, evidenceSHA256)
	request := struct {
		JobID, RoleExecutionID, OperationID, TurnID, SessionID, UsageID string
		Tokens                                                          uint64
		Complete                                                        bool
		EvidenceSHA256, PrincipalID                                     string
	}{
		jobID, roleExecutionID, operationID, turnID, sessionID, usageID,
		tokens, complete, evidenceSHA256, principal.PrincipalID(),
	}
	return persistPlanningFact(ctx, s, jobID, expectedRevision, commandID, addProviderUsageObservationCommandType, request,
		func(job domain.Job, now time.Time) (domain.ProviderUsageObservation, error) {
			return domain.NewProviderUsageObservation(
				job, roleExecutionID, principal.PrincipalID(), operationID, turnID, sessionID,
				usageID, tokens, complete, evidenceSHA256, now,
			)
		},
		func(job *domain.Job, fact domain.ProviderUsageObservation) {
			job.ProviderUsageObservations = append(job.ProviderUsageObservations, fact)
		},
		func(job domain.Job) (domain.ProviderUsageObservation, bool) {
			for _, fact := range job.ProviderUsageObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.ProviderUsageObservation{}, false
		})
}
