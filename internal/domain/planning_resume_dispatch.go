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
	PlanningResumeDispatchReleasedAttached = "attached"
	PlanningResumeDispatchReleasedFailed   = "failed"
)

type PlanningResumeDispatchClaim struct {
	ID              string    `json:"id"`
	CommandID       string    `json:"command_id"`
	JobID           string    `json:"job_id"`
	ResumeID        string    `json:"resume_id"`
	OperationID     string    `json:"operation_id"`
	RoleExecutionID string    `json:"role_execution_id"`
	SessionID       string    `json:"session_id"`
	PrincipalID     string    `json:"principal_id"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedRevision uint64    `json:"created_revision"`
}

type PlanningResumeDispatchRelease struct {
	ID              string    `json:"id"`
	CommandID       string    `json:"command_id"`
	JobID           string    `json:"job_id"`
	ClaimID         string    `json:"claim_id"`
	OperationID     string    `json:"operation_id"`
	PrincipalID     string    `json:"principal_id"`
	Outcome         string    `json:"outcome"`
	CreatedAt       time.Time `json:"created_at"`
	CreatedRevision uint64    `json:"created_revision"`
}

func PlanningResumeDispatchClaimID(commandID string) string {
	return planningOperationID("planning_resume_dispatch_claim", commandID)
}

func PlanningResumeDispatchClaimCommandID(resumeID string) string {
	sum := sha256.Sum256([]byte("taxion:planning-resume-dispatch-claim:" + resumeID))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func PlanningResumeDispatchReleaseID(commandID string) string {
	return planningOperationID("planning_resume_dispatch_release", commandID)
}

func PlanningResumeDispatchReleaseCommandID(claimID, outcome string) string {
	sum := sha256.Sum256([]byte("taxion:planning-resume-dispatch-release:" + claimID + ":" + outcome))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func planningResumeDispatchClaimByID(job Job, claimID string) (PlanningResumeDispatchClaim, bool) {
	for _, claim := range job.PlanningResumeDispatchClaims {
		if claim.ID == claimID {
			return claim, true
		}
	}
	return PlanningResumeDispatchClaim{}, false
}

func ActivePlanningResumeDispatchClaim(job Job, operationID string) (PlanningResumeDispatchClaim, bool) {
	var latest PlanningResumeDispatchClaim
	found := false
	for _, claim := range job.PlanningResumeDispatchClaims {
		if claim.OperationID == operationID && (!found || claim.CreatedRevision > latest.CreatedRevision) {
			latest, found = claim, true
		}
	}
	if !found {
		return PlanningResumeDispatchClaim{}, false
	}
	for _, release := range job.PlanningResumeDispatchReleases {
		if release.ClaimID == latest.ID && release.CreatedRevision > latest.CreatedRevision {
			return PlanningResumeDispatchClaim{}, false
		}
	}
	return latest, true
}

func ActivePlanningResumeDispatchClaimForSession(job Job, roleExecutionID, sessionID string) (PlanningResumeDispatchClaim, bool) {
	var latest PlanningResumeDispatchClaim
	found := false
	for _, claim := range job.PlanningResumeDispatchClaims {
		if claim.RoleExecutionID == roleExecutionID && claim.SessionID == sessionID &&
			(!found || claim.CreatedRevision > latest.CreatedRevision) {
			latest, found = claim, true
		}
	}
	if !found {
		return PlanningResumeDispatchClaim{}, false
	}
	for _, release := range job.PlanningResumeDispatchReleases {
		if release.ClaimID == latest.ID && release.CreatedRevision > latest.CreatedRevision {
			return PlanningResumeDispatchClaim{}, false
		}
	}
	return latest, true
}

func NewPlanningResumeDispatchClaim(
	job Job, resumeID, principalID, commandID string, now time.Time,
) (PlanningResumeDispatchClaim, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil {
		return PlanningResumeDispatchClaim{}, fmt.Errorf("validate Job before Resume dispatch claim: %w", err)
	}
	if !validCommandID(commandID) || now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return PlanningResumeDispatchClaim{}, errors.New("Resume dispatch claim command and timestamp are required")
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
		return PlanningResumeDispatchClaim{}, errors.New("Resume dispatch claim requires a persisted intent")
	}
	latest := latestPlanningResumeForSession(job, resume.RoleExecutionID, resume.ProviderSessionID)
	if latest == nil || latest.ID != resume.ID {
		return PlanningResumeDispatchClaim{}, errors.New("only the latest Resume intent for a Session may acquire dispatch")
	}
	if _, active := ActivePlanningResumeDispatchClaimForSession(job, resume.RoleExecutionID, resume.ProviderSessionID); active {
		return PlanningResumeDispatchClaim{}, errors.New("Provider Resume Session already has an active dispatch claim")
	}
	execution := roleExecutionByID(job, resume.RoleExecutionID)
	principalIDFromAuthority, _, _, _, err := providerStopExecutionAuthority(job, execution)
	if err != nil || principalIDFromAuthority != principalID {
		return PlanningResumeDispatchClaim{}, errors.New("Resume dispatch claim Principal does not match execution authority")
	}
	claim := PlanningResumeDispatchClaim{
		ID: PlanningResumeDispatchClaimID(commandID), CommandID: commandID, JobID: job.ID,
		ResumeID: resume.ID, OperationID: resume.OperationID, RoleExecutionID: resume.RoleExecutionID,
		SessionID: resume.ProviderSessionID, PrincipalID: principalID,
		CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := claim.Validate(job); err != nil {
		return PlanningResumeDispatchClaim{}, err
	}
	return claim, nil
}

func roleExecutionByID(job Job, roleExecutionID string) RoleExecution {
	for _, execution := range job.RoleExecutions {
		if execution.ID == roleExecutionID {
			return execution
		}
	}
	return RoleExecution{}
}

func latestPlanningResumeForSession(job Job, roleExecutionID, sessionID string) *PlanningResumeIntent {
	var latest *PlanningResumeIntent
	for i := range job.PlanningResumeIntents {
		candidate := &job.PlanningResumeIntents[i]
		if candidate.RoleExecutionID == roleExecutionID && candidate.ProviderSessionID == sessionID &&
			(latest == nil || candidate.CreatedRevision > latest.CreatedRevision) {
			latest = candidate
		}
	}
	return latest
}

func (c PlanningResumeDispatchClaim) Validate(job Job) error {
	if !validPlanningIdentity(c.ID) || !validCommandID(c.CommandID) ||
		c.ID != PlanningResumeDispatchClaimID(c.CommandID) || c.JobID != job.ID ||
		!validPlanningIdentity(c.ResumeID) || !validPlanningIdentity(c.OperationID) ||
		!validPlanningIdentity(c.RoleExecutionID) || !validPlanningIdentity(c.SessionID) ||
		!strings.HasPrefix(c.PrincipalID, "human_") || c.CreatedAt.IsZero() || c.CreatedRevision == 0 {
		return errors.New("PlanningResumeDispatchClaim identity or metadata is invalid")
	}
	var resume PlanningResumeIntent
	found := false
	for _, candidate := range job.PlanningResumeIntents {
		if candidate.ID == c.ResumeID {
			resume, found = candidate, true
			break
		}
	}
	if !found || resume.OperationID != c.OperationID || resume.RoleExecutionID != c.RoleExecutionID ||
		resume.ProviderSessionID != c.SessionID || c.CreatedRevision <= resume.CreatedRevision ||
		c.CreatedAt.Before(resume.CreatedAt) {
		return errors.New("PlanningResumeDispatchClaim does not match its Resume intent")
	}
	var latestBeforeClaim *PlanningResumeIntent
	for i := range job.PlanningResumeIntents {
		candidate := &job.PlanningResumeIntents[i]
		if candidate.RoleExecutionID == c.RoleExecutionID && candidate.ProviderSessionID == c.SessionID &&
			candidate.CreatedRevision < c.CreatedRevision &&
			(latestBeforeClaim == nil || candidate.CreatedRevision > latestBeforeClaim.CreatedRevision) {
			latestBeforeClaim = candidate
		}
	}
	if latestBeforeClaim == nil || latestBeforeClaim.ID != c.ResumeID {
		return errors.New("PlanningResumeDispatchClaim was not acquired for the latest Resume intent")
	}
	execution := roleExecutionByID(job, c.RoleExecutionID)
	principalIDFromAuthority, _, _, _, err := providerStopExecutionAuthority(job, execution)
	if err != nil || principalIDFromAuthority != c.PrincipalID {
		return errors.New("PlanningResumeDispatchClaim Principal does not match execution authority")
	}
	return nil
}

func NewPlanningResumeDispatchRelease(
	job Job, claimID, principalID, commandID, outcome string, now time.Time,
) (PlanningResumeDispatchRelease, error) {
	now = now.UTC().Round(0)
	if err := job.Validate(); err != nil {
		return PlanningResumeDispatchRelease{}, fmt.Errorf("validate Job before Resume dispatch release: %w", err)
	}
	if !validCommandID(commandID) || now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return PlanningResumeDispatchRelease{}, errors.New("Resume dispatch release command and timestamp are required")
	}
	claim, found := planningResumeDispatchClaimByID(job, claimID)
	if !found || claim.PrincipalID != principalID {
		return PlanningResumeDispatchRelease{}, errors.New("Resume dispatch release requires the claim owner")
	}
	if outcome != PlanningResumeDispatchReleasedAttached && outcome != PlanningResumeDispatchReleasedFailed {
		return PlanningResumeDispatchRelease{}, errors.New("unsupported Resume dispatch release outcome")
	}
	release := PlanningResumeDispatchRelease{
		ID: PlanningResumeDispatchReleaseID(commandID), CommandID: commandID,
		JobID: job.ID, ClaimID: claim.ID, OperationID: claim.OperationID,
		PrincipalID: principalID, Outcome: outcome, CreatedAt: now, CreatedRevision: job.Revision + 1,
	}
	if err := release.Validate(job); err != nil {
		return PlanningResumeDispatchRelease{}, err
	}
	return release, nil
}

func (r PlanningResumeDispatchRelease) Validate(job Job) error {
	if !validPlanningIdentity(r.ID) || !validCommandID(r.CommandID) ||
		r.ID != PlanningResumeDispatchReleaseID(r.CommandID) || r.JobID != job.ID ||
		!validPlanningIdentity(r.ClaimID) || !validPlanningIdentity(r.OperationID) ||
		!strings.HasPrefix(r.PrincipalID, "human_") || r.CreatedAt.IsZero() || r.CreatedRevision == 0 ||
		(r.Outcome != PlanningResumeDispatchReleasedAttached && r.Outcome != PlanningResumeDispatchReleasedFailed) {
		return errors.New("PlanningResumeDispatchRelease identity or metadata is invalid")
	}
	claim, found := planningResumeDispatchClaimByID(job, r.ClaimID)
	if !found || claim.OperationID != r.OperationID || claim.PrincipalID != r.PrincipalID ||
		r.CreatedRevision <= claim.CreatedRevision || r.CreatedAt.Before(claim.CreatedAt) {
		return errors.New("PlanningResumeDispatchRelease does not match its claim")
	}
	expected := PlanningResumeAttached
	if r.Outcome == PlanningResumeDispatchReleasedFailed {
		expected = PlanningResumeFailed
	}
	var latest *PlanningResumeObservation
	for i := range job.PlanningResumeObservations {
		observation := &job.PlanningResumeObservations[i]
		if observation.ResumeID == claim.ResumeID &&
			(latest == nil || observation.CreatedRevision > latest.CreatedRevision) {
			latest = observation
		}
	}
	if latest == nil || latest.DispatchClaimID != claim.ID || latest.Status != expected ||
		latest.CreatedRevision >= r.CreatedRevision {
		return errors.New("Resume dispatch release requires a prior terminal observation from the same claim")
	}
	return nil
}

func validatePlanningResumeDispatchFacts(job Job) error {
	seenClaims := make(map[string]struct{}, len(job.PlanningResumeDispatchClaims))
	seenCommands := make(map[string]struct{}, len(job.PlanningResumeDispatchClaims))
	claimsBySession := make(map[string][]PlanningResumeDispatchClaim)
	var previousRevision uint64
	var previousAt time.Time
	for _, claim := range job.PlanningResumeDispatchClaims {
		if err := claim.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid PlanningResumeDispatchClaim: %v", ErrInvalidJob, err)
		}
		if claim.CreatedAt.After(job.UpdatedAt) || claim.CreatedRevision > job.Revision ||
			claim.CreatedRevision <= previousRevision || claim.CreatedAt.Before(previousAt) {
			return fmt.Errorf("%w: PlanningResumeDispatchClaim append order is invalid", ErrInvalidJob)
		}
		previousRevision, previousAt = claim.CreatedRevision, claim.CreatedAt
		if _, ok := seenClaims[claim.ID]; ok {
			return fmt.Errorf("%w: duplicate PlanningResumeDispatchClaim ID", ErrInvalidJob)
		}
		if _, ok := seenCommands[claim.CommandID]; ok {
			return fmt.Errorf("%w: duplicate PlanningResumeDispatchClaim command", ErrInvalidJob)
		}
		seenClaims[claim.ID] = struct{}{}
		seenCommands[claim.CommandID] = struct{}{}
		key := claim.RoleExecutionID + "\x00" + claim.SessionID
		claimsBySession[key] = append(claimsBySession[key], claim)
	}
	releasesByClaim := make(map[string]PlanningResumeDispatchRelease)
	seenReleases := make(map[string]struct{}, len(job.PlanningResumeDispatchReleases))
	seenReleaseCommands := make(map[string]struct{}, len(job.PlanningResumeDispatchReleases))
	previousRevision, previousAt = 0, time.Time{}
	for _, release := range job.PlanningResumeDispatchReleases {
		if err := release.Validate(job); err != nil {
			return fmt.Errorf("%w: invalid PlanningResumeDispatchRelease: %v", ErrInvalidJob, err)
		}
		if release.CreatedAt.After(job.UpdatedAt) || release.CreatedRevision > job.Revision ||
			release.CreatedRevision <= previousRevision || release.CreatedAt.Before(previousAt) {
			return fmt.Errorf("%w: PlanningResumeDispatchRelease append order is invalid", ErrInvalidJob)
		}
		previousRevision, previousAt = release.CreatedRevision, release.CreatedAt
		if _, ok := seenReleases[release.ID]; ok {
			return fmt.Errorf("%w: duplicate PlanningResumeDispatchRelease ID", ErrInvalidJob)
		}
		if _, ok := seenReleaseCommands[release.CommandID]; ok {
			return fmt.Errorf("%w: duplicate PlanningResumeDispatchRelease command", ErrInvalidJob)
		}
		if _, ok := releasesByClaim[release.ClaimID]; ok {
			return fmt.Errorf("%w: PlanningResumeDispatchClaim has multiple releases", ErrInvalidJob)
		}
		seenReleases[release.ID] = struct{}{}
		seenReleaseCommands[release.CommandID] = struct{}{}
		releasesByClaim[release.ClaimID] = release
	}
	for key, claims := range claimsBySession {
		for i := 0; i < len(claims); i++ {
			claim := claims[i]
			release, released := releasesByClaim[claim.ID]
			for _, intent := range job.PlanningResumeIntents {
				if intent.RoleExecutionID == claim.RoleExecutionID &&
					intent.ProviderSessionID == claim.SessionID &&
					intent.CreatedRevision > claim.CreatedRevision &&
					(!released || release.CreatedRevision >= intent.CreatedRevision) {
					return fmt.Errorf("%w: a later Resume intent overlaps dispatch claim for Session %s", ErrInvalidJob, key)
				}
			}
			if i > 0 {
				previous := claims[i-1]
				previousRelease, ok := releasesByClaim[previous.ID]
				if !ok || previousRelease.CreatedRevision >= claim.CreatedRevision {
					return fmt.Errorf("%w: Resume dispatch claims overlap for Session %s", ErrInvalidJob, key)
				}
			}
		}
	}
	return nil
}
