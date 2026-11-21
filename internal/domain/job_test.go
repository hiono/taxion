package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/identity"
)

func TestNewDraftJobPreservesOriginalRequest(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.FixedZone("JST", 9*60*60))
	request := "  Create a tool that helps me organize my ideas.\nKeep the original wording.  "

	job, err := NewDraftJob("job_test", request, now)
	if err != nil {
		t.Fatalf("NewDraftJob() error = %v", err)
	}
	if job.Request != request {
		t.Fatalf("Request = %q, want exact original %q", job.Request, request)
	}
	if job.Goal != "" {
		t.Fatalf("Goal = %q, want unset before planning", job.Goal)
	}
	if job.State != JobDraft {
		t.Fatalf("State = %q, want %q", job.State, JobDraft)
	}
	if job.Revision != 1 {
		t.Fatalf("Revision = %d, want 1", job.Revision)
	}
	if job.CreatedAt.Location() != time.UTC || job.UpdatedAt.Location() != time.UTC {
		t.Fatal("timestamps must be normalized to UTC")
	}
}

func TestJobValidateAcceptsLaterRevision(t *testing.T) {
	job, err := NewDraftJob("job_revision", "Persisted Job revision.", time.Now().UTC())
	if err != nil {
		t.Fatalf("NewDraftJob() error = %v", err)
	}
	job.Revision = 2
	if err := job.Validate(); err != nil {
		t.Fatalf("Validate() rejected a later aggregate revision: %v", err)
	}
}

func TestJobValidateRejectsZeroRevision(t *testing.T) {
	job, err := NewDraftJob("job_zero_revision", "Persisted Job revision.", time.Now().UTC())
	if err != nil {
		t.Fatalf("NewDraftJob() error = %v", err)
	}
	job.Revision = 0
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted revision zero")
	}
}

func TestNewDraftJobRejectsEmptyRequest(t *testing.T) {
	_, err := NewDraftJob("job_test", " \n\t ", time.Now())
	if err == nil {
		t.Fatal("NewDraftJob() accepted an empty request")
	}
}

func TestNewIDUsesPrefixAndUniqueValues(t *testing.T) {
	first, err := NewID("job")
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	second, err := NewID("job")
	if err != nil {
		t.Fatalf("NewID() error = %v", err)
	}
	if first == second {
		t.Fatal("NewID() returned duplicate identifiers")
	}
	if len(first) <= len("job_") || first[:4] != "job_" {
		t.Fatalf("NewID() = %q, want job_ prefix", first)
	}
}

func TestNewDraftJobWithProvenancePreservesOriginalRequest(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	request := "  Create a product from these notes.\nKeep the exact original text.  "
	provenance := IntakeProvenance{
		PrincipalID: identity.PrincipalID("https://identity.example", "subject-123"), Issuer: "https://identity.example",
		Subject: "subject-123", IngressSource: IngressSourceCLI,
		AuthenticatedAt: now.Add(-time.Minute), ReceivedAt: now.Add(-2 * time.Minute),
		RequestIdentity: "cmd_test", SchemaVersion: IntakeProvenanceSchemaVersion,
	}
	job, err := NewDraftJobWithProvenance("job_trusted", request, provenance, now)
	if err != nil {
		t.Fatalf("NewDraftJobWithProvenance() error = %v", err)
	}
	if job.Request != request || job.IntakeProvenance == nil {
		t.Fatalf("Job does not preserve request and provenance: %#v", job)
	}
	if job.IntakeProvenance.OriginalRequestSHA256 != originalRequestDigest(request) {
		t.Fatalf("OriginalRequestSHA256 = %q, want digest of original request", job.IntakeProvenance.OriginalRequestSHA256)
	}
	if err := job.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestJobValidateRejectsInvalidIntakeProvenance(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	base := IntakeProvenance{
		PrincipalID: identity.PrincipalID("https://identity.example", "subject-123"), Issuer: "https://identity.example",
		Subject: "subject-123", IngressSource: IngressSourceCLI,
		AuthenticatedAt: now.Add(-time.Minute), ReceivedAt: now.Add(-2 * time.Minute),
		RequestIdentity: "cmd_test", SchemaVersion: IntakeProvenanceSchemaVersion,
	}
	cases := []struct {
		name   string
		mutate func(*IntakeProvenance)
	}{
		{name: "non-HUMAN principal", mutate: func(p *IntakeProvenance) { p.PrincipalID = "agent_123" }},
		{name: "issuer-subject mismatch", mutate: func(p *IntakeProvenance) { p.PrincipalID = identity.PrincipalID("https://other.example", p.Subject) }},
		{name: "insecure issuer", mutate: func(p *IntakeProvenance) { p.Issuer = "http://identity.example" }},
		{name: "missing subject", mutate: func(p *IntakeProvenance) { p.Subject = "" }},
		{name: "missing ingress source", mutate: func(p *IntakeProvenance) { p.IngressSource = "" }},
		{name: "missing request identity", mutate: func(p *IntakeProvenance) { p.RequestIdentity = "" }},
		{name: "unsupported schema", mutate: func(p *IntakeProvenance) { p.SchemaVersion = 99 }},
		{name: "missing received time", mutate: func(p *IntakeProvenance) { p.ReceivedAt = time.Time{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provenance := base
			tc.mutate(&provenance)
			if _, err := NewDraftJobWithProvenance("job_invalid_provenance", "original request", provenance, now); err == nil {
				t.Fatal("NewDraftJobWithProvenance() accepted invalid provenance")
			}
		})
	}
}

func TestJobValidateRejectsChangedOriginalRequestDigest(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	provenance := IntakeProvenance{
		PrincipalID: identity.PrincipalID("https://identity.example", "subject-123"), Issuer: "https://identity.example",
		Subject: "subject-123", IngressSource: IngressSourceCLI,
		AuthenticatedAt: now.Add(-time.Minute), ReceivedAt: now.Add(-2 * time.Minute),
		RequestIdentity: "cmd_test", SchemaVersion: IntakeProvenanceSchemaVersion,
	}
	job, err := NewDraftJobWithProvenance("job_tampered_provenance", "original request", provenance, now)
	if err != nil {
		t.Fatalf("NewDraftJobWithProvenance() error = %v", err)
	}
	job.IntakeProvenance.OriginalRequestSHA256 = strings.Repeat("0", 64)
	if err := job.Validate(); err == nil {
		t.Fatal("Validate() accepted a mismatched original request digest")
	}
}

func TestJobValidateRejectsInvalidUTF8Request(t *testing.T) {
	request := string([]byte{0xff, 0xfe})
	if _, err := NewDraftJob("job_invalid_utf8", request, time.Now().UTC()); err == nil {
		t.Fatal("NewDraftJob() accepted an invalid UTF-8 request")
	}
}

func newPlanningTestJob(t *testing.T) Job {
	t.Helper()
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	provenance := IntakeProvenance{
		PrincipalID: identity.PrincipalID("https://identity.example", "subject-123"),
		Issuer:      "https://identity.example", Subject: "subject-123",
		IngressSource: IngressSourceCLI, AuthenticatedAt: now.Add(-time.Minute),
		ReceivedAt: now.Add(-2 * time.Minute), RequestIdentity: "cmd_planning_job",
		SchemaVersion: IntakeProvenanceSchemaVersion,
	}
	job, err := NewDraftJobWithProvenance("job_planning", "Plan this request without modifying the repository.", provenance, now)
	if err != nil {
		t.Fatalf("NewDraftJobWithProvenance() error = %v", err)
	}
	return job
}

func newValidPlanningAuthorization(t *testing.T, job Job, issuedAt time.Time) PlanningAuthorization {
	t.Helper()
	request := PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.test",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: PlanningReadOnlyBoundary{
			Mode:      PlanningReadOnlyBoundarySnapshot,
			Reference: "snapshot_1",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:   PlanningBudget{MaxTurns: 8, MaxTokens: 100000},
		Deadline: issuedAt.Add(15 * time.Minute),
	}
	grant, err := NewPlanningAuthorization(job, job.IntakeProvenance.PrincipalID,
		"cmd_11111111111111111111111111111111", request, issuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() error = %v", err)
	}
	return grant
}

func TestPlanningAuthorizationBindsTrustedDraftJob(t *testing.T) {
	job := newPlanningTestJob(t)
	issuedAt := job.CreatedAt.Add(time.Minute)
	grant := newValidPlanningAuthorization(t, job, issuedAt)
	if err := grant.ValidateFor(job, job.IntakeProvenance.PrincipalID, issuedAt); err != nil {
		t.Fatalf("ValidateFor() error = %v", err)
	}
	job.PlanningAuthorizations = []PlanningAuthorization{grant}
	job.Revision = 2
	job.UpdatedAt = issuedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate() error = %v", err)
	}
}

func TestPlanningAuthorizationRejectsScopeAndIdentityChanges(t *testing.T) {
	job := newPlanningTestJob(t)
	issuedAt := job.CreatedAt.Add(time.Minute)
	base := newValidPlanningAuthorization(t, job, issuedAt)
	cases := []struct {
		name   string
		mutate func(*PlanningAuthorization)
	}{
		{name: "job identity", mutate: func(a *PlanningAuthorization) { a.JobID = "job_other" }},
		{name: "request digest", mutate: func(a *PlanningAuthorization) { a.RequestSHA256 = strings.Repeat("0", 64) }},
		{name: "principal", mutate: func(a *PlanningAuthorization) { a.PrincipalID = "human_other" }},
		{name: "provider", mutate: func(a *PlanningAuthorization) { a.ProviderID = "" }},
		{name: "account", mutate: func(a *PlanningAuthorization) { a.AccountID = "" }},
		{name: "configuration digest", mutate: func(a *PlanningAuthorization) { a.ConfigurationSHA256 = "not-a-digest" }},
		{name: "repository identity", mutate: func(a *PlanningAuthorization) { a.RepositoryID = "https://github.com/example/project@main" }},
		{name: "read-only mode", mutate: func(a *PlanningAuthorization) { a.ReadOnlyBoundary.Mode = "repository.read-write" }},
		{name: "read-only reference", mutate: func(a *PlanningAuthorization) { a.ReadOnlyBoundary.Reference = "" }},
		{name: "read-only digest", mutate: func(a *PlanningAuthorization) { a.ReadOnlyBoundary.SHA256 = "not-a-digest" }},
		{name: "additional effect", mutate: func(a *PlanningAuthorization) { a.PermittedEffects = append(a.PermittedEffects, "repository.write") }},
		{name: "empty turn budget", mutate: func(a *PlanningAuthorization) { a.Budget.MaxTurns = 0 }},
		{name: "empty token budget", mutate: func(a *PlanningAuthorization) { a.Budget.MaxTokens = 0 }},
		{name: "command identity", mutate: func(a *PlanningAuthorization) { a.CommandID = "cmd_22222222222222222222222222222222" }},
		{name: "grant identity", mutate: func(a *PlanningAuthorization) { a.ID = "plan_auth_other" }},
		{name: "deadline", mutate: func(a *PlanningAuthorization) { a.Deadline = a.IssuedAt }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			grant := base
			grant.PermittedEffects = append([]string(nil), base.PermittedEffects...)
			tc.mutate(&grant)
			if err := grant.Validate(job); err == nil {
				t.Fatal("Validate() accepted a changed PlanningAuthorization")
			}
		})
	}
}

func TestPlanningAuthorizationRequiresMatchingPrincipalAndActiveDeadline(t *testing.T) {
	job := newPlanningTestJob(t)
	issuedAt := job.CreatedAt.Add(time.Minute)
	grant := newValidPlanningAuthorization(t, job, issuedAt)
	if err := grant.ValidateFor(job, "human_other", issuedAt); err == nil {
		t.Fatal("ValidateFor() accepted a different principal")
	}
	if err := grant.ValidateFor(job, grant.PrincipalID, grant.Deadline); err == nil {
		t.Fatal("ValidateFor() accepted an expired grant")
	}
	if err := grant.ValidateFor(job, grant.PrincipalID, issuedAt.Add(-time.Second)); err == nil {
		t.Fatal("ValidateFor() accepted a grant before its issue time")
	}
	job.PlanningAuthorizations = []PlanningAuthorization{grant}
	job.Revision = 2
	job.UpdatedAt = issuedAt
	if err := job.Validate(); err != nil {
		t.Fatalf("Job.Validate() rejected a structurally valid expired grant: %v", err)
	}
}

func TestPlanningAuthorizationRequiresDraftJob(t *testing.T) {
	job := newPlanningTestJob(t)
	issuedAt := job.CreatedAt.Add(time.Minute)
	grant := newValidPlanningAuthorization(t, job, issuedAt)
	job.State = JobState("ACTIVE")
	if err := grant.ValidateFor(job, grant.PrincipalID, issuedAt); err == nil {
		t.Fatal("ValidateFor() accepted a non-DRAFT Job")
	}
}

func TestJobRejectsDuplicatePlanningAuthorizationIDs(t *testing.T) {
	job := newPlanningTestJob(t)
	issuedAt := job.CreatedAt.Add(time.Minute)
	grant := newValidPlanningAuthorization(t, job, issuedAt)
	job.PlanningAuthorizations = []PlanningAuthorization{grant, grant}
	job.Revision = 3
	job.UpdatedAt = issuedAt
	if err := job.Validate(); err == nil {
		t.Fatal("Job.Validate() accepted duplicate PlanningAuthorization IDs")
	}
}

func TestPlanningAuthorizationRequiresTrustedIntake(t *testing.T) {
	job, err := NewDraftJob("job_local_planning", "Untrusted local request.", time.Now().UTC())
	if err != nil {
		t.Fatalf("NewDraftJob() error = %v", err)
	}
	request := PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.test",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: PlanningReadOnlyBoundary{
			Mode:      PlanningReadOnlyBoundarySnapshot,
			Reference: "snapshot_1",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:   PlanningBudget{MaxTurns: 8, MaxTokens: 100000},
		Deadline: job.CreatedAt.Add(15 * time.Minute),
	}
	if _, err := NewPlanningAuthorization(job, "human_123",
		"cmd_11111111111111111111111111111111", request, job.CreatedAt.Add(time.Minute)); err == nil {
		t.Fatal("NewPlanningAuthorization() accepted a local Job without trusted provenance")
	}
}

func TestPlanningAuthorizationCanReconstructExpiredIdempotentRetry(t *testing.T) {
	job := newPlanningTestJob(t)
	issuedAt := job.CreatedAt.Add(time.Minute)
	request := PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.test",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: PlanningReadOnlyBoundary{
			Mode:      PlanningReadOnlyBoundarySnapshot,
			Reference: "snapshot_1",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:   PlanningBudget{MaxTurns: 8, MaxTokens: 100000},
		Deadline: issuedAt.Add(time.Minute),
	}
	retryAt := request.Deadline.Add(time.Minute)
	grant, err := NewPlanningAuthorization(job, job.IntakeProvenance.PrincipalID,
		"cmd_88888888888888888888888888888888", request, retryAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() retry error = %v", err)
	}
	if !grant.IssuedAt.Before(grant.Deadline) {
		t.Fatalf("reconstructed issue time %s does not precede deadline %s", grant.IssuedAt, grant.Deadline)
	}
	if err := grant.ValidateFor(job, grant.PrincipalID, retryAt); err == nil {
		t.Fatal("ValidateFor() accepted an expired grant")
	}
}
