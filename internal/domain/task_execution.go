package domain

import (
	"errors"
	"fmt"
	"time"
)

type TaskExecutionStart struct {
	RoleExecution       RoleExecution       `json:"role_execution"`
	ProviderStartIntent ProviderStartIntent `json:"provider_start_intent"`
}

func NewTaskRoleExecutionStart(
	job Job,
	taskID, permissionID, commandID, requestSHA256 string,
	now time.Time,
) (TaskExecutionStart, error) {
	if err := job.Validate(); err != nil {
		return TaskExecutionStart{}, fmt.Errorf("validate Job before Task execution start: %w", err)
	}
	if !validCommandID(commandID) || !validPlanningIdentity(taskID) ||
		!validPlanningIdentity(permissionID) || !validSHA256(requestSHA256) || now.IsZero() {
		return TaskExecutionStart{}, errors.New("Task execution start identity, request digest, and timestamp are required")
	}
	if job.State != JobActive || len(job.JobContractRevisions) == 0 {
		return TaskExecutionStart{}, errors.New("Task execution start requires an ACTIVE Job with an approved JobContract")
	}
	latest := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	approval, approved := jobApprovalForRevision(job, latest.Revision)
	if !approved || approval.Validate(job, latest) != nil {
		return TaskExecutionStart{}, errors.New("Task execution start requires the exact latest JobApproval")
	}
	task, taskFound := taskByID(job, taskID)
	if !taskFound || task.ContractRevision != latest.Revision || task.JobApprovalID != approval.ID {
		return TaskExecutionStart{}, errors.New("Task execution start requires a Task in the latest approved contract revision")
	}
	permission, permissionFound := executionPermissionByID(job, permissionID)
	if !permissionFound || permission.TaskID != task.ID ||
		(permission.Role != RoleCoder && permission.Role != RoleReviewer) {
		return TaskExecutionStart{}, errors.New("ExecutionPermission does not authorize this Task RoleExecution")
	}
	now = now.UTC().Round(0)
	if err := permission.ValidateFor(job, permission.OperationID, requestSHA256, now); err != nil {
		return TaskExecutionStart{}, fmt.Errorf("ExecutionPermission is not valid for Task execution start: %w", err)
	}
	for _, existing := range job.RoleExecutions {
		if existing.ExecutionPermissionID == permission.ID {
			return TaskExecutionStart{}, errors.New("ExecutionPermission has already been consumed by a RoleExecution")
		}
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return TaskExecutionStart{}, errors.New("Job revision exhausted")
	}
	createdRevision := job.Revision + 1
	execution := RoleExecution{
		ID: RoleExecutionID(commandID), CommandID: commandID, JobID: job.ID,
		Role: permission.Role, TaskID: task.ID, ExecutionPermissionID: permission.ID,
		ProviderStartOperationID: permission.OperationID,
		CreatedAt:                now, CreatedRevision: createdRevision,
	}
	intent := ProviderStartIntent{
		ID: permission.OperationID, CommandID: commandID, JobID: job.ID,
		RoleExecutionID: execution.ID, TaskID: task.ID, ExecutionPermissionID: permission.ID,
		ProviderID: permission.ProviderID, AccountID: permission.AccountID,
		ConfigurationSHA256: permission.ConfigurationSHA256,
		Budget:              PlanningBudget{MaxTurns: permission.ReservedTurns, MaxTokens: permission.ReservedTokens},
		Deadline:            permission.ExpiresAt, RequestSHA256: permission.RequestSHA256,
		CreatedAt: now, CreatedRevision: createdRevision,
	}
	if err := execution.Validate(job); err != nil {
		return TaskExecutionStart{}, err
	}
	if err := intent.Validate(job, execution); err != nil {
		return TaskExecutionStart{}, err
	}
	return TaskExecutionStart{RoleExecution: execution, ProviderStartIntent: intent}, nil
}
