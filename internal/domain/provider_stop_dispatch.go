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
	ProviderStopDispatchReleasedStopped           = "stopped"
	ProviderStopDispatchReleasedDefinitiveFailure = "definitive_failure"
	ProviderStopDispatchReleasedNotDispatched     = "not_dispatched"
	ProviderStopDispatchReleasedFencedNotFound    = "fenced_not_found"
)

type ProviderStopDispatchClaim struct {
	ID              string    `json:"id"`
	CommandID       string    `json:"command_id"`
	JobID           string    `json:"job_id"`
	StopIntentID    string    `json:"stop_intent_id"`
	RoleExecutionID string    `json:"role_execution_id"`
	OperationID     string    `json:"operation_id"`
	SessionID       string    `json:"session_id"`
	PrincipalID     string    `json:"principal_id"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedRevision uint64    `json:"created_revision"`
}

type ProviderStopDispatchStart struct {
	ID              string    `json:"id"`
	CommandID       string    `json:"command_id"`
	JobID           string    `json:"job_id"`
	ClaimID         string    `json:"claim_id"`
	OperationID     string    `json:"operation_id"`
	PrincipalID     string    `json:"principal_id"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedRevision uint64    `json:"created_revision"`
}

type ProviderStopDispatchRelease struct {
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

func ProviderStopDispatchClaimID(commandID string) string {
	return planningOperationID("provider_stop_dispatch_claim", commandID)
}

func ProviderStopDispatchStartID(commandID string) string {
	return planningOperationID("provider_stop_dispatch_start", commandID)
}

func ProviderStopDispatchStartCommandID(claimID string) string {
	sum := sha256.Sum256([]byte("taxion:provider-stop-dispatch-start:" + claimID))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func ProviderStopDispatchReleaseID(commandID string) string {
	return planningOperationID("provider_stop_dispatch_release", commandID)
}

func ProviderStopDispatchReleaseCommandID(claimID, outcome string) string {
	sum := sha256.Sum256([]byte("taxion:provider-stop-dispatch-release:" + claimID + ":" + outcome))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func NewProviderStopDispatchClaim(
	job Job, stopIntentID, principalID, commandID string, now time.Time,
) (ProviderStopDispatchClaim, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil {
		return ProviderStopDispatchClaim{}, fmt.Errorf("validate Job before Provider Stop dispatch claim: %w", err)
	}
	if !validCommandID(commandID) || now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return ProviderStopDispatchClaim{}, errors.New("Provider Stop dispatch claim command and timestamp are required")
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
		return ProviderStopDispatchClaim{}, errors.New("Provider Stop dispatch claim requires a persisted Stop intent")
	}
	expectedPrincipal, _, _, _, err := providerStopExecutionAuthority(job, providerStopDispatchRoleExecutionByID(job, intent.RoleExecutionID))
	if err != nil {
		return ProviderStopDispatchClaim{}, err
	}
	if principalID != expectedPrincipal {
		return ProviderStopDispatchClaim{}, errors.New("Provider Stop dispatch claim Principal does not match execution authority")
	}
	if _, active := ActiveProviderStopDispatchClaim(job, intent.OperationID); active {
		return ProviderStopDispatchClaim{}, errors.New("Provider Stop operation already has an active dispatch claim")
	}
	for _, turn := range job.TaskTurnIntents {
		if turn.RoleExecutionID == intent.RoleExecutionID && turn.ProviderSessionID == intent.SessionID {
			if _, active := ActiveTaskTurnDispatchClaim(job, turn.OperationID); active {
				return ProviderStopDispatchClaim{}, errors.New("Provider Stop dispatch is blocked by an active Task Turn claim")
			}
		}
	}
	for _, observation := range job.ProviderStopObservations {
		if observation.OperationID == intent.OperationID && observation.Quiescent {
			return ProviderStopDispatchClaim{}, errors.New("Provider Stop operation is already confirmed quiescent")
		}
	}
	claim := ProviderStopDispatchClaim{
		ID: ProviderStopDispatchClaimID(commandID), CommandID: commandID, JobID: job.ID,
		StopIntentID: intent.ID, RoleExecutionID: intent.RoleExecutionID,
		OperationID: intent.OperationID, SessionID: intent.SessionID,
		PrincipalID: principalID, CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := claim.Validate(job); err != nil {
		return ProviderStopDispatchClaim{}, err
	}
	return claim, nil
}

func (c ProviderStopDispatchClaim) Validate(job Job) error {
	if !validPlanningIdentity(c.ID) || !validCommandID(c.CommandID) ||
		c.ID != ProviderStopDispatchClaimID(c.CommandID) || c.JobID != job.ID ||
		!validPlanningIdentity(c.StopIntentID) || !validPlanningIdentity(c.RoleExecutionID) ||
		!validPlanningIdentity(c.OperationID) || !validPlanningIdentity(c.SessionID) ||
		!strings.HasPrefix(c.PrincipalID, "human_") || c.CreatedAt.IsZero() || c.CreatedRevision == 0 {
		return errors.New("Provider Stop dispatch claim identity or metadata is invalid")
	}
	var intent ProviderStopIntent
	found := false
	for _, candidate := range job.ProviderStopIntents {
		if candidate.ID == c.StopIntentID {
			intent, found = candidate, true
			break
		}
	}
	if !found || intent.OperationID != c.OperationID || intent.RoleExecutionID != c.RoleExecutionID ||
		intent.SessionID != c.SessionID || c.CreatedRevision <= intent.CreatedRevision ||
		c.CreatedAt.Before(intent.CreatedAt) {
		return errors.New("Provider Stop dispatch claim does not match its Stop intent")
	}
	expectedPrincipal, _, _, _, err := providerStopExecutionAuthority(job, providerStopDispatchRoleExecutionByID(job, c.RoleExecutionID))
	if err != nil || expectedPrincipal != c.PrincipalID {
		return errors.New("Provider Stop dispatch claim Principal does not match execution authority")
	}
	for _, turn := range job.TaskTurnIntents {
		if turn.RoleExecutionID == c.RoleExecutionID && turn.ProviderSessionID == c.SessionID {
			if _, active := ActiveTaskTurnDispatchClaim(job, turn.OperationID); active {
				return errors.New("Provider Stop dispatch claim overlaps an active Task Turn dispatch")
			}
		}
	}
	return nil
}

func NewProviderStopDispatchStart(
	job Job, claimID, principalID, commandID string, now time.Time,
) (ProviderStopDispatchStart, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil {
		return ProviderStopDispatchStart{}, fmt.Errorf("validate Job before Provider Stop dispatch start: %w", err)
	}
	if !validCommandID(commandID) || commandID != ProviderStopDispatchStartCommandID(claimID) ||
		now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return ProviderStopDispatchStart{}, errors.New("Provider Stop dispatch start command and timestamp are invalid")
	}
	var claim ProviderStopDispatchClaim
	found := false
	for _, candidate := range job.ProviderStopDispatchClaims {
		if candidate.ID == claimID {
			claim, found = candidate, true
			break
		}
	}
	if !found || claim.PrincipalID != principalID {
		return ProviderStopDispatchStart{}, errors.New("Provider Stop dispatch start requires the active claim owner")
	}
	active, activeFound := ActiveProviderStopDispatchClaim(job, claim.OperationID)
	if !activeFound || active.ID != claim.ID {
		return ProviderStopDispatchStart{}, errors.New("Provider Stop dispatch start claim is no longer active")
	}
	if _, started := ProviderStopDispatchStartedForClaim(job, claim.ID); started {
		return ProviderStopDispatchStart{}, errors.New("Provider Stop dispatch claim has already started")
	}
	start := ProviderStopDispatchStart{
		ID: ProviderStopDispatchStartID(commandID), CommandID: commandID,
		JobID: job.ID, ClaimID: claim.ID, OperationID: claim.OperationID,
		PrincipalID: principalID, CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := start.Validate(job); err != nil {
		return ProviderStopDispatchStart{}, err
	}
	return start, nil
}

func (s ProviderStopDispatchStart) Validate(job Job) error {
	if !validPlanningIdentity(s.ID) || !validCommandID(s.CommandID) ||
		s.ID != ProviderStopDispatchStartID(s.CommandID) ||
		s.CommandID != ProviderStopDispatchStartCommandID(s.ClaimID) ||
		s.JobID != job.ID || !validPlanningIdentity(s.ClaimID) ||
		!validPlanningIdentity(s.OperationID) || !strings.HasPrefix(s.PrincipalID, "human_") ||
		s.CreatedAt.IsZero() || s.CreatedRevision == 0 {
		return errors.New("Provider Stop dispatch start identity or metadata is invalid")
	}
	var claim ProviderStopDispatchClaim
	found := false
	for _, candidate := range job.ProviderStopDispatchClaims {
		if candidate.ID == s.ClaimID {
			claim, found = candidate, true
			break
		}
	}
	if !found || claim.OperationID != s.OperationID || claim.PrincipalID != s.PrincipalID ||
		s.CreatedRevision <= claim.CreatedRevision || s.CreatedAt.Before(claim.CreatedAt) {
		return errors.New("Provider Stop dispatch start does not match its claim")
	}
	return nil
}

func ProviderStopDispatchStartedForClaim(job Job, claimID string) (ProviderStopDispatchStart, bool) {
	for _, start := range job.ProviderStopDispatchStarts {
		if start.ClaimID == claimID {
			return start, true
		}
	}
	return ProviderStopDispatchStart{}, false
}

func NewProviderStopDispatchRelease(
	job Job, claimID, principalID, commandID, outcome, evidenceSHA256 string, now time.Time,
) (ProviderStopDispatchRelease, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil {
		return ProviderStopDispatchRelease{}, fmt.Errorf("validate Job before Provider Stop dispatch release: %w", err)
	}
	if !validCommandID(commandID) || now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return ProviderStopDispatchRelease{}, errors.New("Provider Stop dispatch release command and timestamp are required")
	}
	var claim ProviderStopDispatchClaim
	found := false
	for _, candidate := range job.ProviderStopDispatchClaims {
		if candidate.ID == claimID {
			claim, found = candidate, true
			break
		}
	}
	if !found {
		return ProviderStopDispatchRelease{}, errors.New("Provider Stop dispatch release requires a persisted claim")
	}
	if claim.PrincipalID != principalID {
		return ProviderStopDispatchRelease{}, errors.New("Provider Stop dispatch release Principal does not match claim owner")
	}
	if outcome != ProviderStopDispatchReleasedStopped && outcome != ProviderStopDispatchReleasedDefinitiveFailure &&
		outcome != ProviderStopDispatchReleasedNotDispatched && outcome != ProviderStopDispatchReleasedFencedNotFound {
		return ProviderStopDispatchRelease{}, errors.New("unsupported Provider Stop dispatch release outcome")
	}
	if _, active := ActiveProviderStopDispatchClaim(job, claim.OperationID); !active {
		return ProviderStopDispatchRelease{}, errors.New("Provider Stop dispatch claim is already released")
	}
	if outcome == ProviderStopDispatchReleasedNotDispatched {
		if _, started := ProviderStopDispatchStartedForClaim(job, claim.ID); started {
			return ProviderStopDispatchRelease{}, errors.New("started Provider Stop dispatch cannot be released as not dispatched")
		}
		if !validSHA256(evidenceSHA256) {
			return ProviderStopDispatchRelease{}, errors.New("not-dispatched release requires verified lookup evidence")
		}
	} else if outcome == ProviderStopDispatchReleasedFencedNotFound {
		if _, started := ProviderStopDispatchStartedForClaim(job, claim.ID); !started {
			return ProviderStopDispatchRelease{}, errors.New("fenced NOT_FOUND release requires a started Provider Stop dispatch")
		}
		if !validSHA256(evidenceSHA256) {
			return ProviderStopDispatchRelease{}, errors.New("fenced NOT_FOUND release requires verified lookup evidence")
		}
	}
	release := ProviderStopDispatchRelease{
		ID: ProviderStopDispatchReleaseID(commandID), CommandID: commandID,
		JobID: job.ID, ClaimID: claim.ID, OperationID: claim.OperationID,
		PrincipalID: principalID, Outcome: outcome, EvidenceSHA256: evidenceSHA256,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := release.Validate(job); err != nil {
		return ProviderStopDispatchRelease{}, err
	}
	return release, nil
}

func (r ProviderStopDispatchRelease) Validate(job Job) error {
	if !validPlanningIdentity(r.ID) || !validCommandID(r.CommandID) ||
		r.ID != ProviderStopDispatchReleaseID(r.CommandID) || r.JobID != job.ID ||
		!validPlanningIdentity(r.ClaimID) || !validPlanningIdentity(r.OperationID) ||
		!strings.HasPrefix(r.PrincipalID, "human_") || r.CreatedAt.IsZero() || r.CreatedRevision == 0 ||
		(r.Outcome != ProviderStopDispatchReleasedStopped && r.Outcome != ProviderStopDispatchReleasedDefinitiveFailure &&
			r.Outcome != ProviderStopDispatchReleasedNotDispatched && r.Outcome != ProviderStopDispatchReleasedFencedNotFound) {
		return errors.New("Provider Stop dispatch release identity or metadata is invalid")
	}
	var claim ProviderStopDispatchClaim
	found := false
	for _, candidate := range job.ProviderStopDispatchClaims {
		if candidate.ID == r.ClaimID {
			claim, found = candidate, true
			break
		}
	}
	if !found || claim.OperationID != r.OperationID || claim.PrincipalID != r.PrincipalID ||
		r.CreatedRevision <= claim.CreatedRevision || r.CreatedAt.Before(claim.CreatedAt) {
		return errors.New("Provider Stop dispatch release does not match its claim")
	}
	if r.Outcome == ProviderStopDispatchReleasedNotDispatched {
		if _, started := ProviderStopDispatchStartedForClaim(job, claim.ID); started {
			return errors.New("not-dispatched release conflicts with a Provider Stop dispatch start")
		}
		if !validSHA256(r.EvidenceSHA256) {
			return errors.New("not-dispatched release requires verified lookup evidence")
		}
	} else if r.Outcome == ProviderStopDispatchReleasedFencedNotFound {
		if _, started := ProviderStopDispatchStartedForClaim(job, claim.ID); !started || !validSHA256(r.EvidenceSHA256) {
			return errors.New("fenced NOT_FOUND release requires a started claim and verified lookup evidence")
		}
	}
	if r.Outcome == ProviderStopDispatchReleasedStopped {
		var latest *ProviderStopObservation
		for i := range job.ProviderStopObservations {
			observation := &job.ProviderStopObservations[i]
			if observation.OperationID == r.OperationID &&
				(latest == nil || observation.CreatedRevision > latest.CreatedRevision) {
				latest = observation
			}
		}
		if latest == nil || !latest.Quiescent || latest.CreatedRevision >= r.CreatedRevision {
			return errors.New("STOPPED dispatch release requires prior quiescent Provider evidence")
		}
	}
	return nil
}

func ActiveProviderStopDispatchClaim(job Job, operationID string) (ProviderStopDispatchClaim, bool) {
	var latest ProviderStopDispatchClaim
	found := false
	for _, claim := range job.ProviderStopDispatchClaims {
		if claim.OperationID == operationID && (!found || claim.CreatedRevision > latest.CreatedRevision) {
			latest, found = claim, true
		}
	}
	if !found {
		return ProviderStopDispatchClaim{}, false
	}
	for _, release := range job.ProviderStopDispatchReleases {
		if release.ClaimID == latest.ID && release.CreatedRevision > latest.CreatedRevision {
			return ProviderStopDispatchClaim{}, false
		}
	}
	return latest, true
}

func validateProviderStopDispatchFacts(job Job) error {
	claimsByOperation := make(map[string][]ProviderStopDispatchClaim)
	seenClaimIDs := make(map[string]struct{}, len(job.ProviderStopDispatchClaims))
	seenClaimCommands := make(map[string]struct{}, len(job.ProviderStopDispatchClaims))
	var previousClaimRevision uint64
	var previousClaimAt time.Time
	for _, claim := range job.ProviderStopDispatchClaims {
		if err := claim.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid ProviderStopDispatchClaim: %v", ErrInvalidJob, err)
		}
		if claim.CreatedAt.After(job.UpdatedAt) || claim.CreatedRevision > job.Revision ||
			claim.CreatedRevision <= previousClaimRevision || claim.CreatedAt.Before(previousClaimAt) {
			return fmt.Errorf("%w: ProviderStopDispatchClaim append order is invalid", ErrInvalidJob)
		}
		previousClaimRevision, previousClaimAt = claim.CreatedRevision, claim.CreatedAt
		if _, exists := seenClaimIDs[claim.ID]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopDispatchClaim ID", ErrInvalidJob)
		}
		if _, exists := seenClaimCommands[claim.CommandID]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopDispatchClaim command", ErrInvalidJob)
		}
		seenClaimIDs[claim.ID] = struct{}{}
		seenClaimCommands[claim.CommandID] = struct{}{}
		claimsByOperation[claim.OperationID] = append(claimsByOperation[claim.OperationID], claim)
	}
	seenReleaseIDs := make(map[string]struct{}, len(job.ProviderStopDispatchReleases))
	seenReleaseCommands := make(map[string]struct{}, len(job.ProviderStopDispatchReleases))
	releasesByClaim := make(map[string]ProviderStopDispatchRelease)
	var previousReleaseRevision uint64
	var previousReleaseAt time.Time
	for _, release := range job.ProviderStopDispatchReleases {
		if err := release.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid ProviderStopDispatchRelease: %v", ErrInvalidJob, err)
		}
		if release.CreatedAt.After(job.UpdatedAt) || release.CreatedRevision > job.Revision ||
			release.CreatedRevision <= previousReleaseRevision || release.CreatedAt.Before(previousReleaseAt) {
			return fmt.Errorf("%w: ProviderStopDispatchRelease append order is invalid", ErrInvalidJob)
		}
		previousReleaseRevision, previousReleaseAt = release.CreatedRevision, release.CreatedAt
		if _, exists := seenReleaseIDs[release.ID]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopDispatchRelease ID", ErrInvalidJob)
		}
		if _, exists := seenReleaseCommands[release.CommandID]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopDispatchRelease command", ErrInvalidJob)
		}
		if _, exists := releasesByClaim[release.ClaimID]; exists {
			return fmt.Errorf("%w: ProviderStopDispatchClaim has multiple releases", ErrInvalidJob)
		}
		seenReleaseIDs[release.ID] = struct{}{}
		seenReleaseCommands[release.CommandID] = struct{}{}
		releasesByClaim[release.ClaimID] = release
	}
	seenStartIDs := make(map[string]struct{}, len(job.ProviderStopDispatchStarts))
	seenStartCommands := make(map[string]struct{}, len(job.ProviderStopDispatchStarts))
	startedByClaim := make(map[string]ProviderStopDispatchStart, len(job.ProviderStopDispatchStarts))
	var previousStartRevision uint64
	var previousStartAt time.Time
	for _, start := range job.ProviderStopDispatchStarts {
		if err := start.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid ProviderStopDispatchStart: %v", ErrInvalidJob, err)
		}
		if start.CreatedAt.After(job.UpdatedAt) || start.CreatedRevision > job.Revision ||
			start.CreatedRevision <= previousStartRevision || start.CreatedAt.Before(previousStartAt) {
			return fmt.Errorf("%w: ProviderStopDispatchStart append order is invalid", ErrInvalidJob)
		}
		previousStartRevision, previousStartAt = start.CreatedRevision, start.CreatedAt
		if _, exists := seenStartIDs[start.ID]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopDispatchStart ID", ErrInvalidJob)
		}
		if _, exists := seenStartCommands[start.CommandID]; exists {
			return fmt.Errorf("%w: duplicate ProviderStopDispatchStart command", ErrInvalidJob)
		}
		if _, exists := startedByClaim[start.ClaimID]; exists {
			return fmt.Errorf("%w: ProviderStopDispatchClaim has multiple start facts", ErrInvalidJob)
		}
		seenStartIDs[start.ID] = struct{}{}
		seenStartCommands[start.CommandID] = struct{}{}
		startedByClaim[start.ClaimID] = start
	}
	for claimID, start := range startedByClaim {
		if release, exists := releasesByClaim[claimID]; exists && release.CreatedRevision <= start.CreatedRevision {
			return fmt.Errorf("%w: ProviderStopDispatchStart follows its claim release", ErrInvalidJob)
		}
	}
	for operationID, claims := range claimsByOperation {
		for index, claim := range claims {
			if index == 0 {
				continue
			}
			previous := claims[index-1]
			release, exists := releasesByClaim[previous.ID]
			if !exists || release.CreatedRevision >= claim.CreatedRevision {
				return fmt.Errorf("%w: Provider Stop dispatch claims overlap for Operation %s", ErrInvalidJob, operationID)
			}
			if claim.CreatedRevision <= previous.CreatedRevision || claim.CreatedAt.Before(previous.CreatedAt) {
				return fmt.Errorf("%w: Provider Stop dispatch claim sequence is invalid for Operation %s", ErrInvalidJob, operationID)
			}
		}
	}
	return nil
}

func providerStopDispatchRoleExecutionByID(job Job, executionID string) RoleExecution {
	for _, execution := range job.RoleExecutions {
		if execution.ID == executionID {
			return execution
		}
	}
	return RoleExecution{}
}
