package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/provider"
	"github.com/hiono/taxion/internal/store"
)

type ProviderExecutionStore interface {
	PlanningLifecycleStore
	StartTasklessPlanner(context.Context, string, uint64, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.RoleExecution, domain.ProviderStartIntent, error)
	AddProviderEventObservation(context.Context, string, uint64, string, string, string, string, string, string, string, string, string, time.Time, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderEventObservation, error)
	AddProviderStopIntent(context.Context, string, uint64, string, string, string, domain.ProviderStopReason, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderStopIntent, error)
	AddProviderStopIntentWithStatus(context.Context, string, uint64, string, string, string, domain.ProviderStopReason, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderStopIntent, bool, error)
	AddProviderStopObservation(context.Context, string, uint64, string, string, bool, bool, string, bool, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderStopObservation, error)
	AddProviderStopDispatchClaim(context.Context, string, uint64, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderStopDispatchClaim, bool, error)
	StartProviderStopDispatchClaim(context.Context, string, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderStopDispatchStart, bool, error)
	AddProviderStopDispatchStart(context.Context, string, uint64, string, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderStopDispatchStart, bool, error)
	ReleaseProviderStopDispatchClaim(context.Context, string, string, auth.AuthenticatedPrincipal, string, string) (store.CommandReceipt, domain.ProviderStopDispatchRelease, error)
	ReleaseProviderStopDispatchClaimAt(context.Context, string, string, auth.AuthenticatedPrincipal, string, string, time.Time) (store.CommandReceipt, domain.ProviderStopDispatchRelease, error)
	AddProviderResultObservation(context.Context, string, uint64, string, string, string, string, string, string, bool, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderResultObservation, error)
	AddProviderUsageObservation(context.Context, string, uint64, string, string, string, string, string, uint64, bool, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.ProviderUsageObservation, error)
	AddTaskProviderStartObservation(context.Context, string, uint64, string, string, string, string, string, string, bool) (store.CommandReceipt, domain.ProviderStartObservation, error)
	AddTaskTurnIntent(context.Context, string, uint64, string, string, string, string, string, uint64) (store.CommandReceipt, domain.TaskTurnIntent, error)
	AddTaskTurnIntentWithStatus(context.Context, string, uint64, string, string, string, string, string, uint64) (store.CommandReceipt, domain.TaskTurnIntent, bool, error)
	AddTaskTurnObservation(context.Context, string, uint64, string, string, string, uint64, string, bool) (store.CommandReceipt, domain.TaskTurnObservation, error)
	AddTaskTurnDispatchClaim(context.Context, string, uint64, string, string, auth.AuthenticatedPrincipal) (store.CommandReceipt, domain.TaskTurnDispatchClaim, bool, error)
	ReleaseTaskTurnDispatchClaim(context.Context, string, string, auth.AuthenticatedPrincipal, string, string) (store.CommandReceipt, domain.TaskTurnDispatchRelease, error)
}

type PlanningProviderRuntime interface {
	Identity() provider.Identity
	Capabilities(context.Context) (provider.Capabilities, error)
	Start(context.Context, provider.StartRequest) (provider.StartResult, error)
	Send(context.Context, provider.TurnRequest) (provider.TurnResult, error)
	Resume(context.Context, provider.ResumeRequest) (provider.ResumeResult, error)
	Stop(context.Context, provider.StopRequest) (provider.StopResult, error)
	Lookup(context.Context, provider.LookupRequest) (provider.LookupResult, error)
	ReplayEvents(context.Context, provider.ReplayEventsRequest) (provider.EventPage, error)
	Result(context.Context, provider.ResultRequest) (provider.Result, error)
	Usage(context.Context, provider.UsageRequest) (provider.Usage, error)
}

type TaskWorktreeVerifier interface {
	Verify(context.Context, string, domain.Task, domain.WorktreeSnapshot) error
}

type ProviderController struct {
	store            ProviderExecutionStore
	runtime          PlanningProviderRuntime
	planning         *PlanningController
	execution        *ExecutionController
	worktreeVerifier TaskWorktreeVerifier
	now              func() time.Time
}

func NewProviderController(lifecycleStore ProviderExecutionStore, runtime PlanningProviderRuntime) (*ProviderController, error) {
	if lifecycleStore == nil || runtime == nil {
		return nil, errors.New("ProviderExecutionStore and Provider Runtime are required")
	}
	planning, err := NewPlanningController(lifecycleStore)
	if err != nil {
		return nil, err
	}
	return &ProviderController{
		store: lifecycleStore, runtime: runtime, planning: planning,
		now: func() time.Time { return time.Now().UTC().Round(0) },
	}, nil
}

func NewProviderControllerWithExecution(
	lifecycleStore ProviderExecutionStore,
	runtime PlanningProviderRuntime,
	execution *ExecutionController,
) (*ProviderController, error) {
	if execution == nil {
		return nil, errors.New("trusted ExecutionController is required for Task-bound Provider dispatch")
	}
	controller, err := NewProviderController(lifecycleStore, runtime)
	if err != nil {
		return nil, err
	}
	controller.execution = execution
	return controller, nil
}

func NewProviderControllerWithExecutionAndWorktree(
	lifecycleStore ProviderExecutionStore,
	runtime PlanningProviderRuntime,
	execution *ExecutionController,
	verifier TaskWorktreeVerifier,
) (*ProviderController, error) {
	if verifier == nil {
		return nil, errors.New("trusted Task Worktree verifier is required for Task-bound Provider dispatch")
	}
	controller, err := NewProviderControllerWithExecution(lifecycleStore, runtime, execution)
	if err != nil {
		return nil, err
	}
	controller.worktreeVerifier = verifier
	return controller, nil
}

// StartPlanning starts the DRAFT planning Provider operation. Task-bound execution
// uses DispatchTaskExecution and requires a trusted ExecutionController.
func (c *ProviderController) StartPlanning(
	ctx context.Context,
	jobID, authorizationID, commandID string,
	contextDocuments []provider.ContextDocument,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.RoleExecution, domain.ProviderStartIntent, provider.StartResult, error) {
	if err := principal.Validate(); err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
	}
	grant, found := planningAuthorization(job, authorizationID)
	if !found {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, errors.New("PlanningAuthorization was not found")
	}
	now := c.now()
	if job.State != domain.JobDraft || grant.PrincipalID != principal.PrincipalID() {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, errors.New("Provider Start requires the authorized HUMAN Principal and a DRAFT Job")
	}
	if err := grant.Validate(job); err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
	}
	existingIntent := false
	for _, existing := range job.ProviderStartIntents {
		if existing.CommandID == commandID && existing.PlanningAuthorizationID == authorizationID {
			existingIntent = true
			break
		}
	}
	if !existingIntent {
		if err := grant.ValidateFor(job, principal.PrincipalID(), now); err != nil {
			return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
		}
	}
	if !providerIdentityMatches(c.runtime.Identity(), grant) {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, errors.New("Provider Runtime identity differs from PlanningAuthorization")
	}
	if err := validatePlanningContext(grant, contextDocuments); err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
	}

	request := provider.StartRequest{
		OperationID: domain.ProviderStartOperationID(commandID),
		JobID:       job.ID, RoleExecutionID: domain.RoleExecutionID(commandID),
		RequestSHA256: grant.RequestSHA256,
		Prompt:        job.Request, PromptSHA256: sha256String(job.Request),
		Context:  append([]provider.ContextDocument(nil), contextDocuments...),
		MaxTurns: grant.Budget.MaxTurns, MaxTokens: grant.Budget.MaxTokens,
		Deadline: grant.Deadline,
	}
	if err := request.Validate(); err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, provider.StartResult{}, err
	}

	initialRevision := job.Revision
	receipt, execution, intent, err := c.store.StartTasklessPlanner(
		ctx, jobID, initialRevision, authorizationID, commandID, principal,
	)
	if err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	current, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	currentGrant, found := planningAuthorization(current, authorizationID)
	if !found {
		return receipt, execution, intent, provider.StartResult{}, errors.New("PlanningAuthorization disappeared after Start intent commit")
	}
	if !providerIdentityMatches(c.runtime.Identity(), currentGrant) {
		return receipt, execution, intent, provider.StartResult{}, errors.New("Provider Runtime identity changed after Start intent commit")
	}
	if err := intent.Validate(current, execution); err != nil {
		return receipt, execution, intent, provider.StartResult{}, fmt.Errorf("validate committed Provider Start intent: %w", err)
	}
	request.OperationID = intent.ID
	request.RoleExecutionID = execution.ID

	if observation, exists := latestStartObservation(current, intent.ID); exists {
		switch observation.Status {
		case domain.ProviderStartAccepted:
			return receipt, execution, intent, startResultFromObservation(observation), nil
		case domain.ProviderStartFailed:
			return receipt, execution, intent, provider.StartResult{}, errors.New("Provider Start was definitively rejected")
		case domain.ProviderStartUnknown:
			return c.reconcileStart(ctx, receipt, execution, intent, request, currentGrant, principal)
		}
	}

	if receipt.Revision != initialRevision+1 {
		return c.reconcileStart(ctx, receipt, execution, intent, request, currentGrant, principal)
	}
	if err := currentGrant.ValidateFor(current, principal.PrincipalID(), c.now()); err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	result, err := c.runtime.Start(ctx, request)
	if err != nil {
		if providerOperationMayHaveStarted(err) {
			if persistErr := c.recordStartObservation(ctx, jobID, intent.ID, domain.ProviderStartUnknown, "", "", "", false, principal); persistErr != nil {
				return receipt, execution, intent, provider.StartResult{}, errors.Join(err, fmt.Errorf("persist UNKNOWN Provider Start observation: %w", persistErr))
			}
		} else if evidence, definitive := providerDefinitiveFailureEvidence(intent.ID, err); definitive {
			if persistErr := c.recordStartObservation(ctx, jobID, intent.ID, domain.ProviderStartFailed, "", "", evidence, false, principal); persistErr != nil {
				return receipt, execution, intent, provider.StartResult{}, errors.Join(err, fmt.Errorf("persist FAILED Provider Start observation: %w", persistErr))
			}
		}
		return receipt, execution, intent, provider.StartResult{}, err
	}
	if err := c.recordStartObservation(ctx, jobID, intent.ID, domain.ProviderStartAccepted,
		result.SessionID, result.ReceiptID, result.EvidenceSHA256, false, principal); err != nil {
		return receipt, execution, intent, provider.StartResult{}, fmt.Errorf("persist Provider Start observation: %w", err)
	}
	return receipt, execution, intent, result, nil
}

type taskProviderStartPrompt struct {
	Protocol            string   `json:"protocol"`
	Role                string   `json:"role"`
	JobRequest          string   `json:"job_request"`
	ContractPurpose     string   `json:"contract_purpose"`
	ContractConstraints []string `json:"contract_constraints"`
	TaskTitle           string   `json:"task_title"`
	TaskPurpose         string   `json:"task_purpose"`
	AcceptanceCriteria  []string `json:"acceptance_criteria"`
	RequiredEvidence    []string `json:"required_evidence"`
	TargetRepositories  []string `json:"target_repositories"`
	TargetResources     []string `json:"target_resources"`
	Workspaces          []string `json:"workspaces"`
}

// DispatchTaskExecution dispatches only a Coder RoleExecution whose durable Start
// intent and current one-operation permission are revalidated immediately before Start.
func (c *ProviderController) DispatchTaskExecution(
	ctx context.Context,
	jobID, roleExecutionID, permissionID string,
	permissionRequest domain.ExecutionPermissionRequest,
) (domain.ProviderStartObservation, provider.StartResult, error) {
	if ctx == nil || c.execution == nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider dispatch requires context and a trusted ExecutionController")
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, err
	}
	var execution domain.RoleExecution
	executionFound := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, executionFound = candidate, true
			break
		}
	}
	if !executionFound || execution.JobID != job.ID {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider dispatch RoleExecution was not found")
	}
	if execution.Role != domain.RoleCoder {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Reviewer Provider dispatch requires an immutable Worktree snapshot and enforced read-only boundary")
	}
	if execution.ExecutionPermissionID != permissionID || execution.TaskID == "" {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider dispatch permission or Task binding mismatch")
	}
	var intent domain.ProviderStartIntent
	intentFound := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			intent, intentFound = candidate, true
			break
		}
	}
	if !intentFound || intent.ExecutionPermissionID != permissionID {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider dispatch requires the persisted matching ProviderStartIntent")
	}
	if err := intent.Validate(job, execution); err != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, fmt.Errorf("validate Task Provider Start intent: %w", err)
	}
	requestSHA256, err := domain.ExecutionPermissionRequestSHA256(permissionRequest)
	if err != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, err
	}
	var permission domain.ExecutionPermission
	permissionFound := false
	for _, candidate := range job.ExecutionPermissions {
		if candidate.ID == permissionID {
			permission, permissionFound = candidate, true
			break
		}
	}
	if !permissionFound || permission.Validate(job) != nil ||
		requestSHA256 != permission.RequestSHA256 || permissionRequest.JobID != job.ID ||
		permissionRequest.TaskID != execution.TaskID || permissionRequest.Role != execution.Role {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider dispatch permission does not match the persisted immutable request")
	}
	if permission.TaskID != execution.TaskID || permission.Role != execution.Role ||
		permission.OperationID != intent.ID || permission.RequestSHA256 != intent.RequestSHA256 ||
		permission.ProviderID != intent.ProviderID || permission.AccountID != intent.AccountID ||
		permission.ConfigurationSHA256 != intent.ConfigurationSHA256 {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider dispatch does not match its current ExecutionPermission")
	}
	identity := c.runtime.Identity()
	if identity.ProviderID != permission.ProviderID || identity.AccountID != permission.AccountID ||
		identity.ConfigurationSHA256 != permission.ConfigurationSHA256 {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Provider Runtime identity differs from ExecutionPermission")
	}
	task, taskFound := findTask(job, execution.TaskID)
	if !taskFound {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider dispatch Task was not found")
	}
	if c.worktreeVerifier == nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider dispatch requires a trusted Worktree verifier")
	}
	var assignment domain.WorktreeAssignment
	assignmentFound := false
	for _, candidate := range job.WorktreeAssignments {
		if candidate.ID == execution.WorktreeID {
			assignment, assignmentFound = candidate, true
			break
		}
	}
	if !assignmentFound || assignment.OwnerRoleExecutionID != execution.ID ||
		assignment.TaskID != task.ID || assignment.JobID != job.ID {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider dispatch requires the matching persisted Worktree assignment")
	}
	if err := c.worktreeVerifier.Verify(ctx, job.ID, task, assignment.Snapshot()); err != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, fmt.Errorf("verify Task Worktree before Provider dispatch: %w", err)
	}
	var contract domain.JobContract
	contractFound := false
	for _, revision := range job.JobContractRevisions {
		if revision.Revision == task.ContractRevision {
			contract, contractFound = revision.Contract, true
			break
		}
	}
	if !contractFound {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider dispatch contract revision was not found")
	}
	prompt, err := buildTaskProviderStartPrompt(job, task, contract, execution.Role)
	if err != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, err
	}
	request := provider.StartRequest{
		OperationID: intent.ID, JobID: job.ID, RoleExecutionID: execution.ID,
		RequestSHA256: permission.RequestSHA256, Prompt: prompt, PromptSHA256: sha256String(prompt),
		MaxTurns: permission.ReservedTurns, MaxTokens: permission.ReservedTokens, Deadline: intent.Deadline,
		RepositoryID: assignment.RepositoryID, WorktreePath: assignment.WorktreePath, BaselineCommit: assignment.BaselineCommit,
	}
	if err := request.Validate(); err != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, err
	}
	if previous, found := latestStartObservation(job, intent.ID); found {
		switch previous.Status {
		case domain.ProviderStartAccepted:
			return previous, startResultFromObservation(previous), nil
		case domain.ProviderStartFailed:
			return previous, provider.StartResult{}, errors.New("Task Provider Start was definitively rejected")
		}
	}
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, err
	}
	if capabilities.Identity != identity ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityTaskWorktree, provider.EvidenceLive) {
		return domain.ProviderStartObservation{}, provider.StartResult{}, provider.ErrUnsupportedCapability
	}
	canLookup := capabilities.HasEvidenceAtLeast(provider.CapabilityStartLookup, provider.EvidenceSynthetic)
	canRetry := capabilities.HasEvidenceAtLeast(provider.CapabilityStartIdempotency, provider.EvidenceSynthetic)
	if !canLookup && !canRetry {
		observation, persistErr := c.recordTaskStartObservation(ctx, job.ID, intent.ID, domain.ProviderStartUnknown, "", "", "", false)
		if persistErr != nil {
			return observation, provider.StartResult{}, errors.Join(provider.ErrUnsupportedCapability, persistErr)
		}
		return observation, provider.StartResult{}, provider.ErrUnsupportedCapability
	}
	if canLookup {
		lookupRequest := provider.LookupRequest{OperationID: intent.ID}
		lookup, lookupErr := c.runtime.Lookup(ctx, lookupRequest)
		if lookupErr != nil {
			observation, persistErr := c.recordTaskStartObservation(ctx, job.ID, intent.ID, domain.ProviderStartUnknown, "", "", "", false)
			if persistErr != nil {
				return observation, provider.StartResult{}, errors.Join(lookupErr, persistErr)
			}
			return observation, provider.StartResult{}, lookupErr
		}
		if err := lookup.Validate(lookupRequest); err != nil {
			observation, persistErr := c.recordTaskStartObservation(ctx, job.ID, intent.ID, domain.ProviderStartUnknown, "", "", "", false)
			if persistErr != nil {
				return observation, provider.StartResult{}, errors.Join(provider.ErrProviderInvalidResult, persistErr)
			}
			return observation, provider.StartResult{}, provider.ErrProviderInvalidResult
		}
		if lookup.Found {
			switch lookup.Status {
			case provider.LookupStatusFailed:
				observation, persistErr := c.recordTaskStartObservation(ctx, job.ID, intent.ID, domain.ProviderStartFailed, "", lookup.ReceiptID, lookup.EvidenceSHA256, false)
				if persistErr != nil {
					return observation, provider.StartResult{}, persistErr
				}
				return observation, provider.StartResult{}, errors.New("Task Provider Start lookup confirmed failure")
			case "ACCEPTED", "RUNNING", "ATTACHED", "COMPLETED":
				observation, persistErr := c.recordTaskStartObservation(ctx, job.ID, intent.ID, domain.ProviderStartAccepted,
					lookup.SessionID, lookup.ReceiptID, lookup.EvidenceSHA256, false)
				if persistErr != nil {
					return observation, provider.StartResult{}, persistErr
				}
				return observation, startResultFromObservation(observation), nil
			default:
				observation, persistErr := c.recordTaskStartObservation(ctx, job.ID, intent.ID, domain.ProviderStartUnknown,
					lookup.SessionID, lookup.ReceiptID, lookup.EvidenceSHA256, false)
				if persistErr != nil {
					return observation, provider.StartResult{}, persistErr
				}
				return observation, provider.StartResult{}, errors.New("Task Provider Start lookup did not confirm a terminally classifiable state")
			}
		}
	}
	// Lookup NOT_FOUND, or a verified Start idempotency capability, permits only
	// the same immutable OperationID. Revalidate authority after the external lookup.
	if _, err := c.execution.ValidatePermission(ctx, permissionID, permissionRequest); err != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, err
	}
	current, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, err
	}
	var currentExecution domain.RoleExecution
	var currentIntent domain.ProviderStartIntent
	for _, candidate := range current.RoleExecutions {
		if candidate.ID == roleExecutionID {
			currentExecution = candidate
			break
		}
	}
	for _, candidate := range current.ProviderStartIntents {
		if candidate.RoleExecutionID == roleExecutionID {
			currentIntent = candidate
			break
		}
	}
	if currentExecution.ID == "" || currentIntent.ID != intent.ID ||
		currentExecution.ExecutionPermissionID != permissionID || currentIntent.ExecutionPermissionID != permissionID ||
		currentIntent.Validate(current, currentExecution) != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Provider Start intent changed before dispatch")
	}
	currentTask, currentTaskFound := findTask(current, currentExecution.TaskID)
	if !currentTaskFound {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task disappeared before Provider Start")
	}
	var currentAssignment domain.WorktreeAssignment
	currentAssignmentFound := false
	for _, candidate := range current.WorktreeAssignments {
		if candidate.ID == currentExecution.WorktreeID {
			currentAssignment, currentAssignmentFound = candidate, true
			break
		}
	}
	if !currentAssignmentFound || currentAssignment != assignment ||
		currentAssignment.OwnerRoleExecutionID != currentExecution.ID ||
		currentAssignment.TaskID != currentTask.ID || currentAssignment.JobID != current.ID {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Task Worktree assignment changed before Provider Start")
	}
	if err := c.worktreeVerifier.Verify(ctx, current.ID, currentTask, currentAssignment.Snapshot()); err != nil {
		return domain.ProviderStartObservation{}, provider.StartResult{}, fmt.Errorf("verify Task Worktree immediately before Provider Start: %w", err)
	}
	if c.runtime.Identity() != identity {
		return domain.ProviderStartObservation{}, provider.StartResult{}, errors.New("Provider Runtime identity changed before dispatch")
	}
	result, startErr := c.runtime.Start(ctx, request)
	if startErr != nil {
		status, evidence := domain.ProviderStartUnknown, ""
		if failureEvidence, definitive := providerDefinitiveFailureEvidence(intent.ID, startErr); definitive {
			status, evidence = domain.ProviderStartFailed, failureEvidence
		}
		observation, persistErr := c.recordTaskStartObservation(ctx, job.ID, intent.ID, status, "", "", evidence, false)
		if persistErr != nil {
			return observation, provider.StartResult{}, errors.Join(startErr, persistErr)
		}
		return observation, provider.StartResult{}, startErr
	}
	if result.OperationID != intent.ID || result.SessionID == "" || result.ReceiptID == "" ||
		!validProviderEvidenceSHA256(result.EvidenceSHA256) {
		observation, persistErr := c.recordTaskStartObservation(ctx, job.ID, intent.ID, domain.ProviderStartUnknown, "", "", "", false)
		if persistErr != nil {
			return observation, provider.StartResult{}, errors.Join(provider.ErrProviderInvalidResult, persistErr)
		}
		return observation, provider.StartResult{}, provider.ErrProviderInvalidResult
	}
	observation, err := c.recordTaskStartObservation(ctx, job.ID, intent.ID, domain.ProviderStartAccepted,
		result.SessionID, result.ReceiptID, result.EvidenceSHA256, false)
	if err != nil {
		return observation, provider.StartResult{}, fmt.Errorf("persist Task Provider Start observation: %w", err)
	}
	return observation, result, nil
}

func findTask(job domain.Job, taskID string) (domain.Task, bool) {
	for _, task := range job.Tasks {
		if task.ID == taskID {
			return task, true
		}
	}
	return domain.Task{}, false
}

func buildTaskProviderStartPrompt(job domain.Job, task domain.Task, contract domain.JobContract, role string) (string, error) {
	prompt, err := json.Marshal(taskProviderStartPrompt{
		Protocol: "taxion.task-start.v1", Role: role, JobRequest: job.Request,
		ContractPurpose: contract.Purpose, ContractConstraints: append([]string(nil), contract.Constraints...),
		TaskTitle: task.Title, TaskPurpose: task.Purpose,
		AcceptanceCriteria: append([]string(nil), task.AcceptanceCriteria...),
		RequiredEvidence:   append([]string(nil), task.RequiredEvidence...),
		TargetRepositories: append([]string(nil), task.TargetRepositories...),
		TargetResources:    append([]string(nil), task.TargetResources...),
		Workspaces:         append([]string(nil), task.Workspaces...),
	})
	if err != nil {
		return "", fmt.Errorf("encode immutable Task Provider prompt: %w", err)
	}
	return string(prompt), nil
}

func (c *ProviderController) recordTaskStartObservation(
	ctx context.Context,
	jobID, operationID, status, sessionID, receiptID, evidenceSHA256 string,
	reconcilesUnknown bool,
) (domain.ProviderStartObservation, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.ProviderStartObservation{}, err
	}
	if previous, found := latestStartObservation(job, operationID); found {
		if previous.Status == status && status == domain.ProviderStartUnknown &&
			previous.ProviderSessionID == sessionID && previous.ReceiptID == receiptID &&
			previous.EvidenceSHA256 == evidenceSHA256 {
			return previous, nil
		}
		if previous.Status == domain.ProviderStartUnknown && status != domain.ProviderStartUnknown {
			reconcilesUnknown = true
		} else if previous.Status != domain.ProviderStartUnknown {
			return domain.ProviderStartObservation{}, errors.New("Task Provider Start observation is already terminal")
		}
	}
	commandID := domain.ProviderStartObservationCommandID(
		operationID, status, sessionID, receiptID, evidenceSHA256, reconcilesUnknown,
	)
	_, observation, err := c.store.AddTaskProviderStartObservation(
		ctx, jobID, job.Revision, operationID, commandID, status,
		sessionID, receiptID, evidenceSHA256, reconcilesUnknown,
	)
	return observation, err
}

type ProviderEventReplayResult struct {
	Observations []domain.ProviderEventObservation
	NextCursor   string
	HasMore      bool
}

// ReplayProviderEvents appends Provider events to the JobAggregate. Replaying from
// an earlier persisted cursor is allowed; deterministic event commands make it idempotent.
// StopPlanning persists the immutable Stop intent before asking the Provider to stop the Session.
func (c *ProviderController) StopPlanning(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, commandID string,
	reason domain.ProviderStopReason,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderStopIntent, domain.ProviderStopObservation, error) {
	if ctx == nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, provider.ErrProviderInvalidRequest
	}
	if err := principal.Validate(); err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	if job.State != domain.JobDraft {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Stop requires a DRAFT Job")
	}
	var execution domain.RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Stop RoleExecution was not found")
	}
	var grant domain.PlanningAuthorization
	foundGrant := false
	for _, candidate := range job.PlanningAuthorizations {
		if candidate.ID == execution.PlanningAuthorizationID {
			grant, foundGrant = candidate, true
			break
		}
	}
	if !foundGrant || grant.PrincipalID != principal.PrincipalID() {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Stop requires the authorized HUMAN Principal")
	}
	if err := grant.Validate(job); err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, err
	}
	if !providerIdentityMatches(c.runtime.Identity(), grant) {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Runtime identity differs from PlanningAuthorization")
	}
	var startIntent domain.ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			startIntent, foundStart = candidate, true
			break
		}
	}
	if !foundStart {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Stop has no Start intent")
	}
	if err := startIntent.Validate(job, execution); err != nil {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, fmt.Errorf("validate Provider Start intent: %w", err)
	}
	startObservation, foundStartObservation := latestStartObservation(job, startIntent.ID)
	if !foundStartObservation || startObservation.Status != domain.ProviderStartAccepted ||
		startObservation.ProviderSessionID != sessionID {
		return store.CommandReceipt{}, domain.ProviderStopIntent{}, domain.ProviderStopObservation{}, errors.New("Provider Stop requires the accepted Start Session")
	}
	existingStopIntent := false
	for _, existing := range job.ProviderStopIntents {
		if existing.CommandID == commandID {
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
	stopResult, stopErr := c.runtime.Stop(ctx, provider.StopRequest{
		OperationID:  intent.OperationID,
		SessionID:    sessionID,
		FencingToken: intent.CreatedRevision,
	})
	if stopErr != nil {
		if providerOperationMayHaveStarted(stopErr) {
			if hasPrevious && (previous.Stopped || providerStopObservationUnknown(previous)) {
				return intentReceipt, intent, previous, stopErr
			}
			observation, persistErr := c.persistProviderStopObservation(
				ctx, currentJob, intent, principal, false, false, "", false,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(stopErr, persistErr)
			}
			return intentReceipt, intent, observation, stopErr
		}
		if evidence, definitive := providerStopDefinitiveFailureEvidence(intent.OperationID, stopErr); definitive {
			if hasPrevious && previous.Stopped {
				return intentReceipt, intent, previous, stopErr
			}
			reconcilesUnknown := hasPrevious && providerStopObservationUnknown(previous)
			observation, persistErr := c.persistProviderStopObservation(
				ctx, currentJob, intent, principal, false, false, evidence, reconcilesUnknown,
			)
			if persistErr != nil {
				return intentReceipt, intent, observation, errors.Join(stopErr, persistErr)
			}
			return intentReceipt, intent, observation, stopErr
		}
		return intentReceipt, intent, previous, stopErr
	}
	if stopResult.OperationID != intent.OperationID || stopResult.SessionID != sessionID ||
		!stopResult.Stopped || !stopResult.Quiescent || !validProviderEvidenceSHA256(stopResult.EvidenceSHA256) {
		stopErr = errors.Join(provider.ErrStopUnconfirmed, &provider.OperationError{
			Class: provider.FailureAmbiguous, Code: provider.OperationCodeUnknown,
		})
		if hasPrevious && (previous.Stopped || providerStopObservationUnknown(previous)) {
			return intentReceipt, intent, previous, stopErr
		}
		observation, persistErr := c.persistProviderStopObservation(
			ctx, currentJob, intent, principal, false, false, "", false,
		)
		if persistErr != nil {
			return intentReceipt, intent, observation, errors.Join(stopErr, persistErr)
		}
		return intentReceipt, intent, observation, stopErr
	}
	reconcilesUnknown := hasPrevious && providerStopObservationUnknown(previous)
	observation, err := c.persistProviderStopObservation(
		ctx, currentJob, intent, principal, true, true, stopResult.EvidenceSHA256, reconcilesUnknown,
	)
	return intentReceipt, intent, observation, err
}

func (c *ProviderController) persistProviderStopObservation(
	ctx context.Context,
	job domain.Job,
	intent domain.ProviderStopIntent,
	principal auth.AuthenticatedPrincipal,
	stopped, quiescent bool,
	evidenceSHA256 string,
	reconcilesUnknown bool,
) (domain.ProviderStopObservation, error) {
	commandID := domain.ProviderStopObservationCommandID(
		intent.OperationID, stopped, quiescent, evidenceSHA256, reconcilesUnknown,
	)
	_, observation, err := c.store.AddProviderStopObservation(
		ctx, job.ID, job.Revision, intent.ID, commandID,
		stopped, quiescent, evidenceSHA256, reconcilesUnknown, principal,
	)
	return observation, err
}

func (c *ProviderController) ReplayProviderEvents(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, afterCursor string,
	principal auth.AuthenticatedPrincipal,
) (ProviderEventReplayResult, error) {
	result := ProviderEventReplayResult{NextCursor: afterCursor}
	if ctx == nil {
		return result, provider.ErrProviderInvalidRequest
	}
	if err := principal.Validate(); err != nil {
		return result, err
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return result, err
	}
	if job.State != domain.JobDraft {
		return result, errors.New("Provider event replay requires a DRAFT Job")
	}
	var execution domain.RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return result, errors.New("Provider event replay RoleExecution was not found")
	}
	grant, found := planningAuthorization(job, execution.PlanningAuthorizationID)
	if !found || grant.PrincipalID != principal.PrincipalID() {
		return result, errors.New("Provider event replay requires the authorized HUMAN Principal")
	}
	if err := grant.Validate(job); err != nil {
		return result, err
	}
	if !providerIdentityMatches(c.runtime.Identity(), grant) {
		return result, errors.New("Provider Runtime identity differs from PlanningAuthorization")
	}
	var startIntent domain.ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			startIntent, foundStart = candidate, true
			break
		}
	}
	if !foundStart {
		return result, errors.New("Provider event replay has no Start intent")
	}
	if err := startIntent.Validate(job, execution); err != nil {
		return result, fmt.Errorf("validate Provider Start intent: %w", err)
	}
	startObservation, found := latestStartObservation(job, startIntent.ID)
	if !found || startObservation.Status != domain.ProviderStartAccepted ||
		startObservation.ProviderSessionID != sessionID {
		return result, errors.New("Provider event replay requires the accepted Start Session")
	}
	request := provider.ReplayEventsRequest{SessionID: sessionID, AfterCursor: afterCursor}
	if err := request.Validate(); err != nil {
		return result, provider.ErrProviderInvalidRequest
	}
	cursorKnown := afterCursor == ""
	for _, observation := range job.ProviderEventObservations {
		if observation.SessionID == sessionID && observation.Cursor == afterCursor {
			cursorKnown = true
			break
		}
	}
	if !cursorKnown {
		return result, errors.New("Provider event replay cursor is not part of the persisted Session history")
	}
	page, err := c.runtime.ReplayEvents(ctx, request)
	if err != nil {
		return result, err
	}
	if err := page.Validate(request); err != nil {
		return result, fmt.Errorf("validate Provider event page: %w", err)
	}
	currentJob := job
	for _, event := range page.Events {
		_, observation, err := c.store.AddProviderEventObservation(
			ctx, jobID, currentJob.Revision, roleExecutionID, event.OperationID, event.TurnID,
			sessionID, event.EventID, string(event.Kind), event.PreviousCursor, event.Cursor,
			event.EvidenceSHA256, event.ObservedAt, principal,
		)
		if err != nil {
			return result, fmt.Errorf("persist Provider event observation: %w", err)
		}
		result.Observations = append(result.Observations, observation)
		result.NextCursor = observation.Cursor
		currentJob, err = c.store.GetJob(ctx, jobID)
		if err != nil {
			return result, err
		}
	}
	result.NextCursor = page.NextCursor
	result.HasMore = page.HasMore
	return result, nil
}

func (c *ProviderController) reconcileStart(
	ctx context.Context,
	receipt store.CommandReceipt,
	execution domain.RoleExecution,
	intent domain.ProviderStartIntent,
	request provider.StartRequest,
	grant domain.PlanningAuthorization,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.RoleExecution, domain.ProviderStartIntent, provider.StartResult, error) {
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	canLookup := capabilities.HasEvidenceAtLeast(provider.CapabilityStartLookup, provider.EvidenceLive)
	canRetry := capabilities.HasEvidenceAtLeast(provider.CapabilityStartIdempotency, provider.EvidenceLive)
	if canLookup {
		lookup, lookupErr := c.runtime.Lookup(ctx, provider.LookupRequest{OperationID: intent.ID})
		if lookupErr != nil {
			if persistErr := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown, "", "", "", false, principal); persistErr != nil {
				return receipt, execution, intent, provider.StartResult{}, errors.Join(lookupErr, fmt.Errorf("persist UNKNOWN Provider Start observation: %w", persistErr))
			}
			return receipt, execution, intent, provider.StartResult{}, lookupErr
		}
		if lookup.Found {
			if lookup.Status == provider.LookupStatusFailed {
				if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartFailed, "", lookup.ReceiptID, lookup.EvidenceSHA256, false, principal); err != nil {
					return receipt, execution, intent, provider.StartResult{}, err
				}
				return receipt, execution, intent, provider.StartResult{}, errors.New("Provider Start lookup confirmed failure")
			}
			switch lookup.Status {
			case provider.LookupStatusUnknown, "PENDING", "DETACHED", "STOPPED":
				if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown,
					lookup.SessionID, lookup.ReceiptID, lookup.EvidenceSHA256, false, principal); err != nil {
					return receipt, execution, intent, provider.StartResult{}, err
				}
				return receipt, execution, intent, provider.StartResult{}, errors.New("Provider Start lookup does not confirm an attached Session")
			case "ACCEPTED", "RUNNING", "ATTACHED", "COMPLETED":
				if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartAccepted,
					lookup.SessionID, lookup.ReceiptID, lookup.EvidenceSHA256, false, principal); err != nil {
					return receipt, execution, intent, provider.StartResult{}, err
				}
			default:
				if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown,
					lookup.SessionID, lookup.ReceiptID, lookup.EvidenceSHA256, false, principal); err != nil {
					return receipt, execution, intent, provider.StartResult{}, err
				}
				return receipt, execution, intent, provider.StartResult{}, errors.New("Provider Start lookup returned an unrecognized state")
			}
			return receipt, execution, intent, provider.StartResult{
				OperationID: intent.ID, SessionID: lookup.SessionID,
				ReceiptID: lookup.ReceiptID, EvidenceSHA256: lookup.EvidenceSHA256,
			}, nil
		}
	} else if !canRetry {
		if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown, "", "", "", false, principal); err != nil {
			return receipt, execution, intent, provider.StartResult{}, fmt.Errorf("persist unresolved Provider Start: %w", err)
		}
		return receipt, execution, intent, provider.StartResult{}, provider.ErrUnsupportedCapability
	}

	current, err := c.store.GetJob(ctx, intent.JobID)
	if err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	currentGrant, found := planningAuthorization(current, grant.ID)
	if !found {
		return receipt, execution, intent, provider.StartResult{}, errors.New("PlanningAuthorization was not found during Start recovery")
	}
	if err := currentGrant.ValidateFor(current, principal.PrincipalID(), c.now()); err != nil {
		return receipt, execution, intent, provider.StartResult{}, err
	}
	result, err := c.runtime.Start(ctx, request)
	if err != nil {
		if providerOperationMayHaveStarted(err) {
			if persistErr := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartUnknown, "", "", "", false, principal); persistErr != nil {
				return receipt, execution, intent, provider.StartResult{}, errors.Join(err, fmt.Errorf("persist UNKNOWN Provider Start observation: %w", persistErr))
			}
		} else if evidence, definitive := providerDefinitiveFailureEvidence(intent.ID, err); definitive {
			if persistErr := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartFailed, "", "", evidence, false, principal); persistErr != nil {
				return receipt, execution, intent, provider.StartResult{}, errors.Join(err, fmt.Errorf("persist FAILED Provider Start observation: %w", persistErr))
			}
		}
		return receipt, execution, intent, provider.StartResult{}, err
	}
	if err := c.recordStartObservation(ctx, intent.JobID, intent.ID, domain.ProviderStartAccepted,
		result.SessionID, result.ReceiptID, result.EvidenceSHA256, false, principal); err != nil {
		return receipt, execution, intent, provider.StartResult{}, fmt.Errorf("persist recovered Provider Start observation: %w", err)
	}
	return receipt, execution, intent, result, nil
}

func (c *ProviderController) recordStartObservation(
	ctx context.Context,
	jobID, operationID, status, sessionID, receiptID, evidenceSHA256 string,
	reconcilesUnknown bool,
	principal auth.AuthenticatedPrincipal,
) error {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return err
	}
	if previous, found := latestStartObservation(job, operationID); found {
		if previous.Status == status && status == domain.ProviderStartUnknown &&
			previous.ProviderSessionID == sessionID && previous.ReceiptID == receiptID &&
			previous.EvidenceSHA256 == evidenceSHA256 {
			return nil
		}
		if previous.Status == domain.ProviderStartUnknown && status != domain.ProviderStartUnknown {
			reconcilesUnknown = true
		} else if previous.Status != domain.ProviderStartUnknown {
			return errors.New("Provider Start observation is already terminal")
		}
	}
	commandID := domain.ProviderStartObservationCommandID(
		operationID, status, sessionID, receiptID, evidenceSHA256, reconcilesUnknown,
	)
	_, _, err = c.store.AddProviderStartObservation(
		ctx, jobID, job.Revision, operationID, commandID, status,
		sessionID, receiptID, evidenceSHA256, reconcilesUnknown, principal,
	)
	return err
}

func planningAuthorization(job domain.Job, id string) (domain.PlanningAuthorization, bool) {
	for _, grant := range job.PlanningAuthorizations {
		if grant.ID == id {
			return grant, true
		}
	}
	return domain.PlanningAuthorization{}, false
}

func latestStartObservation(job domain.Job, operationID string) (domain.ProviderStartObservation, bool) {
	var latest domain.ProviderStartObservation
	found := false
	for _, observation := range job.ProviderStartObservations {
		if observation.OperationID == operationID {
			latest, found = observation, true
		}
	}
	return latest, found
}

func providerIdentityMatches(identity provider.Identity, grant domain.PlanningAuthorization) bool {
	return identity.ProviderID == grant.ProviderID &&
		identity.AccountID == grant.AccountID &&
		identity.ConfigurationSHA256 == grant.ConfigurationSHA256
}

func validatePlanningContext(grant domain.PlanningAuthorization, documents []provider.ContextDocument) error {
	if len(documents) == 0 {
		return errors.New("DRAFT planning requires an immutable read-only context")
	}
	switch grant.ReadOnlyBoundary.Mode {
	case domain.PlanningReadOnlyBoundarySnapshot:
		if len(documents) != 1 || documents[0].ID != grant.ReadOnlyBoundary.Reference ||
			documents[0].SHA256 != grant.ReadOnlyBoundary.SHA256 {
			return errors.New("Provider context snapshot differs from PlanningAuthorization read-only boundary")
		}
		return nil
	case domain.PlanningReadOnlyBoundaryDocuments:
		ordered := append([]provider.ContextDocument(nil), documents...)
		sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
		var material string
		for i, document := range ordered {
			if i > 0 && ordered[i-1].ID == document.ID {
				return errors.New("Provider context contains duplicate document identity")
			}
			material += document.ID + "\n" + document.SHA256 + "\n"
		}
		if sha256String(material) != grant.ReadOnlyBoundary.SHA256 {
			return errors.New("Provider document set differs from PlanningAuthorization read-only boundary")
		}
		return nil
	default:
		return errors.New("unsupported PlanningAuthorization read-only boundary mode")
	}
}

func validProviderEvidenceSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func sha256String(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func latestProviderStopObservation(job domain.Job, operationID string) (domain.ProviderStopObservation, bool) {
	var latest domain.ProviderStopObservation
	found := false
	for _, observation := range job.ProviderStopObservations {
		if observation.OperationID != operationID {
			continue
		}
		if !found || observation.CreatedRevision > latest.CreatedRevision {
			latest, found = observation, true
		}
	}
	return latest, found
}

func providerStopObservationUnknown(observation domain.ProviderStopObservation) bool {
	return !observation.Stopped && !observation.Quiescent && observation.EvidenceSHA256 == ""
}

func providerStopDefinitiveFailureEvidence(operationID string, err error) (string, bool) {
	var operationError *provider.OperationError
	if !errors.As(err, &operationError) || operationError == nil ||
		operationError.Class != provider.FailureDefinitive {
		return "", false
	}
	return sha256String("taxion:provider-stop-definitive-failure:" + operationID + ":" +
		string(operationError.Class) + ":" + operationError.Code), true
}

func providerResumeDefinitiveFailureEvidence(operationID string, err error) (string, bool) {
	var operationError *provider.OperationError
	if !errors.As(err, &operationError) || operationError == nil ||
		operationError.Class != provider.FailureDefinitive {
		return "", false
	}
	return sha256String("taxion:provider-resume-definitive-failure:" + operationID + ":" +
		string(operationError.Class) + ":" + operationError.Code), true
}

func providerOperationMayHaveStarted(err error) bool {
	var operationError *provider.OperationError
	return errors.As(err, &operationError) && operationError != nil &&
		operationError.Class == provider.FailureAmbiguous
}

func providerDefinitiveFailureEvidence(operationID string, err error) (string, bool) {
	var operationError *provider.OperationError
	if !errors.As(err, &operationError) || operationError == nil ||
		operationError.Class != provider.FailureDefinitive {
		return "", false
	}
	return sha256String("taxion:provider-start-definitive-failure:" + operationID + ":" + string(operationError.Class) + ":" + operationError.Code), true
}

func startResultFromObservation(observation domain.ProviderStartObservation) provider.StartResult {
	return provider.StartResult{
		OperationID: observation.OperationID, SessionID: observation.ProviderSessionID,
		ReceiptID: observation.ReceiptID, EvidenceSHA256: observation.EvidenceSHA256,
	}
}

func (c *ProviderController) RetrieveProviderResult(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, turnID string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderResultObservation, error) {
	_, _, _, operationID, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, turnID, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	if capabilities.Identity != c.runtime.Identity() ||
		!capabilities.HasEvidenceAtLeast(provider.CapabilityFinalOutput, provider.EvidenceLive) {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, provider.ErrUnsupportedCapability
	}
	request := provider.ResultRequest{OperationID: operationID, SessionID: sessionID, TurnID: turnID}
	if err := request.Validate(); err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, provider.ErrProviderInvalidRequest
	}
	result, err := c.runtime.Result(ctx, request)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	if result.OperationID != operationID || result.SessionID != sessionID || result.TurnID != turnID {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, provider.ErrProviderInvalidResult
	}
	current, _, _, currentOperationID, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, turnID, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, err
	}
	if currentOperationID != operationID {
		return store.CommandReceipt{}, domain.ProviderResultObservation{}, provider.ErrProviderInvalidResult
	}
	return c.store.AddProviderResultObservation(
		ctx, current.ID, current.Revision, roleExecutionID, operationID, turnID, sessionID,
		result.Content, result.ResultSHA256, result.Final, result.EvidenceSHA256, principal,
	)
}

func (c *ProviderController) RetrieveProviderUsage(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, turnID string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderUsageObservation, error) {
	return c.retrieveProviderUsage(ctx, jobID, roleExecutionID, sessionID, turnID, "", principal)
}

func (c *ProviderController) retrieveProviderUsage(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, turnID, expectedUsageID string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.ProviderUsageObservation, error) {
	_, _, _, operationID, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, turnID, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, err
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
	request := provider.UsageRequest{OperationID: operationID, SessionID: sessionID, TurnID: turnID}
	if err := request.Validate(); err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, provider.ErrProviderInvalidRequest
	}
	result, err := c.runtime.Usage(ctx, request)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, err
	}
	if result.OperationID != operationID || result.SessionID != sessionID || result.TurnID != turnID ||
		(expectedUsageID != "" && result.UsageID != expectedUsageID) {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, provider.ErrProviderInvalidResult
	}
	current, _, _, currentOperationID, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, turnID, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, err
	}
	if currentOperationID != operationID {
		return store.CommandReceipt{}, domain.ProviderUsageObservation{}, provider.ErrProviderInvalidResult
	}
	return c.store.AddProviderUsageObservation(
		ctx, current.ID, current.Revision, roleExecutionID, operationID, turnID, sessionID,
		result.UsageID, result.Tokens, result.Complete, result.EvidenceSHA256, principal,
	)
}

func (c *ProviderController) providerObservationTarget(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, turnID string,
	principal auth.AuthenticatedPrincipal,
) (domain.Job, domain.RoleExecution, domain.PlanningAuthorization, string, error) {
	if ctx == nil {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", provider.ErrProviderInvalidRequest
	}
	if err := principal.Validate(); err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", err
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", err
	}
	if job.State != domain.JobDraft {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage requires a DRAFT Job")
	}
	var execution domain.RoleExecution
	foundExecution := false
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution, foundExecution = candidate, true
			break
		}
	}
	if !foundExecution {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider observation RoleExecution was not found")
	}
	grant, foundGrant := planningAuthorization(job, execution.PlanningAuthorizationID)
	if !foundGrant || grant.PrincipalID != principal.PrincipalID() {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage requires the authorized HUMAN Principal")
	}
	if err := grant.Validate(job); err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", err
	}
	if !providerIdentityMatches(c.runtime.Identity(), grant) {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Runtime identity differs from PlanningAuthorization")
	}
	var startIntent domain.ProviderStartIntent
	foundStart := false
	for _, candidate := range job.ProviderStartIntents {
		if candidate.RoleExecutionID == execution.ID {
			startIntent, foundStart = candidate, true
			break
		}
	}
	if !foundStart {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage has no Start intent")
	}
	if err := startIntent.Validate(job, execution); err != nil {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", fmt.Errorf("validate Provider Start intent: %w", err)
	}
	startObservation, foundObservation := latestStartObservation(job, startIntent.ID)
	if !foundObservation || startObservation.Status != domain.ProviderStartAccepted ||
		startObservation.ProviderSessionID != sessionID {
		return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage requires the accepted Start Session")
	}
	if turnID == "" {
		return job, execution, grant, startIntent.ID, nil
	}
	for _, turn := range job.PlanningTurnIntents {
		if turn.ID != turnID {
			continue
		}
		if err := turn.Validate(job); err != nil {
			return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", err
		}
		if turn.RoleExecutionID != execution.ID || turn.ProviderSessionID != sessionID {
			return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage Turn identity mismatch")
		}
		return job, execution, grant, turn.OperationID, nil
	}
	return domain.Job{}, domain.RoleExecution{}, domain.PlanningAuthorization{}, "", errors.New("Provider Result / Usage Turn intent was not found")
}

func (c *ProviderController) SendPlanning(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, commandID, prompt string,
	reservedTokens uint64,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.PlanningTurnIntent, domain.PlanningTurnObservation, provider.TurnResult, error) {
	if ctx == nil || prompt == "" || reservedTokens == 0 {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, provider.ErrProviderInvalidRequest
	}
	job, execution, grant, _, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, "", principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	existingIntent := false
	for _, existing := range job.PlanningTurnIntents {
		if existing.CommandID == commandID && existing.RoleExecutionID == execution.ID {
			existingIntent = true
			break
		}
	}
	if !existingIntent {
		if err := grant.ValidateFor(job, principal.PrincipalID(), c.now()); err != nil {
			return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
		}
	}
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	if capabilities.Identity != c.runtime.Identity() || !capabilities.SupportsSafeSend() {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, provider.ErrUnsupportedCapability
	}
	promptSHA256 := sha256String(prompt)
	request := provider.TurnRequest{
		OperationID: domain.PlanningTurnOperationID(commandID), TurnID: domain.PlanningTurnIntentID(commandID),
		SessionID: sessionID, PromptSHA256: promptSHA256, Prompt: prompt,
		MaxTokens: reservedTokens, Deadline: grant.Deadline,
	}
	if err := request.Validate(); err != nil {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, provider.ErrProviderInvalidRequest
	}
	receipt, turn, err := c.store.AddPlanningTurnIntent(
		ctx, jobID, job.Revision, execution.ID, sessionID, commandID, promptSHA256, reservedTokens, principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningTurnIntent{}, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	current, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	for _, candidate := range current.PlanningTurnIntents {
		if candidate.CommandID == commandID {
			turn = candidate
			break
		}
	}
	if err := turn.Validate(current); err != nil {
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	request.OperationID, request.TurnID = turn.OperationID, turn.ID
	if previous, found := latestProviderTurnObservation(current, turn.ID); found && previous.Status != domain.PlanningTurnUnknown {
		return receipt, turn, previous, turnResultFromObservation(previous), nil
	}
	return c.reconcileTurn(ctx, receipt, current, turn, request, principal)
}

func (c *ProviderController) reconcileTurn(
	ctx context.Context,
	receipt store.CommandReceipt,
	job domain.Job,
	turn domain.PlanningTurnIntent,
	request provider.TurnRequest,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.PlanningTurnIntent, domain.PlanningTurnObservation, provider.TurnResult, error) {
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	if capabilities.Identity != c.runtime.Identity() {
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, provider.ErrUnsupportedCapability
	}
	canLookup := capabilities.HasEvidenceAtLeast(provider.CapabilitySendLookup, provider.EvidenceLive)
	canRetry := capabilities.HasEvidenceAtLeast(provider.CapabilitySendIdempotency, provider.EvidenceLive)
	if canLookup {
		lookup, lookupErr := c.runtime.Lookup(ctx, provider.LookupRequest{
			OperationID: turn.OperationID, SessionID: turn.ProviderSessionID, TurnID: turn.ID,
		})
		if lookupErr != nil {
			observation, persistErr := c.recordTurnObservation(
				ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
			)
			if persistErr != nil {
				return receipt, turn, observation, provider.TurnResult{}, errors.Join(lookupErr, persistErr)
			}
			return receipt, turn, observation, provider.TurnResult{}, lookupErr
		}
		if lookup.Found {
			if lookup.Status == domain.PlanningTurnCompleted || lookup.Status == domain.PlanningTurnFailed {
				_, usage, usageErr := c.retrieveProviderUsage(
					ctx, job.ID, turn.RoleExecutionID, turn.ProviderSessionID, turn.ID, lookup.UsageID, principal,
				)
				if usageErr != nil {
					observation, persistErr := c.recordTurnObservation(
						ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
					)
					if persistErr != nil {
						return receipt, turn, observation, provider.TurnResult{}, errors.Join(usageErr, persistErr)
					}
					return receipt, turn, observation, provider.TurnResult{}, usageErr
				}
				if !usage.Complete {
					observation, persistErr := c.recordTurnObservation(
						ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
					)
					if persistErr != nil {
						return receipt, turn, observation, provider.TurnResult{}, persistErr
					}
					return receipt, turn, observation, provider.TurnResult{}, errors.New("Provider Turn terminal state awaits complete Usage evidence")
				}
				observation, err := c.recordTurnObservation(
					ctx, job.ID, turn.ID, lookup.Status, usage.Tokens, usage.EvidenceSHA256, principal,
				)
				if err != nil {
					unknown, persistErr := c.recordTurnObservation(
						ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
					)
					if persistErr != nil {
						return receipt, turn, unknown, provider.TurnResult{}, errors.Join(err, persistErr)
					}
					return receipt, turn, unknown, provider.TurnResult{}, err
				}
				return receipt, turn, observation, provider.TurnResult{
					OperationID: turn.OperationID, TurnID: turn.ID, SessionID: turn.ProviderSessionID,
					ReceiptID: lookup.ReceiptID, Status: lookup.Status, TokensUsed: usage.Tokens,
					EvidenceSHA256: usage.EvidenceSHA256,
				}, nil
			}
			observation, persistErr := c.recordTurnObservation(
				ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
			)
			if persistErr != nil {
				return receipt, turn, observation, provider.TurnResult{}, persistErr
			}
			return receipt, turn, observation, provider.TurnResult{}, errors.New("Provider Turn lookup has not confirmed a terminal result")
		}
	} else if !canRetry {
		observation, persistErr := c.recordTurnObservation(
			ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
		)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, persistErr
		}
		return receipt, turn, observation, provider.TurnResult{}, provider.ErrUnsupportedCapability
	}
	current, _, grant, _, err := c.providerObservationTarget(
		ctx, job.ID, turn.RoleExecutionID, turn.ProviderSessionID, turn.ID, principal,
	)
	if err != nil {
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	if err := grant.ValidateFor(current, principal.PrincipalID(), c.now()); err != nil {
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	result, err := c.runtime.Send(ctx, request)
	if err != nil {
		if providerOperationMayHaveStarted(err) {
			observation, persistErr := c.recordTurnObservation(
				ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
			)
			if persistErr != nil {
				return receipt, turn, observation, provider.TurnResult{}, errors.Join(err, persistErr)
			}
			return receipt, turn, observation, provider.TurnResult{}, err
		}
		return receipt, turn, domain.PlanningTurnObservation{}, provider.TurnResult{}, err
	}
	if result.OperationID != turn.OperationID || result.TurnID != turn.ID || result.SessionID != turn.ProviderSessionID {
		observation, persistErr := c.recordTurnObservation(
			ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
		)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, errors.Join(provider.ErrProviderInvalidResult, persistErr)
		}
		return receipt, turn, observation, provider.TurnResult{}, provider.ErrProviderInvalidResult
	}
	if result.Status == domain.PlanningTurnUnknown {
		observation, persistErr := c.recordTurnObservation(
			ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
		)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, persistErr
		}
		return receipt, turn, observation, result, errors.New("Provider Turn remains UNKNOWN")
	}
	if result.Status != domain.PlanningTurnCompleted && result.Status != domain.PlanningTurnFailed {
		observation, persistErr := c.recordTurnObservation(
			ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
		)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, persistErr
		}
		return receipt, turn, observation, provider.TurnResult{}, provider.ErrProviderInvalidResult
	}
	observation, err := c.recordTurnObservation(
		ctx, job.ID, turn.ID, result.Status, result.TokensUsed, result.EvidenceSHA256, principal,
	)
	if err != nil {
		observation, persistErr := c.recordTurnObservation(
			ctx, job.ID, turn.ID, domain.PlanningTurnUnknown, 0, "", principal,
		)
		if persistErr != nil {
			return receipt, turn, observation, provider.TurnResult{}, errors.Join(err, persistErr)
		}
		return receipt, turn, observation, provider.TurnResult{}, err
	}
	return receipt, turn, observation, result, nil
}

func (c *ProviderController) recordTurnObservation(
	ctx context.Context,
	jobID, turnID, status string,
	tokensUsed uint64,
	evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (domain.PlanningTurnObservation, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.PlanningTurnObservation{}, err
	}
	previous, found := latestProviderTurnObservation(job, turnID)
	if found {
		if previous.Status == domain.PlanningTurnUnknown {
			if status == domain.PlanningTurnUnknown {
				return previous, nil
			}
		} else if previous.Status == status && previous.TokensUsed == tokensUsed && previous.EvidenceSHA256 == evidenceSHA256 {
			return previous, nil
		} else {
			return domain.PlanningTurnObservation{}, errors.New("Provider Turn already has a terminal observation")
		}
	}
	reconcilesUnknown := found && previous.Status == domain.PlanningTurnUnknown && status != domain.PlanningTurnUnknown
	commandID := providerTurnObservationCommandID(turnID, status, tokensUsed, evidenceSHA256, reconcilesUnknown)
	_, observation, err := c.store.AddPlanningTurnObservation(
		ctx, job.ID, job.Revision, turnID, commandID, status, tokensUsed, evidenceSHA256, reconcilesUnknown, principal,
	)
	return observation, err
}

func latestProviderTurnObservation(job domain.Job, turnID string) (domain.PlanningTurnObservation, bool) {
	var latest domain.PlanningTurnObservation
	found := false
	for _, observation := range job.PlanningTurnObservations {
		if observation.TurnID == turnID && (!found || observation.CreatedRevision > latest.CreatedRevision) {
			latest, found = observation, true
		}
	}
	return latest, found
}

func providerTurnObservationCommandID(turnID, status string, tokensUsed uint64, evidenceSHA256 string, reconcilesUnknown bool) string {
	material := fmt.Sprintf("taxion:planning-turn-observation:%s:%s:%d:%s:%t", turnID, status, tokensUsed, evidenceSHA256, reconcilesUnknown)
	sum := sha256.Sum256([]byte(material))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func turnResultFromObservation(observation domain.PlanningTurnObservation) provider.TurnResult {
	return provider.TurnResult{
		OperationID: observation.OperationID, TurnID: observation.TurnID, SessionID: observation.ProviderSessionID,
		Status: observation.Status, TokensUsed: observation.TokensUsed, EvidenceSHA256: observation.EvidenceSHA256,
	}
}

func (c *ProviderController) ResumePlanning(
	ctx context.Context,
	jobID, roleExecutionID, sessionID, commandID string,
	principal auth.AuthenticatedPrincipal,
) (store.CommandReceipt, domain.PlanningResumeIntent, domain.PlanningResumeObservation, provider.ResumeResult, error) {
	job, execution, grant, _, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, "", principal,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
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
		if previous, found := latestProviderResumeObservation(job, existing.ID); found && previous.Status != domain.PlanningResumeUnknown {
			receipt, intent, err := c.store.AddPlanningResumeIntent(
				ctx, job.ID, job.Revision, execution.ID, sessionID, commandID,
				existing.CapabilityEvidenceSHA256, true, principal,
			)
			if err != nil {
				return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
			}
			return receipt, intent, previous, resumeResultFromObservation(previous), nil
		}
	}
	capabilities, err := c.runtime.Capabilities(ctx)
	if err != nil {
		return store.CommandReceipt{}, domain.PlanningResumeIntent{}, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if capabilities.Identity != c.runtime.Identity() || !capabilities.SupportsCrossProcessReattachment() {
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
		if err := grant.ValidateFor(job, principal.PrincipalID(), c.now()); err != nil {
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
		if candidate.CommandID == commandID {
			intent = candidate
			break
		}
	}
	if err := intent.Validate(current); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if previous, found := latestProviderResumeObservation(current, intent.ID); found && previous.Status != domain.PlanningResumeUnknown {
		return receipt, intent, previous, resumeResultFromObservation(previous), nil
	}
	request := provider.ResumeRequest{
		OperationID: intent.OperationID, SessionID: intent.ProviderSessionID, Deadline: grant.Deadline,
	}
	if err := request.Validate(); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, provider.ErrProviderInvalidRequest
	}
	latest, _, currentGrant, _, err := c.providerObservationTarget(
		ctx, jobID, roleExecutionID, sessionID, "", principal,
	)
	if err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if err := currentGrant.ValidateFor(latest, principal.PrincipalID(), c.now()); err != nil {
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	result, err := c.runtime.Resume(ctx, request)
	if err != nil {
		if providerOperationMayHaveStarted(err) || errors.Is(err, provider.ErrResumeUnconfirmed) {
			observation, persistErr := c.recordResumeObservation(
				ctx, jobID, intent.ID, domain.PlanningResumeUnknown, "", principal,
			)
			if persistErr != nil {
				return receipt, intent, observation, provider.ResumeResult{}, errors.Join(err, persistErr)
			}
			return receipt, intent, observation, provider.ResumeResult{}, err
		}
		return receipt, intent, domain.PlanningResumeObservation{}, provider.ResumeResult{}, err
	}
	if result.OperationID != intent.OperationID || result.SessionID != intent.ProviderSessionID ||
		!result.Attached || !validProviderSHA256(result.EvidenceSHA256) {
		observation, persistErr := c.recordResumeObservation(
			ctx, jobID, intent.ID, domain.PlanningResumeUnknown, "", principal,
		)
		if persistErr != nil {
			return receipt, intent, observation, provider.ResumeResult{}, errors.Join(provider.ErrResumeUnconfirmed, persistErr)
		}
		return receipt, intent, observation, provider.ResumeResult{}, provider.ErrResumeUnconfirmed
	}
	observation, err := c.recordResumeObservation(
		ctx, jobID, intent.ID, domain.PlanningResumeAttached, result.EvidenceSHA256, principal,
	)
	if err != nil {
		unknown, persistErr := c.recordResumeObservation(
			ctx, jobID, intent.ID, domain.PlanningResumeUnknown, "", principal,
		)
		if persistErr != nil {
			return receipt, intent, unknown, provider.ResumeResult{}, errors.Join(err, persistErr)
		}
		return receipt, intent, unknown, provider.ResumeResult{}, err
	}
	return receipt, intent, observation, result, nil
}

func (c *ProviderController) recordResumeObservation(
	ctx context.Context,
	jobID, resumeID, status, evidenceSHA256 string,
	principal auth.AuthenticatedPrincipal,
) (domain.PlanningResumeObservation, error) {
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return domain.PlanningResumeObservation{}, err
	}
	previous, found := latestProviderResumeObservation(job, resumeID)
	if found {
		if previous.Status == domain.PlanningResumeUnknown {
			if status == domain.PlanningResumeUnknown {
				return previous, nil
			}
		} else if previous.Status == status && previous.EvidenceSHA256 == evidenceSHA256 {
			return previous, nil
		} else {
			return domain.PlanningResumeObservation{}, errors.New("Provider Resume already has a terminal observation")
		}
	}
	reconcilesUnknown := found && previous.Status == domain.PlanningResumeUnknown && status != domain.PlanningResumeUnknown
	commandID := providerResumeObservationCommandID(resumeID, status, evidenceSHA256, reconcilesUnknown)
	_, observation, err := c.store.AddPlanningResumeObservation(
		ctx, job.ID, job.Revision, resumeID, commandID, status, evidenceSHA256, reconcilesUnknown, principal,
	)
	return observation, err
}

func latestProviderResumeObservation(job domain.Job, resumeID string) (domain.PlanningResumeObservation, bool) {
	var latest domain.PlanningResumeObservation
	found := false
	for _, observation := range job.PlanningResumeObservations {
		if observation.ResumeID == resumeID && (!found || observation.CreatedRevision > latest.CreatedRevision) {
			latest, found = observation, true
		}
	}
	return latest, found
}

func providerResumeObservationCommandID(resumeID, status, evidenceSHA256 string, reconcilesUnknown bool) string {
	material := fmt.Sprintf("taxion:planning-resume-observation:%s:%s:%s:%t", resumeID, status, evidenceSHA256, reconcilesUnknown)
	sum := sha256.Sum256([]byte(material))
	return "cmd_" + hex.EncodeToString(sum[:16])
}

func resumeResultFromObservation(observation domain.PlanningResumeObservation) provider.ResumeResult {
	return provider.ResumeResult{
		OperationID: observation.OperationID, SessionID: observation.ProviderSessionID,
		Attached: observation.Status == domain.PlanningResumeAttached, EvidenceSHA256: observation.EvidenceSHA256,
	}
}

func capabilityEvidenceDigest(capabilities provider.Capabilities, capability provider.Capability, minimum provider.EvidenceLevel) (string, error) {
	if !capabilities.HasEvidenceAtLeast(capability, minimum) {
		return "", provider.ErrUnsupportedCapability
	}
	var digest string
	var verifiedAt time.Time
	for _, evidence := range capabilities.Evidence {
		if evidence.Capability != capability || evidence.Identity != capabilities.Identity ||
			evidence.EvidenceLevel != minimum || evidence.VerifiedAt.IsZero() || !validProviderSHA256(evidence.EvidenceSHA256) {
			continue
		}
		if digest == "" || evidence.VerifiedAt.After(verifiedAt) {
			digest, verifiedAt = evidence.EvidenceSHA256, evidence.VerifiedAt
		}
	}
	if digest == "" {
		return "", provider.ErrUnsupportedCapability
	}
	return digest, nil
}

func hasCapabilityEvidenceDigest(capabilities provider.Capabilities, capability provider.Capability, minimum provider.EvidenceLevel, digest string) bool {
	if !capabilities.HasEvidenceAtLeast(capability, minimum) || !validProviderSHA256(digest) {
		return false
	}
	for _, evidence := range capabilities.Evidence {
		if evidence.Capability == capability && evidence.Identity == capabilities.Identity &&
			evidence.EvidenceLevel == minimum && evidence.EvidenceSHA256 == digest && !evidence.VerifiedAt.IsZero() {
			return true
		}
	}
	return false
}

func validProviderSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
