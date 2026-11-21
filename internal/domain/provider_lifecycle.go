package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

const (
	ProviderStartAccepted = "ACCEPTED"
	ProviderStartFailed   = "FAILED"
	ProviderStartUnknown  = "UNKNOWN"
)

type ProviderStartObservation struct {
	ID                  string    `json:"id"`
	CommandID           string    `json:"command_id"`
	JobID               string    `json:"job_id"`
	RoleExecutionID     string    `json:"role_execution_id"`
	OperationID         string    `json:"operation_id"`
	ProviderID          string    `json:"provider_id"`
	AccountID           string    `json:"account_id"`
	ConfigurationSHA256 string    `json:"configuration_sha256"`
	Status              string    `json:"status"`
	ProviderSessionID   string    `json:"provider_session_id,omitempty"`
	ReceiptID           string    `json:"receipt_id,omitempty"`
	EvidenceSHA256      string    `json:"evidence_sha256,omitempty"`
	ReconcilesUnknown   bool      `json:"reconciles_unknown,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
	CreatedRevision     uint64    `json:"created_revision"`
}

func ProviderStartObservationID(commandID string) string {
	return planningOperationID("provider_start_observation", commandID)
}

func ProviderStartObservationCommandID(operationID, status, sessionID, receiptID, evidenceSHA256 string, reconcilesUnknown bool) string {
	material := operationID + "\x00" + status + "\x00" + sessionID + "\x00" + receiptID + "\x00" + evidenceSHA256
	if reconcilesUnknown {
		material += "\x00reconcile"
	}
	sum := sha256.Sum256([]byte("taxion:provider-start-observation-command:" + material))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func NewProviderStartObservation(
	job Job,
	operationID, principalID, commandID, status, providerSessionID, receiptID, evidenceSHA256 string,
	reconcilesUnknown bool,
	now time.Time,
) (ProviderStartObservation, error) {
	now = now.UTC().Round(0)
	if !validCommandID(commandID) || !validPlanningIdentity(operationID) || now.IsZero() {
		return ProviderStartObservation{}, errors.New("Provider Start observation command, operation, and timestamp are required")
	}
	var start ProviderStartIntent
	found := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.ID == operationID {
			start, found = candidate, true
			break
		}
	}
	if !found {
		return ProviderStartObservation{}, errors.New("Provider Start intent was not found")
	}
	if _, _, err := planningGrantAndExecutionForObservation(job, start.RoleExecutionID, principalID); err != nil {
		return ProviderStartObservation{}, err
	}
	switch status {
	case ProviderStartUnknown:
		if providerSessionID != "" || receiptID != "" || evidenceSHA256 != "" || reconcilesUnknown {
			return ProviderStartObservation{}, errors.New("UNKNOWN Provider Start observation cannot claim receipt, Session, or terminal evidence")
		}
	case ProviderStartAccepted:
		if !validPlanningIdentity(providerSessionID) || !validPlanningIdentity(receiptID) || !validSHA256(evidenceSHA256) {
			return ProviderStartObservation{}, errors.New("accepted Provider Start observation requires Session, receipt, and evidence")
		}
	case ProviderStartFailed:
		if providerSessionID != "" || !validSHA256(evidenceSHA256) {
			return ProviderStartObservation{}, errors.New("failed Provider Start observation requires terminal evidence and no Session")
		}
	default:
		return ProviderStartObservation{}, errors.New("unsupported Provider Start observation status")
	}
	var previous *ProviderStartObservation
	for i := range job.ProviderStartObservations {
		candidate := &job.ProviderStartObservations[i]
		if candidate.OperationID == operationID {
			previous = candidate
		}
	}
	if previous == nil {
		if reconcilesUnknown {
			return ProviderStartObservation{}, errors.New("first Provider Start observation cannot reconcile UNKNOWN")
		}
	} else if previous.Status != ProviderStartUnknown || status == ProviderStartUnknown ||
		!reconcilesUnknown || previous.ReconcilesUnknown {
		return ProviderStartObservation{}, errors.New("only UNKNOWN Provider Start may be reconciled by same-operation terminal evidence")
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return ProviderStartObservation{}, errors.New("Job revision exhausted")
	}
	observation := ProviderStartObservation{
		ID: ProviderStartObservationID(commandID), CommandID: commandID, JobID: job.ID,
		RoleExecutionID: start.RoleExecutionID, OperationID: start.ID,
		ProviderID: start.ProviderID, AccountID: start.AccountID,
		ConfigurationSHA256: start.ConfigurationSHA256,
		Status:              status, ProviderSessionID: providerSessionID, ReceiptID: receiptID,
		EvidenceSHA256: evidenceSHA256, ReconcilesUnknown: reconcilesUnknown,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := observation.Validate(job, start); err != nil {
		return ProviderStartObservation{}, err
	}
	return observation, nil
}

func (o ProviderStartObservation) Validate(job Job, start ProviderStartIntent) error {
	if !validPlanningIdentity(o.ID) || !validCommandID(o.CommandID) ||
		o.ID != ProviderStartObservationID(o.CommandID) ||
		o.JobID != job.ID || o.RoleExecutionID != start.RoleExecutionID ||
		o.OperationID != start.ID || o.ProviderID != start.ProviderID ||
		o.AccountID != start.AccountID || o.ConfigurationSHA256 != start.ConfigurationSHA256 ||
		o.CreatedAt.IsZero() || o.CreatedAt.Before(start.CreatedAt) ||
		o.CreatedRevision <= start.CreatedRevision {
		return errors.New("Provider Start observation identity or metadata mismatch")
	}
	switch o.Status {
	case ProviderStartUnknown:
		if o.ProviderSessionID != "" || o.ReceiptID != "" || o.EvidenceSHA256 != "" || o.ReconcilesUnknown {
			return errors.New("UNKNOWN Provider Start observation contains terminal claims")
		}
	case ProviderStartAccepted:
		if !validPlanningIdentity(o.ProviderSessionID) || !validPlanningIdentity(o.ReceiptID) ||
			!validSHA256(o.EvidenceSHA256) {
			return errors.New("accepted Provider Start observation evidence is invalid")
		}
	case ProviderStartFailed:
		if o.ProviderSessionID != "" || !validSHA256(o.EvidenceSHA256) {
			return errors.New("failed Provider Start observation evidence is invalid")
		}
	default:
		return errors.New("Provider Start observation status is invalid")
	}
	return nil
}

func latestProviderStartObservation(job Job, operationID string) (ProviderStartObservation, bool) {
	var latest ProviderStartObservation
	found := false
	for _, observation := range job.ProviderStartObservations {
		if observation.OperationID == operationID {
			latest = observation
			found = true
		}
	}
	return latest, found
}

func validateProviderStartSession(job Job, start ProviderStartIntent, sessionID string, beforeRevision uint64, beforeTime time.Time) error {
	observation, found := latestProviderStartObservation(job, start.ID)
	if !found {
		return &PlanningBoundaryError{
			Boundary: PlanningBoundaryTurn, ReasonCode: "provider_start_unconfirmed",
			Cause: errors.New("Provider Start has no accepted observation"),
		}
	}
	if observation.CreatedRevision >= beforeRevision || observation.CreatedAt.After(beforeTime) {
		return errors.New("Provider Start observation does not precede the planning turn")
	}
	switch observation.Status {
	case ProviderStartUnknown:
		return &PlanningBoundaryError{
			Boundary: PlanningBoundaryTurn, ReasonCode: "provider_start_unknown",
			Cause: errors.New("Provider Start is unresolved; reconcile the same OperationID before starting a turn"),
		}
	case ProviderStartFailed:
		return &PlanningBoundaryError{
			Boundary: PlanningBoundaryTurn, ReasonCode: "provider_start_failed",
			Cause: errors.New("Provider Start failed; a turn cannot use this Session"),
		}
	case ProviderStartAccepted:
		if observation.ProviderSessionID != sessionID {
			return &PlanningBoundaryError{
				Boundary: PlanningBoundaryTurn, ReasonCode: "provider_session_mismatch",
				Cause: errors.New("planning turn Session differs from the accepted Provider Start Session"),
			}
		}
	default:
		return errors.New("Provider Start observation status is invalid")
	}
	return nil
}
