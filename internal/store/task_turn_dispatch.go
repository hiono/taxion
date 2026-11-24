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
	addTaskTurnDispatchClaimCommandType   = "AddTaskTurnDispatchClaim"
	addTaskTurnDispatchReleaseCommandType = "AddTaskTurnDispatchRelease"
)

func (s *Store) AddTaskTurnDispatchClaim(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	turnID, commandID string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.TaskTurnDispatchClaim, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.TaskTurnDispatchClaim{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, TurnID, CommandID, PrincipalID string
	}{jobID, turnID, commandID, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addTaskTurnDispatchClaimCommandType, request,
		func(job domain.Job, now time.Time) (domain.TaskTurnDispatchClaim, error) {
			return domain.NewTaskTurnDispatchClaim(job, turnID, principal.PrincipalID(), commandID, now)
		},
		func(job *domain.Job, claim domain.TaskTurnDispatchClaim) {
			job.TaskTurnDispatchClaims = append(job.TaskTurnDispatchClaims, claim)
		},
		func(job domain.Job) (domain.TaskTurnDispatchClaim, bool) {
			for _, claim := range job.TaskTurnDispatchClaims {
				if claim.CommandID == commandID {
					return claim, true
				}
			}
			return domain.TaskTurnDispatchClaim{}, false
		},
	)
}

func (s *Store) AddTaskTurnDispatchRelease(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	claimID, commandID, outcome, evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.TaskTurnDispatchRelease, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.TaskTurnDispatchRelease{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, ClaimID, CommandID, Outcome, EvidenceSHA256, PrincipalID string
	}{jobID, claimID, commandID, outcome, evidenceSHA256, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addTaskTurnDispatchReleaseCommandType, request,
		func(job domain.Job, now time.Time) (domain.TaskTurnDispatchRelease, error) {
			return domain.NewTaskTurnDispatchRelease(job, claimID, principal.PrincipalID(), commandID, outcome, evidenceSHA256, now)
		},
		func(job *domain.Job, release domain.TaskTurnDispatchRelease) {
			job.TaskTurnDispatchReleases = append(job.TaskTurnDispatchReleases, release)
		},
		func(job domain.Job) (domain.TaskTurnDispatchRelease, bool) {
			for _, release := range job.TaskTurnDispatchReleases {
				if release.CommandID == commandID {
					return release, true
				}
			}
			return domain.TaskTurnDispatchRelease{}, false
		},
	)
}

func (s *Store) ReleaseTaskTurnDispatchClaim(
	ctx context.Context,
	jobID, claimID string,
	principal auth.AuthenticatedPrincipal,
	outcome, evidenceSHA256 string,
) (CommandReceipt, domain.TaskTurnDispatchRelease, error) {
	if claimID == "" || outcome == "" {
		return CommandReceipt{}, domain.TaskTurnDispatchRelease{}, fmt.Errorf("Task Turn dispatch claim and outcome are required")
	}
	commandID := domain.TaskTurnDispatchReleaseCommandID(claimID, outcome)
	var lastConflict error
	for attempt := 0; attempt < 5; attempt++ {
		job, err := s.GetJob(ctx, jobID)
		if err != nil {
			return CommandReceipt{}, domain.TaskTurnDispatchRelease{}, err
		}
		receipt, release, _, err := s.AddTaskTurnDispatchRelease(
			ctx, jobID, job.Revision, claimID, commandID, outcome, evidenceSHA256, principal,
		)
		if err == nil {
			return receipt, release, nil
		}
		if !errors.Is(err, ErrIdempotencyConflict) {
			return CommandReceipt{}, domain.TaskTurnDispatchRelease{}, err
		}
		lastConflict = err
	}
	return CommandReceipt{}, domain.TaskTurnDispatchRelease{}, fmt.Errorf(
		"retry Task Turn dispatch claim release after concurrent Job revisions: %w", lastConflict,
	)
}
