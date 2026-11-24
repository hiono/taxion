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
	addProviderStopDispatchClaimCommandType   = "AddProviderStopDispatchClaim"
	addProviderStopDispatchStartCommandType   = "AddProviderStopDispatchStart"
	addProviderStopDispatchReleaseCommandType = "AddProviderStopDispatchRelease"
)

func (s *Store) AddProviderStopDispatchClaim(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	stopIntentID, commandID string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderStopDispatchClaim, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.ProviderStopDispatchClaim{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, StopIntentID, CommandID, PrincipalID string
	}{jobID, stopIntentID, commandID, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addProviderStopDispatchClaimCommandType, request,
		func(job domain.Job, now time.Time) (domain.ProviderStopDispatchClaim, error) {
			return domain.NewProviderStopDispatchClaim(job, stopIntentID, principal.PrincipalID(), commandID, now)
		},
		func(job *domain.Job, claim domain.ProviderStopDispatchClaim) {
			job.ProviderStopDispatchClaims = append(job.ProviderStopDispatchClaims, claim)
		},
		func(job domain.Job) (domain.ProviderStopDispatchClaim, bool) {
			for _, claim := range job.ProviderStopDispatchClaims {
				if claim.CommandID == commandID {
					return claim, true
				}
			}
			return domain.ProviderStopDispatchClaim{}, false
		},
	)
}

func (s *Store) AddProviderStopDispatchStart(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	claimID, commandID string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderStopDispatchStart, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.ProviderStopDispatchStart{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, ClaimID, CommandID, PrincipalID string
	}{jobID, claimID, commandID, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addProviderStopDispatchStartCommandType, request,
		func(job domain.Job, now time.Time) (domain.ProviderStopDispatchStart, error) {
			return domain.NewProviderStopDispatchStart(job, claimID, principal.PrincipalID(), commandID, now)
		},
		func(job *domain.Job, start domain.ProviderStopDispatchStart) {
			job.ProviderStopDispatchStarts = append(job.ProviderStopDispatchStarts, start)
		},
		func(job domain.Job) (domain.ProviderStopDispatchStart, bool) {
			for _, start := range job.ProviderStopDispatchStarts {
				if start.CommandID == commandID {
					return start, true
				}
			}
			return domain.ProviderStopDispatchStart{}, false
		},
	)
}

func (s *Store) StartProviderStopDispatchClaim(
	ctx context.Context,
	jobID, claimID string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderStopDispatchStart, bool, error) {
	if claimID == "" {
		return CommandReceipt{}, domain.ProviderStopDispatchStart{}, false, errors.New("Provider Stop dispatch claim is required")
	}
	job, err := s.GetJob(ctx, jobID)
	if err != nil {
		return CommandReceipt{}, domain.ProviderStopDispatchStart{}, false, err
	}
	commandID := domain.ProviderStopDispatchStartCommandID(claimID)
	return s.AddProviderStopDispatchStart(ctx, jobID, job.Revision, claimID, commandID, principal)
}

func (s *Store) AddProviderStopDispatchRelease(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	claimID, commandID, outcome, evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderStopDispatchRelease, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.ProviderStopDispatchRelease{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, ClaimID, CommandID, Outcome, EvidenceSHA256, PrincipalID string
	}{jobID, claimID, commandID, outcome, evidenceSHA256, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addProviderStopDispatchReleaseCommandType, request,
		func(job domain.Job, now time.Time) (domain.ProviderStopDispatchRelease, error) {
			return domain.NewProviderStopDispatchRelease(job, claimID, principal.PrincipalID(), commandID, outcome, evidenceSHA256, now)
		},
		func(job *domain.Job, release domain.ProviderStopDispatchRelease) {
			job.ProviderStopDispatchReleases = append(job.ProviderStopDispatchReleases, release)
		},
		func(job domain.Job) (domain.ProviderStopDispatchRelease, bool) {
			for _, release := range job.ProviderStopDispatchReleases {
				if release.CommandID == commandID {
					return release, true
				}
			}
			return domain.ProviderStopDispatchRelease{}, false
		},
	)
}

func (s *Store) ReleaseProviderStopDispatchClaim(
	ctx context.Context,
	jobID string,
	claimID string,
	principal auth.AuthenticatedPrincipal,
	outcome, evidenceSHA256 string,
) (CommandReceipt, domain.ProviderStopDispatchRelease, error) {
	if claimID == "" || outcome == "" {
		return CommandReceipt{}, domain.ProviderStopDispatchRelease{}, errors.New("Provider Stop dispatch claim and outcome are required")
	}
	commandID := domain.ProviderStopDispatchReleaseCommandID(claimID, outcome)
	job, err := s.GetJob(ctx, jobID)
	if err != nil {
		return CommandReceipt{}, domain.ProviderStopDispatchRelease{}, err
	}
	receipt, release, _, err := s.AddProviderStopDispatchRelease(
		ctx, jobID, job.Revision, claimID, commandID, outcome, evidenceSHA256, principal,
	)
	return receipt, release, err
}
