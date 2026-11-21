package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

type ProviderStopReason string

const (
	ProviderStopReasonPlanningComplete ProviderStopReason = "planning_complete"
	ProviderStopReasonCancelled        ProviderStopReason = "cancelled"
	ProviderStopReasonBudgetExhausted  ProviderStopReason = "budget_exhausted"
	ProviderStopReasonDeadline         ProviderStopReason = "deadline"
	ProviderStopReasonFailure          ProviderStopReason = "failure"
)

type ProviderStopIntent struct {
	ID                  string             `json:"id"`
	CommandID           string             `json:"command_id"`
	JobID               string             `json:"job_id"`
	RoleExecutionID     string             `json:"role_execution_id"`
	OperationID         string             `json:"operation_id"`
	SessionID           string             `json:"session_id"`
	ProviderID          string             `json:"provider_id"`
	AccountID           string             `json:"account_id"`
	ConfigurationSHA256 string             `json:"configuration_sha256"`
	Reason              ProviderStopReason `json:"reason"`
	CreatedAt           time.Time          `json:"created_at"`
	CreatedRevision     uint64             `json:"created_revision"`
}

type ProviderStopObservation struct {
	ID                string    `json:"id"`
	CommandID         string    `json:"command_id"`
	JobID             string    `json:"job_id"`
	RoleExecutionID   string    `json:"role_execution_id"`
	OperationID       string    `json:"operation_id"`
	SessionID         string    `json:"session_id"`
	Stopped           bool      `json:"stopped"`
	Quiescent         bool      `json:"quiescent"`
	EvidenceSHA256    string    `json:"evidence_sha256,omitempty"`
	ReconcilesUnknown bool      `json:"reconciles_unknown,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	CreatedRevision   uint64    `json:"created_revision"`
}

func ProviderStopIntentID(commandID string) string {
	return planningOperationID("provider_stop_intent", commandID)
}

func ProviderStopOperationID(commandID string) string {
	return planningOperationID("provider_stop", commandID)
}

func ProviderStopObservationID(commandID string) string {
	return planningOperationID("provider_stop_observation", commandID)
}

func ProviderStopObservationCommandID(operationID string, stopped, quiescent bool, evidenceSHA256 string, reconcilesUnknown bool) string {
	material := operationID + "\x00" + evidenceSHA256
	if stopped {
		material += "\x00stopped"
	}
	if quiescent {
		material += "\x00quiescent"
	}
	if reconcilesUnknown {
		material += "\x00reconcile"
	}
	sum := sha256.Sum256([]byte("taxion:provider-stop-observation-command:" + material))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func NewProviderStopIntent(
	job Job,
	roleExecutionID, principalID, sessionID, commandID string,
	reason ProviderStopReason,
	now time.Time,
) (ProviderStopIntent, error) {
	if err := job.Validate(); err != nil {
		return ProviderStopIntent{}, err
	}
	grant, _, err := planningGrantAndExecutionForObservation(job, roleExecutionID, principalID)
	if err != nil {
		return ProviderStopIntent{}, err
	}
	now = now.UTC().Round(0)
	if job.State != JobDraft || !validCommandID(commandID) || !validPlanningIdentity(sessionID) ||
		!validProviderStopReason(reason) || now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return ProviderStopIntent{}, errors.New("Provider Stop intent requires a DRAFT Job, valid identity, reason, and timestamp")
	}
	var start ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == roleExecutionID {
			start, foundStart = candidate, true
			break
		}
	}
	if !foundStart {
		return ProviderStopIntent{}, errors.New("Provider Stop intent has no Start intent")
	}
	startObservation, foundObservation := latestProviderStartObservation(job, start.ID)
	if !foundObservation || startObservation.Status != ProviderStartAccepted ||
		startObservation.ProviderSessionID != sessionID {
		return ProviderStopIntent{}, errors.New("Provider Stop intent requires the accepted Start Session")
	}
	for _, existing := range job.ProviderStopIntents {
		if existing.RoleExecutionID == roleExecutionID && existing.SessionID == sessionID {
			return ProviderStopIntent{}, errors.New("Provider Session already has a Stop intent")
		}
	}
	intent := ProviderStopIntent{
		ID: ProviderStopIntentID(commandID), CommandID: commandID, JobID: job.ID,
		RoleExecutionID: roleExecutionID, OperationID: ProviderStopOperationID(commandID),
		SessionID: sessionID, ProviderID: grant.ProviderID, AccountID: grant.AccountID,
		ConfigurationSHA256: grant.ConfigurationSHA256, Reason: reason,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := intent.Validate(job); err != nil {
		return ProviderStopIntent{}, err
	}
	return intent, nil
}

func (i ProviderStopIntent) Validate(job Job) error {
	if !validPlanningIdentity(i.ID) || !validCommandID(i.CommandID) ||
		i.ID != ProviderStopIntentID(i.CommandID) || i.OperationID != ProviderStopOperationID(i.CommandID) ||
		i.JobID != job.ID || !validPlanningIdentity(i.RoleExecutionID) ||
		!validPlanningIdentity(i.SessionID) || !validProviderStopReason(i.Reason) ||
		!validPlanningIdentity(i.ProviderID) || !validPlanningIdentity(i.AccountID) ||
		!validSHA256(i.ConfigurationSHA256) || i.CreatedAt.IsZero() || i.CreatedRevision == 0 {
		return errors.New("Provider Stop intent identity or binding is invalid")
	}
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == i.RoleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return errors.New("Provider Stop intent RoleExecution was not found")
	}
	grant, foundGrant := planningAuthorization(job, execution.PlanningAuthorizationID)
	if !foundGrant || grant.ProviderID != i.ProviderID || grant.AccountID != i.AccountID ||
		grant.ConfigurationSHA256 != i.ConfigurationSHA256 {
		return errors.New("Provider Stop intent Provider identity differs from PlanningAuthorization")
	}
	var start ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == i.RoleExecutionID {
			start, foundStart = candidate, true
			break
		}
	}
	if !foundStart {
		return errors.New("Provider Stop intent has no Start intent")
	}
	startObservation, foundObservation := latestProviderStartObservation(job, start.ID)
	if !foundObservation || startObservation.Status != ProviderStartAccepted ||
		startObservation.ProviderSessionID != i.SessionID ||
		i.CreatedRevision <= start.CreatedRevision || i.CreatedRevision <= startObservation.CreatedRevision ||
		i.CreatedAt.Before(start.CreatedAt) || i.CreatedAt.Before(startObservation.CreatedAt) {
		return errors.New("Provider Stop intent does not follow the accepted Start Session")
	}
	for _, turn := range job.PlanningTurnIntents {
		if turn.RoleExecutionID == i.RoleExecutionID && turn.ProviderSessionID == i.SessionID &&
			turn.CreatedRevision >= i.CreatedRevision {
			return errors.New("Planning Turn intent was created after Provider Stop intent")
		}
	}
	for _, resume := range job.PlanningResumeIntents {
		if resume.RoleExecutionID == i.RoleExecutionID && resume.ProviderSessionID == i.SessionID &&
			resume.CreatedRevision >= i.CreatedRevision {
			return errors.New("Planning Resume intent was created after Provider Stop intent")
		}
	}
	for _, existing := range job.ProviderStopIntents {
		if existing.ID != i.ID && existing.RoleExecutionID == i.RoleExecutionID && existing.SessionID == i.SessionID {
			return errors.New("Provider Session has multiple Stop intents")
		}
	}
	return nil
}

func NewProviderStopObservation(
	job Job,
	stopIntentID, principalID, commandID string,
	stopped, quiescent bool,
	evidenceSHA256 string,
	reconcilesUnknown bool,
	now time.Time,
) (ProviderStopObservation, error) {
	if err := job.Validate(); err != nil {
		return ProviderStopObservation{}, err
	}
	var intent ProviderStopIntent
	found := false
	for _, candidate := range job.ProviderStopIntents {
		if candidate.ID == stopIntentID {
			intent, found = candidate, true
			break
		}
	}
	if !found {
		return ProviderStopObservation{}, errors.New("Provider Stop intent was not found")
	}
	if _, _, err := planningGrantAndExecutionForObservation(job, intent.RoleExecutionID, principalID); err != nil {
		return ProviderStopObservation{}, err
	}
	now = now.UTC().Round(0)
	if !validCommandID(commandID) || now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return ProviderStopObservation{}, errors.New("Provider Stop observation command and timestamp are required")
	}
	observation := ProviderStopObservation{
		ID: ProviderStopObservationID(commandID), CommandID: commandID, JobID: job.ID,
		RoleExecutionID: intent.RoleExecutionID, OperationID: intent.OperationID, SessionID: intent.SessionID,
		Stopped: stopped, Quiescent: quiescent, EvidenceSHA256: evidenceSHA256,
		ReconcilesUnknown: reconcilesUnknown, CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := observation.Validate(job); err != nil {
		return ProviderStopObservation{}, err
	}
	var previous *ProviderStopObservation
	for i := range job.ProviderStopObservations {
		existing := &job.ProviderStopObservations[i]
		if existing.OperationID != intent.OperationID {
			continue
		}
		if previous == nil || existing.CreatedRevision > previous.CreatedRevision {
			previous = existing
		}
	}
	if previous == nil {
		if reconcilesUnknown {
			return ProviderStopObservation{}, errors.New("first Provider Stop observation cannot reconcile UNKNOWN")
		}
	} else {
		previousUnknown := !previous.Stopped && !previous.Quiescent && previous.EvidenceSHA256 == ""
		currentUnknown := !stopped && !quiescent && evidenceSHA256 == ""
		if previous.Quiescent || now.Before(previous.CreatedAt) {
			return ProviderStopObservation{}, errors.New("Provider Stop observation follows a terminal or later observation")
		}
		if previousUnknown {
			if currentUnknown || !reconcilesUnknown {
				return ProviderStopObservation{}, errors.New("Provider Stop observation must resolve prior UNKNOWN")
			}
		} else if reconcilesUnknown {
			return ProviderStopObservation{}, errors.New("Provider Stop observation unexpectedly reconciles UNKNOWN")
		}
	}
	return observation, nil
}

func (o ProviderStopObservation) Validate(job Job) error {
	if !validPlanningIdentity(o.ID) || !validCommandID(o.CommandID) ||
		o.ID != ProviderStopObservationID(o.CommandID) || o.JobID != job.ID ||
		!validPlanningIdentity(o.RoleExecutionID) || !validPlanningIdentity(o.OperationID) ||
		!validPlanningIdentity(o.SessionID) || o.CreatedAt.IsZero() || o.CreatedRevision == 0 ||
		(o.Quiescent && !o.Stopped) {
		return errors.New("Provider Stop observation identity or state is invalid")
	}
	if (o.Stopped || o.Quiescent || o.EvidenceSHA256 != "") && !validSHA256(o.EvidenceSHA256) {
		return errors.New("Provider Stop observation evidence is invalid")
	}
	var intent ProviderStopIntent
	found := false
	for _, candidate := range job.ProviderStopIntents {
		if candidate.OperationID == o.OperationID {
			intent, found = candidate, true
			break
		}
	}
	if !found || intent.RoleExecutionID != o.RoleExecutionID || intent.SessionID != o.SessionID ||
		o.CreatedRevision <= intent.CreatedRevision || o.CreatedAt.Before(intent.CreatedAt) {
		return errors.New("Provider Stop observation does not match its Stop intent")
	}
	return nil
}

func providerStopIntentExists(job Job, roleExecutionID, sessionID string, beforeRevision uint64) bool {
	for _, intent := range job.ProviderStopIntents {
		if intent.RoleExecutionID == roleExecutionID && intent.SessionID == sessionID &&
			intent.CreatedRevision < beforeRevision {
			return true
		}
	}
	return false
}

func validProviderStopReason(reason ProviderStopReason) bool {
	switch reason {
	case ProviderStopReasonPlanningComplete, ProviderStopReasonCancelled,
		ProviderStopReasonBudgetExhausted, ProviderStopReasonDeadline, ProviderStopReasonFailure:
		return true
	default:
		return false
	}
}
