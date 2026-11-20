package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/domain"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "jobs.sqlite")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = database.Close()
	})
	return database, path
}

func newTestJob(t *testing.T, id, request string) domain.Job {
	t.Helper()
	job, err := domain.NewDraftJob(id, request, time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("NewDraftJob() error = %v", err)
	}
	return job
}

func TestCreateJobPersistsAggregateAndCommandReceipt(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_one", "Build a small useful product from my notes.")

	receipt, err := database.CreateJob(ctx, job, "cmd_one")
	if err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if receipt.JobID != job.ID || receipt.Revision != 1 {
		t.Fatalf("receipt = %#v", receipt)
	}

	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()

	got, err := reopened.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	if got != job {
		t.Fatalf("GetJob() = %#v, want %#v", got, job)
	}
	jobs, err := reopened.ListJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != job.ID {
		t.Fatalf("ListJobs() = %#v, want one Job", jobs)
	}
}

func TestCreateJobIsIdempotentForSameCommandAndPayload(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_idempotent", "Keep this request exactly.")

	first, err := database.CreateJob(ctx, job, "cmd_idempotent")
	if err != nil {
		t.Fatalf("first CreateJob() error = %v", err)
	}
	second, err := database.CreateJob(ctx, job, "cmd_idempotent")
	if err != nil {
		t.Fatalf("retry CreateJob() error = %v", err)
	}
	if first != second {
		t.Fatalf("retry receipt = %#v, want %#v", second, first)
	}
	jobs, err := database.ListJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("stored Jobs = %d, want 1", len(jobs))
	}
}

func TestCreateJobRejectsIdempotencyKeyReuseWithDifferentPayload(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	firstJob := newTestJob(t, "job_first", "First request.")
	secondJob := newTestJob(t, "job_second", "Different request.")

	if _, err := database.CreateJob(ctx, firstJob, "cmd_reused"); err != nil {
		t.Fatalf("first CreateJob() error = %v", err)
	}
	if _, err := database.CreateJob(ctx, secondJob, "cmd_reused"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("second CreateJob() error = %v, want ErrIdempotencyConflict", err)
	}
	got, err := database.GetJob(ctx, firstJob.ID)
	if err != nil || got.Request != firstJob.Request {
		t.Fatalf("original Job changed: got=%#v err=%v", got, err)
	}
	if _, err := database.GetJob(ctx, secondJob.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Job lookup error = %v, want ErrNotFound", err)
	}
}

func TestGetJobDetectsPayloadTampering(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_tamper", "Original request.")
	if _, err := database.CreateJob(ctx, job, "cmd_tamper"); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_aggregates SET payload_json = ? WHERE job_id = ?",
		`{"id":"job_tamper","request":"tampered","state":"DRAFT","revision":1,"created_at":"2026-11-20T12:00:00Z","updated_at":"2026-11-20T12:00:00Z"}`,
		job.ID,
	); err != nil {
		t.Fatalf("tamper fixture: %v", err)
	}
	if _, err := database.GetJob(ctx, job.ID); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("GetJob() error = %v, want ErrIntegrity", err)
	}
}
