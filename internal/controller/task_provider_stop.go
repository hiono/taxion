package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

// StopTaskExecution stops only the accepted Session owned by the approved Task Coder execution.
// The Stop intent is durable before Runtime.Stop and an ambiguous retry never repeats the side effect.
func (c *ProviderController) StopTaskExecution(
	ctx context.Context,
	jobID, roleExecutionID, permissionID, commandID string,
	permissionRequest domain.ExecutionPermissionRequest,
	reason domain.ProviderStopReason,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderStopIntent, domain.ProviderStopObservation, error) {
	if ctx == nil || c.worktreeVerifier == nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, provider.ErrProviderInvalidRequest
	}
	job, execution, permission, task, assignment, _, sessionID, err := c.taskProviderExecutionTarget(
		ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	if execution.Role != domain.RoleCoder || permission.Role != domain.RoleCoder {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Task Provider Stop requires a Coder execution")
	}
	if err := c.worktreeVerifier.Verify(ctx, job.ID, task, assignment.Snapshot()); err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, fmt.Errorf("verify owned Task Worktree before Stop: %w", err)
	}
	identity := c.runtime.Identity()
	if identity.ProviderID != permission.ProviderID || identity.AccountID != permission.AccountID ||
		identity.ConfigurationSHA256 != permission.ConfigurationSHA256 {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Runtime identity differs from ExecutionPermission")
	}
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	if capabilities.Identity != identity {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, provider.ErrUnsupportedCapability
	}
	fencingEvidenceSHA256 := providerCapabilityEvidenceSHA256(capabilities, provider.CapabilityStopDispatchFencing)
	existingStopIntent := false
	for _, existing := range job.ProviderStopIntents {
		if existing.CommandID == commandID && existing.RoleExecutionID == execution.ID {
			existingStopIntent = true
			break
		}
	}
	if !existingStopIntent && !capabilities.HasEvidenceAtLeast(provider.CapabilityStopProof, provider.EvidenceLive) {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, provider.ErrUnsupportedCapability
	}
	intentReceipt, intent, created, err := c.store.AddProviderStopIntentWithStatus(
		ctx, jobID, job.Revision, roleExecutionID, sessionID, commandID, reason, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	currentJob, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return intentReceipt, intent, domain.ProviderStopObservation{}, err
	}
	existingStopIntent = !created
	previous, hasPrevious := latestProviderStopObservation(currentJob, intent.OperationID)
	previousUnknown := hasPrevious && !previous.Stopped && !previous.Quiescent && previous.EvidenceSHA256 == ""
	activeClaim, activeClaimFound := domain.ActiveProviderStopDispatchClaim(currentJob, intent.OperationID)
	fencingRequired := providerStopDispatchRequiresFencing(currentJob, intent.OperationID)
	if activeClaimFound && hasPrevious && !previous.Stopped && !previous.Quiescent &&
		validProviderEvidenceSHA256(previous.EvidenceSHA256) {
		if _, started := domain.ProviderStopDispatchStartedForClaim(currentJob, activeClaim.ID); started {
			if _, _, releaseErr := c.store.ReleaseProviderStopDispatchClaim(
				ctx, jobID, activeClaim.ID, principal, domain.ProviderStopDispatchReleasedDefinitiveFailure, previous.EvidenceSHA256,
			); releaseErr != nil {
				return intentReceipt, intent, previous, releaseErr
			}
			return intentReceipt, intent, previous, provider.ErrStopUnconfirmed
		}
	}
	if hasPrevious && previous.Stopped && previous.Quiescent {
		if activeClaimFound {
			if _, _, releaseErr := c.store.ReleaseProviderStopDispatchClaim(
				ctx, jobID, activeClaim.ID, principal, domain.ProviderStopDispatchReleasedStopped, "",
			); releaseErr != nil {
				return intentReceipt, intent, previous, releaseErr
			}
		}
		return intentReceipt, intent, previous, nil
	}
	if existingStopIntent {
		if !capabilities.HasEvidenceAtLeast(provider.CapabilityStopLookup, provider.EvidenceLive) {
			if hasPrevious {
				return intentReceipt, intent, previous, provider.ErrStopUnconfirmed
			}
			observation, persistErr := c.persistProviderStopObservation(
				ctx, currentJob, intent, principal, false, false, "", false,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, persistErr)
			}
			return intentReceipt, intent, observation, provider.ErrStopUnconfirmed
		}

		lookupRequest := provider.LookupRequest{Purpose: provider.LookupPurposeStop, OperationID: intent.OperationID, SessionID: sessionID}
		lookupClaimID, lookupStartID := "", ""
		if activeClaimFound {
			lookupClaimID = activeClaim.ID
			if start, started := domain.ProviderStopDispatchStartedForClaim(currentJob, activeClaim.ID); started {
				lookupStartID = start.ID
			}
		}
		lookupFencingRequired := fencingRequired
		lookup, lookupErr := c.runtime.Lookup(ctx, lookupRequest)
		lookupObservedAt := time.Now().UTC().Round(0)
		currentJob, err = c.store.GetJob(ctx, jobID)
		if err != nil {
			return intentReceipt, intent, domain.ProviderStopObservation{}, err
		}
		previous, hasPrevious = latestProviderStopObservation(currentJob, intent.OperationID)
		previousUnknown = hasPrevious && !previous.Stopped && !previous.Quiescent && previous.EvidenceSHA256 == ""
		activeClaim, activeClaimFound = domain.ActiveProviderStopDispatchClaim(currentJob, intent.OperationID)
		fencingRequired = providerStopDispatchRequiresFencing(currentJob, intent.OperationID)
		currentClaimID, currentStartID := "", ""
		if activeClaimFound {
			currentClaimID = activeClaim.ID
			if start, started := domain.ProviderStopDispatchStartedForClaim(currentJob, activeClaim.ID); started {
				currentStartID = start.ID
			}
		}
		if currentClaimID != lookupClaimID || currentStartID != lookupStartID || fencingRequired != lookupFencingRequired {
			return intentReceipt, intent, previous, provider.ErrStopUnconfirmed
		}
		if hasPrevious && previous.Stopped && previous.Quiescent {
			if activeClaimFound {
				if _, _, releaseErr := c.store.ReleaseProviderStopDispatchClaim(
					ctx, jobID, activeClaim.ID, principal, domain.ProviderStopDispatchReleasedStopped, "",
				); releaseErr != nil {
					return intentReceipt, intent, previous, releaseErr
				}
			}
			return intentReceipt, intent, previous, nil
		}
		lookupValidationErr := lookup.Validate(lookupRequest)
		if lookupErr != nil || lookupValidationErr != nil {
			if hasPrevious {
				return intentReceipt, intent, previous, errors.Join(provider.ErrStopUnconfirmed, lookupErr, lookupValidationErr)
			}
			observation, persistErr := c.persistProviderStopObservation(
				ctx, currentJob, intent, principal, false, false, "", false,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, lookupErr, persistErr)
			}
			return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, lookupErr)
		}
		if lookup.Found {
			if lookup.Status == "STOPPED" && lookup.Stopped && lookup.Quiescent {
				reconcilesUnknown := previousUnknown
				observation, persistErr := c.persistProviderStopObservation(
					ctx, currentJob, intent, principal, true, true, lookup.EvidenceSHA256, reconcilesUnknown,
				)
				if persistErr != nil {
					return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, persistErr)
				}
				if activeClaimFound {
					if _, _, releaseErr := c.store.ReleaseProviderStopDispatchClaim(
						ctx, jobID, activeClaim.ID, principal, domain.ProviderStopDispatchReleasedStopped, "",
					); releaseErr != nil {
						return intentReceipt, intent, observation, releaseErr
					}
				}
				return intentReceipt, intent, observation, nil
			}
			if hasPrevious {
				return intentReceipt, intent, previous, provider.ErrStopUnconfirmed
			}
			observation, persistErr := c.persistProviderStopObservation(
				ctx, currentJob, intent, principal, false, false, "", false,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, persistErr)
			}
			return intentReceipt, intent, observation, provider.ErrStopUnconfirmed
		}
		// A pre-start claim can be reclaimed directly. A started claim requires
		// Provider-enforced fencing so a paused caller's older token is rejected.
		if activeClaimFound {
			if startFact, started := domain.ProviderStopDispatchStartedForClaim(currentJob, activeClaim.ID); started {
				if !validProviderEvidenceSHA256(startFact.FencingEvidenceSHA256) ||
					!validProviderEvidenceSHA256(fencingEvidenceSHA256) {
					if hasPrevious {
						return intentReceipt, intent, previous, provider.ErrStopUnconfirmed
					}
					observation, persistErr := c.persistProviderStopObservation(
						ctx, currentJob, intent, principal, false, false, "", false,
					)
					return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, persistErr)
				}
				fencingRequired = true
				if _, _, releaseErr := c.store.ReleaseProviderStopDispatchClaimAt(
					ctx, jobID, activeClaim.ID, principal,
					domain.ProviderStopDispatchReleasedFencedNotFound, lookup.EvidenceSHA256, lookupObservedAt,
				); releaseErr != nil {
					return intentReceipt, intent, previous, releaseErr
				}
			} else {
				if _, _, releaseErr := c.store.ReleaseProviderStopDispatchClaim(
					ctx, jobID, activeClaim.ID, principal, domain.ProviderStopDispatchReleasedNotDispatched, lookup.EvidenceSHA256,
				); releaseErr != nil {
					return intentReceipt, intent, previous, releaseErr
				}
			}
		}
	}
	if fencingRequired && !validProviderEvidenceSHA256(fencingEvidenceSHA256) {
		if hasPrevious {
			return intentReceipt, intent, previous, provider.ErrStopUnconfirmed
		}
		observation, persistErr := c.persistProviderStopObservation(
			ctx, currentJob, intent, principal, false, false, "", false,
		)
		return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, persistErr)
	}

	latest, currentExecution, currentPermission, currentTask, currentAssignment, _, currentSession, err :=
		c.taskProviderExecutionTarget(ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal)
	if err != nil {
		return intentReceipt, intent, domain.ProviderStopObservation{}, err
	}
	if latest.ID != job.ID || currentExecution.ID != execution.ID || currentPermission.ID != permission.ID ||
		currentTask.ID != task.ID || currentAssignment.ID != assignment.ID || currentSession != sessionID {
		return intentReceipt, intent, domain.ProviderStopObservation{}, errors.New("Task Provider Stop target changed after intent commit")
	}
	if err := c.worktreeVerifier.Verify(ctx, latest.ID, currentTask, currentAssignment.Snapshot()); err != nil {
		return intentReceipt, intent, domain.ProviderStopObservation{}, fmt.Errorf("reverify owned Task Worktree immediately before Stop: %w", err)
	}
	if !capabilities.HasEvidenceAtLeast(provider.CapabilityStopProof, provider.EvidenceLive) {
		return intentReceipt, intent, domain.ProviderStopObservation{}, provider.ErrUnsupportedCapability
	}

	claimCommandID, err := domain.NewID("cmd")
	if err != nil {
		return intentReceipt, intent, domain.ProviderStopObservation{}, err
	}
	_, claim, createdClaim, err := c.store.AddProviderStopDispatchClaim(
		ctx, jobID, latest.Revision, intent.ID, claimCommandID, principal,
	)
	if err != nil {
		return intentReceipt, intent, domain.ProviderStopObservation{}, err
	}
	if !createdClaim {
		return intentReceipt, intent, domain.ProviderStopObservation{}, provider.ErrStopUnconfirmed
	}
	postClaimJob, postExecution, postPermission, postTask, postAssignment, _, postSession, verifyErr :=
		c.taskProviderExecutionTarget(ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal)
	if verifyErr != nil || postClaimJob.ID != latest.ID || postExecution.ID != execution.ID ||
		postPermission.ID != permission.ID || postTask.ID != task.ID || postAssignment.ID != assignment.ID || postSession != sessionID {
		_, _, releaseErr := c.store.ReleaseProviderStopDispatchClaim(
			ctx, jobID, claim.ID, principal, domain.ProviderStopDispatchReleasedDefinitiveFailure, "",
		)
		return intentReceipt, intent, domain.ProviderStopObservation{}, errors.Join(errors.New("Task Provider Stop authority changed after claim acquisition"), verifyErr, releaseErr)
	}
	if err := postPermission.ValidateFor(postClaimJob, postPermission.OperationID, postPermission.RequestSHA256, c.now()); err != nil {
		_, _, releaseErr := c.store.ReleaseProviderStopDispatchClaim(
			ctx, jobID, claim.ID, principal, domain.ProviderStopDispatchReleasedDefinitiveFailure, "",
		)
		return intentReceipt, intent, domain.ProviderStopObservation{}, errors.Join(err, releaseErr)
	}
	if err := c.worktreeVerifier.Verify(ctx, postClaimJob.ID, postTask, postAssignment.Snapshot()); err != nil {
		_, _, releaseErr := c.store.ReleaseProviderStopDispatchClaim(
			ctx, jobID, claim.ID, principal, domain.ProviderStopDispatchReleasedDefinitiveFailure, "",
		)
		return intentReceipt, intent, domain.ProviderStopObservation{}, errors.Join(fmt.Errorf("reverify owned Task Worktree after Stop claim acquisition: %w", err), releaseErr)
	}
	startCommandID := domain.ProviderStopDispatchStartCommandID(claim.ID)
	_, _, started, err := c.store.AddProviderStopDispatchStart(
		ctx, jobID, postClaimJob.Revision, claim.ID, startCommandID, fencingEvidenceSHA256, principal,
	)
	if err != nil {
		return intentReceipt, intent, domain.ProviderStopObservation{}, err
	}
	if !started {
		return intentReceipt, intent, domain.ProviderStopObservation{}, provider.ErrStopUnconfirmed
	}
	stopResult, stopErr := c.runtime.Stop(ctx, provider.StopRequest{
		OperationID:    intent.OperationID,
		SessionID:      sessionID,
		FencingToken:   claim.CreatedRevision,
		RequireFencing: fencingEvidenceSHA256 != "",
	})
	latest, err = c.store.GetJob(ctx, jobID)
	if err != nil {
		return intentReceipt, intent, domain.ProviderStopObservation{}, err
	}
	latestPrevious, latestHasPrevious := latestProviderStopObservation(latest, intent.OperationID)
	latestPreviousUnknown := latestHasPrevious && !latestPrevious.Stopped && !latestPrevious.Quiescent && latestPrevious.EvidenceSHA256 == ""
	if latestHasPrevious && latestPrevious.Stopped && latestPrevious.Quiescent {
		return intentReceipt, intent, latestPrevious, nil
	}
	if stopErr != nil {
		if providerOperationMayHaveStarted(stopErr) {
			if latestPreviousUnknown {
				return intentReceipt, intent, latestPrevious, errors.Join(provider.ErrStopUnconfirmed, stopErr)
			}
			observation, persistErr := c.persistProviderStopObservation(
				ctx, latest, intent, principal, false, false, "", false,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(stopErr, persistErr)
			}
			return intentReceipt, intent, observation, stopErr
		}
		if evidence, definitive := providerStopDefinitiveFailureEvidenceForClaim(intent.OperationID, claim.ID, stopErr); definitive {
			observation, persistErr := c.persistProviderStopObservation(
				ctx, latest, intent, principal, false, false, evidence, latestPreviousUnknown,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(stopErr, persistErr)
			}
			_, _, releaseErr := c.store.ReleaseProviderStopDispatchClaim(
				ctx, jobID, claim.ID, principal, domain.ProviderStopDispatchReleasedDefinitiveFailure, evidence,
			)
			return intentReceipt, intent, observation, errors.Join(stopErr, releaseErr)
		}
		observation, persistErr := c.persistProviderStopObservation(
			ctx, latest, intent, principal, false, false, "", latestPreviousUnknown,
		)
		return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, stopErr, persistErr)
	}
	if stopResult.OperationID != intent.OperationID || stopResult.SessionID != sessionID ||
		!stopResult.Stopped || !stopResult.Quiescent || !validProviderEvidenceSHA256(stopResult.EvidenceSHA256) {
		if latestPreviousUnknown {
			return intentReceipt, intent, latestPrevious, provider.ErrStopUnconfirmed
		}
		observation, persistErr := c.persistProviderStopObservation(
			ctx, latest, intent, principal, false, false, "", false,
		)
		if persistErr != nil {
			return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, persistErr)
		}
		return intentReceipt, intent, observation, provider.ErrStopUnconfirmed
	}
	reconcilesUnknown := latestPreviousUnknown
	observation, err := c.persistProviderStopObservation(
		ctx, latest, intent, principal, true, true, stopResult.EvidenceSHA256, reconcilesUnknown,
	)
	if err != nil {
		return intentReceipt, intent, observation, err
	}
	if _, _, err := c.store.ReleaseProviderStopDispatchClaim(
		ctx, jobID, claim.ID, principal, domain.ProviderStopDispatchReleasedStopped, "",
	); err != nil {
		return intentReceipt, intent, observation, err
	}
	return intentReceipt, intent, observation, nil
}

func providerStopDispatchRequiresFencing(job domain.Job, operationID string) bool {
	for _, release := range job.ProviderStopDispatchReleases {
		if release.OperationID == operationID && release.Outcome == domain.ProviderStopDispatchReleasedFencedNotFound {
			return true
		}
	}
	return false
}
