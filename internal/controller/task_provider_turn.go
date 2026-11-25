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

// SendTaskExecution persists a TaskTurnIntent before sending and does not terminalize
// the Turn until final Result and complete Usage have both been persisted.
func (c *ProviderController) SendTaskExecution(
	ctx context.Context,
	jobID, roleExecutionID, permissionID, commandID, prompt string,
	permissionRequest domain.ExecutionPermissionRequest,
	reservedTokens uint64,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.TaskTurnIntent, domain.TaskTurnObservation, provider.TurnResult, error) {
	if ctx == nil || prompt == "" || reservedTokens == 0 {
		return store.CommandReceipt{}, domain.TaskTurnIntent{}, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrProviderInvalidRequest
	}
	job, execution, permission, task, _, _, sessionID, err := c.taskProviderExecutionTarget(
		ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.TaskTurnIntent{}, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	if permission.Role != domain.RoleCoder || execution.Role != domain.RoleCoder || task.ID != execution.TaskID {
		return store.CommandReceipt{}, domain.TaskTurnIntent{}, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.New("Task Provider Send requires the matching Coder execution")
	}
	identity := c.runtime.Identity()
	if identity.ProviderID != permission.ProviderID || identity.AccountID != permission.AccountID ||
		identity.ConfigurationSHA256 != permission.ConfigurationSHA256 {
		return store.CommandReceipt{}, domain.TaskTurnIntent{}, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.New("Provider Runtime identity differs from ExecutionPermission")
	}
	var capabilities provider.Capabilities
	existingTurn := false
	for _, existing := range job.TaskTurnIntents {
		if existing.CommandID == commandID && existing.RoleExecutionID == execution.ID {
			existingTurn = true
			break
		}
	}
	if !existingTurn {
		capabilities, err = c.runtime.Capabilities(ctx)
		if err != nil {
			return store.CommandReceipt{}, domain.TaskTurnIntent{}, domain.TaskTurnObservation{}, provider.TurnResult{}, err
		}
		if capabilities.Identity != identity || !taskTurnCapabilitiesVerified(capabilities) {
			return store.CommandReceipt{}, domain.TaskTurnIntent{}, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrUnsupportedCapability
		}
	}
	promptSHA := sha256String(prompt)
	request := provider.TurnRequest{
		OperationID: domain.TaskTurnOperationID(commandID), TurnID: domain.TaskTurnIntentID(commandID),
		SessionID: sessionID, PromptSHA256: promptSHA, Prompt: prompt,
		MaxTokens: reservedTokens, Deadline: permission.ExpiresAt,
		RequireIdempotency: capabilities.HasEvidenceAtLeast(provider.CapabilitySendIdempotency, provider.EvidenceLive),
	}
	if err := request.Validate(); err != nil {
		return store.CommandReceipt{}, domain.TaskTurnIntent{}, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrProviderInvalidRequest
	}
	receipt, turn, created, err := c.store.AddTaskTurnIntentWithStatus(
		ctx, jobID, job.Revision, execution.ID, permission.ID, sessionID, commandID, promptSHA, reservedTokens,
	)
	if err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	current, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	for _, candidate := range current.TaskTurnIntents {
		if candidate.CommandID == commandID {
			turn = candidate
			break
		}
	}
	if err := turn.Validate(current); err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	request.OperationID, request.TurnID = turn.OperationID, turn.ID
	if previous, found := latestTaskTurnObservation(current, turn.ID); found && previous.Status != domain.TaskTurnUnknown {
		if releaseErr := c.releaseTaskTurnDispatchClaim(ctx, jobID, turn, previous, principal); releaseErr != nil {
			return receipt, turn, previous, provider.TurnResult{}, releaseErr
		}
		return receipt, turn, previous, taskTurnResultFromObservation(previous), nil
	}
	if existingTurn {
		capabilities, err = c.runtime.Capabilities(ctx)
		if err != nil {
			return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
		}
	}
	if capabilities.Identity != identity || !taskTurnCapabilitiesVerified(capabilities) {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrUnsupportedCapability
	}
	// No claim or observation means no call could have crossed the dispatch
	// boundary. Recover that pre-dispatch case without relying on Provider Lookup.
	if !created && taskTurnNeverDispatched(current, turn.ID) {
		return c.dispatchTaskTurn(ctx, receipt, current, turn, request, permissionRequest, principal, capabilities)
	}
	// A repeated call may follow a process crash after Send. Reconcile the same
	// OperationID / TurnID before any idempotent retry.
	if !created {
		if _, active := domain.ActiveTaskTurnDispatchClaim(current, turn.OperationID); !active &&
			taskTurnRetryAuthorized(current, turn.ID) {
			request.RequireIdempotency = true
			return c.dispatchTaskTurn(ctx, receipt, current, turn, request, permissionRequest, principal, capabilities)
		}
		return c.reconcileTaskTurn(ctx, receipt, current, turn, request, permissionRequest, principal, capabilities)
	}
	return c.dispatchTaskTurn(ctx, receipt, current, turn, request, permissionRequest, principal, capabilities)
}

func taskTurnCapabilitiesVerified(capabilities provider.Capabilities) bool {
	return capabilities.HasEvidenceAtLeast(provider.CapabilitySendLookup, provider.EvidenceLive) &&
		capabilities.HasEvidenceAtLeast(provider.CapabilitySendIdempotency, provider.EvidenceLive) &&
		capabilities.HasEvidenceAtLeast(provider.CapabilityFinalOutput, provider.EvidenceLive) &&
		capabilities.HasEvidenceAtLeast(provider.CapabilityUsageIdentity, provider.EvidenceLive) &&
		capabilities.HasEvidenceAtLeast(provider.CapabilityUsageCompleteness, provider.EvidenceLive)
}

func taskTurnNeverDispatched(job domain.Job, turnID string) bool {
	for _, claim := range job.TaskTurnDispatchClaims {
		if claim.TurnID != turnID {
			continue
		}
		releasedBeforeDispatch := false
		for _, release := range job.TaskTurnDispatchReleases {
			if release.ClaimID == claim.ID {
				releasedBeforeDispatch = release.Outcome == domain.TaskTurnDispatchReleasedDefinitiveFailure
				break
			}
		}
		if !releasedBeforeDispatch {
			return false
		}
	}
	for _, observation := range job.TaskTurnObservations {
		if observation.TurnID == turnID {
			return false
		}
	}
	return true
}

func taskTurnRetryAuthorized(job domain.Job, turnID string) bool {
	for _, claim := range job.TaskTurnDispatchClaims {
		if claim.TurnID != turnID {
			continue
		}
		for _, release := range job.TaskTurnDispatchReleases {
			if release.ClaimID != claim.ID || release.Outcome != domain.TaskTurnDispatchReleasedIdempotentRetry {
				continue
			}
			observation, exists := taskTurnDispatchObservation(job, claim.ID, domain.TaskTurnDispatchObservationLookupNotFound)
			if exists && observation.EvidenceSHA256 == release.LookupEvidenceSHA256 && validProviderEvidenceSHA256(release.EvidenceSHA256) {
				return true
			}
		}
	}
	return false
}

func permissionRequestDigest(request domain.ExecutionPermissionRequest) string {
	digest, _ := domain.ExecutionPermissionRequestSHA256(request)
	return digest
}

func (c *ProviderController) taskProviderExecutionTarget(
	ctx context.Context,
	jobID, roleExecutionID, permissionID string,
	permissionRequest domain.ExecutionPermissionRequest,
	principal auth.AuthenticatedPrincipal,
) (domain.Job, domain.RoleExecution, domain.ExecutionPermission, domain.Task, domain.WorktreeAssignment, domain.ProviderStartIntent, string, error) {
	if ctx == nil {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", provider.ErrProviderInvalidRequest
	}
	if err := principal.Validate(); err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", err
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", err
	}
	if job.State != domain.JobActive {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", errors.New("Task Provider operation requires an ACTIVE Job")
	}
	var execution domain.RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution || execution.JobID != job.ID || execution.Role != domain.RoleCoder ||
		execution.TaskID == "" || execution.ExecutionPermissionID != permissionID {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", errors.New("Task Provider operation RoleExecution binding is invalid")
	}
	var permission domain.ExecutionPermission
	foundPermission := false
	for _, candidate := range job.ExecutionPermissions {
		if candidate.ID == permissionID {
			permission, foundPermission = candidate, true
			break
		}
	}
	requestSHA, err := domain.ExecutionPermissionRequestSHA256(permissionRequest)
	if err != nil || !foundPermission || permission.Validate(job) != nil ||
		requestSHA != permission.RequestSHA256 || permissionRequest.JobID != job.ID ||
		permissionRequest.TaskID != execution.TaskID || permissionRequest.Role != execution.Role {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", errors.New("Task Provider operation does not match the immutable ExecutionPermission")
	}
	var approval *domain.JobApproval
	for i := range job.JobApprovals {
		if job.JobApprovals[i].ID == permission.JobApprovalID {
			approval = &job.JobApprovals[i]
			break
		}
	}
	if approval == nil || approval.ApproverPrincipalID != principal.PrincipalID() {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", errors.New("Task Provider operation requires the HUMAN Principal bound to JobApproval")
	}
	var contractRevision *domain.JobContractRevision
	for i := range job.JobContractRevisions {
		if job.JobContractRevisions[i].Revision == permission.ContractRevision {
			contractRevision = &job.JobContractRevisions[i]
			break
		}
	}
	if contractRevision == nil {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", errors.New("Task Provider operation JobContract revision is missing")
	}
	if err := approval.Validate(job, *contractRevision); err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", err
	}
	task, foundTask := findTask(job, execution.TaskID)
	if !foundTask {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", errors.New("Task Provider operation Task was not found")
	}
	var start domain.ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			start, foundStart = candidate, true
			break
		}
	}
	if !foundStart || start.ExecutionPermissionID != permission.ID || start.ID != permission.OperationID ||
		start.ProviderID != permission.ProviderID || start.AccountID != permission.AccountID ||
		start.ConfigurationSHA256 != permission.ConfigurationSHA256 {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", errors.New("Task Provider operation has no matching Start intent")
	}
	if err := start.Validate(job, execution); err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", err
	}
	startObservation, foundObservation := latestStartObservation(job, start.ID)
	if !foundObservation || startObservation.Status != domain.ProviderStartAccepted || startObservation.ProviderSessionID == "" {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", errors.New("Task Provider operation requires the accepted Start Session")
	}
	identity := c.runtime.Identity()
	if identity.ProviderID != permission.ProviderID || identity.AccountID != permission.AccountID ||
		identity.ConfigurationSHA256 != permission.ConfigurationSHA256 {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", errors.New("Provider Runtime identity differs from ExecutionPermission")
	}
	var assignment domain.WorktreeAssignment
	foundAssignment := false
	for _, candidate := range job.WorktreeAssignments {
		if candidate.ID == execution.WorktreeID {
			assignment, foundAssignment = candidate, true
			break
		}
	}
	if !foundAssignment || assignment.OwnerRoleExecutionID != execution.ID ||
		assignment.TaskID != task.ID || assignment.JobID != job.ID {
		return domain.Job{}, domain.RoleExecution{}, domain.ExecutionPermission{}, domain.Task{}, domain.WorktreeAssignment{}, domain.ProviderStartIntent{}, "", errors.New("Task Provider operation requires the owned Worktree assignment")
	}
	return job, execution, permission, task, assignment, start, startObservation.ProviderSessionID, nil
}

func (c *ProviderController) dispatchTaskTurn(
	ctx context.Context,
	receipt store.CommandReceipt,
	job domain.Job,
	turn domain.TaskTurnIntent,
	request provider.TurnRequest,
	permissionRequest domain.ExecutionPermissionRequest,
	principal auth.AuthenticatedPrincipal,
	capabilities provider.Capabilities,
) (store.CommandReceipt, domain.TaskTurnIntent, domain.TaskTurnObservation, provider.TurnResult, error) {
	current, _, permission, task, assignment, _, _, err := c.taskProviderExecutionTarget(
		ctx, job.ID, turn.RoleExecutionID, turn.ExecutionPermissionID, permissionRequest, principal,
	)
	if err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	if err := permission.ValidateFor(current, permission.OperationID, permission.RequestSHA256, c.now()); err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	if c.worktreeVerifier == nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.New("Task Provider Send requires a trusted Worktree verifier")
	}
	if err := c.worktreeVerifier.Verify(ctx, current.ID, task, assignment.Snapshot()); err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, fmt.Errorf("verify Task Worktree before Provider Send: %w", err)
	}
	latest, _, latestPermission, latestTask, latestAssignment, _, latestSession, err := c.taskProviderExecutionTarget(
		ctx, current.ID, turn.RoleExecutionID, turn.ExecutionPermissionID, permissionRequest, principal,
	)
	if err != nil || latestSession != turn.ProviderSessionID ||
		latestPermission.ID != permission.ID || latestAssignment != assignment || latestTask.ID != task.ID {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(errors.New("Task authority or Worktree changed before Provider Send"), err)
	}
	if !request.RequireIdempotency && !taskTurnNeverDispatched(latest, turn.ID) {
		return c.reconcileTaskTurn(ctx, receipt, latest, turn, request, permissionRequest, principal, capabilities)
	}
	if err := latestPermission.ValidateFor(latest, latestPermission.OperationID, latestPermission.RequestSHA256, c.now()); err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	for _, stopIntent := range latest.ProviderStopIntents {
		if stopIntent.RoleExecutionID == turn.RoleExecutionID && stopIntent.SessionID == turn.ProviderSessionID {
			return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.New("Task Provider Send is blocked by an existing Stop intent")
		}
	}
	if err := c.worktreeVerifier.Verify(ctx, latest.ID, latestTask, latestAssignment.Snapshot()); err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, fmt.Errorf("reverify Task Worktree immediately before Provider Send: %w", err)
	}
	if capabilities.Identity != c.runtime.Identity() ||
		(taskTurnNeverDispatched(latest, turn.ID) && !taskTurnCapabilitiesVerified(capabilities)) {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrUnsupportedCapability
	}
	if _, active := domain.ActiveTaskTurnDispatchClaim(latest, turn.OperationID); active {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrStopUnconfirmed
	}
	claimCommandID, err := domain.NewID("cmd")
	if err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	_, claim, createdClaim, err := c.store.AddTaskTurnDispatchClaim(
		ctx, latest.ID, latest.Revision, turn.ID, claimCommandID, principal,
	)
	if err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	if !createdClaim {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrStopUnconfirmed
	}
	postClaimJob, postExecution, postPermission, postTask, postAssignment, _, postSession, postErr :=
		c.taskProviderExecutionTarget(ctx, latest.ID, turn.RoleExecutionID, turn.ExecutionPermissionID, permissionRequest, principal)
	if postErr != nil || postClaimJob.ID != latest.ID || postExecution.ID != turn.RoleExecutionID ||
		postPermission.ID != permission.ID || postTask.ID != task.ID || postAssignment.ID != assignment.ID || postSession != turn.ProviderSessionID {
		releaseErr := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, latest.ID, turn, claim.ID, principal)
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(errors.New("Task Send authority or Worktree changed after dispatch claim acquisition"), postErr, releaseErr)
	}
	if err := postPermission.ValidateFor(postClaimJob, postPermission.OperationID, postPermission.RequestSHA256, c.now()); err != nil {
		releaseErr := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, latest.ID, turn, claim.ID, principal)
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(err, releaseErr)
	}
	if err := c.worktreeVerifier.Verify(ctx, postClaimJob.ID, postTask, postAssignment.Snapshot()); err != nil {
		releaseErr := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, latest.ID, turn, claim.ID, principal)
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(fmt.Errorf("reverify Task Worktree after Send claim acquisition: %w", err), releaseErr)
	}
	// Re-read the Job after Worktree verification. A concurrent recovery may
	// have released or replaced this claim while the verifier was running.
	finalJob, finalExecution, finalPermission, finalTask, finalAssignment, _, finalSession, finalErr :=
		c.taskProviderExecutionTarget(ctx, latest.ID, turn.RoleExecutionID, turn.ExecutionPermissionID, permissionRequest, principal)
	if finalErr != nil || finalJob.ID != postClaimJob.ID || finalExecution.ID != postExecution.ID ||
		finalPermission.ID != postPermission.ID || finalTask.ID != postTask.ID ||
		finalAssignment != postAssignment || finalSession != postSession {
		releaseErr := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, latest.ID, turn, claim.ID, principal)
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{},
			errors.Join(errors.New("Task Send authority or Worktree changed during final verification"), finalErr, releaseErr)
	}
	finalClaim, finalClaimActive := domain.ActiveTaskTurnDispatchClaim(finalJob, turn.OperationID)
	if !finalClaimActive || finalClaim.ID != claim.ID {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrStopUnconfirmed
	}
	if err := finalPermission.ValidateFor(finalJob, finalPermission.OperationID, finalPermission.RequestSHA256, c.now()); err != nil {
		releaseErr := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, latest.ID, turn, claim.ID, principal)
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(err, releaseErr)
	}
	for _, stopIntent := range finalJob.ProviderStopIntents {
		if stopIntent.RoleExecutionID == turn.RoleExecutionID && stopIntent.SessionID == turn.ProviderSessionID {
			releaseErr := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, latest.ID, turn, claim.ID, principal)
			return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{},
				errors.Join(errors.New("Task Provider Send is blocked by a Stop intent saved during final verification"), releaseErr)
		}
	}
	postClaimJob, postExecution, postPermission, postTask, postAssignment, postSession =
		finalJob, finalExecution, finalPermission, finalTask, finalAssignment, finalSession
	// ExecutionPermission is operation-scoped and must still be valid after the
	// final Worktree verification, immediately before the Provider side effect.
	if err := postPermission.ValidateFor(postClaimJob, postPermission.OperationID, postPermission.RequestSHA256, c.now()); err != nil {
		releaseErr := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, latest.ID, turn, claim.ID, principal)
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(err, releaseErr)
	}
	for _, stopIntent := range postClaimJob.ProviderStopIntents {
		if stopIntent.RoleExecutionID == turn.RoleExecutionID && stopIntent.SessionID == turn.ProviderSessionID {
			releaseErr := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, latest.ID, turn, claim.ID, principal)
			return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(errors.New("Task Provider Send is blocked by a Stop intent saved after claim acquisition"), releaseErr)
		}
	}
	if !request.RequireIdempotency && taskTurnHasObservation(postClaimJob, turn.ID) {
		releaseErr := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, latest.ID, turn, claim.ID, principal)
		if releaseErr != nil {
			return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, releaseErr
		}
		return c.reconcileTaskTurn(ctx, receipt, postClaimJob, turn, request, permissionRequest, principal, capabilities)
	}
	activeClaim, active := domain.ActiveTaskTurnDispatchClaim(postClaimJob, turn.OperationID)
	if !active || activeClaim.ID != claim.ID {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrStopUnconfirmed
	}
	if resumeStatus, found := domain.LatestPlanningResumeStatus(postClaimJob, turn.RoleExecutionID); found &&
		resumeStatus != domain.PlanningResumeAttached {
		releaseErr := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, latest.ID, turn, claim.ID, principal)
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{},
			errors.Join(errors.New("Task Provider Send requires the latest Resume to be ATTACHED"), releaseErr)
	}
	_, started, err := c.recordTaskTurnDispatchObservation(
		ctx, postClaimJob.ID, claim.ID, domain.TaskTurnDispatchObservationStarted, "", principal,
	)
	if err != nil || !started {
		if err == nil {
			err = provider.ErrStopUnconfirmed
		}
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	// A replay may replace this claim after an earlier Send has crossed the
	// dispatch boundary. Require Provider-side deduplication whenever verified
	// E4 Send idempotency is available, including on the older in-flight call.
	request.RequireIdempotency = request.RequireIdempotency ||
		capabilities.HasEvidenceAtLeast(provider.CapabilitySendIdempotency, provider.EvidenceLive)
	current = postClaimJob
	result, err := c.runtime.Send(ctx, request)
	if err != nil {
		if evidenceSHA256, rejected := providerTurnPreAdapterRejectionEvidence(turn.OperationID, err); rejected {
			_, _, dispatchErr := c.recordTaskTurnDispatchObservation(ctx, current.ID, claim.ID,
				domain.TaskTurnDispatchObservationPreAdapterRejection, evidenceSHA256, principal)
			if dispatchErr != nil {
				return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(err, dispatchErr)
			}
			_, _, releaseErr := c.store.ReleaseTaskTurnDispatchClaim(
				ctx, current.ID, claim.ID, principal,
				domain.TaskTurnDispatchReleasedDefinitiveFailure, evidenceSHA256, "",
			)
			return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(err, releaseErr)
		}
		if evidenceSHA256, definitive := providerTurnDefinitiveFailureEvidence(turn.OperationID, err); definitive {
			_, _, dispatchErr := c.recordTaskTurnDispatchObservation(ctx, current.ID, claim.ID,
				domain.TaskTurnDispatchObservationDefinitiveFailure, evidenceSHA256, principal)
			if dispatchErr != nil {
				return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(err, dispatchErr)
			}
			_, _, releaseErr := c.store.ReleaseTaskTurnDispatchClaim(
				ctx, current.ID, claim.ID, principal,
				domain.TaskTurnDispatchReleasedDefinitiveFailure, evidenceSHA256, "",
			)
			return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, errors.Join(err, releaseErr)
		}
		observation, persistErr := c.recordTaskTurnObservation(ctx, current.ID, turn.ID, claim.ID, domain.TaskTurnUnknown, 0, "", false)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, errors.Join(err, persistErr)
		}
		return receipt, turn, observation, provider.TurnResult{}, err
	}
	if result.OperationID != turn.OperationID || result.TurnID != turn.ID ||
		result.SessionID != turn.ProviderSessionID {
		observation, persistErr := c.recordTaskTurnObservation(ctx, current.ID, turn.ID, claim.ID, domain.TaskTurnUnknown, 0, "", false)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, errors.Join(provider.ErrProviderInvalidResult, persistErr)
		}
		return receipt, turn, observation, provider.TurnResult{}, provider.ErrProviderInvalidResult
	}
	receipt, turn, observation, result, finalizeErr := c.finalizeTaskTurn(
		ctx, receipt, current, turn, claim.ID, result, "", "", permissionRequest, principal,
	)
	if observation.Status == domain.TaskTurnCompleted || observation.Status == domain.TaskTurnFailed {
		finalizeErr = errors.Join(finalizeErr, c.releaseTaskTurnDispatchClaim(ctx, current.ID, turn, observation, principal))
	}
	return receipt, turn, observation, result, finalizeErr
}

func (c *ProviderController) reconcileTaskTurn(
	ctx context.Context,
	receipt store.CommandReceipt,
	job domain.Job,
	turn domain.TaskTurnIntent,
	request provider.TurnRequest,
	permissionRequest domain.ExecutionPermissionRequest,
	principal auth.AuthenticatedPrincipal,
	capabilities provider.Capabilities,
) (store.CommandReceipt, domain.TaskTurnIntent, domain.TaskTurnObservation, provider.TurnResult, error) {
	claim, activeClaim := domain.ActiveTaskTurnDispatchClaim(job, turn.OperationID)
	if !activeClaim {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrStopUnconfirmed
	}
	claimID := claim.ID
	latestTurnObservation, hasTurnObservation := latestTaskTurnObservation(job, turn.ID)
	hasUnknownTurnObservation := hasTurnObservation && latestTurnObservation.Status == domain.TaskTurnUnknown
	for _, failureStatus := range []string{
		domain.TaskTurnDispatchObservationPreAdapterRejection,
		domain.TaskTurnDispatchObservationDefinitiveFailure,
	} {
		if failure, found := taskTurnDispatchObservation(job, claimID, failureStatus); found && !hasUnknownTurnObservation {
			_, _, releaseErr := c.store.ReleaseTaskTurnDispatchClaim(
				ctx, job.ID, claimID, principal,
				domain.TaskTurnDispatchReleasedDefinitiveFailure, failure.EvidenceSHA256, "",
			)
			if releaseErr != nil {
				return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, releaseErr
			}
			latest, err := c.store.GetJob(ctx, job.ID)
			if err != nil {
				return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
			}
			return c.dispatchTaskTurn(ctx, receipt, latest, turn, request, permissionRequest, principal, capabilities)
		}
	}
	if _, started := taskTurnDispatchObservation(job, claimID, domain.TaskTurnDispatchObservationStarted); !started {
		if err := c.releaseTaskTurnDispatchClaimBeforeSend(ctx, job.ID, turn, claimID, principal); err != nil {
			return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
		}
		latest, err := c.store.GetJob(ctx, job.ID)
		if err != nil {
			return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
		}
		return c.dispatchTaskTurn(ctx, receipt, latest, turn, request, permissionRequest, principal, capabilities)
	}
	if capabilities.Identity != c.runtime.Identity() {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrUnsupportedCapability
	}
	canLookup := capabilities.HasEvidenceAtLeast(provider.CapabilitySendLookup, provider.EvidenceLive)
	canRetry := capabilities.HasEvidenceAtLeast(provider.CapabilitySendIdempotency, provider.EvidenceLive)
	lookupEvidence := ""
	if canLookup {
		lookupRequest := provider.LookupRequest{
			Purpose: provider.LookupPurposeTurn, OperationID: turn.OperationID,
			SessionID: turn.ProviderSessionID, TurnID: turn.ID,
		}
		lookup, err := c.runtime.Lookup(ctx, lookupRequest)
		if err != nil {
			observation, persistErr := c.recordTaskTurnObservation(ctx, job.ID, turn.ID, claimID, domain.TaskTurnUnknown, 0, "", false)
			if persistErr != nil {
				return receipt, turn, observation, provider.TurnResult{}, errors.Join(err, persistErr)
			}
			return receipt, turn, observation, provider.TurnResult{}, err
		}
		if validationErr := lookup.Validate(lookupRequest); validationErr != nil {
			observation, persistErr := c.recordTaskTurnObservation(ctx, job.ID, turn.ID, claimID, domain.TaskTurnUnknown, 0, "", false)
			if persistErr != nil {
				return receipt, turn, observation, provider.TurnResult{}, errors.Join(provider.ErrProviderInvalidResult, validationErr, persistErr)
			}
			return receipt, turn, observation, provider.TurnResult{}, errors.Join(provider.ErrProviderInvalidResult, validationErr)
		}
		if lookup.Found {
			if lookup.Status == domain.TaskTurnCompleted || lookup.Status == domain.TaskTurnFailed {
				result := provider.TurnResult{
					OperationID: turn.OperationID, TurnID: turn.ID, SessionID: turn.ProviderSessionID,
					ReceiptID: lookup.ReceiptID, Status: lookup.Status, EvidenceSHA256: lookup.EvidenceSHA256,
				}
				receipt, turn, observation, result, finalizeErr := c.finalizeTaskTurn(
					ctx, receipt, job, turn, claimID, result, lookup.UsageID, lookup.ResultSHA256, permissionRequest, principal,
				)
				if observation.Status == domain.TaskTurnCompleted || observation.Status == domain.TaskTurnFailed {
					finalizeErr = errors.Join(finalizeErr, c.releaseTaskTurnDispatchClaim(ctx, job.ID, turn, observation, principal))
				}
				return receipt, turn, observation, result, finalizeErr
			}
			observation, persistErr := c.recordTaskTurnObservation(ctx, job.ID, turn.ID, claimID, domain.TaskTurnUnknown, 0, "", false)
			if persistErr != nil {
				return receipt, turn, observation, provider.TurnResult{}, persistErr
			}
			return receipt, turn, observation, provider.TurnResult{}, errors.New("Provider Send lookup has not confirmed a terminal Turn")
		}
		lookupEvidence = lookup.EvidenceSHA256
	}
	if canLookup {
		if _, _, persistErr := c.recordTaskTurnDispatchObservation(ctx, job.ID, claimID,
			domain.TaskTurnDispatchObservationLookupNotFound, lookupEvidence, principal); persistErr != nil {
			return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, persistErr
		}
	}
	if !canLookup || !canRetry {
		observation, persistErr := c.recordTaskTurnObservation(ctx, job.ID, turn.ID, claimID, domain.TaskTurnUnknown, 0, "", false)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, persistErr
		}
		return receipt, turn, observation, provider.TurnResult{}, provider.ErrUnsupportedCapability
	}
	idempotencyEvidence := providerCapabilityEvidenceSHA256(capabilities, provider.CapabilitySendIdempotency)
	if !validProviderEvidenceSHA256(idempotencyEvidence) || !validProviderEvidenceSHA256(lookupEvidence) {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, provider.ErrUnsupportedCapability
	}
	job, err := c.releaseTaskTurnDispatchClaimForIdempotentRetry(ctx, job, turn, principal, idempotencyEvidence, lookupEvidence)
	if err != nil {
		return receipt, turn, domain.TaskTurnObservation{}, provider.TurnResult{}, err
	}
	request.RequireIdempotency = true
	return c.dispatchTaskTurn(ctx, receipt, job, turn, request, permissionRequest, principal, capabilities)
}

func providerCapabilityEvidenceSHA256(capabilities provider.Capabilities, capability provider.Capability) string {
	for _, evidence := range capabilities.Evidence {
		if evidence.Capability == capability &&
			capabilities.HasEvidenceAtLeast(capability, provider.EvidenceLive) &&
			validProviderEvidenceSHA256(evidence.EvidenceSHA256) {
			return evidence.EvidenceSHA256
		}
	}
	return ""
}

func (c *ProviderController) releaseTaskTurnDispatchClaimForIdempotentRetry(
	ctx context.Context,
	job domain.Job,
	turn domain.TaskTurnIntent,
	principal auth.AuthenticatedPrincipal,
	idempotencyEvidenceSHA256, lookupEvidenceSHA256 string,
) (domain.Job, error) {
	latest, err := c.store.GetJob(ctx, job.ID)
	if err != nil {
		return domain.Job{}, err
	}
	expectedClaim, expectedActive := domain.ActiveTaskTurnDispatchClaim(job, turn.OperationID)
	if !expectedActive {
		return latest, nil
	}
	claim, active := domain.ActiveTaskTurnDispatchClaim(latest, turn.OperationID)
	if !active {
		return latest, nil
	}
	if claim.ID != expectedClaim.ID || claim.CreatedRevision != expectedClaim.CreatedRevision {
		return domain.Job{}, provider.ErrStopUnconfirmed
	}
	_, _, err = c.store.ReleaseTaskTurnDispatchClaim(
		ctx, latest.ID, expectedClaim.ID, principal,
		domain.TaskTurnDispatchReleasedIdempotentRetry, idempotencyEvidenceSHA256, lookupEvidenceSHA256,
	)
	if err != nil {
		return domain.Job{}, err
	}
	return c.store.GetJob(ctx, latest.ID)
}

func (c *ProviderController) releaseTaskTurnDispatchClaimBeforeSend(
	ctx context.Context,
	jobID string,
	turn domain.TaskTurnIntent,
	claimID string,
	principal auth.AuthenticatedPrincipal,
) error {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	activeClaim, active := domain.ActiveTaskTurnDispatchClaim(job, turn.OperationID)
	if !active || activeClaim.ID != claimID {
		return nil
	}
	evidenceSHA256 := sha256String("taxion:task-turn-pre-dispatch-failure:" + claimID)
	_, _, dispatchErr := c.recordTaskTurnDispatchObservation(ctx, jobID, claimID,
		domain.TaskTurnDispatchObservationPreDispatchFailure, evidenceSHA256, principal)
	if dispatchErr != nil {
		return dispatchErr
	}
	_, _, err = c.store.ReleaseTaskTurnDispatchClaim(
		ctx, jobID, claimID, principal, domain.TaskTurnDispatchReleasedDefinitiveFailure, evidenceSHA256, "",
	)
	return err
}

func (c *ProviderController) releaseTaskTurnDispatchClaim(
	ctx context.Context,
	jobID string,
	turn domain.TaskTurnIntent,
	observation domain.TaskTurnObservation,
	principal auth.AuthenticatedPrincipal,
) error {
	outcome := ""
	switch observation.Status {
	case domain.TaskTurnCompleted:
		outcome = domain.TaskTurnDispatchReleasedCompleted
	case domain.TaskTurnFailed:
		outcome = domain.TaskTurnDispatchReleasedFailed
	default:
		return nil
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	claim, active := domain.ActiveTaskTurnDispatchClaim(job, turn.OperationID)
	if !active {
		return nil
	}
	_, _, err = c.store.ReleaseTaskTurnDispatchClaim(ctx, jobID, claim.ID, principal, outcome, "", "")
	return err
}

func taskProviderResultObservation(job domain.Job, turn domain.TaskTurnIntent) (domain.ProviderResultObservation, bool) {
	for _, observation := range job.ProviderResultObservations {
		if observation.RoleExecutionID == turn.RoleExecutionID &&
			observation.OperationID == turn.OperationID &&
			observation.SessionID == turn.ProviderSessionID &&
			observation.TurnID == turn.ID && observation.Final {
			return observation, true
		}
	}
	return domain.ProviderResultObservation{}, false
}

func (c *ProviderController) finalizeTaskTurn(
	ctx context.Context,
	receipt store.CommandReceipt,
	job domain.Job,
	turn domain.TaskTurnIntent,
	claimID string,
	result provider.TurnResult,
	expectedUsageID, expectedResultSHA256 string,
	permissionRequest domain.ExecutionPermissionRequest,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.TaskTurnIntent, domain.TaskTurnObservation, provider.TurnResult, error) {
	if result.Status != domain.TaskTurnCompleted && result.Status != domain.TaskTurnFailed {
		observation, err := c.recordTaskTurnObservation(ctx, job.ID, turn.ID, claimID, domain.TaskTurnUnknown, 0, "", false)
		return receipt, turn, observation, provider.TurnResult{}, errors.Join(errors.New("Provider Turn is not terminal"), err)
	}
	resultObservation, resultFound := taskProviderResultObservation(job, turn)
	if resultFound && expectedResultSHA256 != "" && resultObservation.ContentSHA256 != expectedResultSHA256 {
		resultFound = false
	}
	if !resultFound {
		var resultErr error
		_, resultObservation, resultErr = c.retrieveTaskProviderResult(ctx, job.ID, turn.RoleExecutionID, turn.ExecutionPermissionID, permissionRequest, turn.ProviderSessionID, turn.ID, expectedResultSHA256, principal)
		if resultErr != nil {
			observation, persistErr := c.recordTaskTurnObservation(ctx, job.ID, turn.ID, claimID, domain.TaskTurnUnknown, 0, "", false)
			return receipt, turn, observation, provider.TurnResult{}, errors.Join(resultErr, persistErr)
		}
	}
	if expectedResultSHA256 != "" && resultObservation.ContentSHA256 != expectedResultSHA256 {
		observation, persistErr := c.recordTaskTurnObservation(ctx, job.ID, turn.ID, claimID, domain.TaskTurnUnknown, 0, "", false)
		return receipt, turn, observation, provider.TurnResult{},
			errors.Join(provider.ErrProviderInvalidResult, errors.New("Provider Lookup Result digest differs from final Result"), persistErr)
	}
	_, usage, usageErr := c.RetrieveTaskProviderUsage(ctx, job.ID, turn.RoleExecutionID, turn.ExecutionPermissionID, permissionRequest, turn.ProviderSessionID, turn.ID, expectedUsageID, principal)
	if usageErr != nil || !usage.Complete {
		observation, persistErr := c.recordTaskTurnObservation(ctx, job.ID, turn.ID, claimID, domain.TaskTurnUnknown, 0, "", false)
		if usageErr == nil {
			usageErr = errors.New("Provider Usage is incomplete")
		}
		return receipt, turn, observation, provider.TurnResult{}, errors.Join(usageErr, persistErr)
	}
	evidenceSHA256 := result.EvidenceSHA256
	if !validProviderEvidenceSHA256(evidenceSHA256) {
		evidenceSHA256 = resultObservation.EvidenceSHA256
	}
	status := domain.TaskTurnCompleted
	if result.Status == domain.TaskTurnFailed {
		status = domain.TaskTurnFailed
	}
	observation, err := c.recordTaskTurnObservation(ctx, job.ID, turn.ID, claimID, status, usage.Tokens, evidenceSHA256, false)
	if err != nil {
		unknown, persistErr := c.recordTaskTurnObservation(ctx, job.ID, turn.ID, claimID, domain.TaskTurnUnknown, 0, "", false)
		return receipt, turn, unknown, provider.TurnResult{}, errors.Join(err, persistErr)
	}
	result.TokensUsed = usage.Tokens
	result.EvidenceSHA256 = observation.EvidenceSHA256
	return receipt, turn, observation, result, nil
}

func (c *ProviderController) RetrieveTaskProviderResult(
	ctx context.Context,
	jobID, roleExecutionID, permissionID string,
	permissionRequest domain.ExecutionPermissionRequest,
	sessionID, turnID string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderResultObservation, error) {
	return c.retrieveTaskProviderResult(
		ctx, jobID, roleExecutionID, permissionID, permissionRequest, sessionID, turnID, "", principal,
	)
}

func (c *ProviderController) retrieveTaskProviderResult(
	ctx context.Context,
	jobID, roleExecutionID, permissionID string,
	permissionRequest domain.ExecutionPermissionRequest,
	sessionID, turnID, expectedResultSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderResultObservation, error) {
	job, execution, _, _, _, start, acceptedSession, err := c.taskProviderExecutionTarget(
		ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	if acceptedSession != sessionID {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, errors.New("Task Provider Result Session mismatch")
	}
	turn, found := taskTurnByID(job, turnID)
	if !found || turn.RoleExecutionID != execution.ID || turn.ProviderStartID != start.ID ||
		turn.ProviderSessionID != sessionID || turn.Validate(job) != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, errors.New("Task Provider Result requires the matching TaskTurnIntent")
	}
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	if capabilities.Identity != c.runtime.Identity() ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityFinalOutput, provider.EvidenceLive) {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, provider.ErrUnsupportedCapability
	}
	result, err := c.runtime.Result(ctx, provider.ResultRequest{OperationID: turn.OperationID, SessionID: sessionID, TurnID: turn.ID})
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	if result.OperationID != turn.OperationID || result.SessionID != sessionID || result.TurnID != turn.ID ||
		(expectedResultSHA256 != "" && result.ResultSHA256 != expectedResultSHA256) {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, provider.ErrProviderInvalidResult
	}
	current, _, _, _, _, _, currentSession, err := c.taskProviderExecutionTarget(
		ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal,
	)
	if err != nil || currentSession != sessionID {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, errors.Join(provider.ErrProviderInvalidResult, err)
	}
	return c.store.AddProviderResultObservation(
		ctx, current.ID, current.Revision, execution.ID, turn.OperationID, turn.ID, sessionID,
		result.Content, result.ResultSHA256, result.Final, result.EvidenceSHA256, principal,
	)
}

func (c *ProviderController) RetrieveTaskProviderUsage(
	ctx context.Context,
	jobID, roleExecutionID, permissionID string,
	permissionRequest domain.ExecutionPermissionRequest,
	sessionID, turnID, expectedUsageID string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderUsageObservation, error) {
	job, execution, _, _, _, start, acceptedSession, err := c.taskProviderExecutionTarget(
		ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, err
	}
	if acceptedSession != sessionID {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, errors.New("Task Provider Usage Session mismatch")
	}
	turn, found := taskTurnByID(job, turnID)
	if !found || turn.RoleExecutionID != execution.ID || turn.ProviderStartID != start.ID ||
		turn.ProviderSessionID != sessionID || turn.Validate(job) != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, errors.New("Task Provider Usage requires the matching TaskTurnIntent")
	}
	var latestTurnObservation *domain.TaskTurnObservation
	for i := range job.TaskTurnObservations {
		observation := &job.TaskTurnObservations[i]
		if observation.TurnID == turn.ID &&
			(latestTurnObservation == nil || observation.CreatedRevision > latestTurnObservation.CreatedRevision) {
			latestTurnObservation = observation
		}
	}
	var existingUsage *domain.ProviderUsageObservation
	for i := range job.ProviderUsageObservations {
		observation := &job.ProviderUsageObservations[i]
		if observation.OperationID != turn.OperationID || observation.TurnID != turn.ID ||
			observation.SessionID != sessionID ||
			(expectedUsageID != "" && observation.UsageID != expectedUsageID) {
			continue
		}
		if existingUsage == nil || observation.CreatedRevision > existingUsage.CreatedRevision {
			existingUsage = observation
		}
	}
	if existingUsage != nil && existingUsage.Complete {
		return c.store.AddProviderUsageObservation(
			ctx, job.ID, job.Revision, existingUsage.RoleExecutionID, existingUsage.OperationID,
			existingUsage.TurnID, existingUsage.SessionID, existingUsage.UsageID,
			existingUsage.Tokens, existingUsage.Complete, existingUsage.EvidenceSHA256, principal,
		)
	}
	if latestTurnObservation != nil && latestTurnObservation.Status != domain.TaskTurnUnknown {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, errors.New("Provider Usage cannot be added after a terminal Task Turn observation")
	}
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, err
	}
	if capabilities.Identity != c.runtime.Identity() ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityUsageIdentity, provider.EvidenceLive) ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityUsageCompleteness, provider.EvidenceLive) {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, provider.ErrUnsupportedCapability
	}
	usage, err := c.runtime.Usage(ctx, provider.UsageRequest{OperationID: turn.OperationID, SessionID: sessionID, TurnID: turn.ID})
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, err
	}
	if usage.OperationID != turn.OperationID || usage.SessionID != sessionID || usage.TurnID != turn.ID ||
		(expectedUsageID != "" && usage.UsageID != expectedUsageID) {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, provider.ErrProviderInvalidResult
	}
	current, _, _, _, _, _, currentSession, err := c.taskProviderExecutionTarget(
		ctx, jobID, roleExecutionID, permissionID, permissionRequest, principal,
	)
	if err != nil || currentSession != sessionID {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, errors.Join(provider.ErrProviderInvalidResult, err)
	}
	return c.store.AddProviderUsageObservation(
		ctx, current.ID, current.Revision, execution.ID, turn.OperationID, turn.ID, sessionID,
		usage.UsageID, usage.Tokens, usage.Complete, usage.EvidenceSHA256, principal,
	)
}

func (c *ProviderController) recordTaskTurnDispatchObservation(
	ctx context.Context,
	jobID, claimID, status, evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, bool, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, false, err
	}
	for _, existing := range job.TaskTurnDispatchObservations {
		if existing.ClaimID == claimID && existing.Status == status {
			if existing.EvidenceSHA256 == evidenceSHA256 {
				return store.CommandReceipt{}, false, nil
			}
			if status != domain.TaskTurnDispatchObservationLookupNotFound {
				return store.CommandReceipt{}, false, errors.New("Task Turn dispatch observation evidence changed")
			}
		}
	}
	commandID := domain.TaskTurnDispatchObservationCommandID(claimID, status, evidenceSHA256)
	receipt, _, created, err := c.store.AddTaskTurnDispatchObservation(
		ctx, jobID, job.Revision, claimID, commandID, status, evidenceSHA256, principal,
	)
	return receipt, created, err
}

func taskTurnDispatchObservation(
	job domain.Job, claimID, status string,
) (domain.TaskTurnDispatchObservation, bool) {
	var latest domain.TaskTurnDispatchObservation
	found := false
	for _, observation := range job.TaskTurnDispatchObservations {
		if observation.ClaimID == claimID && observation.Status == status &&
			(!found || observation.CreatedRevision > latest.CreatedRevision) {
			latest, found = observation, true
		}
	}
	return latest, found
}

func (c *ProviderController) recordTaskTurnObservation(
	ctx context.Context,
	jobID, turnID, claimID, status string,
	tokensUsed uint64,
	evidenceSHA256 string,
	reconcilesUnknown bool,
) (domain.TaskTurnObservation, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.TaskTurnObservation{}, err
	}
	previous, found := latestTaskTurnObservation(job, turnID)
	if found {
		if previous.Status == domain.TaskTurnUnknown && status == domain.TaskTurnUnknown {
			return previous, nil
		}
		if previous.Status != domain.TaskTurnUnknown {
			if previous.Status == status && previous.TokensUsed == tokensUsed && previous.EvidenceSHA256 == evidenceSHA256 {
				return previous, nil
			}
			return domain.TaskTurnObservation{}, errors.New("Task Turn already has a terminal observation")
		}
		reconcilesUnknown = true
	}
	commandID := taskTurnObservationCommandID(turnID, status, tokensUsed, evidenceSHA256, reconcilesUnknown)
	_, observation, err := c.store.AddTaskTurnObservationForClaim(
		ctx, jobID, job.Revision, turnID, claimID, commandID, status, tokensUsed, evidenceSHA256, reconcilesUnknown,
	)
	return observation, err
}

func taskTurnObservationCommandID(turnID, status string, tokensUsed uint64, evidenceSHA256 string, reconcilesUnknown bool) string {
	return "cmd_" + sha256String(fmt.Sprintf("taxion:task-turn-observation:%s:%s:%d:%s:%t", turnID, status, tokensUsed, evidenceSHA256, reconcilesUnknown))[:32]
}

func taskTurnHasObservation(job domain.Job, turnID string) bool {
	for _, observation := range job.TaskTurnObservations {
		if observation.TurnID == turnID {
			return true
		}
	}
	return false
}

func latestTaskTurnObservation(job domain.Job, turnID string) (domain.TaskTurnObservation, bool) {
	var latest domain.TaskTurnObservation
	found := false
	for _, observation := range job.TaskTurnObservations {
		if observation.TurnID == turnID && (!found || observation.CreatedRevision > latest.CreatedRevision) {
			latest, found = observation, true
		}
	}
	return latest, found
}

func taskTurnByID(job domain.Job, turnID string) (domain.TaskTurnIntent, bool) {
	for _, turn := range job.TaskTurnIntents {
		if turn.ID == turnID {
			return turn, true
		}
	}
	return domain.TaskTurnIntent{}, false
}

func taskTurnResultFromObservation(observation domain.TaskTurnObservation) provider.TurnResult {
	return provider.TurnResult{
		OperationID: observation.OperationID, TurnID: observation.TurnID, SessionID: observation.ProviderSessionID,
		Status: observation.Status, TokensUsed: observation.TokensUsed, EvidenceSHA256: observation.EvidenceSHA256,
	}
}
