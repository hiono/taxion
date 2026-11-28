package controller

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/store"
)

type ExecutionPermissionStore interface {
	GetJob(context.Context, string) (domain.Job, error)
	AddExecutionPermission(context.Context, uint64, domain.ExecutionPermissionRequest, string, domain.ExecutionEffectAssessment) (store.CommandReceipt, domain.ExecutionPermission, error)
	StartTaskExecution(context.Context, uint64, string, string, string, string, string) (store.CommandReceipt, domain.TaskExecutionStart, error)
	BindRoleExecutionWorktree(context.Context, uint64, string, string, string, domain.WorktreeAssignment) (store.CommandReceipt, domain.WorktreeAssignment, error)
}

type ExecutionEffectAssessor interface {
	Assess(context.Context, domain.ExecutionPermissionRequest, string) (domain.ExecutionEffectAssessment, error)
}

type ExecutionController struct {
	store    ExecutionPermissionStore
	assessor ExecutionEffectAssessor
	now      func() time.Time
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

// BindWorktree persists the exact Job-owned Worktree assignment before Provider Start.
func (c *ExecutionController) BindWorktree(
	ctx context.Context,
	roleExecutionID, commandID string,
	assignment domain.WorktreeAssignment,
) (store.CommandReceipt, domain.WorktreeAssignment, error) {
	if c == nil || ctx == nil || roleExecutionID == "" || commandID == "" || assignment.JobID == "" {
		return store.CommandReceipt{}, domain.WorktreeAssignment{}, errors.New("Worktree binding requires context and exact Job execution identities")
	}
	job, err := c.store.GetJob(ctx, assignment.JobID)
	if err != nil {
		return store.CommandReceipt{}, domain.WorktreeAssignment{}, err
	}
	return c.store.BindRoleExecutionWorktree(
		ctx, job.Revision, job.ID, roleExecutionID, commandID, assignment,
	)
}

// StartTaskExecution atomically consumes a Task-bound permission into a Coder / Reviewer RoleExecution and ProviderStartIntent.
func (c *ExecutionController) StartTaskExecution(
	ctx context.Context,
	permissionID, commandID string,
	request domain.ExecutionPermissionRequest,
) (store.CommandReceipt, domain.RoleExecution, domain.ProviderStartIntent, error) {
	permission, err := c.ValidatePermission(ctx, permissionID, request)
	if err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, err
	}
	job, err := c.store.GetJob(ctx, request.JobID)
	if err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, err
	}
	receipt, start, err := c.store.StartTaskExecution(
		ctx, job.Revision, request.JobID, request.TaskID, permissionID, commandID, permission.RequestSHA256,
	)
	if err != nil {
		return store.CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, err
	}
	return receipt, start.RoleExecution, start.ProviderStartIntent, nil
}
