package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
	"github.com/hiono/taxion/internal/worktree"
)

type ExecutionPermissionStore interface {
	GetJob(context.Context, string) (domain.Job, error)
	AddExecutionPermission(context.Context, uint64, domain.ExecutionPermissionRequest, string, domain.ExecutionEffectAssessment) (store.CommandReceipt, domain.ExecutionPermission, error)
	StartTaskExecution(context.Context, uint64, string, string, string, string, string, domain.WorktreeSnapshot) (store.CommandReceipt, domain.TaskExecutionStart, error)
}

type TaskWorktreeManager interface {
	Prepare(context.Context, string, domain.Task) (domain.WorktreeSnapshot, error)
	Verify(context.Context, string, domain.Task, domain.WorktreeSnapshot) error
}

type ExecutionEffectAssessor interface {
	Assess(context.Context, domain.ExecutionPermissionRequest, string) (domain.ExecutionEffectAssessment, error)
}

type ExecutionController struct {
	store     ExecutionPermissionStore
	assessor  ExecutionEffectAssessor
	worktrees TaskWorktreeManager
	now       func() time.Time
}

func NewExecutionController(permissionStore ExecutionPermissionStore, assessor ExecutionEffectAssessor) (*ExecutionController, error) {
	if permissionStore == nil || assessor == nil {
		return nil, errors.New("ExecutionPermissionStore and trusted ExecutionEffectAssessor are required")
	}
	return &ExecutionController{
		store: permissionStore, assessor: assessor,
		now: func() time.Time { return time.Now().UTC().Round(0) },
	}, nil
}

func NewExecutionControllerWithWorktree(
	permissionStore ExecutionPermissionStore,
	assessor ExecutionEffectAssessor,
	worktrees TaskWorktreeManager,
) (*ExecutionController, error) {
	if worktrees == nil {
		return nil, errors.New("trusted Task Worktree manager is required")
	}
	controller, err := NewExecutionController(permissionStore, assessor)
	if err != nil {
		return nil, err
	}
	controller.worktrees = worktrees
	return controller, nil
}

// Authorize issues a one-operation permission only after current JobApproval and trusted effect assessment checks.
func (c *ExecutionController) Authorize(
	ctx context.Context,
	request domain.ExecutionPermissionRequest,
) (store.CommandReceipt, domain.ExecutionPermission, error) {
	if ctx == nil || request.JobID == "" {
		return store.CommandReceipt{}, domain.ExecutionPermission{}, errors.New("ExecutionPermission requires context and Job identity")
	}
	job, err := c.store.GetJob(ctx, request.JobID)
	if err != nil {
		return store.CommandReceipt{}, domain.ExecutionPermission{}, err
	}
	now := c.now().UTC().Round(0)
	if err := domain.ValidateExecutionPermissionRequest(job, request, now); err != nil {
		return store.CommandReceipt{}, domain.ExecutionPermission{}, fmt.Errorf("ExecutionPermission request rejected: %w", err)
	}
	requestSHA, err := domain.ExecutionPermissionRequestSHA256(request)
	if err != nil {
		return store.CommandReceipt{}, domain.ExecutionPermission{}, err
	}
	assessment, err := c.assessor.Assess(ctx, request, requestSHA)
	if err != nil {
		return store.CommandReceipt{}, domain.ExecutionPermission{}, fmt.Errorf("assess execution effect: %w", err)
	}
	if _, err := domain.NewExecutionPermission(job, request, requestSHA, assessment, now); err != nil {
		return store.CommandReceipt{}, domain.ExecutionPermission{}, fmt.Errorf("ExecutionPermission assessment rejected: %w", err)
	}
	return c.store.AddExecutionPermission(ctx, job.Revision, request, requestSHA, assessment)
}

// ValidatePermission rechecks current approval, revision, exact operation identity, and expiry before dispatch.
func (c *ExecutionController) ValidatePermission(
	ctx context.Context,
	permissionID string,
	request domain.ExecutionPermissionRequest,
) (domain.ExecutionPermission, error) {
	if ctx == nil || permissionID == "" || request.JobID == "" {
		return domain.ExecutionPermission{}, errors.New("ExecutionPermission validation requires complete identities")
	}
	requestSHA256, err := domain.ExecutionPermissionRequestSHA256(request)
	if err != nil {
		return domain.ExecutionPermission{}, err
	}
	job, err := c.store.GetJob(ctx, request.JobID)
	if err != nil {
		return domain.ExecutionPermission{}, err
	}
	for _, permission := range job.ExecutionPermissions {
		if permission.ID != permissionID {
			continue
		}
		if err := permission.ValidateFor(job, domain.ExecutionPermissionOperationID(request.CommandID), requestSHA256, c.now()); err != nil {
			return domain.ExecutionPermission{}, fmt.Errorf("ExecutionPermission is not current: %w", err)
		}
		return permission, nil
	}
	return domain.ExecutionPermission{}, store.ErrNotFound
}

// StartTaskExecution atomically consumes a Task-bound permission into a Coder / Reviewer RoleExecution and ProviderStartIntent.
func (c *ExecutionController) StartTaskExecution(
	ctx context.Context,
	permissionID, commandID string,
	request domain.ExecutionPermissionRequest,
) (store.CommandReceipt, domain.RoleExecution, domain.ProviderStartIntent, error) {
	if c.worktrees == nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, errors.New("Task execution start requires a trusted Worktree manager")
	}
	permission, err := c.ValidatePermission(ctx, permissionID, request)
	if err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, err
	}
	job, err := c.store.GetJob(ctx, request.JobID)
	if err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, err
	}
	var task domain.Task
	taskFound := false
	for _, candidate := range job.Tasks {
		if candidate.ID == request.TaskID {
			task, taskFound = candidate, true
			break
		}
	}
	if !taskFound {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, errors.New("Task execution start Task was not found")
	}
	for _, existing := range job.RoleExecutions {
		if existing.TaskID == task.ID && existing.CommandID != commandID {
			return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, errors.New("Task Worktree is already owned; an explicit safe handoff is required")
		}
	}
	var snapshot domain.WorktreeSnapshot
	assignmentFound := false
	for _, assignment := range job.WorktreeAssignments {
		if assignment.TaskID != task.ID {
			continue
		}
		if assignment.OwnerRoleExecutionID != domain.RoleExecutionID(commandID) {
			return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, errors.New("Task Worktree is already assigned to another RoleExecution")
		}
		snapshot = assignment.Snapshot()
		assignmentFound = true
		break
	}
	if assignmentFound {
		if err := c.worktrees.Verify(ctx, job.ID, task, snapshot); err != nil {
			return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("verify existing Task Worktree before exact Start replay: %w", err)
		}
	} else {
		snapshot, err = c.worktrees.Prepare(ctx, job.ID, task)
		if err != nil {
			return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("prepare Task Worktree before Start intent: %w", err)
		}
	}
	receipt, start, err := c.store.StartTaskExecution(
		ctx, job.Revision, request.JobID, request.TaskID, permissionID, commandID, permission.RequestSHA256, snapshot,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, err
	}
	return receipt, start.RoleExecution, start.ProviderStartIntent, nil
}


// CaptureTaskOutput records the exact committed Worktree tree after the Coder
// Turn, final Result / Usage, and Provider quiescence have been confirmed.
func (c *ExecutionController) CaptureTaskOutput(
	ctx context.Context,
	jobID, roleExecutionID, turnID string,
) (store.CommandReceipt, domain.JobOutput, error) {
	if ctx == nil || jobID == "" || roleExecutionID == "" || turnID == "" {
		return store.CommandReceipt{}, domain.JobOutput{}, errors.New("Task output capture requires complete identities")
	}
	outputStore, ok := c.store.(interface {
		AddJobOutput(context.Context, string, uint64, string, string, string, string, string) (store.CommandReceipt, domain.JobOutput, error)
	})
	if !ok {
		return store.CommandReceipt{}, domain.JobOutput{}, errors.New("ExecutionPermissionStore does not support JobOutput persistence")
	}
	outputManager, ok := c.worktrees.(interface {
		CaptureOutput(context.Context, string, domain.Task, domain.WorktreeSnapshot) (worktree.OutputSnapshot, error)
	})
	if !ok {
		return store.CommandReceipt{}, domain.JobOutput{}, errors.New("Task Worktree manager does not support immutable output capture")
	}
	job, err := c.store.GetJob(ctx, jobID)
	if err != nil {
		return store.CommandReceipt{}, domain.JobOutput{}, err
	}
	var task domain.Task
	taskFound := false
	for _, candidate := range job.Tasks {
		if candidate.ID == roleExecutionTaskID(job, roleExecutionID) {
			task, taskFound = candidate, true
			break
		}
	}
	if !taskFound {
		return store.CommandReceipt{}, domain.JobOutput{}, errors.New("Task output producer Task was not found")
	}
	var assignment domain.WorktreeAssignment
	assignmentFound := false
	for _, candidate := range job.WorktreeAssignments {
		if candidate.TaskID == task.ID && candidate.OwnerRoleExecutionID == roleExecutionID {
			assignment, assignmentFound = candidate, true
			break
		}
	}
	if !assignmentFound {
		return store.CommandReceipt{}, domain.JobOutput{}, errors.New("Task output producer does not own its Worktree assignment")
	}
	snapshot, err := outputManager.CaptureOutput(ctx, jobID, task, assignment.Snapshot())
	if err != nil {
		return store.CommandReceipt{}, domain.JobOutput{}, err
	}
	return outputStore.AddJobOutput(ctx, jobID, job.Revision, task.ID, roleExecutionID, turnID, snapshot.CommitSHA, snapshot.TreeSHA)
}

func roleExecutionTaskID(job domain.Job, roleExecutionID string) string {
	for _, execution := range job.RoleExecutions {
		if execution.ID == roleExecutionID {
			return execution.TaskID
		}
	}
	return ""
}
