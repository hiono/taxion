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
