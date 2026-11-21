package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/identity"
)

func jobWithAcceptedPlanner(t *testing.T) (Job, RoleExecution, time.Time) {
	t.Helper()
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	issuer := "https://identity.example"
	subject := "human-subject"
	principalID := identity.PrincipalID(issuer, subject)
	provenance := IntakeProvenance{
		PrincipalID: principalID, Issuer: issuer, Subject: subject, IngressSource: IngressSourceCLI,
		AuthenticatedAt: now, ReceivedAt: now, RequestIdentity: "request_job_contract_test",
		SchemaVersion: IntakeProvenanceSchemaVersion,
	}
	job, err := NewDraftJobWithProvenance("job_contract_test", "Propose a bounded implementation.", provenance, now)
	if err != nil {
		t.Fatalf("NewDraftJobWithProvenance: %v", err)
	}
	request := PlanningAuthorizationRequest{
		ProviderID: "provider.test", AccountID: "account.primary", ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:     "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: PlanningReadOnlyBoundary{Mode: PlanningReadOnlyBoundarySnapshot, Reference: "snapshot_1", SHA256: strings.Repeat("c", 64)},
		Budget:           PlanningBudget{MaxTurns: 3, MaxTokens: 1000}, Deadline: now.Add(2 * time.Hour),
	}
	grant, err := NewPlanningAuthorization(job, principalID, "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", request, now)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization: %v", err)
	}
	job.PlanningAuthorizations = append(job.PlanningAuthorizations, grant)
	job.Revision++
	job.UpdatedAt = now
	execution, start, err := NewTasklessPlannerStart(
		job, grant.ID, principalID, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", strings.Repeat("d", 64), now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("NewTasklessPlannerStart: %v", err)
	}
	job.RoleExecutions = append(job.RoleExecutions, execution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, start)
	job.Revision++
	job.UpdatedAt = now.Add(time.Minute)
	observation, err := NewProviderStartObservation(
		job, start.ID, principalID, "cmd_cccccccccccccccccccccccccccccccc",
		ProviderStartAccepted, "session_1", "receipt_1", strings.Repeat("e", 64), false, now.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("NewProviderStartObservation: %v", err)
	}
	job.ProviderStartObservations = append(job.ProviderStartObservations, observation)
	job.ProviderLifecycleEnforcedFromRevision = observation.CreatedRevision
	job.Revision++
	job.UpdatedAt = now.Add(2 * time.Minute)
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate fixture: %v", err)
	}
	return job, execution, now
}

func validJobContract(now time.Time) JobContract {
	return JobContract{
		Purpose:            "Deliver the approved feature",
		Constraints:        []string{" no network writes ", "Linux only"},
		TargetRepositories: []string{"repo_taxion"},
		TargetResources:    []string{"resource_ci"},
		Workspaces:         []string{"workspace_taxion"},
		AcceptanceCriteria: []string{"Tests pass", "No secret leakage"},
		RequiredEvidence:   []string{"Linux CI", "Secret scan"},
		Budget:             JobContractBudget{MaxTurns: 3, MaxTokens: 1000},
		ConcurrencyCeiling: 1, Deadline: now.Add(time.Hour),
		ProhibitedEffects:       []string{"production deployment"},
		ApprovalRequiredEffects: []string{"publish release"},
		ConfigurationSHA256:     strings.Repeat("f", 64),
	}
}

func TestCanonicalizeJobContractStableAndSensitive(t *testing.T) {
	contract := validJobContract(time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC))
	first, firstHash, err := CanonicalizeJobContract(contract, JobContractCanonicalizationVersion)
	if err != nil {
		t.Fatalf("CanonicalizeJobContract: %v", err)
	}
	contract.Constraints = []string{"Linux only", "no network writes"}
	contract.AcceptanceCriteria = []string{"No secret leakage", "Tests pass"}
	second, secondHash, err := CanonicalizeJobContract(contract, JobContractCanonicalizationVersion)
	if err != nil {
		t.Fatalf("CanonicalizeJobContract reordered: %v", err)
	}
	if firstHash != secondHash || first.Purpose != second.Purpose {
		t.Fatalf("canonical hash changed for set reordering: %s != %s", firstHash, secondHash)
	}
	contract.Budget.MaxTokens++
	_, changedHash, err := CanonicalizeJobContract(contract, JobContractCanonicalizationVersion)
	if err != nil || changedHash == firstHash {
		t.Fatalf("budget change did not alter canonical hash: %s %v", changedHash, err)
	}
}

func TestCanonicalizeJobContractRejectsIncompleteAndDuplicateFields(t *testing.T) {
	now := time.Now().UTC()
	base := validJobContract(now)
	cases := []struct {
		name   string
		mutate func(*JobContract)
	}{
		{name: "missing purpose", mutate: func(c *JobContract) { c.Purpose = " " }},
		{name: "missing target", mutate: func(c *JobContract) { c.TargetRepositories = nil; c.TargetResources = nil }},
		{name: "missing workspace", mutate: func(c *JobContract) { c.Workspaces = nil }},
		{name: "missing acceptance", mutate: func(c *JobContract) { c.AcceptanceCriteria = nil }},
		{name: "missing evidence", mutate: func(c *JobContract) { c.RequiredEvidence = nil }},
		{name: "zero budget", mutate: func(c *JobContract) { c.Budget.MaxTokens = 0 }},
		{name: "zero concurrency", mutate: func(c *JobContract) { c.ConcurrencyCeiling = 0 }},
		{name: "invalid config digest", mutate: func(c *JobContract) { c.ConfigurationSHA256 = "secret-value" }},
		{name: "duplicate after trim", mutate: func(c *JobContract) { c.Workspaces = []string{"workspace_taxion", " workspace_taxion "} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			contract := base
			tc.mutate(&contract)
			if _, _, err := CanonicalizeJobContract(contract, JobContractCanonicalizationVersion); err == nil {
				t.Fatal("CanonicalizeJobContract accepted invalid contract")
			}
		})
	}
}

func TestJobContractRevisionAndApprovalBindExactImmutableRevision(t *testing.T) {
	job, execution, now := jobWithAcceptedPlanner(t)
	revision, err := NewJobContractRevision(
		job, "cmd_dddddddddddddddddddddddddddddddd", execution.ID,
		validJobContract(now.Add(2*time.Minute)), now.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("NewJobContractRevision: %v", err)
	}
	job.JobContractRevisions = append(job.JobContractRevisions, revision)
	job.Goal = revision.Contract.Purpose
	job.Revision++
	job.UpdatedAt = revision.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with contract revision: %v", err)
	}
	principalID := identity.PrincipalID("https://identity.example", "human-subject")
	approval := JobApproval{
		ID:        JobApprovalID("cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
		CommandID: "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", JobID: job.ID,
		ContractRevision: revision.Revision, CanonicalizationVersion: revision.CanonicalizationVersion,
		CanonicalHash: revision.CanonicalHash, ApproverPrincipalID: principalID,
		ApproverSource: "https://identity.example", ApproverSubject: "human-subject",
		Reason: "Approved the exact bounded scope", ApprovedAt: now.Add(4 * time.Minute), CreatedRevision: job.Revision + 1,
	}
	job.JobApprovals = append(job.JobApprovals, approval)
	job.State = JobActive
	job.Revision++
	job.UpdatedAt = approval.ApprovedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate with exact approval: %v", err)
	}
	newContract := validJobContract(now.Add(5 * time.Minute))
	newContract.Purpose = "Deliver the revised approved feature"
	next, err := NewJobContractRevision(
		job, "cmd_ffffffffffffffffffffffffffffffff", execution.ID, newContract, now.Add(6*time.Minute),
	)
	if err != nil {
		t.Fatalf("NewJobContractRevision after approval: %v", err)
	}
	job.JobContractRevisions = append(job.JobContractRevisions, next)
	job.Goal = next.Contract.Purpose
	job.State = JobDraft
	job.Revision++
	job.UpdatedAt = next.CreatedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate after revision invalidated approval: %v", err)
	}
	if len(job.JobApprovals) != 1 || job.State != JobDraft {
		t.Fatalf("previous approval was not preserved as historical: %#v state=%s", job.JobApprovals, job.State)
	}
}

func TestJobApprovalRejectsMismatchedHumanIdentity(t *testing.T) {
	job, execution, now := jobWithAcceptedPlanner(t)
	revision, err := NewJobContractRevision(
		job, "cmd_12121212121212121212121212121212", execution.ID,
		validJobContract(now.Add(2*time.Minute)), now.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("NewJobContractRevision: %v", err)
	}
	job.JobContractRevisions = append(job.JobContractRevisions, revision)
	job.Goal = revision.Contract.Purpose
	job.Revision++
	job.UpdatedAt = revision.CreatedAt
	approval := JobApproval{
		ID:        JobApprovalID("cmd_13131313131313131313131313131313"),
		CommandID: "cmd_13131313131313131313131313131313", JobID: job.ID,
		ContractRevision: revision.Revision, CanonicalizationVersion: revision.CanonicalizationVersion,
		CanonicalHash: revision.CanonicalHash, ApproverPrincipalID: "agent_123",
		ApproverSource: "https://identity.example", ApproverSubject: "human-subject",
		Reason: "Approved", ApprovedAt: now.Add(4 * time.Minute), CreatedRevision: job.Revision + 1,
	}
	job.Revision++
	job.UpdatedAt = approval.ApprovedAt
	if err := approval.Validate(job, revision); err == nil {
		t.Fatal("JobApproval.Validate accepted an agent Principal")
	}
}
