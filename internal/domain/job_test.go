package domain

import (
	"testing"
	"time"
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
	if err != nil { t.Fatalf("NewDraftJob() error = %v", err) }
	job.Revision = 2
	if err := job.Validate(); err != nil { t.Fatalf("Validate() rejected a later aggregate revision: %v", err) }
}

func TestJobValidateRejectsZeroRevision(t *testing.T) {
	job, err := NewDraftJob("job_zero_revision", "Persisted Job revision.", time.Now().UTC())
	if err != nil { t.Fatalf("NewDraftJob() error = %v", err) }
	job.Revision = 0
	if err := job.Validate(); err == nil { t.Fatal("Validate() accepted revision zero") }
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
