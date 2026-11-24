package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	TaskTurnDispatchReleasedCompleted         = "completed"
	TaskTurnDispatchReleasedFailed            = "failed"
	TaskTurnDispatchReleasedDefinitiveFailure = "definitive_failure"
	TaskTurnDispatchReleasedIdempotentRetry = "idempotent_retry"
)

type TaskTurnDispatchClaim struct {
	ID              string    `json:"id"`
	CommandID       string    `json:"command_id"`
	JobID           string    `json:"job_id"`
	TurnID          string    `json:"turn_id"`
	RoleExecutionID string    `json:"role_execution_id"`
	OperationID     string    `json:"operation_id"`
	SessionID       string    `json:"session_id"`
	PrincipalID     string    `json:"principal_id"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedRevision uint64    `json:"created_revision"`
}

type TaskTurnDispatchRelease struct {
	ID              string    `json:"id"`
	CommandID       string    `json:"command_id"`
	JobID           string    `json:"job_id"`
	ClaimID         string    `json:"claim_id"`
	OperationID     string    `json:"operation_id"`
	PrincipalID     string    `json:"principal_id"`
	Outcome         string    `json:"outcome"`
	EvidenceSHA256  string    `json:"evidence_sha256,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedRevision uint64    `json:"created_revision"`
}

func TaskTurnDispatchClaimID(commandID string) string {
	return planningOperationID("task_turn_dispatch_claim", commandID)
}

func TaskTurnDispatchReleaseID(commandID string) string {
	return planningOperationID("task_turn_dispatch_release", commandID)
}

func TaskTurnDispatchReleaseCommandID(claimID, outcome string) string {
	sum := sha256.Sum256([]byte("taxion:task-turn-dispatch-release:" + claimID + ":" + outcome))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func NewTaskTurnDispatchClaim(
	job Job, turnID, principalID, commandID string, now time.Time,
) (TaskTurnDispatchClaim, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil {
		return TaskTurnDispatchClaim{}, fmt.Errorf("validate Job before Task Turn dispatch claim: %w", err)
	}
	if !validCommandID(commandID) || now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return TaskTurnDispatchClaim{}, errors.New("Task Turn dispatch claim command and timestamp are required")
	}
	var turn TaskTurnIntent
	found := false
	for _, candidate := range job.TaskTurnIntents {
		if candidate.ID == turnID {
			turn, found = candidate, true
			break
		}
	}
	if !found {
		return TaskTurnDispatchClaim{}, errors.New("Task Turn dispatch claim requires a persisted Turn intent")
	}
	if err := turn.Validate(job); err != nil {
		return TaskTurnDispatchClaim{}, err
	}
	if _, active := ActiveTaskTurnDispatchClaim(job, turn.OperationID); active {
		return TaskTurnDispatchClaim{}, errors.New("Task Turn operation already has an active dispatch claim")
	}
	if _, found := latestTaskTurnObservation(job, turn.ID); found {
		observation, _ := latestTaskTurnObservation(job, turn.ID)
		if observation.Status != TaskTurnUnknown {
			return TaskTurnDispatchClaim{}, errors.New("terminal Task Turn cannot acquire a dispatch claim")
		}
	}
	for _, stopIntent := range job.ProviderStopIntents {
		if stopIntent.RoleExecutionID == turn.RoleExecutionID && stopIntent.SessionID == turn.ProviderSessionID {
			return TaskTurnDispatchClaim{}, errors.New("Task Turn dispatch is blocked by an existing Stop intent")
		}
	}
	if _, active := ActiveProviderStopDispatchClaim(job, turn.OperationID); active {
		return TaskTurnDispatchClaim{}, errors.New("Task Turn dispatch is blocked by an active Provider Stop claim")
	}
	principalIDFromAuthority, _, err := providerResultUsageExecution(job, turn.RoleExecutionID)
	if err != nil || principalIDFromAuthority != principalID {
		return TaskTurnDispatchClaim{}, errors.New("Task Turn dispatch claim Principal does not match execution authority")
	}
	claim := TaskTurnDispatchClaim{
		ID: TaskTurnDispatchClaimID(commandID), CommandID: commandID, JobID: job.ID,
		TurnID: turn.ID, RoleExecutionID: turn.RoleExecutionID,
		OperationID: turn.OperationID, SessionID: turn.ProviderSessionID,
		PrincipalID: principalID, CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := claim.Validate(job); err != nil {
		return TaskTurnDispatchClaim{}, err
	}
	return claim, nil
}

func (c TaskTurnDispatchClaim) Validate(job Job) error {
	if !validPlanningIdentity(c.ID) || !validCommandID(c.CommandID) ||
		c.ID != TaskTurnDispatchClaimID(c.CommandID) || c.JobID != job.ID ||
		!validPlanningIdentity(c.TurnID) || !validPlanningIdentity(c.RoleExecutionID) ||
		!validPlanningIdentity(c.OperationID) || !validPlanningIdentity(c.SessionID) ||
		!strings.HasPrefix(c.PrincipalID, "human_") || c.CreatedAt.IsZero() || c.CreatedRevision == 0 {
		return errors.New("Task Turn dispatch claim identity or metadata is invalid")
	}
	var turn TaskTurnIntent
	found := false
	for _, candidate := range job.TaskTurnIntents {
		if candidate.ID == c.TurnID {
			turn, found = candidate, true
			break
		}
	}
	if !found || turn.OperationID != c.OperationID || turn.RoleExecutionID != c.RoleExecutionID ||
		turn.ProviderSessionID != c.SessionID || c.CreatedRevision <= turn.CreatedRevision ||
		c.CreatedAt.Before(turn.CreatedAt) {
		return errors.New("Task Turn dispatch claim does not match its Turn intent")
	}
	for _, stopIntent := range job.ProviderStopIntents {
		if stopIntent.RoleExecutionID == c.RoleExecutionID && stopIntent.SessionID == c.SessionID &&
			stopIntent.CreatedRevision < c.CreatedRevision {
			return errors.New("Task Turn dispatch claim follows an existing Stop intent")
		}
	}
	principalIDFromAuthority, _, err := providerResultUsageExecution(job, c.RoleExecutionID)
	if err != nil || principalIDFromAuthority != c.PrincipalID {
		return errors.New("Task Turn dispatch claim Principal does not match execution authority")
	}
	return nil
}

func NewTaskTurnDispatchRelease(
	job Job, claimID, principalID, commandID, outcome, evidenceSHA256 string, now time.Time,
) (TaskTurnDispatchRelease, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil {
		return TaskTurnDispatchRelease{}, fmt.Errorf("validate Job before Task Turn dispatch release: %w", err)
	}
	if !validCommandID(commandID) || now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return TaskTurnDispatchRelease{}, errors.New("Task Turn dispatch release command and timestamp are required")
	}
	var claim TaskTurnDispatchClaim
	found := false
	for _, candidate := range job.TaskTurnDispatchClaims {
		if candidate.ID == claimID {
			claim, found = candidate, true
			break
		}
	}
	if !found || claim.PrincipalID != principalID {
		return TaskTurnDispatchRelease{}, errors.New("Task Turn dispatch release requires the claim owner")
	}
	if outcome != TaskTurnDispatchReleasedCompleted &&
		outcome != TaskTurnDispatchReleasedFailed &&
		outcome != TaskTurnDispatchReleasedDefinitiveFailure &&
		outcome != TaskTurnDispatchReleasedIdempotentRetry {
		return TaskTurnDispatchRelease{}, errors.New("unsupported Task Turn dispatch release outcome")
	}
	if outcome == TaskTurnDispatchReleasedIdempotentRetry && !validSHA256(evidenceSHA256) {
		return TaskTurnDispatchRelease{}, errors.New("idempotent Task Turn retry requires verified Send idempotency evidence")
	}
	if outcome != TaskTurnDispatchReleasedIdempotentRetry && evidenceSHA256 != "" {
		return TaskTurnDispatchRelease{}, errors.New("Task Turn dispatch release evidence is only valid for idempotent retry")
	}
	if _, active := ActiveTaskTurnDispatchClaim(job, claim.OperationID); !active {
		return TaskTurnDispatchRelease{}, errors.New("Task Turn dispatch claim is already released")
	}
	release := TaskTurnDispatchRelease{
		ID: TaskTurnDispatchReleaseID(commandID), CommandID: commandID,
		JobID: job.ID, ClaimID: claim.ID, OperationID: claim.OperationID,
		PrincipalID: principalID, Outcome: outcome, EvidenceSHA256: evidenceSHA256,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := release.Validate(job); err != nil {
		return TaskTurnDispatchRelease{}, err
	}
	return release, nil
}

func (r TaskTurnDispatchRelease) Validate(job Job) error {
	if !validPlanningIdentity(r.ID) || !validCommandID(r.CommandID) ||
		r.ID != TaskTurnDispatchReleaseID(r.CommandID) || r.JobID != job.ID ||
		!validPlanningIdentity(r.ClaimID) || !validPlanningIdentity(r.OperationID) ||
		!strings.HasPrefix(r.PrincipalID, "human_") || r.CreatedAt.IsZero() || r.CreatedRevision == 0 ||
		(r.Outcome != TaskTurnDispatchReleasedCompleted && r.Outcome != TaskTurnDispatchReleasedFailed &&
			r.Outcome != TaskTurnDispatchReleasedDefinitiveFailure &&
			r.Outcome != TaskTurnDispatchReleasedIdempotentRetry) {
		return errors.New("Task Turn dispatch release identity or metadata is invalid")
	}
	var claim TaskTurnDispatchClaim
	found := false
	for _, candidate := range job.TaskTurnDispatchClaims {
		if candidate.ID == r.ClaimID {
			claim, found = candidate, true
			break
		}
	}
	if !found || claim.OperationID != r.OperationID || claim.PrincipalID != r.PrincipalID ||
		r.CreatedRevision <= claim.CreatedRevision || r.CreatedAt.Before(claim.CreatedAt) {
		return errors.New("Task Turn dispatch release does not match its claim")
	}
	if r.Outcome == TaskTurnDispatchReleasedIdempotentRetry {
		if !validSHA256(r.EvidenceSHA256) {
			return errors.New("idempotent Task Turn retry requires verified Send idempotency evidence")
		}
		var previous *TaskTurnObservation
		for i := range job.TaskTurnObservations {
			observation := &job.TaskTurnObservations[i]
			if observation.TurnID == claim.TurnID && observation.CreatedRevision < r.CreatedRevision &&
				(previous == nil || observation.CreatedRevision > previous.CreatedRevision) {
				previous = observation
			}
		}
		if previous != nil && previous.Status != TaskTurnUnknown {
			return errors.New("idempotent Task Turn retry cannot release a claim after a terminal observation")
		}
	} else if r.EvidenceSHA256 != "" {
		return errors.New("Task Turn dispatch release has unexpected evidence")
	}
	if r.Outcome == TaskTurnDispatchReleasedCompleted || r.Outcome == TaskTurnDispatchReleasedFailed {
		var latest *TaskTurnObservation
		for i := range job.TaskTurnObservations {
			observation := &job.TaskTurnObservations[i]
			if observation.TurnID == claim.TurnID &&
				(latest == nil || observation.CreatedRevision > latest.CreatedRevision) {
				latest = observation
			}
		}
		expected := TaskTurnCompleted
		if r.Outcome == TaskTurnDispatchReleasedFailed {
			expected = TaskTurnFailed
		}
		if latest == nil || latest.Status != expected || latest.CreatedRevision >= r.CreatedRevision {
			return errors.New("terminal Task Turn dispatch release requires prior matching observation")
		}
	}
	return nil
}

func ActiveTaskTurnDispatchClaim(job Job, operationID string) (TaskTurnDispatchClaim, bool) {
	var latest TaskTurnDispatchClaim
	found := false
	for _, claim := range job.TaskTurnDispatchClaims {
		if claim.OperationID == operationID && (!found || claim.CreatedRevision > latest.CreatedRevision) {
			latest, found = claim, true
		}
	}
	if !found {
		return TaskTurnDispatchClaim{}, false
	}
	for _, release := range job.TaskTurnDispatchReleases {
		if release.ClaimID == latest.ID && release.CreatedRevision > latest.CreatedRevision {
			return TaskTurnDispatchClaim{}, false
		}
	}
	return latest, true
}

func validateTaskTurnDispatchFacts(job Job) error {
	claimsByOperation := make(map[string][]TaskTurnDispatchClaim)
	seenClaimIDs := make(map[string]struct{}, len(job.TaskTurnDispatchClaims))
	seenClaimCommands := make(map[string]struct{}, len(job.TaskTurnDispatchClaims))
	var previousClaimRevision uint64
	var previousClaimAt time.Time
	for _, claim := range job.TaskTurnDispatchClaims {
		if err := claim.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid TaskTurnDispatchClaim: %v", ErrInvalidJob, err)
		}
		if claim.CreatedAt.After(job.UpdatedAt) || claim.CreatedRevision > job.Revision ||
			claim.CreatedRevision <= previousClaimRevision || claim.CreatedAt.Before(previousClaimAt) {
			return fmt.Errorf("%w: TaskTurnDispatchClaim append order is invalid", ErrInvalidJob)
		}
		previousClaimRevision, previousClaimAt = claim.CreatedRevision, claim.CreatedAt
		if _, exists := seenClaimIDs[claim.ID]; exists {
			return fmt.Errorf("%w: duplicate TaskTurnDispatchClaim ID", ErrInvalidJob)
		}
		if _, exists := seenClaimCommands[claim.CommandID]; exists {
			return fmt.Errorf("%w: duplicate TaskTurnDispatchClaim command", ErrInvalidJob)
		}
		seenClaimIDs[claim.ID] = struct{}{}
		seenClaimCommands[claim.CommandID] = struct{}{}
		claimsByOperation[claim.OperationID] = append(claimsByOperation[claim.OperationID], claim)
	}
	seenReleaseIDs := make(map[string]struct{}, len(job.TaskTurnDispatchReleases))
	seenReleaseCommands := make(map[string]struct{}, len(job.TaskTurnDispatchReleases))
	releasesByClaim := make(map[string]TaskTurnDispatchRelease)
	var previousReleaseRevision uint64
	var previousReleaseAt time.Time
	for _, release := range job.TaskTurnDispatchReleases {
		if err := release.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid TaskTurnDispatchRelease: %v", ErrInvalidJob, err)
		}
		if release.CreatedAt.After(job.UpdatedAt) || release.CreatedRevision > job.Revision ||
			release.CreatedRevision <= previousReleaseRevision || release.CreatedAt.Before(previousReleaseAt) {
			return fmt.Errorf("%w: TaskTurnDispatchRelease append order is invalid", ErrInvalidJob)
		}
		previousReleaseRevision, previousReleaseAt = release.CreatedRevision, release.CreatedAt
		if _, exists := seenReleaseIDs[release.ID]; exists {
			return fmt.Errorf("%w: duplicate TaskTurnDispatchRelease ID", ErrInvalidJob)
		}
		if _, exists := seenReleaseCommands[release.CommandID]; exists {
			return fmt.Errorf("%w: duplicate TaskTurnDispatchRelease command", ErrInvalidJob)
		}
		if _, exists := releasesByClaim[release.ClaimID]; exists {
			return fmt.Errorf("%w: TaskTurnDispatchClaim has multiple releases", ErrInvalidJob)
		}
		seenReleaseIDs[release.ID] = struct{}{}
		seenReleaseCommands[release.CommandID] = struct{}{}
		releasesByClaim[release.ClaimID] = release
	}
	for operationID, claims := range claimsByOperation {
		for index, claim := range claims {
			if index == 0 {
				continue
			}
			previous := claims[index-1]
			release, exists := releasesByClaim[previous.ID]
			if !exists || release.CreatedRevision >= claim.CreatedRevision ||
				claim.CreatedRevision <= previous.CreatedRevision || claim.CreatedAt.Before(previous.CreatedAt) {
				return fmt.Errorf("%w: Task Turn dispatch claims overlap for Operation %s", ErrInvalidJob, operationID)
			}
		}
	}
	for _, claim := range job.TaskTurnDispatchClaims {
		for _, stopIntent := range job.ProviderStopIntents {
			if stopIntent.RoleExecutionID != claim.RoleExecutionID || stopIntent.SessionID != claim.SessionID ||
				stopIntent.CreatedRevision <= claim.CreatedRevision {
				continue
			}
			release, released := releasesByClaim[claim.ID]
			if !released || release.CreatedRevision >= stopIntent.CreatedRevision {
				return fmt.Errorf("%w: Stop intent overlaps an active Task Turn dispatch claim", ErrInvalidJob)
			}
		}
	}
	return nil
}
