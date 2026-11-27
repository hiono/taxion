package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

const (
	PlanningTurnUnknown       = "UNKNOWN"
	PlanningTurnNotDispatched = "NOT_DISPATCHED"
	PlanningTurnCompleted     = "COMPLETED"
	PlanningTurnFailed        = "FAILED"
	PlanningEffectAllow       = "ALLOW"
	PlanningEffectDeny        = "DENY"
	PlanningBoundaryTurn      = "turn"
	PlanningBoundaryEffect    = "effect"
	PlanningBoundaryResume    = "resume"
	PlanningResumeUnknown     = "UNKNOWN"
	PlanningResumeAttached    = "ATTACHED"
	PlanningResumeFailed      = "FAILED"
)

type PlanningBoundaryError struct {
	Boundary   string
	ReasonCode string
	Cause      error
}

func (e *PlanningBoundaryError) Error() string {
	if e.Cause == nil {
		return "PlanningAuthorization boundary rejected: " + e.ReasonCode
	}
	return "PlanningAuthorization boundary rejected: " + e.ReasonCode + ": " + e.Cause.Error()
}

func (e *PlanningBoundaryError) Unwrap() error { return e.Cause }

type PlanningBoundaryDecision struct {
	ID                      string    `json:"id"`
	CommandID               string    `json:"command_id"`
	SourceCommandID         string    `json:"source_command_id"`
	JobID                   string    `json:"job_id"`
	RoleExecutionID         string    `json:"role_execution_id"`
	PlanningAuthorizationID string    `json:"planning_authorization_id"`
	PrincipalID             string    `json:"principal_id"`
	Boundary                string    `json:"boundary"`
	Decision                string    `json:"decision"`
	ReasonCode              string    `json:"reason_code"`
	RequestSHA256           string    `json:"request_sha256"`
	CreatedAt               time.Time `json:"created_at"`
	CreatedRevision         uint64    `json:"created_revision"`
}

func PlanningBoundaryDecisionCommandID(sourceCommandID, boundary string) string {
	sum := sha256.Sum256([]byte("taxion:planning_boundary_command:" + boundary + ":" + sourceCommandID))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func PlanningBoundaryDecisionID(commandID string) string {
	return planningOperationID("planning_boundary_decision", commandID)
}

func validPlanningBoundaryReason(reasonCode string) bool {
	switch reasonCode {
	case "authorization_invalid_or_expired", "principal_mismatch", "provider_session_mismatch",
		"previous_operation_unknown", "previous_resume_unknown", "turn_budget_exhausted", "token_budget_exhausted",
		"provider_configuration_mismatch", "reattachment_not_supported", "resume_before_latest_turn",
		"provider_session_not_attached", "read_only_context_mismatch", "effect_not_permitted":
		return true
	default:
		return false
	}
}

type PlanningTurnIntent struct {
	ID                      string    `json:"id"`
	OperationID             string    `json:"operation_id"`
	CommandID               string    `json:"command_id"`
	JobID                   string    `json:"job_id"`
	RoleExecutionID         string    `json:"role_execution_id"`
	PlanningAuthorizationID string    `json:"planning_authorization_id"`
	RequestSHA256           string    `json:"request_sha256"`
	ProviderSessionID       string    `json:"provider_session_id"`
	ProviderID              string    `json:"provider_id"`
	AccountID               string    `json:"account_id"`
	ConfigurationSHA256     string    `json:"configuration_sha256"`
	TurnNumber              uint32    `json:"turn_number"`
	PromptSHA256            string    `json:"prompt_sha256"`
	ReservedTokens          uint64    `json:"reserved_tokens"`
	CreatedAt               time.Time `json:"created_at"`
	CreatedRevision         uint64    `json:"created_revision"`
}

type PlanningTurnObservation struct {
	ID                string    `json:"id"`
	CommandID         string    `json:"command_id"`
	JobID             string    `json:"job_id"`
	TurnID            string    `json:"turn_id"`
	OperationID       string    `json:"operation_id"`
	RoleExecutionID   string    `json:"role_execution_id"`
	ProviderSessionID string    `json:"provider_session_id"`
	Status            string    `json:"status"`
	TokensUsed        uint64    `json:"tokens_used"`
	EvidenceSHA256    string    `json:"evidence_sha256,omitempty"`
	ReconcilesUnknown bool      `json:"reconciles_unknown,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	CreatedRevision   uint64    `json:"created_revision"`
}

type PlanningEffectDecision struct {
	ID                      string    `json:"id"`
	CommandID               string    `json:"command_id"`
	JobID                   string    `json:"job_id"`
	RoleExecutionID         string    `json:"role_execution_id"`
	PlanningAuthorizationID string    `json:"planning_authorization_id"`
	Effect                  string    `json:"effect"`
	ContextSHA256           string    `json:"context_sha256"`
	Decision                string    `json:"decision"`
	Reason                  string    `json:"reason"`
	CreatedAt               time.Time `json:"created_at"`
	CreatedRevision         uint64    `json:"created_revision"`
}

type PlanningResumeObservation struct {
	ID                string    `json:"id"`
	CommandID         string    `json:"command_id"`
	JobID             string    `json:"job_id"`
	ResumeID          string    `json:"resume_id"`
	OperationID       string    `json:"operation_id"`
	RoleExecutionID   string    `json:"role_execution_id"`
	ProviderSessionID string    `json:"provider_session_id"`
	Status            string    `json:"status"`
	EvidenceSHA256    string    `json:"evidence_sha256,omitempty"`
	ReconcilesUnknown bool      `json:"reconciles_unknown,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	CreatedRevision   uint64    `json:"created_revision"`
}

type PlanningResumeIntent struct {
	ID                                string    `json:"id"`
	OperationID                       string    `json:"operation_id"`
	CommandID                         string    `json:"command_id"`
	JobID                             string    `json:"job_id"`
	RoleExecutionID                   string    `json:"role_execution_id"`
	PlanningAuthorizationID           string    `json:"planning_authorization_id"`
	ProviderSessionID                 string    `json:"provider_session_id"`
	ProviderID                        string    `json:"provider_id"`
	AccountID                         string    `json:"account_id"`
	ConfigurationSHA256               string    `json:"configuration_sha256"`
	CapabilityEvidenceSHA256          string    `json:"capability_evidence_sha256"`
	CrossProcessReattachmentSupported bool      `json:"cross_process_reattachment_supported"`
	CreatedAt                         time.Time `json:"created_at"`
	CreatedRevision                   uint64    `json:"created_revision"`
}

func planningOperationID(kind, commandID string) string {
	sum := sha256.Sum256([]byte("taxion:" + kind + ":" + commandID))
	return kind + "_" + hex.EncodeToString(sum[:])
}

func PlanningTurnOperationID(commandID string) string {
	return planningOperationID("provider_turn", commandID)
}

func PlanningTurnIntentID(commandID string) string {
	return planningOperationID("planning_turn", commandID)
}

func PlanningTurnObservationID(commandID string) string {
	return planningOperationID("planning_turn_observation", commandID)
}

func PlanningEffectDecisionID(commandID string) string {
	return planningOperationID("planning_effect", commandID)
}

func PlanningResumeOperationID(commandID string) string {
	return planningOperationID("provider_resume", commandID)
}

func PlanningResumeIntentID(commandID string) string {
	return planningOperationID("planning_resume", commandID)
}

func PlanningResumeObservationID(commandID string) string {
	return planningOperationID("planning_resume_observation", commandID)
}

func planningGrantAndExecution(job Job, roleExecutionID, principalID string, now time.Time, boundary string) (PlanningAuthorization, RoleExecution, error) {
	if err := job.Validate(); err != nil {
		return PlanningAuthorization{}, RoleExecution{}, err
	}
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return PlanningAuthorization{}, RoleExecution{}, errors.New("Taskless PLANNER RoleExecution was not found")
	}
	var grant PlanningAuthorization
	foundGrant := false
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == execution.PlanningAuthorizationID {
			grant, foundGrant = candidate, true
			break
		}
	}
	if !foundGrant {
		return PlanningAuthorization{}, RoleExecution{}, errors.New("PlanningAuthorization was not found")
	}
	if err := grant.ValidateFor(job, principalID, now); err != nil {
		return PlanningAuthorization{}, RoleExecution{}, &PlanningBoundaryError{Boundary: boundary, ReasonCode: "authorization_invalid_or_expired", Cause: err}
	}
	return grant, execution, nil
}

func planningGrantAndExecutionForObservation(job Job, roleExecutionID, principalID string) (PlanningAuthorization, RoleExecution, error) {
	if err := job.Validate(); err != nil {
		return PlanningAuthorization{}, RoleExecution{}, err
	}
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return PlanningAuthorization{}, RoleExecution{}, errors.New("Taskless PLANNER RoleExecution was not found")
	}
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID != execution.PlanningAuthorizationID {
			continue
		}
		if err := grant.Validate(job); err != nil {
			return PlanningAuthorization{}, RoleExecution{}, err
		}
		if grant.PrincipalID != principalID {
			return PlanningAuthorization{}, RoleExecution{}, errors.New("PlanningAuthorization principal mismatch")
		}
		return grant, execution, nil
	}
	return PlanningAuthorization{}, RoleExecution{}, errors.New("PlanningAuthorization was not found")
}

func latestPlanningResumeStatus(job Job, roleExecutionID string, beforeRevision uint64) (string, bool) {
	var latest PlanningResumeIntent
	found := false
	for _, resume := range job.PlanningResumeIntents {
		if resume.RoleExecutionID == roleExecutionID && resume.CreatedRevision < beforeRevision &&
			(!found || resume.CreatedRevision > latest.CreatedRevision) {
			latest = resume
			found = true
		}
	}
	if !found {
		return "", false
	}
	var latestObservation *PlanningResumeObservation
	for i := range job.PlanningResumeObservations {
		observation := &job.PlanningResumeObservations[i]
		if observation.ResumeID == latest.ID && observation.CreatedRevision < beforeRevision &&
			(latestObservation == nil || observation.CreatedRevision > latestObservation.CreatedRevision) {
			latestObservation = observation
		}
	}
	if latestObservation == nil {
		return "", true
	}
	return latestObservation.Status, true
}

func NewPlanningTurnIntent(job Job, roleExecutionID, principalID, providerSessionID, commandID, promptSHA256 string, reservedTokens uint64, now time.Time) (PlanningTurnIntent, error) {
	now = now.UTC().Round(0)
	if !validCommandID(commandID) || !validPlanningIdentity(providerSessionID) ||
		!validSHA256(promptSHA256) || reservedTokens == 0 || now.IsZero() {
		return PlanningTurnIntent{}, errors.New("planning turn command, session, prompt digest, token reservation, and timestamp are required")
	}
	grant, execution, err := planningGrantAndExecution(job, roleExecutionID, principalID, now, PlanningBoundaryTurn)
	if err != nil {
		return PlanningTurnIntent{}, err
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
		return PlanningTurnIntent{}, errors.New("Provider Start intent was not found")
	}
	if err := validateProviderStartSession(job, start, providerSessionID, job.Revision+1, now); err != nil {
		return PlanningTurnIntent{}, err
	}
	var count uint32
	var tokensUsed uint64
	for _, previous := range job.PlanningTurnIntents {
		if previous.RoleExecutionID != execution.ID {
			continue
		}
		if previous.ProviderSessionID != providerSessionID {
			return PlanningTurnIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryTurn, ReasonCode: "provider_session_mismatch", Cause: errors.New("planning turn cannot change Provider Session identity")}
		}
		observations := 0
		lastStatus := ""
		lastTokensUsed := uint64(0)
		for _, observation := range job.PlanningTurnObservations {
			if observation.TurnID == previous.ID {
				observations++
				lastStatus = observation.Status
				lastTokensUsed = observation.TokensUsed
			}
		}
		if observations == 0 || lastStatus == PlanningTurnUnknown {
			return PlanningTurnIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryTurn, ReasonCode: "previous_operation_unknown", Cause: errors.New("previous Provider turn is unresolved; reconcile the same OperationID before another turn")}
		}
		if lastStatus == PlanningTurnNotDispatched {
			continue
		}
		count++
		if ^uint64(0)-tokensUsed < lastTokensUsed {
			return PlanningTurnIntent{}, errors.New("planning token usage overflow")
		}
		tokensUsed += lastTokensUsed
	}
	if status, found := latestPlanningResumeStatus(job, execution.ID, job.Revision+1); found && status != PlanningResumeAttached {
		if status == "" || status == PlanningResumeUnknown {
			return PlanningTurnIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryTurn, ReasonCode: "previous_resume_unknown", Cause: errors.New("previous Provider resume is unresolved; reconcile the same OperationID before another turn")}
		}
		return PlanningTurnIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryTurn, ReasonCode: "provider_session_not_attached", Cause: errors.New("Provider Session is not attached after the latest resume")}
	}
	if count >= grant.Budget.MaxTurns || reservedTokens > grant.Budget.MaxTokens-tokensUsed {
		reasonCode := "turn_budget_exhausted"
		if reservedTokens > grant.Budget.MaxTokens-tokensUsed {
			reasonCode = "token_budget_exhausted"
		}
		return PlanningTurnIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryTurn, ReasonCode: reasonCode, Cause: errors.New("PlanningAuthorization turn or token budget is exhausted")}
	}
	if start.ProviderID != grant.ProviderID || start.AccountID != grant.AccountID ||
		start.ConfigurationSHA256 != grant.ConfigurationSHA256 {
		return PlanningTurnIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryTurn, ReasonCode: "provider_configuration_mismatch", Cause: errors.New("Provider Start intent identity differs from PlanningAuthorization")}
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return PlanningTurnIntent{}, errors.New("Job revision exhausted")
	}
	turn := PlanningTurnIntent{
		ID: PlanningTurnIntentID(commandID), OperationID: PlanningTurnOperationID(commandID),
		CommandID: commandID, JobID: job.ID, RoleExecutionID: execution.ID,
		PlanningAuthorizationID: grant.ID, RequestSHA256: grant.RequestSHA256, ProviderSessionID: providerSessionID,
		ProviderID: grant.ProviderID, AccountID: grant.AccountID,
		ConfigurationSHA256: grant.ConfigurationSHA256, TurnNumber: count + 1,
		PromptSHA256: promptSHA256, ReservedTokens: reservedTokens,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := turn.Validate(job); err != nil {
		return PlanningTurnIntent{}, err
	}
	return turn, nil
}

func (t PlanningTurnIntent) Validate(job Job) error {
	if !validPlanningIdentity(t.ID) || !validPlanningIdentity(t.OperationID) ||
		!validCommandID(t.CommandID) || t.ID != PlanningTurnIntentID(t.CommandID) ||
		t.OperationID != PlanningTurnOperationID(t.CommandID) ||
		!validPlanningIdentity(t.JobID) || !validPlanningIdentity(t.RoleExecutionID) ||
		!validPlanningIdentity(t.PlanningAuthorizationID) || !validSHA256(t.RequestSHA256) || !validPlanningIdentity(t.ProviderSessionID) ||
		!validPlanningIdentity(t.ProviderID) || !validPlanningIdentity(t.AccountID) ||
		!validSHA256(t.ConfigurationSHA256) || !validSHA256(t.PromptSHA256) ||
		t.TurnNumber == 0 || t.ReservedTokens == 0 || t.CreatedAt.IsZero() || t.CreatedRevision < 3 {
		return errors.New("PlanningTurnIntent identity or metadata is invalid")
	}
	if t.JobID != job.ID {
		return errors.New("PlanningTurnIntent Job identity mismatch")
	}
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == t.RoleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution || execution.PlanningAuthorizationID != t.PlanningAuthorizationID {
		return errors.New("PlanningTurnIntent RoleExecution or grant mismatch")
	}
	foundStart := false
	for _, start := range job.ProviderStartIntents {
		if start.RoleExecutionID == execution.ID {
			if t.CreatedRevision <= start.CreatedRevision || t.CreatedAt.Before(start.CreatedAt) {
				return errors.New("PlanningTurnIntent precedes Provider Start intent")
			}
			if job.ProviderLifecycleEnforcedFromRevision > 0 &&
				t.CreatedRevision >= job.ProviderLifecycleEnforcedFromRevision {
				if err := validateProviderStartSession(job, start, t.ProviderSessionID, t.CreatedRevision, t.CreatedAt); err != nil {
					return err
				}
			}
			foundStart = true
			break
		}
	}
	if !foundStart {
		return errors.New("PlanningTurnIntent Provider Start intent was not found")
	}
	if providerStopIntentExists(job, t.RoleExecutionID, t.ProviderSessionID, t.CreatedRevision) {
		return errors.New("Planning Turn cannot be created after a Provider Stop intent")
	}
	if status, found := latestPlanningResumeStatus(job, t.RoleExecutionID, t.CreatedRevision); found && status != PlanningResumeAttached {
		return errors.New("PlanningTurnIntent follows a resume without an attached Provider Session")
	}
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID != t.PlanningAuthorizationID {
			continue
		}
		if err := grant.Validate(job); err != nil {
			return err
		}
		if t.RequestSHA256 != grant.RequestSHA256 || t.ProviderID != grant.ProviderID || t.AccountID != grant.AccountID ||
			t.ConfigurationSHA256 != grant.ConfigurationSHA256 ||
			t.CreatedAt.Before(execution.CreatedAt) || !t.CreatedAt.Before(grant.Deadline) ||
			t.TurnNumber > grant.Budget.MaxTurns || t.ReservedTokens > grant.Budget.MaxTokens {
			return errors.New("PlanningTurnIntent exceeds PlanningAuthorization")
		}
		return nil
	}
	return errors.New("PlanningTurnIntent PlanningAuthorization was not found")
}

func NewPlanningTurnObservation(job Job, turnID, principalID, commandID, status string, tokensUsed uint64, evidenceSHA256 string, reconcilesUnknown bool, now time.Time) (PlanningTurnObservation, error) {
	now = now.UTC().Round(0)
	if !validCommandID(commandID) || now.IsZero() {
		return PlanningTurnObservation{}, errors.New("planning turn observation command and timestamp are required")
	}
	var turn PlanningTurnIntent
	found := false
	for _, candidate := range job.PlanningTurnIntents {
		if candidate.ID == turnID {
			turn, found = candidate, true
			break
		}
	}
	if !found {
		return PlanningTurnObservation{}, errors.New("PlanningTurnIntent was not found")
	}
	_, _, err := planningGrantAndExecutionForObservation(job, turn.RoleExecutionID, principalID)
	if err != nil {
		return PlanningTurnObservation{}, err
	}
	if status != PlanningTurnUnknown && status != PlanningTurnNotDispatched &&
		status != PlanningTurnCompleted && status != PlanningTurnFailed {
		return PlanningTurnObservation{}, errors.New("unsupported Provider turn observation status")
	}
	switch status {
	case PlanningTurnUnknown:
		if tokensUsed != 0 || evidenceSHA256 != "" || reconcilesUnknown {
			return PlanningTurnObservation{}, errors.New("UNKNOWN observation cannot claim token usage or terminal evidence")
		}
	case PlanningTurnNotDispatched:
		if tokensUsed != 0 || !validSHA256(evidenceSHA256) || reconcilesUnknown {
			return PlanningTurnObservation{}, errors.New("NOT_DISPATCHED observation contains Provider operation claims")
		}
	default:
		if !validSHA256(evidenceSHA256) || tokensUsed > turn.ReservedTokens {
			return PlanningTurnObservation{}, errors.New("terminal turn observation requires evidence and must fit its token reservation")
		}
	}
	var previous *PlanningTurnObservation
	for i := range job.PlanningTurnObservations {
		candidate := &job.PlanningTurnObservations[i]
		if candidate.TurnID == turnID {
			previous = candidate
		}
	}
	if previous == nil {
		if reconcilesUnknown {
			return PlanningTurnObservation{}, errors.New("first turn observation cannot reconcile UNKNOWN")
		}
	} else {
		switch previous.Status {
		case PlanningTurnUnknown:
			if status == PlanningTurnUnknown || status == PlanningTurnNotDispatched || !reconcilesUnknown {
				return PlanningTurnObservation{}, errors.New("only terminal evidence may reconcile UNKNOWN Provider Turn")
			}
		case PlanningTurnNotDispatched:
			if reconcilesUnknown {
				return PlanningTurnObservation{}, errors.New("NOT_DISPATCHED Provider Turn retry cannot reconcile UNKNOWN")
			}
		default:
			return PlanningTurnObservation{}, errors.New("terminal PlanningTurnObservation cannot be replaced")
		}
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return PlanningTurnObservation{}, errors.New("Job revision exhausted")
	}
	observation := PlanningTurnObservation{
		ID: PlanningTurnObservationID(commandID), CommandID: commandID, JobID: job.ID,
		TurnID: turn.ID, OperationID: turn.OperationID, RoleExecutionID: turn.RoleExecutionID,
		ProviderSessionID: turn.ProviderSessionID, Status: status, TokensUsed: tokensUsed,
		EvidenceSHA256: evidenceSHA256, ReconcilesUnknown: reconcilesUnknown,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := observation.Validate(job, turn); err != nil {
		return PlanningTurnObservation{}, err
	}
	return observation, nil
}

func (o PlanningTurnObservation) Validate(job Job, turn PlanningTurnIntent) error {
	if !validPlanningIdentity(o.ID) || !validCommandID(o.CommandID) ||
		o.ID != PlanningTurnObservationID(o.CommandID) || o.JobID != job.ID ||
		o.TurnID != turn.ID || o.OperationID != turn.OperationID ||
		o.RoleExecutionID != turn.RoleExecutionID || o.ProviderSessionID != turn.ProviderSessionID ||
		o.CreatedAt.IsZero() || o.CreatedAt.Before(turn.CreatedAt) || o.CreatedRevision < turn.CreatedRevision+1 {
		return errors.New("PlanningTurnObservation identity or metadata mismatch")
	}
	if o.Status == PlanningTurnUnknown {
		if o.TokensUsed != 0 || o.EvidenceSHA256 != "" || o.ReconcilesUnknown {
			return errors.New("UNKNOWN turn observation contains terminal claims")
		}
		return nil
	}
	if o.Status == PlanningTurnNotDispatched {
		if o.TokensUsed != 0 || !validSHA256(o.EvidenceSHA256) || o.ReconcilesUnknown {
			return errors.New("NOT_DISPATCHED PlanningTurnObservation contains Provider operation claims")
		}
		return nil
	}
	if o.Status != PlanningTurnCompleted && o.Status != PlanningTurnFailed {
		return errors.New("PlanningTurnObservation status is invalid")
	}
	if o.TokensUsed > turn.ReservedTokens || !validSHA256(o.EvidenceSHA256) {
		return errors.New("terminal PlanningTurnObservation evidence or usage is invalid")
	}
	return nil
}

func NewPlanningEffectDecision(job Job, roleExecutionID, principalID, commandID, effect, contextSHA256 string, now time.Time) (PlanningEffectDecision, error) {
	now = now.UTC().Round(0)
	if !validCommandID(commandID) || !validPlanningIdentity(effect) || !validSHA256(contextSHA256) || now.IsZero() {
		return PlanningEffectDecision{}, errors.New("planning effect command, identity, context digest, and timestamp are required")
	}
	grant, execution, err := planningGrantAndExecution(job, roleExecutionID, principalID, now, PlanningBoundaryEffect)
	if err != nil {
		return PlanningEffectDecision{}, err
	}
	decision, reason := PlanningEffectDeny, "outside_planning_authorization_scope"
	if effect == PlanningEffectRepositoryRead && contextSHA256 == grant.ReadOnlyBoundary.SHA256 {
		decision, reason = PlanningEffectAllow, "within_bounded_read_only_context"
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return PlanningEffectDecision{}, errors.New("Job revision exhausted")
	}
	result := PlanningEffectDecision{
		ID: PlanningEffectDecisionID(commandID), CommandID: commandID, JobID: job.ID,
		RoleExecutionID: execution.ID, PlanningAuthorizationID: grant.ID,
		Effect: effect, ContextSHA256: contextSHA256, Decision: decision, Reason: reason,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := result.Validate(job); err != nil {
		return PlanningEffectDecision{}, err
	}
	return result, nil
}

func (d PlanningEffectDecision) Validate(job Job) error {
	if !validPlanningIdentity(d.ID) || !validCommandID(d.CommandID) ||
		d.ID != PlanningEffectDecisionID(d.CommandID) || d.JobID != job.ID ||
		!validPlanningIdentity(d.RoleExecutionID) || !validPlanningIdentity(d.PlanningAuthorizationID) ||
		!validPlanningIdentity(d.Effect) || !validSHA256(d.ContextSHA256) ||
		d.CreatedAt.IsZero() || d.CreatedRevision < 3 {
		return errors.New("PlanningEffectDecision identity or metadata is invalid")
	}
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == d.RoleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution || execution.PlanningAuthorizationID != d.PlanningAuthorizationID {
		return errors.New("PlanningEffectDecision RoleExecution or grant mismatch")
	}
	if d.CreatedRevision <= execution.CreatedRevision {
		return errors.New("PlanningEffectDecision precedes RoleExecution")
	}
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID != d.PlanningAuthorizationID {
			continue
		}
		if err := grant.Validate(job); err != nil {
			return err
		}
		expected, reason := PlanningEffectDeny, "outside_planning_authorization_scope"
		if d.Effect == PlanningEffectRepositoryRead && d.ContextSHA256 == grant.ReadOnlyBoundary.SHA256 {
			expected, reason = PlanningEffectAllow, "within_bounded_read_only_context"
		}
		if d.Decision != expected || d.Reason != reason ||
			d.CreatedAt.Before(execution.CreatedAt) || !d.CreatedAt.Before(grant.Deadline) {
			return errors.New("PlanningEffectDecision violates PlanningAuthorization")
		}
		return nil
	}
	return errors.New("PlanningEffectDecision PlanningAuthorization was not found")
}

func NewPlanningResumeIntent(job Job, roleExecutionID, principalID, providerSessionID, commandID, capabilityEvidenceSHA256 string, reattachmentSupported bool, now time.Time) (PlanningResumeIntent, error) {
	now = now.UTC().Round(0)
	if !validCommandID(commandID) || !validPlanningIdentity(providerSessionID) ||
		!validSHA256(capabilityEvidenceSHA256) || !reattachmentSupported || now.IsZero() {
		return PlanningResumeIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryResume, ReasonCode: "reattachment_not_supported", Cause: errors.New("resume requires the same Provider Session and verified cross-process reattachment capability")}
	}
	grant, execution, err := planningGrantAndExecution(job, roleExecutionID, principalID, now, PlanningBoundaryResume)
	if err != nil {
		return PlanningResumeIntent{}, err
	}
	foundSession := false
	var latestTurnAt time.Time
	for _, turn := range job.PlanningTurnIntents {
		if turn.RoleExecutionID != execution.ID {
			continue
		}
		if turn.ProviderSessionID != providerSessionID {
			return PlanningResumeIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryResume, ReasonCode: "provider_session_mismatch", Cause: errors.New("resume Provider Session does not match the RoleExecution's persisted turns")}
		}
		foundSession = true
		if turn.CreatedAt.After(latestTurnAt) {
			latestTurnAt = turn.CreatedAt
		}
	}
	if !foundSession {
		return PlanningResumeIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryResume, ReasonCode: "provider_session_mismatch", Cause: errors.New("resume Provider Session does not match a persisted turn")}
	}
	var startIntent ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			startIntent, foundStart = candidate, true
			break
		}
	}
	if !foundStart {
		return PlanningResumeIntent{}, errors.New("Provider Start intent was not found for Resume")
	}
	if err := validateProviderStartSession(job, startIntent, providerSessionID, job.Revision+1, now); err != nil {
		return PlanningResumeIntent{}, err
	}
	if now.Before(latestTurnAt) {
		return PlanningResumeIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryResume, ReasonCode: "resume_before_latest_turn", Cause: errors.New("resume intent precedes the latest Provider turn")}
	}
	for _, previous := range job.PlanningResumeIntents {
		if previous.RoleExecutionID != execution.ID {
			continue
		}
		if previous.ProviderSessionID != providerSessionID {
			return PlanningResumeIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryResume, ReasonCode: "provider_session_mismatch", Cause: errors.New("resume cannot replace the Provider Session identity")}
		}
		observations := 0
		lastStatus := ""
		for _, observation := range job.PlanningResumeObservations {
			if observation.ResumeID == previous.ID {
				observations++
				lastStatus = observation.Status
			}
		}
		if observations == 0 || lastStatus == PlanningResumeUnknown {
			return PlanningResumeIntent{}, &PlanningBoundaryError{Boundary: PlanningBoundaryResume, ReasonCode: "previous_resume_unknown", Cause: errors.New("previous Provider resume is unresolved; reconcile the same OperationID before another resume")}
		}
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return PlanningResumeIntent{}, errors.New("Job revision exhausted")
	}
	resume := PlanningResumeIntent{
		ID: PlanningResumeIntentID(commandID), OperationID: PlanningResumeOperationID(commandID),
		CommandID: commandID, JobID: job.ID, RoleExecutionID: execution.ID,
		PlanningAuthorizationID: grant.ID, ProviderSessionID: providerSessionID,
		ProviderID: grant.ProviderID, AccountID: grant.AccountID,
		ConfigurationSHA256:      grant.ConfigurationSHA256,
		CapabilityEvidenceSHA256: capabilityEvidenceSHA256, CrossProcessReattachmentSupported: reattachmentSupported,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := resume.Validate(job); err != nil {
		return PlanningResumeIntent{}, err
	}
	return resume, nil
}

func (r PlanningResumeIntent) Validate(job Job) error {
	if !validPlanningIdentity(r.ID) || !validPlanningIdentity(r.OperationID) ||
		!validCommandID(r.CommandID) || r.ID != PlanningResumeIntentID(r.CommandID) ||
		r.OperationID != PlanningResumeOperationID(r.CommandID) ||
		r.JobID != job.ID || !validPlanningIdentity(r.RoleExecutionID) ||
		!validPlanningIdentity(r.PlanningAuthorizationID) || !validPlanningIdentity(r.ProviderSessionID) ||
		!validPlanningIdentity(r.ProviderID) || !validPlanningIdentity(r.AccountID) ||
		!validSHA256(r.ConfigurationSHA256) || !validSHA256(r.CapabilityEvidenceSHA256) || !r.CrossProcessReattachmentSupported ||
		r.CreatedAt.IsZero() || r.CreatedRevision < 3 {
		return errors.New("PlanningResumeIntent identity or metadata is invalid")
	}
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == r.RoleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution || execution.PlanningAuthorizationID != r.PlanningAuthorizationID {
		return errors.New("PlanningResumeIntent RoleExecution or grant mismatch")
	}
	var latestPriorSessionTurn *PlanningTurnIntent
	for i := range job.PlanningTurnIntents {
		turn := &job.PlanningTurnIntents[i]
		if turn.RoleExecutionID == r.RoleExecutionID && turn.ProviderSessionID == r.ProviderSessionID &&
			turn.CreatedRevision < r.CreatedRevision &&
			(latestPriorSessionTurn == nil || turn.CreatedRevision > latestPriorSessionTurn.CreatedRevision) {
			latestPriorSessionTurn = turn
		}
	}
	if latestPriorSessionTurn == nil || r.CreatedAt.Before(latestPriorSessionTurn.CreatedAt) {
		return errors.New("PlanningResumeIntent does not follow the latest persisted turn for the same Provider Session")
	}
	if providerStopIntentExists(job, r.RoleExecutionID, r.ProviderSessionID, r.CreatedRevision) {
		return errors.New("Planning Resume cannot be created after a Provider Stop intent")
	}
	if job.ProviderLifecycleEnforcedFromRevision > 0 &&
		r.CreatedRevision >= job.ProviderLifecycleEnforcedFromRevision {
		var startIntent ProviderStartIntent
		foundStart := false
		for _, candidate := range job.ProviderStartIntents {
			if candidate.RoleExecutionID == r.RoleExecutionID {
				startIntent, foundStart = candidate, true
				break
			}
		}
		if !foundStart {
			return errors.New("PlanningResumeIntent has no Provider Start intent")
		}
		if err := validateProviderStartSession(job, startIntent, r.ProviderSessionID, r.CreatedRevision, r.CreatedAt); err != nil {
			return err
		}
	}
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID != r.PlanningAuthorizationID {
			continue
		}
		if err := grant.Validate(job); err != nil {
			return err
		}
		if r.ProviderID != grant.ProviderID || r.AccountID != grant.AccountID ||
			r.ConfigurationSHA256 != grant.ConfigurationSHA256 ||
			r.CreatedAt.Before(execution.CreatedAt) || !r.CreatedAt.Before(grant.Deadline) {
			return errors.New("PlanningResumeIntent exceeds PlanningAuthorization scope")
		}
		return nil
	}
	return errors.New("PlanningResumeIntent PlanningAuthorization was not found")
}

func NewPlanningBoundaryDecision(job Job, roleExecutionID, principalID, sourceCommandID, boundary, reasonCode string, now time.Time) (PlanningBoundaryDecision, error) {
	now = now.UTC().Round(0)
	commandID := PlanningBoundaryDecisionCommandID(sourceCommandID, boundary)
	if !validCommandID(sourceCommandID) || !validCommandID(commandID) || now.IsZero() ||
		(boundary != PlanningBoundaryTurn && boundary != PlanningBoundaryEffect && boundary != PlanningBoundaryResume) {
		return PlanningBoundaryDecision{}, errors.New("planning boundary decision identity is invalid")
	}
	if !validPlanningBoundaryReason(reasonCode) {
		return PlanningBoundaryDecision{}, errors.New("planning boundary decision reason code is unsupported")
	}
	if err := job.Validate(); err != nil {
		return PlanningBoundaryDecision{}, err
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return PlanningBoundaryDecision{}, errors.New("Job revision exhausted")
	}
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return PlanningBoundaryDecision{}, errors.New("Taskless PLANNER RoleExecution was not found")
	}
	if job.Revision+1 <= execution.CreatedRevision {
		return PlanningBoundaryDecision{}, errors.New("PlanningBoundaryDecision precedes RoleExecution")
	}
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID != execution.PlanningAuthorizationID {
			continue
		}
		if err := grant.Validate(job); err != nil {
			return PlanningBoundaryDecision{}, err
		}
		if grant.PrincipalID != principalID {
			return PlanningBoundaryDecision{}, errors.New("PlanningAuthorization principal mismatch")
		}
		decision := PlanningBoundaryDecision{
			ID: PlanningBoundaryDecisionID(commandID), CommandID: commandID,
			SourceCommandID: sourceCommandID, JobID: job.ID, RoleExecutionID: execution.ID,
			PlanningAuthorizationID: grant.ID, PrincipalID: principalID,
			Boundary: boundary, Decision: PlanningEffectDeny, ReasonCode: reasonCode,
			RequestSHA256: grant.RequestSHA256, CreatedAt: now, CreatedRevision: job.Revision + 1,
		}
		if err := decision.Validate(job); err != nil {
			return PlanningBoundaryDecision{}, err
		}
		return decision, nil
	}
	return PlanningBoundaryDecision{}, errors.New("PlanningAuthorization was not found")
}

func (d PlanningBoundaryDecision) Validate(job Job) error {
	if !validPlanningIdentity(d.ID) || !validCommandID(d.CommandID) ||
		d.ID != PlanningBoundaryDecisionID(d.CommandID) ||
		d.CommandID != PlanningBoundaryDecisionCommandID(d.SourceCommandID, d.Boundary) ||
		!validCommandID(d.SourceCommandID) || d.JobID != job.ID ||
		!validPlanningIdentity(d.RoleExecutionID) || !validPlanningIdentity(d.PlanningAuthorizationID) ||
		!validPlanningIdentity(d.PrincipalID) || !validSHA256(d.RequestSHA256) ||
		(d.Boundary != PlanningBoundaryTurn && d.Boundary != PlanningBoundaryEffect && d.Boundary != PlanningBoundaryResume) ||
		d.Decision != PlanningEffectDeny || !validPlanningBoundaryReason(d.ReasonCode) || d.CreatedAt.IsZero() || d.CreatedRevision < 3 {
		return errors.New("PlanningBoundaryDecision identity or metadata is invalid")
	}
	for _, execution := range job.RoleExecutions {
		if execution.ID != d.RoleExecutionID || execution.PlanningAuthorizationID != d.PlanningAuthorizationID {
			continue
		}
		for _, grant := range job.PlanningAuthorizations {
			if grant.ID != d.PlanningAuthorizationID {
				continue
			}
			if err := grant.Validate(job); err != nil {
				return err
			}
			if grant.PrincipalID != d.PrincipalID || grant.RequestSHA256 != d.RequestSHA256 ||
				d.CreatedAt.Before(execution.CreatedAt) || d.CreatedRevision <= execution.CreatedRevision {
				return errors.New("PlanningBoundaryDecision does not match its authorization")
			}
			return nil
		}
	}
	return errors.New("PlanningBoundaryDecision RoleExecution or PlanningAuthorization was not found")
}

func NewPlanningResumeObservation(job Job, resumeID, principalID, commandID, status, evidenceSHA256 string, reconcilesUnknown bool, now time.Time) (PlanningResumeObservation, error) {
	now = now.UTC().Round(0)
	if !validCommandID(commandID) || now.IsZero() {
		return PlanningResumeObservation{}, errors.New("resume observation command and timestamp are required")
	}
	var resume PlanningResumeIntent
	found := false
	for _, candidate := range job.PlanningResumeIntents {
		if candidate.ID == resumeID {
			resume, found = candidate, true
			break
		}
	}
	if !found {
		return PlanningResumeObservation{}, errors.New("PlanningResumeIntent was not found")
	}
	if _, _, err := planningGrantAndExecutionForObservation(job, resume.RoleExecutionID, principalID); err != nil {
		return PlanningResumeObservation{}, err
	}
	if status != PlanningResumeUnknown && status != PlanningResumeAttached && status != PlanningResumeFailed {
		return PlanningResumeObservation{}, errors.New("unsupported Provider resume observation status")
	}
	if status == PlanningResumeUnknown {
		if evidenceSHA256 != "" || reconcilesUnknown {
			return PlanningResumeObservation{}, errors.New("UNKNOWN resume observation cannot claim terminal evidence")
		}
	} else if !validSHA256(evidenceSHA256) {
		return PlanningResumeObservation{}, errors.New("terminal resume observation requires evidence")
	}
	var previous *PlanningResumeObservation
	for i := range job.PlanningResumeObservations {
		candidate := &job.PlanningResumeObservations[i]
		if candidate.ResumeID == resumeID {
			previous = candidate
		}
	}
	if previous == nil {
		if reconcilesUnknown {
			return PlanningResumeObservation{}, errors.New("first resume observation cannot reconcile UNKNOWN")
		}
	} else if previous.Status != PlanningResumeUnknown || status == PlanningResumeUnknown ||
		!reconcilesUnknown || !validSHA256(evidenceSHA256) {
		return PlanningResumeObservation{}, errors.New("only UNKNOWN may be reconciled by same-operation evidence; blind replay is prohibited")
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return PlanningResumeObservation{}, errors.New("Job revision exhausted")
	}
	observation := PlanningResumeObservation{
		ID: PlanningResumeObservationID(commandID), CommandID: commandID,
		JobID: job.ID, ResumeID: resume.ID, OperationID: resume.OperationID,
		RoleExecutionID: resume.RoleExecutionID, ProviderSessionID: resume.ProviderSessionID,
		Status: status, EvidenceSHA256: evidenceSHA256, ReconcilesUnknown: reconcilesUnknown,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := observation.Validate(job, resume); err != nil {
		return PlanningResumeObservation{}, err
	}
	return observation, nil
}

func (o PlanningResumeObservation) Validate(job Job, resume PlanningResumeIntent) error {
	if !validPlanningIdentity(o.ID) || !validCommandID(o.CommandID) ||
		o.ID != PlanningResumeObservationID(o.CommandID) || o.JobID != job.ID ||
		o.ResumeID != resume.ID || o.OperationID != resume.OperationID ||
		o.RoleExecutionID != resume.RoleExecutionID || o.ProviderSessionID != resume.ProviderSessionID ||
		o.CreatedAt.IsZero() || o.CreatedAt.Before(resume.CreatedAt) ||
		o.CreatedRevision < resume.CreatedRevision+1 {
		return errors.New("PlanningResumeObservation identity or metadata mismatch")
	}
	if o.Status == PlanningResumeUnknown {
		if o.EvidenceSHA256 != "" || o.ReconcilesUnknown {
			return errors.New("UNKNOWN resume observation contains terminal claims")
		}
		return nil
	}
	if (o.Status != PlanningResumeAttached && o.Status != PlanningResumeFailed) || !validSHA256(o.EvidenceSHA256) {
		return errors.New("terminal PlanningResumeObservation evidence or status is invalid")
	}
	return nil
}
