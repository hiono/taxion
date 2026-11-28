package controller

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
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
	var execution domain.RoleExecution
	for _, candidate := range job.RoleExecutions {
		if candidate.ID == roleExecutionID {
			execution = candidate
			break
		}
	}
	if execution.ID == "" || execution.JobID != job.ID || execution.TaskID != assignment.TaskID ||
		execution.ExecutionPermissionID == "" {
		return store.CommandReceipt{}, domain.WorktreeAssignment{}, errors.New("Worktree binding does not match an authorized Task RoleExecution")
	}
	var permission domain.ExecutionPermission
	for _, candidate := range job.ExecutionPermissions {
		if candidate.ID == execution.ExecutionPermissionID {
			permission = candidate
			break
		}
	}
	if permission.ID == "" || permission.JobID != job.ID || permission.TaskID != assignment.TaskID ||
		permission.Resource != assignment.RepositoryID {
		return store.CommandReceipt{}, domain.WorktreeAssignment{}, errors.New("Worktree repository does not match the approved ExecutionPermission resource")
	}
	if err := verifyGitWorktree(ctx, assignment.RepositoryPath, assignment.WorktreePath, assignment.BaselineCommit); err != nil {
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

func verifyGitWorktree(ctx context.Context, repositoryPath, worktreePath, baselineCommit string) error {
	if ctx == nil || repositoryPath == "" || worktreePath == "" || baselineCommit == "" {
		return errors.New("Git Worktree verification requires repository, Worktree, and baseline identities")
	}
	repositoryRoot, err := gitOutput(ctx, repositoryPath, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("resolve approved repository root: %w", err)
	}
	repositoryRoot, err = canonicalGitPath(repositoryRoot)
	if err != nil {
		return fmt.Errorf("canonicalize approved repository root: %w", err)
	}
	requestedRepository, err := canonicalGitPath(repositoryPath)
	if err != nil || requestedRepository != repositoryRoot {
		return errors.New("approved RepositoryPath must identify the Git repository root")
	}
	worktreeRoot, err := gitOutput(ctx, worktreePath, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("resolve Worktree root: %w", err)
	}
	worktreeRoot, err = canonicalGitPath(worktreeRoot)
	if err != nil {
		return fmt.Errorf("canonicalize Worktree root: %w", err)
	}
	requestedWorktree, err := canonicalGitPath(worktreePath)
	if err != nil || requestedWorktree != worktreeRoot {
		return errors.New("WorktreePath must identify the Worktree root")
	}
	repositoryCommon, err := gitCommonDirectory(ctx, repositoryRoot)
	if err != nil {
		return err
	}
	worktreeCommon, err := gitCommonDirectory(ctx, worktreeRoot)
	if err != nil {
		return err
	}
	if repositoryCommon != worktreeCommon {
		return errors.New("Worktree does not belong to the approved repository")
	}
	head, err := gitOutput(ctx, worktreeRoot, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("resolve Worktree baseline commit: %w", err)
	}
	if !strings.EqualFold(head, baselineCommit) {
		return errors.New("Worktree HEAD does not match the approved baseline commit")
	}
	worktrees, err := gitOutput(ctx, repositoryRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return fmt.Errorf("list approved repository Worktrees: %w", err)
	}
	registered := false
	for _, line := range strings.Split(worktrees, "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		registeredPath, pathErr := canonicalGitPath(strings.TrimPrefix(line, "worktree "))
		if pathErr == nil && registeredPath == worktreeRoot {
			registered = true
			break
		}
	}
	if !registered {
		return errors.New("WorktreePath is not registered by the approved repository")
	}
	return nil
}

func gitOutput(ctx context.Context, directory string, args ...string) (string, error) {
	commandArgs := append([]string{"-C", directory}, args...)
	output, err := exec.CommandContext(ctx, "git", commandArgs...).Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func gitCommonDirectory(ctx context.Context, directory string) (string, error) {
	common, err := gitOutput(ctx, directory, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("resolve Git common directory: %w", err)
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(directory, common)
	}
	return canonicalGitPath(common)
}

func canonicalGitPath(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}
