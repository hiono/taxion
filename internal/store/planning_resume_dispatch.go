package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
)

const (
	addPlanningResumeDispatchClaimCommandType   = "AddPlanningResumeDispatchClaim"
	addPlanningResumeDispatchReleaseCommandType = "AddPlanningResumeDispatchRelease"
)

func (s *Store) AddPlanningResumeDispatchClaim(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	resumeID, commandID string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.PlanningResumeDispatchClaim, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.PlanningResumeDispatchClaim{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, ResumeID, CommandID, PrincipalID string
	}{jobID, resumeID, commandID, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addPlanningResumeDispatchClaimCommandType, request,
		func(job domain.Job, now time.Time) (domain.PlanningResumeDispatchClaim, error) {
			return domain.NewPlanningResumeDispatchClaim(job, resumeID, principal.PrincipalID(), commandID, now)
		},
		func(job *domain.Job, claim domain.PlanningResumeDispatchClaim) {
			job.PlanningResumeDispatchClaims = append(job.PlanningResumeDispatchClaims, claim)
		},
		func(job domain.Job) (domain.PlanningResumeDispatchClaim, bool) {
			for _, claim := range job.PlanningResumeDispatchClaims {
				if claim.CommandID == commandID {
					return claim, true
				}
			}
			return domain.PlanningResumeDispatchClaim{}, false
		},
	)
}

func (s *Store) AddPlanningResumeDispatchRelease(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	claimID, commandID, outcome string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.PlanningResumeDispatchRelease, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.PlanningResumeDispatchRelease{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, ClaimID, CommandID, Outcome, PrincipalID string
	}{jobID, claimID, commandID, outcome, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addPlanningResumeDispatchReleaseCommandType, request,
		func(job domain.Job, now time.Time) (domain.PlanningResumeDispatchRelease, error) {
			return domain.NewPlanningResumeDispatchRelease(job, claimID, principal.PrincipalID(), commandID, outcome, now)
		},
		func(job *domain.Job, release domain.PlanningResumeDispatchRelease) {
			job.PlanningResumeDispatchReleases = append(job.PlanningResumeDispatchReleases, release)
		},
		func(job domain.Job) (domain.PlanningResumeDispatchRelease, bool) {
			for _, release := range job.PlanningResumeDispatchReleases {
				if release.CommandID == commandID {
					return release, true
				}
			}
			return domain.PlanningResumeDispatchRelease{}, false
		},
	)
}

func (s *Store) ReleasePlanningResumeDispatchClaim(
	ctx context.Context,
	jobID, claimID string,
	principal auth.AuthenticatedPrincipal,
	outcome string,
) (CommandReceipt, domain.PlanningResumeDispatchRelease, error) {
	if claimID == "" || outcome == "" {
		return CommandReceipt{}, domain.PlanningResumeDispatchRelease{}, errors.New("Resume dispatch claim and outcome are required")
	}
	commandID := domain.PlanningResumeDispatchReleaseCommandID(claimID, outcome)
	var lastConflict error
	for attempt := 0; attempt < 5; attempt++ {
		job, err := s.GetJob(ctx, jobID)
		if err != nil {
			return CommandReceipt{}, domain.PlanningResumeDispatchRelease{}, err
		}
		receipt, release, _, err := s.AddPlanningResumeDispatchRelease(
			ctx, jobID, job.Revision, claimID, commandID, outcome, principal,
		)
		if err == nil {
			return receipt, release, nil
		}
		if !errors.Is(err, ErrIdempotencyConflict) {
			return CommandReceipt{}, domain.PlanningResumeDispatchRelease{}, err
		}
		lastConflict = err
	}
	return CommandReceipt{}, domain.PlanningResumeDispatchRelease{}, fmt.Errorf(
		"retry Resume dispatch claim release after concurrent Job revisions: %w", lastConflict,
	)
}
