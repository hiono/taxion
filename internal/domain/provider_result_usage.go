package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
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

type ProviderUsageObservation struct {
	ID              string    `json:"id"`
	CommandID       string    `json:"command_id"`
	JobID           string    `json:"job_id"`
	RoleExecutionID string    `json:"role_execution_id"`
	OperationID     string    `json:"operation_id"`
	TurnID          string    `json:"turn_id,omitempty"`
	SessionID       string    `json:"session_id"`
	UsageID         string    `json:"usage_id"`
	Tokens          uint64    `json:"tokens"`
	Complete        bool      `json:"complete"`
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

func ProviderUsageObservationCommandID(operationID, turnID, sessionID, usageID string, tokens uint64, complete bool, evidenceSHA256 string) string {
	material := operationID + "\x00" + turnID + "\x00" + sessionID + "\x00" + usageID +
		"\x00" + strconv.FormatUint(tokens, 10) + "\x00" + evidenceSHA256
	if complete {
		material += "\x00complete"
	} else {
		material += "\x00partial"
	}
	sum := sha256.Sum256([]byte("taxion:provider-usage-observation-command:" + material))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func ProviderUsageObservationID(commandID string) string {
	return planningOperationID("provider_usage_observation", commandID)
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
	authorizedPrincipalID, _, err := providerResultUsageExecution(job, roleExecutionID)
	if err != nil {
		return ProviderResultObservation{}, err
	}
	if authorizedPrincipalID != principalID {
		return ProviderResultObservation{}, errors.New("Provider Result Principal does not match the approved execution authority")
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
	_, _, minimumRevision, minimumTime, err := providerResultUsageBinding(
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

func NewProviderUsageObservation(
	job Job,
	roleExecutionID, principalID, operationID, turnID, sessionID, usageID string,
	tokens uint64,
	complete bool,
	evidenceSHA256 string,
	now time.Time,
) (ProviderUsageObservation, error) {
	if err := job.Validate(); err != nil {
		return ProviderUsageObservation{}, err
	}
	authorizedPrincipalID, _, err := providerResultUsageExecution(job, roleExecutionID)
	if err != nil {
		return ProviderUsageObservation{}, err
	}
	if authorizedPrincipalID != principalID {
		return ProviderUsageObservation{}, errors.New("Provider Usage Principal does not match the approved execution authority")
	}
	now = now.UTC().Round(0)
	if now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return ProviderUsageObservation{}, errors.New("Provider Usage observation timestamp or Job revision is invalid")
	}
	commandID := ProviderUsageObservationCommandID(operationID, turnID, sessionID, usageID, tokens, complete, evidenceSHA256)
	observation := ProviderUsageObservation{
		ID: ProviderUsageObservationID(commandID), CommandID: commandID, JobID: job.ID,
		RoleExecutionID: roleExecutionID, OperationID: operationID, TurnID: turnID, SessionID: sessionID,
		UsageID: usageID, Tokens: tokens, Complete: complete, EvidenceSHA256: evidenceSHA256,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	validationJob := job
	if validationJob.ProviderUsageEnforcedFromRevision == 0 {
		validationJob.ProviderUsageEnforcedFromRevision = observation.CreatedRevision
	}
	if err := observation.Validate(validationJob); err != nil {
		return ProviderUsageObservation{}, err
	}
	for _, existing := range job.ProviderUsageObservations {
		if existing.SessionID == sessionID && existing.UsageID == usageID {
			if existing.OperationID != operationID || existing.TurnID != turnID {
				return ProviderUsageObservation{}, errors.New("Provider Usage identity is already bound to another operation")
			}
			if existing.Complete {
				return ProviderUsageObservation{}, errors.New("Provider Usage is already complete")
			}
		}
	}
	return observation, nil
}

func (o ProviderUsageObservation) Validate(job Job) error {
	if !validPlanningIdentity(o.ID) || !validCommandID(o.CommandID) ||
		o.ID != ProviderUsageObservationID(o.CommandID) ||
		o.CommandID != ProviderUsageObservationCommandID(o.OperationID, o.TurnID, o.SessionID, o.UsageID, o.Tokens, o.Complete, o.EvidenceSHA256) ||
		o.JobID != job.ID || !validPlanningIdentity(o.RoleExecutionID) ||
		!validPlanningIdentity(o.OperationID) || !validPlanningIdentity(o.SessionID) ||
		(o.TurnID != "" && !validPlanningIdentity(o.TurnID)) ||
		!validPlanningIdentity(o.UsageID) || !validSHA256(o.EvidenceSHA256) ||
		o.CreatedAt.IsZero() || o.CreatedRevision == 0 {
		return errors.New("Provider Usage observation identity or evidence is invalid")
	}
	_, _, minimumRevision, minimumTime, err := providerResultUsageBinding(
		job, o.RoleExecutionID, o.OperationID, o.TurnID, o.SessionID,
	)
	if err != nil {
		return err
	}
	if o.CreatedRevision <= minimumRevision || o.CreatedAt.Before(minimumTime) {
		return errors.New("Provider Usage observation does not follow its accepted Provider operation")
	}
	usageRulesEnforced := job.ProviderUsageEnforcedFromRevision > 0 &&
		o.CreatedRevision >= job.ProviderUsageEnforcedFromRevision
	if o.TurnID != "" && usageRulesEnforced {
		reservedTokens, found := providerTurnReservedTokens(job, o.TurnID)
		if !found || o.Tokens > reservedTokens {
			return errors.New("Provider Usage exceeds its reserved Turn token budget")
		}
	}
	if o.TurnID != "" && usageRulesEnforced {
		for _, existing := range job.ProviderUsageObservations {
			if existing.OperationID == o.OperationID && existing.TurnID == o.TurnID &&
				existing.Complete && existing.CreatedRevision < o.CreatedRevision {
				return errors.New("Task Turn already has a complete Provider Usage observation")
			}
		}
	}
	if o.TurnID != "" {
		for _, observation := range job.TaskTurnObservations {
			if observation.TurnID == o.TurnID && observation.Status != TaskTurnUnknown &&
				o.CreatedRevision > observation.CreatedRevision {
				return errors.New("Provider Usage cannot be appended after a terminal Task Turn observation")
			}
		}
	}
	return nil
}

func providerResultUsageExecution(job Job, roleExecutionID string) (string, RoleExecution, error) {
	var execution RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return "", RoleExecution{}, errors.New("Provider observation RoleExecution was not found")
	}
	if execution.Role == RolePlanner && execution.TaskID == "" {
		for _, grant := range job.PlanningAuthorizations {
			if grant.ID != execution.PlanningAuthorizationID {
				continue
			}
			if err := grant.Validate(job); err != nil {
				return "", RoleExecution{}, err
			}
			return grant.PrincipalID, execution, nil
		}
		return "", RoleExecution{}, errors.New("Provider observation PlanningAuthorization was not found")
	}
	if execution.Role != RoleCoder || execution.TaskID == "" || execution.ExecutionPermissionID == "" {
		return "", RoleExecution{}, errors.New("Provider Result / Usage requires a Planner or Task-bound Coder execution")
	}
	permission, found := executionPermissionByID(job, execution.ExecutionPermissionID)
	if !found || permission.Role != RoleCoder || permission.TaskID != execution.TaskID || permission.Validate(job) != nil {
		return "", RoleExecution{}, errors.New("Provider observation ExecutionPermission is invalid")
	}
	var revision *JobContractRevision
	for i := range job.JobContractRevisions {
		if job.JobContractRevisions[i].Revision == permission.ContractRevision {
			revision = &job.JobContractRevisions[i]
			break
		}
	}
	approval, found := jobApprovalForRevision(job, permission.ContractRevision)
	if revision == nil || !found || approval.ID != permission.JobApprovalID {
		return "", RoleExecution{}, errors.New("Provider observation JobApproval is missing")
	}
	if err := approval.Validate(job, *revision); err != nil {
		return "", RoleExecution{}, err
	}
	return approval.ApproverPrincipalID, execution, nil
}

func providerResultUsageBinding(job Job, roleExecutionID, operationID, turnID, sessionID string) (ProviderStartIntent, *ProviderStartObservation, uint64, time.Time, error) {
	_, _, err := providerResultUsageExecution(job, roleExecutionID)
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
	for _, turn := range job.TaskTurnIntents {
		if turn.ID != turnID {
			continue
		}
		if turn.OperationID != operationID || turn.RoleExecutionID != roleExecutionID ||
			turn.ProviderSessionID != sessionID || turn.CreatedRevision <= accepted.CreatedRevision ||
			turn.Validate(job) != nil {
			return ProviderStartIntent{}, nil, 0, time.Time{}, errors.New("Provider observation does not match its Task Turn intent")
		}
		if turn.CreatedRevision > minimumRevision {
			minimumRevision = turn.CreatedRevision
			minimumTime = turn.CreatedAt
		}
		return start, accepted, minimumRevision, minimumTime, nil
	}
	return ProviderStartIntent{}, nil, 0, time.Time{}, errors.New("Provider observation Turn intent was not found")
}

func providerTurnReservedTokens(job Job, turnID string) (uint64, bool) {
	var reserved uint64
	found := false
	for _, turn := range job.PlanningTurnIntents {
		if turn.ID == turnID {
			if found {
				return 0, false
			}
			reserved, found = turn.ReservedTokens, true
		}
	}
	for _, turn := range job.TaskTurnIntents {
		if turn.ID == turnID {
			if found {
				return 0, false
			}
			reserved, found = turn.ReservedTokens, true
		}
	}
	return reserved, found
}

func sha256HexDomain(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
