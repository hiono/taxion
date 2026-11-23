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
	if capabilities.Identity != identity ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityStopProof, provider.EvidenceLive) {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, provider.ErrUnsupportedCapability
	}
	existingStopIntent := false
	for _, existing := range job.ProviderStopIntents {
		if existing.CommandID == commandID && existing.RoleExecutionID == execution.ID {
			existingStopIntent = true
			break
		}
	}
	intentReceipt, intent, err := c.store.AddProviderStopIntent(
		ctx, jobID, job.Revision, roleExecutionID, sessionID, commandID, reason, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	currentJob, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return intentReceipt, intent, domain.ProviderStopObservation{}, err
	}
	previous, hasPrevious := latestProviderStopObservation(currentJob, intent.OperationID)
	if hasPrevious && previous.Quiescent {
		return intentReceipt, intent, previous, nil
	}
	if existingStopIntent {
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

	stopResult, stopErr := c.runtime.Stop(ctx, provider.StopRequest{
		OperationID: intent.OperationID,
		SessionID:   sessionID,
	})
	if stopErr != nil {
		if providerOperationMayHaveStarted(stopErr) {
			observation, persistErr := c.persistProviderStopObservation(
				ctx, jobID, intent, principal, false, false, "", false,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(stopErr, persistErr)
			}
			return intentReceipt, intent, observation, stopErr
		}
		if evidence, definitive := providerStopDefinitiveFailureEvidence(intent.OperationID, stopErr); definitive {
			observation, persistErr := c.persistProviderStopObservation(
				ctx, jobID, intent, principal, false, false, evidence, false,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(stopErr, persistErr)
			}
			return intentReceipt, intent, observation, stopErr
		}
		return intentReceipt, intent, domain.ProviderStopObservation{}, stopErr
	}
	if stopResult.OperationID != intent.OperationID || stopResult.SessionID != sessionID ||
		!stopResult.Stopped || !stopResult.Quiescent || !validProviderEvidenceSHA256(stopResult.EvidenceSHA256) {
		observation, persistErr := c.persistProviderStopObservation(
			ctx, jobID, intent, principal, false, false, "", false,
		)
		if persistErr != nil {
			return intentReceipt, intent, observation, errors.Join(provider.ErrStopUnconfirmed, persistErr)
		}
		return intentReceipt, intent, observation, provider.ErrStopUnconfirmed
	}
	observation, err := c.persistProviderStopObservation(
		ctx, jobID, intent, principal, true, true, stopResult.EvidenceSHA256, false,
	)
	return intentReceipt, intent, observation, err
}
