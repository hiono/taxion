package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

type ProviderEventKind string

const (
	ProviderEventOperationAccepted ProviderEventKind = "operation.accepted"
	ProviderEventTurnStarted       ProviderEventKind = "turn.started"
	ProviderEventTurnCompleted     ProviderEventKind = "turn.completed"
	ProviderEventTurnFailed        ProviderEventKind = "turn.failed"
	ProviderEventTurnUnknown       ProviderEventKind = "turn.unknown"
	ProviderEventSessionAttached   ProviderEventKind = "session.attached"
	ProviderEventSessionDetached   ProviderEventKind = "session.detached"
	ProviderEventSessionStopped    ProviderEventKind = "session.stopped"
	ProviderEventUsageReported     ProviderEventKind = "usage.reported"
)

type ProviderEventObservation struct {
	ID              string            `json:"id"`
	CommandID       string            `json:"command_id"`
	JobID           string            `json:"job_id"`
	RoleExecutionID string            `json:"role_execution_id"`
	OperationID     string            `json:"operation_id"`
	TurnID          string            `json:"turn_id,omitempty"`
	SessionID       string            `json:"session_id"`
	EventID         string            `json:"event_id"`
	Kind            ProviderEventKind `json:"kind"`
	PreviousCursor  string            `json:"previous_cursor,omitempty"`
	Cursor          string            `json:"cursor"`
	EvidenceSHA256  string            `json:"evidence_sha256"`
	ObservedAt      time.Time         `json:"observed_at"`
	CreatedRevision uint64            `json:"created_revision"`
}

func ProviderEventObservationCommandID(eventID string) string {
	sum := sha256.Sum256([]byte("taxion:provider-event-observation-command:" + eventID))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func ProviderEventObservationID(commandID string) string {
	return planningOperationID("provider_event_observation", commandID)
}

func NewProviderEventObservation(
	job Job,
	roleExecutionID, principalID, operationID, turnID, sessionID, eventID string,
	kind ProviderEventKind,
	previousCursor, cursor, evidenceSHA256 string,
	observedAt time.Time,
	commandID string,
) (ProviderEventObservation, error) {
	if err := job.Validate(); err != nil {
		return ProviderEventObservation{}, err
	}
	observedAt = observedAt.UTC().Round(0)
	if !validCommandID(commandID) || commandID != ProviderEventObservationCommandID(eventID) ||
		observedAt.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return ProviderEventObservation{}, errors.New("Provider event command, identity, timestamp, or Job revision is invalid")
	}
	observation := ProviderEventObservation{
		ID: ProviderEventObservationID(commandID), CommandID: commandID, JobID: job.ID,
		RoleExecutionID: roleExecutionID, OperationID: operationID, TurnID: turnID,
		SessionID: sessionID, EventID: eventID, Kind: kind,
		PreviousCursor: previousCursor, Cursor: cursor, EvidenceSHA256: evidenceSHA256,
		ObservedAt: observedAt, CreatedRevision: job.Revision + 1,
	}
	if err := observation.Validate(job); err != nil {
		return ProviderEventObservation{}, err
	}
	var previous *ProviderEventObservation
	for i := range job.ProviderEventObservations {
		existing := &job.ProviderEventObservations[i]
		if existing.SessionID != sessionID {
			continue
		}
		if existing.EventID == eventID || existing.Cursor == cursor {
			return ProviderEventObservation{}, errors.New("Provider event identity or cursor is duplicated")
		}
		if previous == nil || existing.CreatedRevision > previous.CreatedRevision {
			previous = existing
		}
	}
	if previous == nil {
		if previousCursor != "" {
			return ProviderEventObservation{}, errors.New("first Provider event must begin at the initial cursor")
		}
	} else if previousCursor != previous.Cursor || observedAt.Before(previous.ObservedAt) ||
		observation.CreatedRevision <= previous.CreatedRevision {
		return ProviderEventObservation{}, errors.New("Provider event cursor or observation order is discontinuous")
	}
	return observation, nil
}

func (o ProviderEventObservation) Validate(job Job) error {
	if !validPlanningIdentity(o.ID) || !validCommandID(o.CommandID) ||
		o.ID != ProviderEventObservationID(o.CommandID) ||
		o.CommandID != ProviderEventObservationCommandID(o.EventID) ||
		o.JobID != job.ID || !validPlanningIdentity(o.RoleExecutionID) ||
		!validPlanningIdentity(o.OperationID) || !validPlanningIdentity(o.SessionID) ||
		!validPlanningIdentity(o.EventID) || !validPlanningIdentity(o.Cursor) ||
		(o.PreviousCursor != "" && !validPlanningIdentity(o.PreviousCursor)) ||
		o.Cursor == o.PreviousCursor || !validSHA256(o.EvidenceSHA256) ||
		o.ObservedAt.IsZero() || o.CreatedRevision == 0 {
		return errors.New("Provider event observation identity or evidence is invalid")
	}
	if !validProviderEventKind(o.Kind) || (providerEventRequiresTurn(o.Kind) != (o.TurnID != "")) {
		return errors.New("Provider event observation kind and Turn identity are inconsistent")
	}

	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == o.RoleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return errors.New("Provider event observation RoleExecution was not found")
	}
	var start ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			start, foundStart = candidate, true
			break
		}
	}
	if !foundStart {
		return errors.New("Provider event observation has no Provider Start intent")
	}
	var accepted *ProviderStartObservation
	for i := range job.ProviderStartObservations {
		candidate := &job.ProviderStartObservations[i]
		if candidate.OperationID == start.ID {
			accepted = candidate
		}
	}
	if accepted == nil || accepted.Status != ProviderStartAccepted ||
		accepted.ProviderSessionID != o.SessionID ||
		o.CreatedRevision <= accepted.CreatedRevision || o.CreatedRevision <= start.CreatedRevision {
		return errors.New("Provider event observation requires the accepted Start Session")
	}

	if providerEventRequiresTurn(o.Kind) {
		foundTurn := false
		for _, turn := range job.PlanningTurnIntents {
			if turn.ID != o.TurnID {
				continue
			}
			if turn.OperationID != o.OperationID || turn.RoleExecutionID != o.RoleExecutionID ||
				turn.ProviderSessionID != o.SessionID || o.CreatedRevision <= turn.CreatedRevision {
				return errors.New("Provider event observation does not match its Turn intent")
			}
			foundTurn = true
			break
		}
		if !foundTurn {
			return errors.New("Provider event observation Turn intent was not found")
		}
	} else {
		if o.TurnID != "" {
			return errors.New("session-level Provider event must not include a Turn identity")
		}
		if o.OperationID == start.ID {
			if o.Kind == ProviderEventOperationAccepted || o.Kind == ProviderEventSessionAttached ||
				o.Kind == ProviderEventSessionDetached || o.Kind == ProviderEventSessionStopped {
				return nil
			}
			return errors.New("Provider event kind is not valid for a Start operation")
		}
		if o.Kind != ProviderEventSessionAttached && o.Kind != ProviderEventSessionDetached {
			return errors.New("Provider event operation does not match the Start intent")
		}
		for _, resume := range job.PlanningResumeIntents {
			if resume.OperationID == o.OperationID && resume.RoleExecutionID == o.RoleExecutionID &&
				resume.ProviderSessionID == o.SessionID && o.CreatedRevision > resume.CreatedRevision {
				return nil
			}
		}
		return errors.New("Provider event observation Resume intent was not found")
	}
	return nil
}

func validProviderEventKind(kind ProviderEventKind) bool {
	switch kind {
	case ProviderEventOperationAccepted, ProviderEventTurnStarted, ProviderEventTurnCompleted,
		ProviderEventTurnFailed, ProviderEventTurnUnknown, ProviderEventSessionAttached,
		ProviderEventSessionDetached, ProviderEventSessionStopped, ProviderEventUsageReported:
		return true
	default:
		return false
	}
}

func providerEventRequiresTurn(kind ProviderEventKind) bool {
	switch kind {
	case ProviderEventTurnStarted, ProviderEventTurnCompleted, ProviderEventTurnFailed,
		ProviderEventTurnUnknown, ProviderEventUsageReported:
		return true
	default:
		return false
	}
}
