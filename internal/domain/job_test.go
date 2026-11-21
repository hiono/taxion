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
