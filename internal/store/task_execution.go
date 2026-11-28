package store

import (
	"context"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func (s *Store) StartTaskExecution(
	ctx context.Context,
	expectedRevision uint64,
	jobID, taskID, permissionID, commandID, requestSHA256 string,
) (CommandReceipt, domain.TaskExecutionStart, error) {
	request := struct {
		JobID        string `json:"job_id"`
		TaskID       string `json:"task_id"`
		PermissionID string `json:"permission_id"`
		CommandID    string `json:"command_id"`
		RequestSHA   string `json:"request_sha256"`
	}{
		JobID: jobID, TaskID: taskID, PermissionID: permissionID,
		CommandID: commandID, RequestSHA: requestSHA256,
	}
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, commandID, "TASK_EXECUTION_START", request,
		func(job domain.Job, now time.Time) (domain.TaskExecutionStart, error) {
			return domain.NewTaskRoleExecutionStart(job, taskID, permissionID, commandID, requestSHA256, now)
		},
		func(job *domain.Job, start domain.TaskExecutionStart) {
			job.RoleExecutions = append(job.RoleExecutions, start.RoleExecution)
			job.ProviderStartIntents = append(job.ProviderStartIntents, start.ProviderStartIntent)
			if job.ProviderLifecycleEnforcedFromRevision == 0 {
				job.ProviderLifecycleEnforcedFromRevision = start.ProviderStartIntent.CreatedRevision
			}
		},
		func(job domain.Job) (domain.TaskExecutionStart, bool) {
			for _, execution := range job.RoleExecutions {
				if execution.CommandID != commandID {
					continue
				}
				for _, intent := range job.ProviderStartIntents {
					if intent.RoleExecutionID == execution.ID {
						return domain.TaskExecutionStart{
							RoleExecution: execution, ProviderStartIntent: intent,
						}, true
					}
				}
			}
			return domain.TaskExecutionStart{}, false
		},
	)
}

func (s *Store) BindRoleExecutionWorktree(
	ctx context.Context,
	expectedRevision uint64,
	jobID, roleExecutionID, commandID string,
	assignment domain.WorktreeAssignment,
) (CommandReceipt, domain.WorktreeAssignment, error) {
	request := struct {
		JobID           string                    `json:"job_id"`
		RoleExecutionID string                    `json:"role_execution_id"`
		Assignment      domain.WorktreeAssignment `json:"assignment"`
	}{
		JobID: jobID, RoleExecutionID: roleExecutionID, Assignment: assignment,
	}
	return persistPlanningFact(
		ctx, s, jobID, expectedRevision, commandID, "ROLE_EXECUTION_WORKTREE_BIND", request,
		func(job domain.Job, now time.Time) (domain.WorktreeAssignment, error) {
			return domain.NewWorktreeAssignmentBinding(job, roleExecutionID, assignment, now)
		},
		func(job *domain.Job, bound domain.WorktreeAssignment) {
			for i := range job.RoleExecutions {
				if job.RoleExecutions[i].ID == roleExecutionID {
					job.RoleExecutions[i].WorktreeAssignmentID = bound.ID
					job.RoleExecutions[i].WorktreePath = bound.WorktreePath
				}
			}
			for i := range job.ProviderStartIntents {
				if job.ProviderStartIntents[i].RoleExecutionID == roleExecutionID {
					job.ProviderStartIntents[i].WorktreeAssignmentID = bound.ID
					job.ProviderStartIntents[i].WorktreePath = bound.WorktreePath
				}
			}
			job.WorktreeAssignments = append(job.WorktreeAssignments, bound)
		},
		func(job domain.Job) (domain.WorktreeAssignment, bool) {
			for _, existing := range job.WorktreeAssignments {
				if existing.OwnerRoleExecutionID == roleExecutionID {
					return existing, true
				}
			}
			return domain.WorktreeAssignment{}, false
		},
	)
}
