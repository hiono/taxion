package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

// ResumeTaskExecution reattaches the same accepted Provider Session for the approved Task Coder execution.
func (c *ProviderController) ResumeTaskExecution(
	ctx context.Context,
	jobID, roleExecutionID, permissionID, commandID string,
	permissionRequest domain.ExecutionPermissionRequest,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.PlanningResumeIntent, domain.PlanningResumeObservation, provider.ResumeResult, error) {
	if ctx == nil || c.worktreeVerifier == nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, provider.ErrProviderInvalidRequest
	}
	job, execution, permission, task, assignment, start, sessionID, err := c.taskProviderExecutionTarget(
		ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if execution.Role != domain.RoleCoder || permission.Role != domain.RoleCoder {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, errors.New("Task Provider Resume requires a Coder execution")
	}
	if err := permission.ValidateFor(job, permission.OperationID, permission.RequestSHA256, c.now()); err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if err := c.worktreeVerifier.Verify(ctx, job.ID, task, assignment.Snapshot()); err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, fmt.Errorf("verify owned Task Worktree before Resume: %w", err)
	}

	var existing *domain.PlanningResumeIntent
	for i := range job.PlanningResumeIntents {
		candidate := &job.PlanningResumeIntents[i]
		if candidate.CommandID == commandID && candidate.RoleExecutionID == execution.ID {
			existing = candidate
			break
		}
	}
	if existing != nil {
		if existing.ProviderSessionID != sessionID || existing.TaskID != task.ID ||
			existing.ExecutionPermissionID != permission.ID {
			return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, errors.New("existing Task Resume intent is bound to another Task authority or Session")
		}
		if previous, found := latestProviderResumeObservation(job, existing.ID); found &&
			previous.Status != domain.PlanningResumeUnknown && previous.Status != domain.PlanningResumeNotDispatched {
			receipt, intent, err := c.store.AddPlanningResumeIntent(
				ctx, job.ID, job.Revision, execution.ID, sessionID, commandID,
				existing.CapabilityEvidenceSHA256, true, principal,
			)
			if err != nil {
				return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
			}
			outcome := domain.PlanningResumeDispatchReleasedAttached
			resultErr := error(nil)
			if previous.Status == domain.PlanningResumeFailed {
				outcome = domain.PlanningResumeDispatchReleasedFailed
				resultErr = errors.New("Task Provider Resume previously failed")
			}
			releaseErr := c.releasePlanningResumeDispatchClaim(ctx, job.ID, intent, outcome, principal)
			return receipt, intent, previous, resumeResultFromObservation(previous), errors.Join(resultErr, releaseErr)
		}
	}

	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	identity := c.runtime.Identity()
	if capabilities.Identity != identity || !capabilities.SupportsCrossProcessReattachment() ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityResumeIdempotency, provider.EvidenceLive) {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, provider.ErrUnsupportedCapability
	}
	evidenceSHA256 := ""
	if existing != nil {
		evidenceSHA256 = existing.CapabilityEvidenceSHA256
		if !hasCapabilityEvidenceDigest(capabilities, provider.CapabilitySessionReattach, provider.EvidenceDurable, evidenceSHA256) {
			return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, provider.ErrUnsupportedCapability
		}
	} else {
		evidenceSHA256, err = capabilityEvidenceDigest(
			capabilities, provider.CapabilitySessionReattach, provider.EvidenceDurable,
		)
		if err != nil {
			return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
		}
	}

	receipt, intent, err := c.store.AddPlanningResumeIntent(
		ctx, job.ID, job.Revision, execution.ID, sessionID, commandID, evidenceSHA256, true, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	current, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	for _, candidate := range current.PlanningResumeIntents {
		if candidate.ID == intent.ID {
			intent = candidate
			break
		}
	}
	if err := intent.Validate(current); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if previous, found := latestProviderResumeObservation(current, intent.ID); found &&
		previous.Status != domain.PlanningResumeUnknown && previous.Status != domain.PlanningResumeNotDispatched {
		outcome := domain.PlanningResumeDispatchReleasedAttached
		resultErr := error(nil)
		if previous.Status == domain.PlanningResumeFailed {
			outcome = domain.PlanningResumeDispatchReleasedFailed
			resultErr = errors.New("Task Provider Resume previously failed")
		}
		releaseErr := c.releasePlanningResumeDispatchClaim(ctx, current.ID, intent, outcome, principal)
		return receipt, intent, previous, resumeResultFromObservation(previous), errors.Join(resultErr, releaseErr)
	}

	request := provider.ResumeRequest{
		OperationID:        intent.OperationID,
		SessionID:          intent.ProviderSessionID,
		Deadline:           permission.ExpiresAt,
		RequireIdempotency: true,
	}
	if err := request.Validate(); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, provider.ErrProviderInvalidRequest
	}
	latest, currentExecution, currentPermission, currentTask, currentAssignment, currentStart, currentSession, err :=
		c.taskProviderExecutionTarget(ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal)
	if err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if latest.ID != job.ID || currentExecution.ID != execution.ID || currentPermission.ID != permission.ID ||
		currentTask.ID != task.ID || currentAssignment != assignment || currentStart.ID != start.ID ||
		currentSession != sessionID {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, errors.New("Task Resume authority, Worktree, or accepted Session changed after intent commit")
	}
	if err := currentPermission.ValidateFor(latest, currentPermission.OperationID, currentPermission.RequestSHA256, c.now()); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	for _, stopIntent := range latest.ProviderStopIntents {
		if stopIntent.RoleExecutionID == execution.ID && stopIntent.SessionID == sessionID {
			return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, errors.New("Task Provider Resume is blocked by an existing Stop intent")
		}
	}
	if err := c.worktreeVerifier.Verify(ctx, latest.ID, currentTask, currentAssignment.Snapshot()); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{},
			fmt.Errorf("reverify owned Task Worktree immediately before Resume: %w", err)
	}

	latest, err = c.store.GetJob(ctx, jobID)
	if err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	claimCommandID := domain.PlanningResumeDispatchClaimCommandID(intent.ID)
	_, claim, _, err := c.store.AddPlanningResumeDispatchClaim(
		ctx, latest.ID, latest.Revision, intent.ID, claimCommandID, principal,
	)
	if err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	claimedJob, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	activeClaim, active := domain.ActivePlanningResumeDispatchClaim(claimedJob, intent.OperationID)
	if !active || activeClaim.ID != claim.ID {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, provider.ErrResumeUnconfirmed
	}
	_, claimedExecution, claimedPermission, claimedTask, claimedAssignment, claimedStart, claimedSession, err :=
		c.taskProviderExecutionTarget(ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal)
	if err != nil || claimedJob.ID != latest.ID || claimedExecution.ID != execution.ID ||
		claimedPermission.ID != permission.ID || claimedTask.ID != task.ID ||
		claimedAssignment != assignment || claimedStart.ID != start.ID || claimedSession != sessionID {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{},
			errors.Join(errors.New("Task Resume authority changed after acquiring dispatch claim"), err)
	}
	if err := c.worktreeVerifier.Verify(ctx, claimedJob.ID, claimedTask, claimedAssignment.Snapshot()); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{},
			fmt.Errorf("reverify owned Task Worktree after Resume dispatch claim: %w", err)
	}

	result, err := c.runtime.Resume(ctx, request)
	if err != nil {
		if evidence, definitive := providerResumeDefinitiveFailureEvidence(intent.OperationID, err); definitive {
			observation, persistErr := c.recordResumeObservation(
				ctx, jobID, intent.ID, domain.PlanningResumeFailed, evidence, provider.ResumeResult{}, principal,
			)
			if persistErr != nil {
				return receipt, intent, observation, provider.ResumeResult{}, errors.Join(err, persistErr)
			}
			releaseErr := c.releasePlanningResumeDispatchClaim(
				ctx, jobID, intent, domain.PlanningResumeDispatchReleasedFailed, principal,
			)
			return receipt, intent, observation, provider.ResumeResult{}, errors.Join(err, releaseErr)
		}
		observation, persistErr := c.recordResumeObservation(
			ctx, jobID, intent.ID, domain.PlanningResumeUnknown, "", provider.ResumeResult{}, principal,
		)
		if persistErr != nil {
			return receipt, intent, observation, provider.ResumeResult{}, errors.Join(provider.ErrResumeUnconfirmed, err, persistErr)
		}
		return receipt, intent, observation, provider.ResumeResult{}, errors.Join(provider.ErrResumeUnconfirmed, err)
	}
	if result.OperationID != intent.OperationID || result.SessionID != intent.ProviderSessionID ||
		!result.Attached || !validProviderSHA256(result.EvidenceSHA256) {
		observation, persistErr := c.recordResumeObservation(
			ctx, jobID, intent.ID, domain.PlanningResumeUnknown, "", provider.ResumeResult{}, principal,
		)
		if persistErr != nil {
			return receipt, intent, observation, provider.ResumeResult{}, errors.Join(provider.ErrResumeUnconfirmed, persistErr)
		}
		return receipt, intent, observation, provider.ResumeResult{}, provider.ErrResumeUnconfirmed
	}
	observation, err := c.recordResumeObservation(
		ctx, jobID, intent.ID, domain.PlanningResumeAttached, result.EvidenceSHA256, result, principal,
	)
	if err != nil {
		unknown, persistErr := c.recordResumeObservation(
			ctx, jobID, intent.ID, domain.PlanningResumeUnknown, "", provider.ResumeResult{}, principal,
		)
		if persistErr != nil {
			return receipt, intent, unknown, provider.ResumeResult{}, errors.Join(err, persistErr)
		}
		return receipt, intent, unknown, provider.ResumeResult{}, err
	}
	releaseErr := c.releasePlanningResumeDispatchClaim(
		ctx, jobID, intent, domain.PlanningResumeDispatchReleasedAttached, principal,
	)
	return receipt, intent, observation, result, releaseErr
}

func (c *ProviderController) releasePlanningResumeDispatchClaim(
	ctx context.Context,
	jobID string,
	intent domain.PlanningResumeIntent,
	outcome string,
	principal auth.AuthenticatedPrincipal,
) error {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	claim, active := domain.ActivePlanningResumeDispatchClaim(job, intent.OperationID)
	if !active || claim.ResumeID != intent.ID {
		return nil
	}
	_, _, err = c.store.ReleasePlanningResumeDispatchClaim(ctx, jobID, claim.ID, principal, outcome)
	return err
}
