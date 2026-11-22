package domain

import (
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
		ID: JobApprovalID("cmd_55555555555555555555555555555555"),
		CommandID: "cmd_55555555555555555555555555555555",
		JobID: job.ID, ContractRevision: revision.Revision,
		CanonicalizationVersion: revision.CanonicalizationVersion,
		CanonicalHash: revision.CanonicalHash,
		ApproverPrincipalID: identity.PrincipalID(issuer, subject),
		ApproverSource: issuer, ApproverSubject: subject,
		Reason: "Approve the bounded implementation",
		ApprovedAt: baseTime.Add(7*time.Minute), CreatedRevision: job.Revision + 1,
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

func executionPermissionTestRequest(job Job, now time.Time) ExecutionPermissionRequest {
	revision := job.JobContractRevisions[len(job.JobContractRevisions)-1]
	return ExecutionPermissionRequest{
		JobID: job.ID, CommandID: "cmd_66666666666666666666666666666666",
		Role: "CODER", Effect: "repository.write", Resource: revision.Contract.TargetRepositories[0],
		ProviderID: "provider.test", AccountID: "account.primary",
		ConfigurationSHA256: revision.Contract.ConfigurationSHA256,
		ReservedTurns: 1, ReservedTokens: 100, ExpiresAt: now.Add(20 * time.Minute),
	}
}

func executionPermissionTestAssessment(request ExecutionPermissionRequest, requestSHA string, now time.Time) ExecutionEffectAssessment {
	return ExecutionEffectAssessment{
		Role: request.Role, Effect: request.Effect, Resource: request.Resource,
		ProviderID: request.ProviderID, AccountID: request.AccountID,
		ConfigurationSHA256: request.ConfigurationSHA256, RequestSHA256: requestSHA,
		Contained: true, Mutating: request.Role == "CODER",
		ValidUntil: request.ExpiresAt.Add(5 * time.Minute),
		EvidenceSHA256: strings.Repeat("e", 64),
	}
}

func TestExecutionPermissionBindsExactApprovedOperation(t *testing.T) {
	job, now := approvedJobForExecutionPermission(t)
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
		name                string
		mutateRequest       func(*ExecutionPermissionRequest)
		mutateAssessment   func(*ExecutionEffectAssessment)
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
			job, now := approvedJobForExecutionPermission(t)
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
	job, now := approvedJobForExecutionPermission(t)
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

func TestExecutionPermissionRejectsMutatingReviewerAndAllowsContainedUnknownEffect(t *testing.T) {
	job, now := approvedJobForExecutionPermission(t)
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
