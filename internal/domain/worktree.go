package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

const (
	WorktreeOriginTaxionCreated = "TAXION_CREATED"
	WorktreeOriginAdopted       = "ADOPTED_EXISTING"
	WorktreeHeadDetached        = "DETACHED"
	WorktreeHeadBranchAttached  = "BRANCH_ATTACHED"
)

type WorktreeSnapshot struct {
	RepositoryID       string `json:"repository_id"`
	RepositoryPath     string `json:"repository_path"`
	WorktreePath       string `json:"worktree_path"`
	BaselineCommit     string `json:"baseline_commit"`
	Origin             string `json:"origin,omitempty"`
	InitialStateSHA256 string `json:"initial_state_sha256,omitempty"`
	HeadMode           string `json:"head_mode,omitempty"`
	HeadRef            string `json:"head_ref,omitempty"`
}

type WorktreeAssignment struct {
	ID                   string    `json:"id"`
	JobID                string    `json:"job_id"`
	TaskID               string    `json:"task_id"`
	RepositoryID         string    `json:"repository_id"`
	RepositoryPath       string    `json:"repository_path"`
	WorktreePath         string    `json:"worktree_path"`
	BaselineCommit       string    `json:"baseline_commit"`
	Origin               string    `json:"origin,omitempty"`
	InitialStateSHA256   string    `json:"initial_state_sha256,omitempty"`
	HeadMode             string    `json:"head_mode,omitempty"`
	HeadRef              string    `json:"head_ref,omitempty"`
	OwnerRoleExecutionID string    `json:"owner_role_execution_id"`
	CreatedAt            time.Time `json:"created_at"`
	CreatedRevision      uint64    `json:"created_revision"`
}

func WorktreeID(jobID, taskID string) string {
	sum := sha256.Sum256([]byte("taxion:task-worktree:" + jobID + ":" + taskID))
	return "worktree_" + hex.EncodeToString(sum[:])
}

func NewWorktreeAssignment(job Job, task Task, execution RoleExecution, snapshot WorktreeSnapshot, now time.Time) (WorktreeAssignment, error) {
	if err := task.Validate(job); err != nil {
		return WorktreeAssignment{}, err
	}
	if execution.JobID != job.ID || execution.TaskID != task.ID ||
		(execution.Role != RoleCoder && execution.Role != RoleReviewer) ||
		execution.ID != RoleExecutionID(execution.CommandID) ||
		execution.WorktreeID != WorktreeID(job.ID, task.ID) {
		return WorktreeAssignment{}, errors.New("Worktree owner must be a Task-bound Coder or Reviewer RoleExecution")
	}
	if len(task.TargetRepositories) != 1 || task.TargetRepositories[0] != snapshot.RepositoryID ||
		strings.TrimSpace(snapshot.RepositoryPath) == "" || strings.TrimSpace(snapshot.WorktreePath) == "" ||
		!filepath.IsAbs(snapshot.RepositoryPath) || !filepath.IsAbs(snapshot.WorktreePath) ||
		filepath.Clean(snapshot.RepositoryPath) != snapshot.RepositoryPath ||
		filepath.Clean(snapshot.WorktreePath) != snapshot.WorktreePath ||
		!validGitCommit(snapshot.BaselineCommit) {
		return WorktreeAssignment{}, errors.New("Worktree snapshot must match one approved repository and contain canonical paths and a valid baseline commit")
	}
	if snapshot.RepositoryPath == snapshot.WorktreePath ||
		worktreePathWithin(snapshot.RepositoryPath, snapshot.WorktreePath) ||
		worktreePathWithin(snapshot.WorktreePath, snapshot.RepositoryPath) {
		return WorktreeAssignment{}, errors.New("Worktree and source repository paths must be separate")
	}
	if (snapshot.Origin != "" && snapshot.Origin != WorktreeOriginTaxionCreated && snapshot.Origin != WorktreeOriginAdopted) ||
		(snapshot.InitialStateSHA256 != "" && !validSHA256(snapshot.InitialStateSHA256)) ||
		(snapshot.HeadMode != "" && snapshot.HeadMode != WorktreeHeadDetached && snapshot.HeadMode != WorktreeHeadBranchAttached) ||
		(snapshot.HeadMode == WorktreeHeadBranchAttached && strings.TrimSpace(snapshot.HeadRef) == "") ||
		(snapshot.HeadMode == WorktreeHeadDetached && snapshot.HeadRef != "") {
		return WorktreeAssignment{}, errors.New("Worktree provenance or initial Git state identity is invalid")
	}
	if now.IsZero() || job.Revision >= uint64(^uint64(0)>>1) {
		return WorktreeAssignment{}, errors.New("Worktree assignment timestamp or Job revision is invalid")
	}
	now = now.UTC().Round(0)
	return WorktreeAssignment{
		ID: WorktreeID(job.ID, task.ID), JobID: job.ID, TaskID: task.ID,
		RepositoryID: snapshot.RepositoryID, RepositoryPath: snapshot.RepositoryPath,
		WorktreePath: snapshot.WorktreePath, BaselineCommit: snapshot.BaselineCommit,
		Origin: snapshot.Origin, InitialStateSHA256: snapshot.InitialStateSHA256,
		HeadMode: snapshot.HeadMode, HeadRef: snapshot.HeadRef,
		OwnerRoleExecutionID: execution.ID, CreatedAt: now, CreatedRevision: job.Revision + 1,
	}, nil
}

func (a WorktreeAssignment) Snapshot() WorktreeSnapshot {
	return WorktreeSnapshot{
		RepositoryID: a.RepositoryID, RepositoryPath: a.RepositoryPath,
		WorktreePath: a.WorktreePath, BaselineCommit: a.BaselineCommit,
		Origin: a.Origin, InitialStateSHA256: a.InitialStateSHA256,
		HeadMode: a.HeadMode, HeadRef: a.HeadRef,
	}
}

func validateWorktreeAssignments(job Job) error {
	if job.WorktreeLifecycleEnforcedFromRevision > job.Revision {
		return errors.New("Worktree lifecycle activation revision exceeds Job revision")
	}
	if job.WorktreeLifecycleEnforcedFromRevision > 0 {
		activationFound := false
		for _, assignment := range job.WorktreeAssignments {
			if assignment.CreatedRevision == job.WorktreeLifecycleEnforcedFromRevision {
				activationFound = true
				break
			}
		}
		if !activationFound {
			return errors.New("Worktree lifecycle activation revision has no matching assignment")
		}
	} else if len(job.WorktreeAssignments) > 0 {
		return errors.New("Worktree assignments require lifecycle activation metadata")
	}
	byTask := make(map[string]WorktreeAssignment, len(job.WorktreeAssignments))
	byPath := make(map[string]string, len(job.WorktreeAssignments))
	byExecution := make(map[string]struct{}, len(job.WorktreeAssignments))
	for _, assignment := range job.WorktreeAssignments {
		if assignment.ID != WorktreeID(job.ID, assignment.TaskID) ||
			assignment.JobID != job.ID || !validPlanningIdentity(assignment.TaskID) ||
			!validPlanningIdentity(assignment.OwnerRoleExecutionID) ||
			strings.TrimSpace(assignment.RepositoryID) == "" || !validGitCommit(assignment.BaselineCommit) ||
			!filepath.IsAbs(assignment.RepositoryPath) || filepath.Clean(assignment.RepositoryPath) != assignment.RepositoryPath ||
			!filepath.IsAbs(assignment.WorktreePath) || filepath.Clean(assignment.WorktreePath) != assignment.WorktreePath ||
			(assignment.Origin != "" && assignment.Origin != WorktreeOriginTaxionCreated && assignment.Origin != WorktreeOriginAdopted) ||
			(assignment.InitialStateSHA256 != "" && !validSHA256(assignment.InitialStateSHA256)) ||
			(assignment.HeadMode != "" && assignment.HeadMode != WorktreeHeadDetached && assignment.HeadMode != WorktreeHeadBranchAttached) ||
			(assignment.HeadMode == WorktreeHeadBranchAttached && strings.TrimSpace(assignment.HeadRef) == "") ||
			(assignment.HeadMode == WorktreeHeadDetached && assignment.HeadRef != "") ||
			assignment.CreatedAt.IsZero() || assignment.CreatedAt.After(job.UpdatedAt) ||
			assignment.CreatedRevision == 0 || assignment.CreatedRevision > job.Revision {
			return errors.New("Worktree assignment identity, path, baseline, or creation metadata is invalid")
		}
		if assignment.RepositoryPath == assignment.WorktreePath ||
			worktreePathWithin(assignment.RepositoryPath, assignment.WorktreePath) ||
			worktreePathWithin(assignment.WorktreePath, assignment.RepositoryPath) {
			return errors.New("Worktree assignment paths overlap")
		}
		task, found := taskByID(job, assignment.TaskID)
		if !found || len(task.TargetRepositories) != 1 || task.TargetRepositories[0] != assignment.RepositoryID {
			return errors.New("Worktree assignment repository is outside its Task scope")
		}
		var owner RoleExecution
		ownerFound := false
		for _, execution := range job.RoleExecutions {
			if execution.ID == assignment.OwnerRoleExecutionID {
				owner, ownerFound = execution, true
				break
			}
		}
		if !ownerFound || owner.TaskID != assignment.TaskID || owner.WorktreeID != assignment.ID ||
			(owner.Role != RoleCoder && owner.Role != RoleReviewer) ||
			owner.CreatedRevision != assignment.CreatedRevision || !owner.CreatedAt.Equal(assignment.CreatedAt) {
			return errors.New("Worktree assignment owner or creation revision is inconsistent")
		}
		if owner.ExecutionPermissionID != "" {
			permission, found := executionPermissionByID(job, owner.ExecutionPermissionID)
			if !found || (permission.WorktreePath != "" && permission.WorktreePath != assignment.WorktreePath) {
				return errors.New("Worktree assignment path differs from its approved ExecutionPermission")
			}
		}
		if _, exists := byTask[assignment.TaskID]; exists {
			return errors.New("Task has more than one Worktree assignment")
		}
		if previous, exists := byPath[assignment.WorktreePath]; exists && previous != assignment.ID {
			return errors.New("Worktree path is assigned to multiple Tasks")
		}
		if _, exists := byExecution[assignment.OwnerRoleExecutionID]; exists {
			return errors.New("RoleExecution owns multiple Worktree assignments")
		}
		byTask[assignment.TaskID] = assignment
		byPath[assignment.WorktreePath] = assignment.ID
		byExecution[assignment.OwnerRoleExecutionID] = struct{}{}
	}
	for _, execution := range job.RoleExecutions {
		if execution.Role != RoleCoder && execution.Role != RoleReviewer {
			continue
		}
		if execution.WorktreeID == "" && (job.WorktreeLifecycleEnforcedFromRevision == 0 || execution.CreatedRevision < job.WorktreeLifecycleEnforcedFromRevision) {
			continue
		}
		assignment, found := byTask[execution.TaskID]
		if !found || assignment.ID != execution.WorktreeID || assignment.OwnerRoleExecutionID != execution.ID {
			return errors.New("Task RoleExecution has no matching Worktree assignment")
		}
	}
	return nil
}

func validGitCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && value == strings.ToLower(value)
}

func worktreePathWithin(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
