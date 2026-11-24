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
	snapshot domain.WorktreeSnapshot,
) (CommandReceipt, domain.TaskExecutionStart, error) {
	request := struct {
		JobID        string                  `json:"job_id"`
		TaskID       string                  `json:"task_id"`
		PermissionID string                  `json:"permission_id"`
		CommandID    string                  `json:"command_id"`
		RequestSHA   string                  `json:"request_sha256"`
		Worktree     domain.WorktreeSnapshot `json:"worktree_snapshot"`
	}{
		JobID: jobID, TaskID: taskID, PermissionID: permissionID,
		CommandID: commandID, RequestSHA: requestSHA256, Worktree: snapshot,
	}
	receipt, start, err := persistPlanningFact(
		ctx, s, jobID, expectedRevision, commandID, "TASK_EXECUTION_START", request,
		func(job domain.Job, now time.Time) (domain.TaskExecutionStart, error) {
			return domain.NewTaskRoleExecutionStart(job, taskID, permissionID, commandID, requestSHA256, snapshot, now)
		},
		func(job *domain.Job, start domain.TaskExecutionStart) {
			job.WorktreeAssignments = append(job.WorktreeAssignments, start.WorktreeAssignment)
			job.RoleExecutions = append(job.RoleExecutions, start.RoleExecution)
			job.ProviderStartIntents = append(job.ProviderStartIntents, start.ProviderStartIntent)
		},
		func(job domain.Job) (domain.TaskExecutionStart, bool) {
			for _, execution := range job.RoleExecutions {
				if execution.CommandID != commandID {
					continue
				}
				for _, intent := range job.ProviderStartIntents {
					if intent.RoleExecutionID != execution.ID {
						continue
					}
					for _, assignment := range job.WorktreeAssignments {
						if assignment.ID == execution.WorktreeID {
							return domain.TaskExecutionStart{
								RoleExecution: execution, ProviderStartIntent: intent,
								WorktreeAssignment: assignment,
							}, true
						}
					}
				}
			}
			return domain.TaskExecutionStart{}, false
		},
	)

	return receipt, start, err
}
