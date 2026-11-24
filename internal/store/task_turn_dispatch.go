package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
)

const addTaskTurnDispatchObservationCommandType = "AddTaskTurnDispatchObservation"

func (s *Store) AddTaskTurnDispatchObservation(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	claimID, commandID, status, evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.TaskTurnDispatchObservation, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.TaskTurnDispatchObservation{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, ClaimID, CommandID, Status, EvidenceSHA256, PrincipalID string
	}{jobID, claimID, commandID, status, evidenceSHA256, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addTaskTurnDispatchObservationCommandType, request,
		func(job domain.Job, now time.Time) (domain.TaskTurnDispatchObservation, error) {
			return domain.NewTaskTurnDispatchObservation(job, claimID, principal.PrincipalID(), commandID, status, evidenceSHA256, now)
		},
		func(job *domain.Job, fact domain.TaskTurnDispatchObservation) {
			job.TaskTurnDispatchObservations = append(job.TaskTurnDispatchObservations, fact)
		},
		func(job domain.Job) (domain.TaskTurnDispatchObservation, bool) {
			for _, fact := range job.TaskTurnDispatchObservations {
				if fact.CommandID == commandID {
					return fact, true
				}
			}
			return domain.TaskTurnDispatchObservation{}, false
		},
	)
}

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
	claimID, commandID, outcome, evidenceSHA256, lookupEvidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.TaskTurnDispatchRelease, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.TaskTurnDispatchRelease{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, ClaimID, CommandID, Outcome, EvidenceSHA256, LookupEvidenceSHA256, PrincipalID string
	}{jobID, claimID, commandID, outcome, evidenceSHA256, lookupEvidenceSHA256, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addTaskTurnDispatchReleaseCommandType, request,
		func(job domain.Job, now time.Time) (domain.TaskTurnDispatchRelease, error) {
			return domain.NewTaskTurnDispatchRelease(job, claimID, principal.PrincipalID(), commandID, outcome, evidenceSHA256, lookupEvidenceSHA256, now)
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
	outcome, evidenceSHA256, lookupEvidenceSHA256 string,
) (CommandReceipt, domain.TaskTurnDispatchRelease, error) {
	if claimID == "" || outcome == "" {
		return CommandReceipt{}, domain.TaskTurnDispatchRelease{}, errors.New("Task Turn dispatch claim and outcome are required")
	}
	commandID := domain.TaskTurnDispatchReleaseCommandID(claimID, outcome)
	job, err := s.GetJob(ctx, jobID)
	if err != nil {
		return CommandReceipt{}, domain.TaskTurnDispatchRelease{}, err
	}
	receipt, release, _, err := s.AddTaskTurnDispatchRelease(
		ctx, jobID, job.Revision, claimID, commandID, outcome, evidenceSHA256, lookupEvidenceSHA256, principal,
	)
	return receipt, release, err
}
