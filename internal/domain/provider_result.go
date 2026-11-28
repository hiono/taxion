package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
	"unicode/utf8"
)

const MaxProviderResultContentBytes = 1 << 20

type ProviderResultObservation struct {
	ID              string    `json:"id"`
	CommandID       string    `json:"command_id"`
	JobID           string    `json:"job_id"`
	RoleExecutionID string    `json:"role_execution_id"`
	OperationID     string    `json:"operation_id"`
	TurnID          string    `json:"turn_id,omitempty"`
	SessionID       string    `json:"session_id"`
	Content         string    `json:"content"`
	ContentSHA256   string    `json:"content_sha256"`
	Final           bool      `json:"final"`
	EvidenceSHA256  string    `json:"evidence_sha256"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedRevision uint64    `json:"created_revision"`
}

func ProviderResultObservationCommandID(operationID, turnID, contentSHA256, evidenceSHA256 string) string {
	material := operationID + "\x00" + turnID + "\x00" + contentSHA256 + "\x00" + evidenceSHA256
	sum := sha256.Sum256([]byte("taxion:provider-result-observation-command:" + material))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func ProviderResultObservationID(commandID string) string {
	return planningOperationID("provider_result_observation", commandID)
}

func NewProviderResultObservation(
	job Job,
	roleExecutionID, principalID, operationID, turnID, sessionID string,
	content, contentSHA256 string,
	final bool,
	evidenceSHA256 string,
	now time.Time,
) (ProviderResultObservation, error) {
	if err := job.Validate(); err != nil {
		return ProviderResultObservation{}, err
	}
	grant, _, err := providerResultExecution(job, roleExecutionID)
	if err != nil {
		return ProviderResultObservation{}, err
	}
	if grant.PrincipalID != principalID {
		return ProviderResultObservation{}, errors.New("Provider Result Principal does not match PlanningAuthorization")
	}
	now = now.UTC().Round(0)
	if now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return ProviderResultObservation{}, errors.New("Provider Result observation timestamp or Job revision is invalid")
	}
	commandID := ProviderResultObservationCommandID(operationID, turnID, contentSHA256, evidenceSHA256)
	observation := ProviderResultObservation{
		ID: ProviderResultObservationID(commandID), CommandID: commandID, JobID: job.ID,
		RoleExecutionID: roleExecutionID, OperationID: operationID, TurnID: turnID, SessionID: sessionID,
		Content: content, ContentSHA256: contentSHA256, Final: final, EvidenceSHA256: evidenceSHA256,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := observation.Validate(job); err != nil {
		return ProviderResultObservation{}, err
	}
	for _, existing := range job.ProviderResultObservations {
		if existing.OperationID == operationID && existing.TurnID == turnID {
			return ProviderResultObservation{}, errors.New("Provider operation already has a terminal Result observation")
		}
	}
	return observation, nil
}

func (o ProviderResultObservation) Validate(job Job) error {
	if !validPlanningIdentity(o.ID) || !validCommandID(o.CommandID) ||
		o.ID != ProviderResultObservationID(o.CommandID) ||
		o.CommandID != ProviderResultObservationCommandID(o.OperationID, o.TurnID, o.ContentSHA256, o.EvidenceSHA256) ||
		o.JobID != job.ID || !validPlanningIdentity(o.RoleExecutionID) ||
		!validPlanningIdentity(o.OperationID) || !validPlanningIdentity(o.SessionID) ||
		(o.TurnID != "" && !validPlanningIdentity(o.TurnID)) ||
		!o.Final || !utf8.ValidString(o.Content) || len([]byte(o.Content)) > MaxProviderResultContentBytes ||
		!validSHA256(o.ContentSHA256) || sha256HexDomain(o.Content) != o.ContentSHA256 ||
		!validSHA256(o.EvidenceSHA256) || o.CreatedAt.IsZero() || o.CreatedRevision == 0 {
		return errors.New("Provider Result observation identity, finality, content, or evidence is invalid")
	}
	_, _, minimumRevision, minimumTime, err := providerResultBinding(
		job, o.RoleExecutionID, o.OperationID, o.TurnID, o.SessionID,
	)
	if err != nil {
		return err
	}
	if o.CreatedRevision <= minimumRevision || o.CreatedAt.Before(minimumTime) {
		return errors.New("Provider Result observation does not follow its accepted Provider operation")
	}
	return nil
}

func providerResultExecution(job Job, roleExecutionID string) (PlanningAuthorization, RoleExecution, error) {
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return PlanningAuthorization{}, RoleExecution{}, errors.New("Provider observation RoleExecution was not found")
	}
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID != execution.PlanningAuthorizationID {
			continue
		}
		if err := grant.Validate(job); err != nil {
			return PlanningAuthorization{}, RoleExecution{}, err
		}
		return grant, execution, nil
	}
	return PlanningAuthorization{}, RoleExecution{}, errors.New("Provider observation PlanningAuthorization was not found")
}

func providerResultBinding(job Job, roleExecutionID, operationID, turnID, sessionID string) (ProviderStartIntent, *ProviderStartObservation, uint64, time.Time, error) {
	_, _, err := providerResultExecution(job, roleExecutionID)
	if err != nil {
		return ProviderStartIntent{}, nil, 0, time.Time{}, err
	}
	var start ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == roleExecutionID {
			start, foundStart = candidate, true
			break
		}
	}
	if !foundStart || operationID == "" || sessionID == "" {
		return ProviderStartIntent{}, nil, 0, time.Time{}, errors.New("Provider observation has no matching Start identity")
	}
	var accepted *ProviderStartObservation
	for i := range job.ProviderStartObservations {
		candidate := &job.ProviderStartObservations[i]
		if candidate.OperationID == start.ID && (accepted == nil || candidate.CreatedRevision > accepted.CreatedRevision) {
			accepted = candidate
		}
	}
	if accepted == nil || accepted.Status != ProviderStartAccepted || accepted.ProviderSessionID != sessionID {
		return ProviderStartIntent{}, nil, 0, time.Time{}, errors.New("Provider observation requires the accepted Start Session")
	}
	minimumRevision, minimumTime := accepted.CreatedRevision, accepted.CreatedAt
	if operationID == start.ID && turnID == "" {
		return start, accepted, minimumRevision, minimumTime, nil
	}
	if turnID == "" {
		return ProviderStartIntent{}, nil, 0, time.Time{}, errors.New("Provider observation operation requires its Turn identity")
	}
	for _, turn := range job.PlanningTurnIntents {
		if turn.ID != turnID {
			continue
		}
		if turn.OperationID != operationID || turn.RoleExecutionID != roleExecutionID ||
			turn.ProviderSessionID != sessionID || turn.CreatedRevision <= accepted.CreatedRevision {
			return ProviderStartIntent{}, nil, 0, time.Time{}, errors.New("Provider observation does not match its Turn intent")
		}
		if turn.CreatedRevision > minimumRevision {
			minimumRevision = turn.CreatedRevision
			minimumTime = turn.CreatedAt
		}
		return start, accepted, minimumRevision, minimumTime, nil
	}
	return ProviderStartIntent{}, nil, 0, time.Time{}, errors.New("Provider observation Turn intent was not found")
}

func sha256HexDomain(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
