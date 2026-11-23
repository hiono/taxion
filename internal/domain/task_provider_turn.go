package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

const (
	TaskTurnUnknown = "UNKNOWN"
	TaskTurnCompleted = "COMPLETED"
	TaskTurnFailed = "FAILED"
)

type TaskTurnIntent struct {
	ID string `json:"id"`
	OperationID string `json:"operation_id"`
	CommandID string `json:"command_id"`
	JobID string `json:"job_id"`
	TaskID string `json:"task_id"`
	RoleExecutionID string `json:"role_execution_id"`
	ExecutionPermissionID string `json:"execution_permission_id"`
	ProviderStartID string `json:"provider_start_id"`
	ProviderSessionID string `json:"provider_session_id"`
	ProviderID string `json:"provider_id"`
	AccountID string `json:"account_id"`
	ConfigurationSHA256 string `json:"configuration_sha256"`
	TurnNumber uint32 `json:"turn_number"`
	PromptSHA256 string `json:"prompt_sha256"`
	ReservedTokens uint64 `json:"reserved_tokens"`
	CreatedAt time.Time `json:"created_at"`
	CreatedRevision uint64 `json:"created_revision"`
}

type TaskTurnObservation struct {
	ID string `json:"id"`
	CommandID string `json:"command_id"`
	JobID string `json:"job_id"`
	TurnID string `json:"turn_id"`
	OperationID string `json:"operation_id"`
	RoleExecutionID string `json:"role_execution_id"`
	ProviderSessionID string `json:"provider_session_id"`
	Status string `json:"status"`
	TokensUsed uint64 `json:"tokens_used"`
	EvidenceSHA256 string `json:"evidence_sha256,omitempty"`
	ReconcilesUnknown bool `json:"reconciles_unknown,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	CreatedRevision uint64 `json:"created_revision"`
}

func taskTurnID(kind, commandID string) string {
	sum := sha256.Sum256([]byte("taxion:" + kind + ":" + commandID))
	return kind + "_" + hex.EncodeToString(sum[:])
}
func TaskTurnIntentID(commandID string) string { return taskTurnID("task_turn", commandID) }
func TaskTurnOperationID(commandID string) string { return taskTurnID("task_provider_turn", commandID) }
func TaskTurnObservationID(commandID string) string { return taskTurnID("task_turn_observation", commandID) }

func NewTaskTurnIntent(job Job, roleExecutionID, permissionID, sessionID, commandID, promptSHA256 string, reservedTokens uint64, now time.Time) (TaskTurnIntent, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil { return TaskTurnIntent{}, fmt.Errorf("validate Job before Task Turn: %w", err) }
	if !validCommandID(commandID) || !validPlanningIdentity(permissionID) || !validPlanningIdentity(sessionID) ||
		!validSHA256(promptSHA256) || reservedTokens == 0 || now.IsZero() {
		return TaskTurnIntent{}, errors.New("Task Turn identity, prompt digest, reservation, and timestamp are required")
	}
	var execution RoleExecution
	found := false
	for _, candidate := range job.RoleExecutions { if candidate.ID == roleExecutionID { execution, found = candidate, true; break } }
	if !found || execution.Role != RoleCoder || execution.TaskID == "" || execution.ExecutionPermissionID != permissionID {
		return TaskTurnIntent{}, errors.New("Task Turn requires the matching Task-bound Coder RoleExecution")
	}
	permission, found := executionPermissionByID(job, permissionID)
	if !found || permission.Role != RoleCoder || permission.TaskID != execution.TaskID ||
		permission.ValidateFor(job, permission.OperationID, permission.RequestSHA256, now) != nil {
		return TaskTurnIntent{}, errors.New("Task Turn requires the current JobApproval-backed Coder ExecutionPermission")
	}
	var start ProviderStartIntent
	found = false
	for _, candidate := range job.ProviderStartIntents { if candidate.RoleExecutionID == execution.ID { start, found = candidate, true; break } }
	if !found || start.ID != permission.OperationID || start.ExecutionPermissionID != permission.ID ||
		start.ProviderID != permission.ProviderID || start.AccountID != permission.AccountID ||
		start.ConfigurationSHA256 != permission.ConfigurationSHA256 {
		return TaskTurnIntent{}, errors.New("Task Turn Provider Start differs from ExecutionPermission")
	}
	startObservation, found := latestProviderStartObservation(job, start.ID)
	if !found || startObservation.Status != ProviderStartAccepted || startObservation.ProviderSessionID != sessionID {
		return TaskTurnIntent{}, errors.New("Task Turn requires the accepted Provider Start Session")
	}
	if providerStopIntentExists(job, execution.ID, sessionID, job.Revision+1) {
		return TaskTurnIntent{}, errors.New("Task Turn cannot start after Provider Stop intent")
	}
	var count uint32
	var tokensUsed uint64
	for _, previous := range job.TaskTurnIntents {
		if previous.RoleExecutionID != execution.ID { continue }
		if previous.ProviderSessionID != sessionID { return TaskTurnIntent{}, errors.New("Task Turn cannot change Provider Session") }
		count++
		last, observed := latestTaskTurnObservation(job, previous.ID)
		if !observed || last.Status == TaskTurnUnknown { return TaskTurnIntent{}, errors.New("previous Task Turn is unresolved") }
		if ^uint64(0)-tokensUsed < last.TokensUsed { return TaskTurnIntent{}, errors.New("Task Turn token usage overflow") }
		tokensUsed += last.TokensUsed
	}
	if count >= uint32(permission.ReservedTurns) || tokensUsed > permission.ReservedTokens ||
		reservedTokens > permission.ReservedTokens-tokensUsed {
		return TaskTurnIntent{}, errors.New("Task Turn exceeds ExecutionPermission turn or token budget")
	}
	if now.Before(startObservation.CreatedAt) || !now.Before(permission.ExpiresAt) ||
		!now.Before(start.Deadline) || now.Before(execution.CreatedAt) {
		return TaskTurnIntent{}, errors.New("Task Turn is outside accepted Start or permission deadline")
	}
	if job.Revision >= uint64(^uint64(0)>>1) { return TaskTurnIntent{}, errors.New("Job revision exhausted") }
	turn := TaskTurnIntent{
		ID: TaskTurnIntentID(commandID), OperationID: TaskTurnOperationID(commandID), CommandID: commandID,
		JobID: job.ID, TaskID: execution.TaskID, RoleExecutionID: execution.ID,
		ExecutionPermissionID: permission.ID, ProviderStartID: start.ID, ProviderSessionID: sessionID,
		ProviderID: permission.ProviderID, AccountID: permission.AccountID, ConfigurationSHA256: permission.ConfigurationSHA256,
		TurnNumber: count+1, PromptSHA256: promptSHA256, ReservedTokens: reservedTokens,
		CreatedAt: now, CreatedRevision: job.Revision+1,
	}
	if err := turn.Validate(job); err != nil { return TaskTurnIntent{}, err }
	return turn, nil
}

func (t TaskTurnIntent) Validate(job Job) error {
	if !validCommandID(t.CommandID) || t.ID != TaskTurnIntentID(t.CommandID) ||
		t.OperationID != TaskTurnOperationID(t.CommandID) || t.JobID != job.ID ||
		!validPlanningIdentity(t.TaskID) || !validPlanningIdentity(t.RoleExecutionID) ||
		!validPlanningIdentity(t.ExecutionPermissionID) || !validPlanningIdentity(t.ProviderStartID) ||
		!validPlanningIdentity(t.ProviderSessionID) || !validPlanningIdentity(t.ProviderID) ||
		!validPlanningIdentity(t.AccountID) || !validSHA256(t.ConfigurationSHA256) ||
		!validSHA256(t.PromptSHA256) || t.TurnNumber == 0 || t.ReservedTokens == 0 ||
		t.CreatedAt.IsZero() || t.CreatedRevision == 0 { return errors.New("TaskTurnIntent identity or metadata is invalid") }
	var execution RoleExecution
	found := false
	for _, candidate := range job.RoleExecutions { if candidate.ID == t.RoleExecutionID { execution, found = candidate, true; break } }
	permission, permissionFound := executionPermissionByID(job, t.ExecutionPermissionID)
	if !found || !permissionFound || execution.Role != RoleCoder || execution.TaskID != t.TaskID ||
		execution.ExecutionPermissionID != t.ExecutionPermissionID || permission.Role != RoleCoder ||
		permission.TaskID != t.TaskID || permission.OperationID != t.ProviderStartID ||
		permission.ProviderID != t.ProviderID || permission.AccountID != t.AccountID ||
		permission.ConfigurationSHA256 != t.ConfigurationSHA256 || permission.Validate(job) != nil {
		return errors.New("TaskTurnIntent is not bound to its approved Coder execution")
	}
	var start ProviderStartIntent
	found = false
	for _, candidate := range job.ProviderStartIntents { if candidate.ID == t.ProviderStartID && candidate.RoleExecutionID == execution.ID { start, found = candidate, true; break } }
	accepted, acceptedFound := latestProviderStartObservation(job, t.ProviderStartID)
	if !found || !acceptedFound || accepted.Status != ProviderStartAccepted ||
		accepted.ProviderSessionID != t.ProviderSessionID || t.CreatedRevision <= accepted.CreatedRevision ||
		t.CreatedAt.Before(accepted.CreatedAt) || t.CreatedRevision <= start.CreatedRevision ||
		t.CreatedAt.Before(start.CreatedAt) || providerStopIntentExists(job, execution.ID, t.ProviderSessionID, t.CreatedRevision) ||
		t.CreatedAt.Before(permission.CreatedAt) || !t.CreatedAt.Before(permission.ExpiresAt) ||
		!t.CreatedAt.Before(start.Deadline) || t.TurnNumber > permission.ReservedTurns ||
		t.ReservedTokens > permission.ReservedTokens {
		return errors.New("TaskTurnIntent does not follow accepted Start or exceeds permission bounds")
	}
	return nil
}

func NewTaskTurnObservation(job Job, turnID, commandID, status string, tokensUsed uint64, evidenceSHA256 string, reconcilesUnknown bool, now time.Time) (TaskTurnObservation, error) {
	now = now.UTC().Round(0)
	if !validCommandID(commandID) || now.IsZero() { return TaskTurnObservation{}, errors.New("Task Turn observation command and timestamp are required") }
	var turn TaskTurnIntent
	found := false
	for _, candidate := range job.TaskTurnIntents { if candidate.ID == turnID { turn, found = candidate, true; break } }
	if !found { return TaskTurnObservation{}, errors.New("TaskTurnIntent was not found") }
	if status != TaskTurnUnknown && status != TaskTurnCompleted && status != TaskTurnFailed { return TaskTurnObservation{}, errors.New("unsupported Task Turn status") }
	if status == TaskTurnUnknown {
		if tokensUsed != 0 || evidenceSHA256 != "" || reconcilesUnknown { return TaskTurnObservation{}, errors.New("UNKNOWN cannot claim terminal usage or evidence") }
	} else if !validSHA256(evidenceSHA256) || tokensUsed > turn.ReservedTokens { return TaskTurnObservation{}, errors.New("terminal Task Turn requires evidence and bounded token usage") }
	previous, exists := latestTaskTurnObservation(job, turnID)
	if !exists && reconcilesUnknown { return TaskTurnObservation{}, errors.New("first observation cannot reconcile UNKNOWN") }
	if exists && (previous.Status != TaskTurnUnknown || status == TaskTurnUnknown || !reconcilesUnknown) {
		return TaskTurnObservation{}, errors.New("only same-Turn UNKNOWN may be reconciled by terminal evidence")
	}
	if job.Revision >= uint64(^uint64(0)>>1) { return TaskTurnObservation{}, errors.New("Job revision exhausted") }
	observation := TaskTurnObservation{
		ID: TaskTurnObservationID(commandID), CommandID: commandID, JobID: job.ID, TurnID: turn.ID,
		OperationID: turn.OperationID, RoleExecutionID: turn.RoleExecutionID, ProviderSessionID: turn.ProviderSessionID,
		Status: status, TokensUsed: tokensUsed, EvidenceSHA256: evidenceSHA256,
		ReconcilesUnknown: reconcilesUnknown, CreatedAt: now, CreatedRevision: job.Revision+1,
	}
	if err := observation.Validate(job, turn); err != nil { return TaskTurnObservation{}, err }
	return observation, nil
}

func (o TaskTurnObservation) Validate(job Job, turn TaskTurnIntent) error {
	if !validCommandID(o.CommandID) || o.ID != TaskTurnObservationID(o.CommandID) ||
		o.JobID != job.ID || o.TurnID != turn.ID || o.OperationID != turn.OperationID ||
		o.RoleExecutionID != turn.RoleExecutionID || o.ProviderSessionID != turn.ProviderSessionID ||
		o.CreatedAt.IsZero() || o.CreatedAt.Before(turn.CreatedAt) || o.CreatedRevision <= turn.CreatedRevision {
		return errors.New("TaskTurnObservation identity or metadata mismatch")
	}
	if o.Status == TaskTurnUnknown {
		if o.TokensUsed != 0 || o.EvidenceSHA256 != "" || o.ReconcilesUnknown { return errors.New("UNKNOWN observation contains terminal claims") }
		return nil
	}
	if (o.Status != TaskTurnCompleted && o.Status != TaskTurnFailed) ||
		o.TokensUsed > turn.ReservedTokens || !validSHA256(o.EvidenceSHA256) {
		return errors.New("terminal TaskTurnObservation status, usage, or evidence is invalid")
	}
	return nil
}

func latestTaskTurnObservation(job Job, turnID string) (TaskTurnObservation, bool) {
	var latest TaskTurnObservation
	found := false
	for _, observation := range job.TaskTurnObservations { if observation.TurnID == turnID { latest, found = observation, true } }
	return latest, found
}

func validateTaskProviderTurnFacts(job Job) error {
	seenIDs, seenCommands, seenOperations := map[string]struct{}{}, map[string]struct{}{}, map[string]struct{}{}
	byExecution := map[string][]TaskTurnIntent{}
	for _, turn := range job.TaskTurnIntents {
		if err := turn.Validate(job); err != nil { return fmt.Errorf("%w: invalid TaskTurnIntent: %v", ErrInvalidJob, err) }
		if turn.CreatedAt.After(job.UpdatedAt) || turn.CreatedRevision > job.Revision { return fmt.Errorf("%w: TaskTurnIntent append order invalid", ErrInvalidJob) }
		if _, ok := seenIDs[turn.ID]; ok { return fmt.Errorf("%w: duplicate TaskTurnIntent", ErrInvalidJob) }
		if _, ok := seenCommands[turn.CommandID]; ok { return fmt.Errorf("%w: duplicate TaskTurn command", ErrInvalidJob) }
		if _, ok := seenOperations[turn.OperationID]; ok { return fmt.Errorf("%w: duplicate TaskTurn operation", ErrInvalidJob) }
		seenIDs[turn.ID], seenCommands[turn.CommandID], seenOperations[turn.OperationID] = struct{}{}, struct{}{}, struct{}{}
		byExecution[turn.RoleExecutionID] = append(byExecution[turn.RoleExecutionID], turn)
	}
	seenObservations := map[string]struct{}{}
	byTurn := map[string][]TaskTurnObservation{}
	for _, observation := range job.TaskTurnObservations {
		var turn TaskTurnIntent
		found := false
		for _, candidate := range job.TaskTurnIntents { if candidate.ID == observation.TurnID { turn, found = candidate, true; break } }
		if !found { return fmt.Errorf("%w: TaskTurnObservation has no intent", ErrInvalidJob) }
		if err := observation.Validate(job, turn); err != nil { return fmt.Errorf("%w: invalid TaskTurnObservation: %v", ErrInvalidJob, err) }
		if observation.CreatedAt.After(job.UpdatedAt) || observation.CreatedRevision > job.Revision { return fmt.Errorf("%w: TaskTurnObservation append order invalid", ErrInvalidJob) }
		if _, ok := seenObservations[observation.ID]; ok { return fmt.Errorf("%w: duplicate TaskTurnObservation", ErrInvalidJob) }
		seenObservations[observation.ID] = struct{}{}
		byTurn[observation.TurnID] = append(byTurn[observation.TurnID], observation)
	}
	for executionID, turns := range byExecution {
		for index, turn := range turns {
			if turn.TurnNumber != uint32(index+1) { return fmt.Errorf("%w: Task Turn numbering is not contiguous", ErrInvalidJob) }
			if index > 0 {
				previous := turns[index-1]
				observations := byTurn[previous.ID]
				if len(observations) == 0 || observations[len(observations)-1].Status == TaskTurnUnknown ||
					observations[len(observations)-1].CreatedRevision >= turn.CreatedRevision {
					return fmt.Errorf("%w: Task Turn starts before previous Turn is terminal for %s", ErrInvalidJob, executionID)
				}
			}
		}
	}
	for turnID, observations := range byTurn {
		for index, observation := range observations {
			if index == 0 { if observation.ReconcilesUnknown { return fmt.Errorf("%w: first Task Turn observation reconciles UNKNOWN", ErrInvalidJob) }; continue }
			previous := observations[index-1]
			if previous.Status != TaskTurnUnknown || observation.Status == TaskTurnUnknown ||
				!observation.ReconcilesUnknown || observation.CreatedRevision <= previous.CreatedRevision ||
				observation.CreatedAt.Before(previous.CreatedAt) {
				return fmt.Errorf("%w: invalid Task Turn reconciliation for %s", ErrInvalidJob, turnID)
			}
		}
		if len(observations) > 2 || (len(observations) == 2 && observations[0].Status != TaskTurnUnknown) {
			return fmt.Errorf("%w: invalid Task Turn observation history for %s", ErrInvalidJob, turnID)
		}
	}
	return nil
}
