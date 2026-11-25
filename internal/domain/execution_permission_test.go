package domain

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/identity"
)

func approvedJobForExecutionPermission(t *testing.T) (Job, time.Time) {
	t.Helper()
	job, execution, result, baseTime := jobWithAcceptedPlanner(t)
	contract := validJobContract(baseTime)
	revision, err := NewJobContractRevision(
		job, "cmd_44444444444444444444444444444444", execution.ID,
		result.ID, result.ContentSHA256, contract, baseTime.Add(6*time.Minute),
	)
	if err != nil {
		t.Fatalf("NewJobContractRevision: %v", err)
	}
	job.JobContractRevisions = append(job.JobContractRevisions, revision)
	job.Goal = revision.Contract.Purpose
	job.Revision++
	job.UpdatedAt = revision.CreatedAt
	issuer, subject := job.IntakeProvenance.Issuer, job.IntakeProvenance.Subject
	approval := JobApproval{
		ID:        JobApprovalID("cmd_55555555555555555555555555555555"),
		CommandID: "cmd_55555555555555555555555555555555",
		JobID:     job.ID, ContractRevision: revision.Revision,
		CanonicalizationVersion: revision.CanonicalizationVersion,
		CanonicalHash:           revision.CanonicalHash,
		ApproverPrincipalID:     identity.PrincipalID(issuer, subject),
		ApproverSource:          issuer, ApproverSubject: subject,
		Reason:     "Approve the bounded implementation",
		ApprovedAt: baseTime.Add(7 * time.Minute), CreatedRevision: job.Revision + 1,
	}
	job.JobApprovals = append(job.JobApprovals, approval)
	job.State = JobActive
	job.Revision++
	job.UpdatedAt = approval.ApprovedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("approved Job fixture validation: %v", err)
	}
	return job, baseTime.Add(8 * time.Minute)
}

func approvedJobWithTaskForExecutionPermission(t *testing.T) (Job, Task, time.Time) {
	t.Helper()
	job, now := approvedJobForExecutionPermission(t)
	request := TaskRequest{
		JobID: job.ID, CommandID: "cmd_81818181818181818181818181818181",
		Title: "Bounded execution task", Purpose: "Provide a task-scoped permission boundary",
		AcceptanceCriteria: []string{"The task-scoped operation is valid"},
	}
	task, err := NewTask(job, request, now)
	if err != nil {
		t.Fatalf("NewTask for ExecutionPermission: %v", err)
	}
	job.Tasks = append(job.Tasks, task)
	job.Revision++
	job.UpdatedAt = task.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job with Task fixture: %v", err)
	}
	return job, task, now.Add(time.Minute)
}

func executionPermissionTestRequest(job Job, now time.Time) ExecutionPermissionRequest {
	revision := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	task := job.Tasks[len(job.Tasks)-1]
	resource := ""
	if len(task.TargetRepositories) > 0 {
		resource = task.TargetRepositories[0]
	} else if len(task.TargetResources) > 0 {
		resource = task.TargetResources[0]
	} else {
		resource = task.Workspaces[0]
	}
	return ExecutionPermissionRequest{
		JobID: job.ID, TaskID: task.ID, CommandID: "cmd_66666666666666666666666666666666",
		Role: "CODER", Effect: "repository.write", Resource: resource,
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: now.Add(20 * time.Minute),
	}
}

func executionPermissionTestAssessment(request ExecutionPermissionRequest, requestSHA string, now time.Time) ExecutionEffectAssessment {
	return ExecutionEffectAssessment{
		Role: request.Role, Effect: request.Effect, Resource: request.Resource,
		ProviderID: request.ProviderID, AccountID: request.AccountID,
		ConfigurationSHA256: request.ConfigurationSHA256, RequestSHA256: requestSHA,
		Contained: true, Mutating: request.Role == "CODER",
		ValidUntil:     request.ExpiresAt.Add(5 * time.Minute),
		EvidenceSHA256: strings.Repeat("e", 64),
	}
}

func TestLegacyTaskRoleExecutionWithoutWorktreeRemainsReadable(t *testing.T) {
	job, task, now := approvedJobWithTaskForExecutionPermission(t)
	request := executionPermissionTestRequest(job, now)
	requestSHA, err := ExecutionPermissionRequestSHA256(request)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	assessment := executionPermissionTestAssessment(request, requestSHA, now)
	permission, err := NewExecutionPermission(job, request, requestSHA, assessment, now)
	if err != nil {
		t.Fatalf("NewExecutionPermission: %v", err)
	}
	job.ExecutionPermissions = append(job.ExecutionPermissions, permission)
	job.Revision++
	job.UpdatedAt = permission.CreatedAt

	legacyRoot := t.TempDir()
	snapshot := WorktreeSnapshot{
		RepositoryID:   task.TargetRepositories[0],
		RepositoryPath: filepath.Join(legacyRoot, "repositories", "project"),
		WorktreePath:   filepath.Join(legacyRoot, "worktrees", "legacy-task"),
		BaselineCommit: strings.Repeat("a", 40),
		Origin:         WorktreeOriginTaxionCreated, InitialStateSHA256: strings.Repeat("b", 64),
		HeadMode:       WorktreeHeadDetached,
	}
	start, err := NewTaskRoleExecutionStart(
		job, task.ID, permission.ID, "cmd_88888888888888888888888888888888",
		requestSHA, snapshot, now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("NewTaskRoleExecutionStart fixture: %v", err)
	}
	start.RoleExecution.WorktreeID = ""
	start.ProviderStartIntent.WorktreeID = ""
	job.RoleExecutions = append(job.RoleExecutions, start.RoleExecution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, start.ProviderStartIntent)
	job.Revision++
	job.UpdatedAt = start.RoleExecution.CreatedAt

	if err := job.Validate(); err != nil {
		t.Fatalf("legacy Task RoleExecution without WorktreeID was not readable: %v", err)
	}
	if job.WorktreeLifecycleEnforcedFromRevision != 0 {
		t.Fatal("reading a legacy RoleExecution unexpectedly enabled Worktree lifecycle enforcement")
	}

	job.WorktreeLifecycleEnforcedFromRevision = start.RoleExecution.CreatedRevision
	if err := job.Validate(); err == nil {
		t.Fatal("Task RoleExecution without WorktreeID passed after Worktree lifecycle enforcement")
	}
}

func TestExecutionPermissionBindsExactApprovedOperation(t *testing.T) {
	job, _, now := approvedJobWithTaskForExecutionPermission(t)
	request := executionPermissionTestRequest(job, now)
	requestSHA, err := ExecutionPermissionRequestSHA256(request)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	assessment := executionPermissionTestAssessment(request, requestSHA, now)
	permission, err := NewExecutionPermission(job, request, requestSHA, assessment, now)
	if err != nil {
		t.Fatalf("NewExecutionPermission: %v", err)
	}
	if permission.JobApprovalID != job.JobApprovals[0].ID ||
		permission.ContractRevision != job.JobContractRevisions[0].Revision ||
		permission.OperationID != ExecutionPermissionOperationID(request.CommandID) ||
		permission.RequestSHA256 != requestSHA {
		t.Fatalf("permission lost exact authority binding: %#v", permission)
	}
	if err := permission.ValidateFor(job, permission.OperationID, requestSHA, now.Add(time.Minute)); err != nil {
		t.Fatalf("ValidateFor exact operation: %v", err)
	}
	if err := permission.ValidateFor(job, "provider_op_foreign", requestSHA, now.Add(time.Minute)); err == nil {
		t.Fatal("permission authorized a different OperationID")
	}
	if err := permission.ValidateFor(job, permission.OperationID, strings.Repeat("0", 64), now.Add(time.Minute)); err == nil {
		t.Fatal("permission authorized a different request digest")
	}
	if err := permission.ValidateFor(job, permission.OperationID, requestSHA, request.ExpiresAt); err == nil {
		t.Fatal("permission remained valid at its expiry")
	}
}

func TestExecutionPermissionRejectsApprovalScopeAndEffectBoundaries(t *testing.T) {
	tests := []struct {
		name             string
		mutateRequest    func(*ExecutionPermissionRequest)
		mutateAssessment func(*ExecutionEffectAssessment)
	}{
		{name: "foreign resource", mutateRequest: func(r *ExecutionPermissionRequest) { r.Resource += "/outside" }},
		{name: "prohibited effect", mutateRequest: func(r *ExecutionPermissionRequest) { r.Effect = "production deployment" }},
		{name: "approval required effect", mutateRequest: func(r *ExecutionPermissionRequest) { r.Effect = "publish release" }},
		{name: "configuration mismatch", mutateRequest: func(r *ExecutionPermissionRequest) { r.ConfigurationSHA256 = strings.Repeat("0", 64) }},
		{name: "assessor approval required", mutateAssessment: func(a *ExecutionEffectAssessment) { a.ApprovalRequired = true }},
		{name: "uncontained and irreversible", mutateAssessment: func(a *ExecutionEffectAssessment) { a.Contained = false; a.Reversible = false }},
		{name: "assessment expired", mutateAssessment: func(a *ExecutionEffectAssessment) { a.ValidUntil = a.ValidUntil.Add(-30 * time.Minute) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job, _, now := approvedJobWithTaskForExecutionPermission(t)
			request := executionPermissionTestRequest(job, now)
			if test.mutateRequest != nil {
				test.mutateRequest(&request)
			}
			requestSHA, err := ExecutionPermissionRequestSHA256(request)
			if err != nil {
				t.Fatalf("request digest: %v", err)
			}
			assessment := executionPermissionTestAssessment(request, requestSHA, now)
			if test.mutateAssessment != nil {
				test.mutateAssessment(&assessment)
			}
			if _, err := NewExecutionPermission(job, request, requestSHA, assessment, now); err == nil {
				t.Fatal("invalid execution permission request was accepted")
			}
			if err := job.Validate(); err != nil {
				t.Fatalf("rejected request mutated Job: %v", err)
			}
		})
	}
}

func TestExecutionPermissionBudgetUsesCumulativeReservations(t *testing.T) {
	job, _, now := approvedJobWithTaskForExecutionPermission(t)
	request := executionPermissionTestRequest(job, now)
	request.ReservedTurns = job.JobContractRevisions[0].Contract.Budget.MaxTurns
	request.ReservedTokens = job.JobContractRevisions[0].Contract.Budget.MaxTokens
	requestSHA, _ := ExecutionPermissionRequestSHA256(request)
	assessment := executionPermissionTestAssessment(request, requestSHA, now)
	permission, err := NewExecutionPermission(job, request, requestSHA, assessment, now)
	if err != nil {
		t.Fatalf("reservation at exact token budget: %v", err)
	}
	job.ExecutionPermissions = append(job.ExecutionPermissions, permission)
	job.Revision++
	job.UpdatedAt = permission.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job with exact budget reservation: %v", err)
	}

	next := request
	next.CommandID = "cmd_77777777777777777777777777777777"
	next.ReservedTurns = 1
	next.ReservedTokens = 1
	nextSHA, _ := ExecutionPermissionRequestSHA256(next)
	nextAssessment := executionPermissionTestAssessment(next, nextSHA, now.Add(time.Minute))
	if _, err := NewExecutionPermission(job, next, nextSHA, nextAssessment, now.Add(time.Minute)); err == nil {
		t.Fatal("permission exceeding cumulative token budget was accepted")
	}
}

func TestExecutionPermissionCannotExpandTaskScopeOrBudget(t *testing.T) {
	job, now := approvedJobForExecutionPermission(t)
	taskRequest := TaskRequest{
		JobID: job.ID, CommandID: "cmd_82828282828282828282828282828282",
		Title: "Narrow Task", Purpose: "Restrict execution to one resource",
		TargetRepositories: []string{}, TargetResources: []string{"resource_ci"},
		Budget:             JobContractBudget{MaxTurns: 1, MaxTokens: 100},
		Deadline:           now.Add(15 * time.Minute),
		AcceptanceCriteria: []string{"Only the selected resource is changed"},
	}
	task, err := NewTask(job, taskRequest, now)
	if err != nil {
		t.Fatalf("NewTask with narrowed execution scope: %v", err)
	}
	job.Tasks = append(job.Tasks, task)
	job.Revision++
	job.UpdatedAt = task.CreatedAt

	request := executionPermissionTestRequest(job, now.Add(time.Minute))
	request.ExpiresAt = task.Deadline.Add(-time.Minute)
	if request.TaskID != task.ID || request.Resource != "resource_ci" {
		t.Fatalf("permission request did not resolve the narrowed Task: %#v", request)
	}
	request.Resource = "repo_taxion"
	requestSHA, _ := ExecutionPermissionRequestSHA256(request)
	assessment := executionPermissionTestAssessment(request, requestSHA, now.Add(time.Minute))
	if _, err := NewExecutionPermission(job, request, requestSHA, assessment, now.Add(time.Minute)); err == nil {
		t.Fatal("Job-approved resource outside Task scope was accepted")
	}

	request.Resource = "resource_ci"
	request.ReservedTurns = 2
	request.ReservedTokens = 101
	requestSHA, _ = ExecutionPermissionRequestSHA256(request)
	assessment = executionPermissionTestAssessment(request, requestSHA, now.Add(time.Minute))
	if _, err := NewExecutionPermission(job, request, requestSHA, assessment, now.Add(time.Minute)); err == nil {
		t.Fatal("permission exceeding Task budget was accepted")
	}
	request.ReservedTurns = 1
	request.ReservedTokens = 100
	request.ExpiresAt = task.Deadline.Add(time.Minute)
	requestSHA, _ = ExecutionPermissionRequestSHA256(request)
	assessment = executionPermissionTestAssessment(request, requestSHA, now.Add(time.Minute))
	if _, err := NewExecutionPermission(job, request, requestSHA, assessment, now.Add(time.Minute)); err == nil {
		t.Fatal("permission exceeding Task deadline was accepted")
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("rejected Task-bound permission mutated Job: %v", err)
	}
}

func TestExecutionPermissionRejectsMutatingReviewerAndAllowsContainedUnknownEffect(t *testing.T) {
	job, _, now := approvedJobWithTaskForExecutionPermission(t)
	request := executionPermissionTestRequest(job, now)
	request.Effect = "vendor.unknown.operation"
	requestSHA, _ := ExecutionPermissionRequestSHA256(request)
	assessment := executionPermissionTestAssessment(request, requestSHA, now)
	if _, err := NewExecutionPermission(job, request, requestSHA, assessment, now); err != nil {
		t.Fatalf("contained unknown effect should be assessed rather than rejected by name: %v", err)
	}

	reviewer := request
	reviewer.CommandID = "cmd_88888888888888888888888888888888"
	reviewer.Role = "REVIEWER"
	reviewerSHA, _ := ExecutionPermissionRequestSHA256(reviewer)
	reviewerAssessment := executionPermissionTestAssessment(reviewer, reviewerSHA, now)
	reviewerAssessment.Mutating = true
	if _, err := NewExecutionPermission(job, reviewer, reviewerSHA, reviewerAssessment, now); err == nil {
		t.Fatal("mutating Reviewer effect was accepted")
	}
}

func TestExecutionPermissionRejectsUnapprovedAdoptedWorktreePath(t *testing.T) {
	job, _, now := approvedJobWithTaskForExecutionPermission(t)
	request := executionPermissionTestRequest(job, now)
	request.WorktreePath = filepath.Join(t.TempDir(), "external-worktree")
	requestSHA, err := ExecutionPermissionRequestSHA256(request)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	assessment := executionPermissionTestAssessment(request, requestSHA, now)
	if _, err := NewExecutionPermission(job, request, requestSHA, assessment, now); err == nil {
		t.Fatal("Worktree path outside the exact approved Task resource was accepted")
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("rejected adopted Worktree path mutated Job: %v", err)
	}
}

func TestExecutionPermissionBindsAdoptedWorktreeToApprovedTaskResource(t *testing.T) {
	worktreePath := filepath.Join(t.TempDir(), "taxion-approved-existing-worktree")
	job, execution, result, baseTime := jobWithAcceptedPlanner(t)
	contract := validJobContract(baseTime)
	contract.TargetResources = []string{worktreePath}
	revision, err := NewJobContractRevision(
		job, "cmd_44444444444444444444444444444444", execution.ID,
		result.ID, result.ContentSHA256, contract, baseTime.Add(6*time.Minute),
	)
	if err != nil {
		t.Fatalf("NewJobContractRevision: %v", err)
	}
	job.JobContractRevisions = append(job.JobContractRevisions, revision)
	job.Goal = revision.Contract.Purpose
	job.Revision++
	job.UpdatedAt = revision.CreatedAt
	issuer, subject := job.IntakeProvenance.Issuer, job.IntakeProvenance.Subject
	approval := JobApproval{
		ID:                      JobApprovalID("cmd_55555555555555555555555555555555"),
		CommandID:               "cmd_55555555555555555555555555555555",
		JobID:                   job.ID,
		ContractRevision:        revision.Revision,
		CanonicalizationVersion: revision.CanonicalizationVersion,
		CanonicalHash:           revision.CanonicalHash,
		ApproverPrincipalID:     identity.PrincipalID(issuer, subject),
		ApproverSource:          issuer,
		ApproverSubject:         subject,
		Reason:                  "Approve the existing Worktree resource",
		ApprovedAt:              baseTime.Add(7 * time.Minute),
		CreatedRevision:         job.Revision + 1,
	}
	job.JobApprovals = append(job.JobApprovals, approval)
	job.State = JobActive
	job.Revision++
	job.UpdatedAt = approval.ApprovedAt

	task, err := NewTask(job, TaskRequest{
		JobID: job.ID, CommandID: "cmd_81818181818181818181818181818181",
		Title: "Adopt existing Worktree", Purpose: "Continue the approved working state",
		TargetRepositories: []string{"repo_taxion"}, TargetResources: []string{worktreePath},
		AcceptanceCriteria: []string{"The existing Worktree is preserved"},
	}, baseTime.Add(8*time.Minute))
	if err != nil {
		t.Fatalf("NewTask: %v", err)
	}
	job.Tasks = append(job.Tasks, task)
	job.Revision++
	job.UpdatedAt = task.CreatedAt

	request := ExecutionPermissionRequest{
		JobID: job.ID, TaskID: task.ID, CommandID: "cmd_66666666666666666666666666666666",
		Role: "CODER", Effect: "repository.write", Resource: worktreePath,
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns:       1, ReservedTokens: 100, ExpiresAt: task.CreatedAt.Add(20 * time.Minute),
		WorktreePath: worktreePath,
	}
	requestSHA, err := ExecutionPermissionRequestSHA256(request)
	if err != nil {
		t.Fatalf("ExecutionPermissionRequestSHA256: %v", err)
	}
	assessment := executionPermissionTestAssessment(request, requestSHA, task.CreatedAt)
	permission, err := NewExecutionPermission(job, request, requestSHA, assessment, task.CreatedAt)
	if err != nil {
		t.Fatalf("approved adopted Worktree path was rejected: %v", err)
	}
	if permission.WorktreePath != worktreePath {
		t.Fatalf("ExecutionPermission WorktreePath = %q, want %q", permission.WorktreePath, worktreePath)
	}
	job.ExecutionPermissions = append(job.ExecutionPermissions, permission)
	job.Revision++
	job.UpdatedAt = permission.CreatedAt
	tampered := permission
	tampered.WorktreePath = worktreePath + "/tampered"
	if err := tampered.Validate(job); err == nil {
		t.Fatal("ExecutionPermission accepted a Worktree path different from its immutable request digest")
	}
	changed := request
	changed.WorktreePath = worktreePath + "/../other"
	changedSHA, err := ExecutionPermissionRequestSHA256(changed)
	if err != nil {
		t.Fatalf("changed request digest: %v", err)
	}
	changedAssessment := executionPermissionTestAssessment(changed, changedSHA, task.CreatedAt)
	if _, err := NewExecutionPermission(job, changed, changedSHA, changedAssessment, task.CreatedAt); err == nil {
		t.Fatal("Worktree path outside the exact approved resource was accepted")
	}

	snapshot := WorktreeSnapshot{
		RepositoryID: task.TargetRepositories[0], RepositoryPath: filepath.Join(filepath.Dir(worktreePath), "taxion-source"),
		WorktreePath: worktreePath + "-mismatch", BaselineCommit: strings.Repeat("a", 40),
		Origin: WorktreeOriginAdopted, InitialStateSHA256: strings.Repeat("b", 64),
		HeadMode: WorktreeHeadDetached,
	}
	commandID := "cmd_d1010101010101010101010101010101"
	if _, err := NewTaskRoleExecutionStart(
		job, task.ID, permission.ID, commandID, requestSHA, snapshot, task.CreatedAt.Add(time.Minute),
	); err == nil {
		t.Fatal("Task execution accepted a Worktree path outside the approved ExecutionPermission")
	}
	snapshot.WorktreePath = worktreePath
	if _, err := NewTaskRoleExecutionStart(
		job, task.ID, permission.ID, commandID, requestSHA, snapshot, task.CreatedAt.Add(time.Minute),
	); err != nil {
		t.Fatalf("Task execution rejected the approved Worktree path: %v", err)
	}
}
