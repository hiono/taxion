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

func (s *Store) AddProviderStopDispatchRelease(
	ctx context.Context,
	jobID string,
	expectedRevision uint64,
	claimID, commandID, outcome string,
	principal auth.AuthenticatedPrincipal,
) (CommandReceipt, domain.ProviderStopDispatchRelease, bool, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.ProviderStopDispatchRelease{}, false, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	request := struct {
		JobID, ClaimID, CommandID, Outcome, PrincipalID string
	}{jobID, claimID, commandID, outcome, principal.PrincipalID()}
	return persistPlanningFactWithStatus(
		ctx, s, jobID, expectedRevision, commandID, addProviderStopDispatchReleaseCommandType, request,
		func(job domain.Job, now time.Time) (domain.ProviderStopDispatchRelease, error) {
			return domain.NewProviderStopDispatchRelease(job, claimID, principal.PrincipalID(), commandID, outcome, now)
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
	outcome string,
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
		ctx, jobID, job.Revision, claimID, commandID, outcome, principal,
	)
	return receipt, release, err
}
