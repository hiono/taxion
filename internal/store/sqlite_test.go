package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/coreos/go-oidc/v3/oidc/oidctest"
	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	"github.com/hiono/taxion/internal/identity"
	"golang.org/x/oauth2"
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

func TestOpenUpgradesOlderSchemaVersionAndPreservesJob(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_schema_upgrade", "Preserve the aggregate during schema upgrade.")
	if _, err := database.createJob(ctx, job, "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", createLocalDraftJobCommandType); err != nil {
		t.Fatalf("create Job before schema upgrade: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, "DROP TRIGGER IF EXISTS job_aggregates_preserve_job_messages"); err != nil {
		t.Fatalf("remove trigger to simulate the prior schema: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, "PRAGMA user_version = 1"); err != nil {
		t.Fatalf("set prior schema version: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close prior database: %v", err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() did not upgrade the supported prior schema: %v", err)
	}
	defer reopened.Close()
	var version int
	if err := reopened.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read upgraded schema version: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("schema version = %d, want %d", version, schemaVersion)
	}
	got, err := reopened.GetJob(ctx, job.ID)
	if err != nil || !reflect.DeepEqual(got, job) {
		t.Fatalf("GetJob after schema upgrade = %#v, %v; want %#v", got, err, job)
	}
}

func TestPlanningFactTransactionAcquiresWriterLockBeforeReading(t *testing.T) {
	ctx := context.Background()
	first, path := newTestStore(t)
	second, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("open second Store: %v", err)
	}
	defer second.Close()

	if _, err := second.db.ExecContext(ctx, "PRAGMA busy_timeout = 0"); err != nil {
		t.Fatalf("disable second writer busy timeout: %v", err)
	}
	firstTransaction, err := beginImmediateSQLiteTransaction(ctx, first.db)
	if err != nil {
		t.Fatalf("begin first immediate transaction: %v", err)
	}
	if _, err := beginImmediateSQLiteTransaction(ctx, second.db); err == nil ||
		(!strings.Contains(strings.ToLower(err.Error()), "locked") && !strings.Contains(strings.ToLower(err.Error()), "busy")) {
		_ = firstTransaction.Rollback()
		t.Fatalf("second writer error = %v, want SQLITE_BUSY while first transaction is active", err)
	}
	if err := firstTransaction.Commit(); err != nil {
		t.Fatalf("commit first immediate transaction: %v", err)
	}
	secondTransaction, err := beginImmediateSQLiteTransaction(ctx, second.db)
	if err != nil {
		t.Fatalf("second writer did not proceed after the first transaction committed: %v", err)
	}
	if err := secondTransaction.Rollback(); err != nil {
		t.Fatalf("rollback second immediate transaction: %v", err)
	}
}


func TestOpenCurrentSchemaDoesNotRequireSQLiteWriterLock(t *testing.T) {
	first, path := newTestStore(t)
	ctx := context.Background()
	writer, err := first.db.Conn(ctx)
	if err != nil {
		t.Fatalf("acquire writer connection: %v", err)
	}
	defer writer.Close()
	if _, err := writer.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatalf("hold writer lock: %v", err)
	}
	defer func() { _, _ = writer.ExecContext(context.Background(), "ROLLBACK") }()
	openContext, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	reopened, err := Open(openContext, path)
	if err != nil {
		t.Fatalf("Open() blocked on a writer lock for a complete schema: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("close reopened Store: %v", err)
	}
}

func TestPreUpgradeProcessCannotOverwriteV2AggregateFacts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-writer.sqlite")
	legacyDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy process database: %v", err)
	}
	legacyDB.SetMaxOpenConns(1)
	defer legacyDB.Close()
	legacyConn, err := legacyDB.Conn(ctx)
	if err != nil {
		t.Fatalf("pin legacy process connection: %v", err)
	}
	defer legacyConn.Close()

	for _, statement := range []string{
		`PRAGMA journal_mode = WAL`,
		`CREATE TABLE job_aggregates (
			job_id TEXT PRIMARY KEY,
			revision INTEGER NOT NULL CHECK (revision > 0),
			payload_json TEXT NOT NULL,
			integrity_sha256 TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`PRAGMA user_version = 1`,
	} {
		if _, err := legacyConn.ExecContext(ctx, statement); err != nil {
			t.Fatalf("initialize v1 database: %v", err)
		}
	}

	job := newTestJob(t, "job_legacy_writer", "Preserve v2 aggregate facts across a live schema upgrade.")
	legacyPayload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal v1 Job: %v", err)
	}
	createdAt := job.CreatedAt.UTC().Format(time.RFC3339Nano)
	if _, err := legacyConn.ExecContext(ctx,
		`INSERT INTO job_aggregates
			(job_id, revision, payload_json, integrity_sha256, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
		job.ID, job.Revision, string(legacyPayload), digest(legacyPayload), createdAt,
		job.UpdatedAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		t.Fatalf("insert v1 Job aggregate: %v", err)
	}

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade database while legacy connection remains open: %v", err)
	}
	defer upgraded.Close()

	var version int
	if err := legacyConn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		t.Fatalf("read schema version from legacy connection: %v", err)
	}
	if version != schemaVersion {
		t.Fatalf("legacy connection schema version = %d, want %d", version, schemaVersion)
	}

	var aggregate map[string]json.RawMessage
	if err := json.Unmarshal(legacyPayload, &aggregate); err != nil {
		t.Fatalf("decode v1 aggregate payload: %v", err)
	}
	aggregate["job_messages"] = json.RawMessage(`[{"id":"message_1"}]`)
	v2Payload, err := json.Marshal(aggregate)
	if err != nil {
		t.Fatalf("marshal v2 aggregate payload: %v", err)
	}
	v2UpdatedAt := job.UpdatedAt.Add(time.Minute).UTC()
	if _, err := upgraded.db.ExecContext(ctx,
		`UPDATE job_aggregates
			SET revision = ?, payload_json = ?, integrity_sha256 = ?, updated_at = ?
			WHERE job_id = ? AND revision = ?`,
		2, string(v2Payload), digest(v2Payload), v2UpdatedAt.Format(time.RFC3339Nano), job.ID, 1,
	); err != nil {
		t.Fatalf("v2 writer appending the first facts: %v", err)
	}

	legacyUpdatedAt := v2UpdatedAt.Add(time.Minute)
	_, err = legacyConn.ExecContext(ctx,
		`UPDATE job_aggregates
			SET revision = ?, payload_json = ?, integrity_sha256 = ?, updated_at = ?
			WHERE job_id = ? AND revision = ?`,
		3, string(legacyPayload), digest(legacyPayload), legacyUpdatedAt.Format(time.RFC3339Nano), job.ID, 2,
	)
	if err == nil || !strings.Contains(err.Error(), "append-only JobMessages") {
		t.Fatalf("legacy v1 writer update error = %v, want JobMessage append-only guard rejection", err)
	}

	var storedRevision int64
	var storedPayload, storedHash string
	if err := upgraded.db.QueryRowContext(ctx,
		"SELECT revision, payload_json, integrity_sha256 FROM job_aggregates WHERE job_id = ?",
		job.ID,
	).Scan(&storedRevision, &storedPayload, &storedHash); err != nil {
		t.Fatalf("read aggregate after rejected legacy update: %v", err)
	}
	if storedRevision != 2 || storedPayload != string(v2Payload) || storedHash != digest(v2Payload) {
		t.Fatalf("legacy update changed v2 aggregate: revision=%d payload=%s hash=%s",
			storedRevision, storedPayload, storedHash)
	}

	mutatedAggregate := make(map[string]json.RawMessage, len(aggregate))
	for field, value := range aggregate {
		mutatedAggregate[field] = append(json.RawMessage(nil), value...)
	}
	var messages []map[string]string
	if err := json.Unmarshal(mutatedAggregate["job_messages"], &messages); err != nil {
		t.Fatalf("decode existing JobMessages: %v", err)
	}
	messages[0]["id"] = "message_mutated"
	mutatedAggregate["job_messages"], err = json.Marshal(messages)
	if err != nil {
		t.Fatalf("mutate existing JobMessage fixture: %v", err)
	}
	mutatedPayload, err := json.Marshal(mutatedAggregate)
	if err != nil {
		t.Fatalf("marshal mutated aggregate: %v", err)
	}
	_, err = upgraded.db.ExecContext(ctx,
		`UPDATE job_aggregates
			SET revision = ?, payload_json = ?, integrity_sha256 = ?, updated_at = ?
			WHERE job_id = ? AND revision = ?`,
		3, string(mutatedPayload), digest(mutatedPayload),
		v2UpdatedAt.Add(time.Minute).Format(time.RFC3339Nano), job.ID, 2,
	)
	if err == nil || !strings.Contains(err.Error(), "append-only JobMessages") {
		t.Fatalf("writer mutation of an existing v2 fact error = %v, want JobMessage append-only guard rejection", err)
	}

	var existingMessages []json.RawMessage
	if err := json.Unmarshal(aggregate["job_messages"], &existingMessages); err != nil {
		t.Fatalf("decode existing JobMessages: %v", err)
	}
	existingMessages = append(existingMessages, json.RawMessage(`{"id":"message_2"}`))
	aggregate["job_messages"], err = json.Marshal(existingMessages)
	if err != nil {
		t.Fatalf("append JobMessage: %v", err)
	}
	appendedPayload, err := json.Marshal(aggregate)
	if err != nil {
		t.Fatalf("marshal appended v2 aggregate: %v", err)
	}
	if _, err := upgraded.db.ExecContext(ctx,
		`UPDATE job_aggregates
			SET revision = ?, payload_json = ?, integrity_sha256 = ?, updated_at = ?
			WHERE job_id = ? AND revision = ?`,
		3, string(appendedPayload), digest(appendedPayload),
		v2UpdatedAt.Add(2*time.Minute).Format(time.RFC3339Nano), job.ID, 2,
	); err != nil {
		t.Fatalf("v2 writer appending facts: %v", err)
	}
}

func TestAggregateUpdateCannotRemoveAppendOnlyFacts(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_append_only_trigger", "Preserve append-only facts against older writers.")
	if _, err := database.createJob(ctx, job, "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", createLocalDraftJobCommandType); err != nil {
		t.Fatalf("create Job: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, "DROP TRIGGER IF EXISTS job_aggregates_preserve_job_messages"); err != nil {
		t.Fatalf("remove trigger to simulate a version 2 database: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, "PRAGMA user_version = 2"); err != nil {
		t.Fatalf("set prior schema version: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close version 2 database: %v", err)
	}

	oldWriter, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open pre-upgrade writer: %v", err)
	}
	oldWriter.SetMaxOpenConns(1)
	oldWriter.SetMaxIdleConns(1)
	defer oldWriter.Close()
	if err := oldWriter.PingContext(ctx); err != nil {
		t.Fatalf("connect pre-upgrade writer: %v", err)
	}
	oldWriteStatement, err := oldWriter.PrepareContext(ctx, `UPDATE job_aggregates
		SET payload_json = json_remove(
			payload_json,
			'$.job_messages'
		)
		WHERE job_id = ?`)
	if err != nil {
		t.Fatalf("prepare pre-upgrade writer statement: %v", err)
	}
	defer oldWriteStatement.Close()

	upgraded, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("upgrade database while old writer remains open: %v", err)
	}
	defer upgraded.Close()
	if _, err := upgraded.db.ExecContext(ctx, `UPDATE job_aggregates
		SET payload_json = json_set(
			payload_json,
			'$.job_messages', json('[{"id":"message-1"}]')
		)
		WHERE job_id = ?`, job.ID); err != nil {
		t.Fatalf("seed append-only facts after upgrade: %v", err)
	}
	if _, err := upgraded.db.ExecContext(ctx, `UPDATE job_aggregates
		SET payload_json = json_set(
			payload_json,
			'$.job_messages', json('[{"id":"message-1"},{"id":"message-2"}]')
		)
		WHERE job_id = ?`, job.ID); err != nil {
		t.Fatalf("append facts while preserving existing entries: %v", err)
	}
	if _, err := upgraded.db.ExecContext(ctx, `UPDATE job_aggregates
		SET payload_json = json_set(
			payload_json,
			'$.job_messages', json('{"0":{"id":"message-1"}}')
		)
		WHERE job_id = ?`, job.ID); err == nil || !strings.Contains(err.Error(), "append-only JobMessages") {
		t.Fatalf("non-array replacement error = %v, want append-only JobMessage guard rejection", err)
	}
	if _, err := oldWriteStatement.ExecContext(ctx, job.ID); err == nil ||
		!strings.Contains(err.Error(), "append-only JobMessages") {
		t.Fatalf("pre-upgrade prepared writer error = %v, want append-only fact trigger rejection", err)
	}
	if _, err := oldWriter.ExecContext(ctx, `UPDATE job_aggregates
		SET payload_json = json_remove(
			payload_json,
			'$.job_messages'
		)
		WHERE job_id = ?`, job.ID); err == nil ||
		!strings.Contains(err.Error(), "append-only JobMessages") {
		t.Fatalf("pre-upgrade writer after statement reprepare error = %v, want append-only fact trigger rejection", err)
	}
	var payload string
	if err := upgraded.db.QueryRowContext(ctx, "SELECT payload_json FROM job_aggregates WHERE job_id = ?", job.ID).Scan(&payload); err != nil {
		t.Fatalf("read aggregate payload after rejected downgrade: %v", err)
	}
	for _, factID := range []string{"message-1", "message-2"} {
		if !strings.Contains(payload, factID) {
			t.Fatalf("aggregate payload lost append-only fact %q: %s", factID, payload)
		}
	}
}

func TestOpenRejectsFutureSchemaVersionWithoutChangingJournalMode(t *testing.T) {
	database, path := newTestStore(t)
	if _, err := database.db.ExecContext(context.Background(), "PRAGMA journal_mode = DELETE"); err != nil {
		t.Fatalf("set DELETE journal mode: %v", err)
	}
	if _, err := database.db.ExecContext(context.Background(), "PRAGMA user_version = 99"); err != nil {
		t.Fatalf("set future schema version: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close future-version database: %v", err)
	}
	if _, err := Open(context.Background(), path); !errors.Is(err, ErrUnsupportedSchema) {
		t.Fatalf("Open() error = %v, want ErrUnsupportedSchema", err)
	}
	check, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("reopen future-version database for journal-mode check: %v", err)
	}
	defer check.Close()
	var journalMode string
	if err := check.QueryRowContext(context.Background(), "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode after rejected Open: %v", err)
	}
	if journalMode != "delete" {
		t.Fatalf("journal mode after rejected Open = %q, want delete", journalMode)
	}
}

func TestCreateJobPersistsAggregateAndCommandReceipt(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_one", "Build a small useful product from my notes.")

	receipt, err := database.CreateLocalDraftJob(ctx, job, "cmd_one")
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
	if !reflect.DeepEqual(got, job) {
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

	first, err := database.CreateLocalDraftJob(ctx, job, "cmd_idempotent")
	if err != nil {
		t.Fatalf("first CreateJob() error = %v", err)
	}
	second, err := database.CreateLocalDraftJob(ctx, job, "cmd_idempotent")
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

func TestCreateJobRequiresInitialRevision(t *testing.T) {
	database, _ := newTestStore(t)
	job := newTestJob(t, "job_later_revision", "A newly created Job must start at revision one.")
	job.Revision = 2
	if _, err := database.CreateLocalDraftJob(context.Background(), job, "cmd_later_revision"); !errors.Is(err, domain.ErrInvalidJob) {
		t.Fatalf("CreateJob() error = %v, want domain.ErrInvalidJob", err)
	}
}

func TestGetJobAcceptsLaterAggregateRevision(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_revision_two", "A persisted aggregate can advance revisions.")
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_revision_two"); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	job.Revision = 2
	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal updated Job: %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_aggregates SET revision = ?, payload_json = ?, integrity_sha256 = ? WHERE job_id = ?",
		job.Revision, string(payload), digest(payload), job.ID,
	); err != nil {
		t.Fatalf("update aggregate fixture: %v", err)
	}
	got, err := database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() rejected revision 2: %v", err)
	}
	if got.Revision != 2 {
		t.Fatalf("GetJob() revision = %d, want 2", got.Revision)
	}
}

func TestGetJobDetectsAggregateMetadataTampering(t *testing.T) {
	tests := []struct {
		name  string
		query string
		args  []any
	}{
		{name: "revision", query: "UPDATE job_aggregates SET revision = revision + 1 WHERE job_id = ?", args: []any{"job_metadata"}},
		{name: "created_at", query: "UPDATE job_aggregates SET created_at = ? WHERE job_id = ?", args: []any{"2026-11-20T13:00:00Z", "job_metadata"}},
		{name: "updated_at", query: "UPDATE job_aggregates SET updated_at = ? WHERE job_id = ?", args: []any{"2026-11-20T13:00:00Z", "job_metadata"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database, _ := newTestStore(t)
			ctx := context.Background()
			job := newTestJob(t, "job_metadata", "Detect duplicated metadata tampering.")
			if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_metadata"); err != nil {
				t.Fatalf("CreateJob() error = %v", err)
			}
			if _, err := database.db.ExecContext(ctx, test.query, test.args...); err != nil {
				t.Fatalf("tamper metadata: %v", err)
			}
			if _, err := database.GetJob(ctx, job.ID); !errors.Is(err, ErrIntegrity) {
				t.Fatalf("GetJob() error = %v, want ErrIntegrity", err)
			}
		})
	}
}

func TestListJobsDetectsAggregateMetadataTampering(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_list_metadata", "List must validate indexed metadata.")
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_list_metadata"); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_aggregates SET updated_at = ? WHERE job_id = ?",
		"2026-11-20T13:00:00Z", job.ID,
	); err != nil {
		t.Fatalf("tamper metadata: %v", err)
	}
	if _, err := database.ListJobs(ctx, 10); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("ListJobs() error = %v, want ErrIntegrity", err)
	}
}

func TestCreateJobRejectsCommandTypeMismatch(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_command_type", "Command type is part of idempotency identity.")
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_command_type"); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_commands SET command_type = ? WHERE command_id = ?",
		"DifferentCommand", "cmd_command_type",
	); err != nil {
		t.Fatalf("tamper command type: %v", err)
	}
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_command_type"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("replayed CreateJob() error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestCreateJobDetectsCommandReceiptMetadataTampering(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTestJob(t, "job_command_receipt", "Command receipt metadata is integrity checked.")
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_command_receipt"); err != nil {
		t.Fatalf("CreateJob() error = %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_commands SET created_at = ? WHERE command_id = ?",
		"2026-11-20T13:00:00Z", "cmd_command_receipt",
	); err != nil {
		t.Fatalf("tamper command receipt: %v", err)
	}
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_command_receipt"); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("replayed CreateJob() error = %v, want ErrIntegrity", err)
	}
}

func TestCreateJobRejectsIdempotencyKeyReuseWithDifferentPayload(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	firstJob := newTestJob(t, "job_first", "First request.")
	secondJob := newTestJob(t, "job_second", "Different request.")

	if _, err := database.CreateLocalDraftJob(ctx, firstJob, "cmd_reused"); err != nil {
		t.Fatalf("first CreateJob() error = %v", err)
	}
	if _, err := database.CreateLocalDraftJob(ctx, secondJob, "cmd_reused"); !errors.Is(err, ErrIdempotencyConflict) {
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
	if _, err := database.CreateLocalDraftJob(ctx, job, "cmd_tamper"); err != nil {
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

func newTrustedTestJob(t *testing.T, id, request, commandID string) domain.Job {
	t.Helper()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	provenance := domain.IntakeProvenance{
		PrincipalID: identity.PrincipalID("https://identity.example", "subject-123"), Issuer: "https://identity.example",
		Subject: "subject-123", IngressSource: domain.IngressSourceCLI,
		AuthenticatedAt: now.Add(-time.Minute), ReceivedAt: now.Add(-2 * time.Minute),
		RequestIdentity: commandID, SchemaVersion: domain.IntakeProvenanceSchemaVersion,
	}
	job, err := domain.NewDraftJobWithProvenance(id, request, provenance, now)
	if err != nil {
		t.Fatalf("NewDraftJobWithProvenance() error = %v", err)
	}
	return job
}

func TestCreateTrustedDraftJobPersistsProvenanceAndReceipt(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	job := newTrustedTestJob(t, "job_trusted", "  Preserve this original request.\n  ", "cmd_trusted")
	first, err := database.createJob(ctx, job, "cmd_trusted", createTrustedDraftJobCommandType)
	if err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	if first.JobID != job.ID || first.Revision != 1 {
		t.Fatalf("receipt = %#v", first)
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
	if !reflect.DeepEqual(got, job) {
		t.Fatalf("GetJob() = %#v, want %#v", got, job)
	}
	provenance := got.IntakeProvenance
	if provenance == nil || provenance.RequestIdentity != first.CommandID ||
		provenance.IngressSource != domain.IngressSourceCLI ||
		provenance.OriginalRequestSHA256 == "" ||
		provenance.SchemaVersion != domain.IntakeProvenanceSchemaVersion {
		t.Fatalf("persisted IntakeProvenance = %#v", provenance)
	}
	retry, err := reopened.createJob(ctx, job, "cmd_trusted", createTrustedDraftJobCommandType)
	if err != nil {
		t.Fatalf("idempotent CreateTrustedDraftJob() error = %v", err)
	}
	if retry != first {
		t.Fatalf("retry receipt = %#v, want %#v", retry, first)
	}
}

func TestCreateTrustedDraftJobRequiresAuthenticatedPrincipal(t *testing.T) {
	database, _ := newTestStore(t)
	if _, err := database.CreateTrustedDraftJob(context.Background(), "job_missing_principal", "Trusted request.", "cmd_missing_principal", time.Now().UTC(), auth.AuthenticatedPrincipal{}); err == nil {
		t.Fatal("CreateTrustedDraftJob() accepted an empty AuthenticatedPrincipal")
	}
	jobs, err := database.ListJobs(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListJobs() error = %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("stored Jobs = %d, want 0", len(jobs))
	}
}

func TestCreateLocalDraftJobRejectsProvenance(t *testing.T) {
	database, _ := newTestStore(t)
	job := newTrustedTestJob(t, "job_wrong_path", "Trusted provenance cannot use local path.", "cmd_wrong_path")
	if _, err := database.CreateLocalDraftJob(context.Background(), job, "cmd_wrong_path"); err == nil {
		t.Fatal("CreateLocalDraftJob() accepted a trusted Job")
	}
}

func TestCreateTrustedDraftJobRejectsCrossPathCommandIDReuse(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	local := newTestJob(t, "job_local_command", "Local DRAFT command.")
	if _, err := database.CreateLocalDraftJob(ctx, local, "cmd_shared"); err != nil {
		t.Fatalf("CreateLocalDraftJob() error = %v", err)
	}
	trusted := newTrustedTestJob(t, "job_trusted_command", "Trusted DRAFT command.", "cmd_shared")
	if _, err := database.createJob(ctx, trusted, "cmd_shared", createTrustedDraftJobCommandType); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("CreateTrustedDraftJob() error = %v, want ErrIdempotencyConflict", err)
	}
	if _, err := database.GetJob(ctx, trusted.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("trusted Job lookup error = %v, want ErrNotFound", err)
	}
}

func TestTrustedJobAggregateAndReceiptCommitAtomically(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTrustedTestJob(t, "job_atomic", "Aggregate and receipt are one transaction.", "cmd_atomic")
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_command_receipt
		BEFORE INSERT ON job_commands
		BEGIN SELECT RAISE(ABORT, 'simulated receipt failure'); END;`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	if _, err := database.createJob(ctx, job, "cmd_atomic", createTrustedDraftJobCommandType); err == nil {
		t.Fatal("CreateTrustedDraftJob() succeeded despite receipt insert failure")
	}
	if _, err := database.GetJob(ctx, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetJob() error = %v, want ErrNotFound after rollback", err)
	}
	var count int
	if err := database.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM job_commands WHERE command_id = ?", "cmd_atomic").Scan(&count); err != nil {
		t.Fatalf("count command receipts: %v", err)
	}
	if count != 0 {
		t.Fatalf("command receipt count = %d, want 0 after rollback", count)
	}
}

func TestGetJobRejectsProvenanceTamperingAfterPayloadHashRecomputed(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	job := newTrustedTestJob(t, "job_provenance_tamper", "Detect provenance tampering.", "cmd_provenance_tamper")
	if _, err := database.createJob(ctx, job, "cmd_provenance_tamper", createTrustedDraftJobCommandType); err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	job.IntakeProvenance.OriginalRequestSHA256 = strings.Repeat("0", 64)
	payload, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("marshal tampered Job: %v", err)
	}
	if _, err := database.db.ExecContext(ctx,
		"UPDATE job_aggregates SET payload_json = ?, integrity_sha256 = ? WHERE job_id = ?",
		string(payload), digest(payload), job.ID,
	); err != nil {
		t.Fatalf("update tampered aggregate: %v", err)
	}
	if _, err := database.GetJob(ctx, job.ID); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("GetJob() error = %v, want ErrIntegrity", err)
	}
}

type synchronizedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *synchronizedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *synchronizedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

func newAuthenticatedTestPrincipal(t *testing.T) auth.AuthenticatedPrincipal {
	t.Helper()
	trustedKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate OIDC signing key: %v", err)
	}
	provider := &oidctest.Server{PublicKeys: []oidctest.PublicKey{{
		PublicKey: trustedKey.Public(), KeyID: "taxion-store-test-key", Algorithm: oidc.RS256,
	}}}
	var mu sync.Mutex
	var challenge, nonce string
	mux := http.NewServeMux()
	mux.Handle("/.well-known/openid-configuration", provider)
	mux.Handle("/keys", provider)
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		mu.Lock()
		challenge, nonce = query.Get("code_challenge"), query.Get("nonce")
		mu.Unlock()
		if query.Get("code_challenge_method") != "S256" || challenge == "" || nonce == "" || query.Get("state") == "" {
			http.Error(w, "invalid authorization request", http.StatusBadRequest)
			return
		}
		callback, err := url.Parse(query.Get("redirect_uri"))
		if err != nil {
			http.Error(w, "invalid callback", http.StatusBadRequest)
			return
		}
		values := callback.Query()
		values.Set("code", "one-time-code")
		values.Set("state", query.Get("state"))
		callback.RawQuery = values.Encode()
		http.Redirect(w, r, callback.String(), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid token request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		expectedChallenge, expectedNonce := challenge, nonce
		mu.Unlock()
		if r.Form.Get("code_verifier") == "" || oauth2.S256ChallengeFromVerifier(r.Form.Get("code_verifier")) != expectedChallenge {
			http.Error(w, "PKCE verification failed", http.StatusBadRequest)
			return
		}
		now := time.Now()
		claims, err := json.Marshal(map[string]any{
			"iss": "https://" + r.Host, "aud": "taxion-store-test", "sub": "subject-store-test",
			"exp": now.Add(5 * time.Minute).Unix(), "iat": now.Unix(), "nonce": expectedNonce,
		})
		if err != nil {
			http.Error(w, "encode claims", http.StatusInternalServerError)
			return
		}
		token := oidctest.SignIDToken(trustedKey, "taxion-store-test-key", oidc.RS256, string(claims))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token", "token_type": "Bearer",
			"expires_in": 300, "id_token": token,
		})
	})

	server := httptest.NewTLSServer(mux)
	defer server.Close()
	provider.SetIssuer(server.URL)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	originalTransport := http.DefaultTransport
	transport, ok := originalTransport.(*http.Transport)
	if !ok {
		t.Fatal("default HTTP transport has an unexpected type")
	}
	trustedTransport := transport.Clone()
	trustedTransport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	http.DefaultTransport = trustedTransport
	t.Cleanup(func() {
		http.DefaultTransport = originalTransport
		trustedTransport.CloseIdleConnections()
	})

	authenticator, err := auth.NewOIDCAuthenticator(auth.OIDCConfig{
		Issuer: server.URL, ClientID: "taxion-store-test", Audience: "taxion-store-test",
		RedirectURI: "http://127.0.0.1:0/callback", Timeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewOIDCAuthenticator() error = %v", err)
	}
	output := &synchronizedBuffer{}
	type result struct {
		principal auth.AuthenticatedPrincipal
		err       error
	}
	done := make(chan result, 1)
	go func() {
		principal, err := authenticator.Authenticate(context.Background(), output)
		done <- result{principal: principal, err: err}
	}()
	var authorizationURL string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, line := range strings.Split(output.String(), "\n") {
			if strings.HasPrefix(line, server.URL+"/auth?") {
				authorizationURL = strings.TrimSpace(line)
				break
			}
		}
		if authorizationURL != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if authorizationURL == "" {
		t.Fatal("OIDC authorization URL was not produced")
	}
	response, err := server.Client().Get(authorizationURL)
	if err != nil {
		t.Fatalf("complete fake OIDC authorization: %v", err)
	}
	_ = response.Body.Close()
	select {
	case completed := <-done:
		if completed.err != nil {
			t.Fatalf("Authenticate() error = %v", completed.err)
		}
		return completed.principal
	case <-time.After(5 * time.Second):
		t.Fatal("OIDC authentication did not complete")
		return auth.AuthenticatedPrincipal{}
	}
}

func TestAddPlanningAuthorizationPersistsAndIsIdempotent(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	receivedAt := principal.AuthenticatedAt()
	if _, err := database.CreateTrustedDraftJob(ctx, "job_planning_store", "Plan this request.", "cmd_planning_create", receivedAt, principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	job, err := database.GetJob(ctx, "job_planning_store")
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	issuedAt := job.UpdatedAt.Add(time.Minute)
	request := domain.PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.test",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode:      domain.PlanningReadOnlyBoundarySnapshot,
			Reference: "snapshot_store",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:   domain.PlanningBudget{MaxTurns: 8, MaxTokens: 100000},
		Deadline: issuedAt.Add(15 * time.Minute),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), "cmd_11111111111111111111111111111111", request, issuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() error = %v", err)
	}
	first, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", grant, principal)
	if err != nil {
		t.Fatalf("AddPlanningAuthorization() error = %v", err)
	}
	if first.Revision != 2 || first.PayloadSHA256 == "" {
		t.Fatalf("receipt = %#v", first)
	}
	second, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", grant, principal)
	if err != nil {
		t.Fatalf("idempotent AddPlanningAuthorization() error = %v", err)
	}
	if second != first {
		t.Fatalf("retry receipt = %#v, want %#v", second, first)
	}
	futureTimestampGrant := grant
	futureTimestampGrant.IssuedAt = time.Now().Add(24 * time.Hour)
	retried, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", futureTimestampGrant, principal)
	if err != nil || retried != first {
		t.Fatalf("retry with caller-supplied timestamp = %#v, %v; want %#v", retried, err, first)
	}
	changedGrant := grant
	changedGrant.Budget.MaxTurns++
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", changedGrant, principal); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("reused command ID error = %v, want ErrIdempotencyConflict", err)
	}
	retriedWithNewRevision, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision+1, "cmd_11111111111111111111111111111111", grant, principal)
	if err != nil || retriedWithNewRevision != first {
		t.Fatalf("idempotent retry with current expected revision = %#v, %v; want %#v", retriedWithNewRevision, err, first)
	}
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision+1, "cmd_44444444444444444444444444444444", grant, principal); err == nil {
		t.Fatal("AddPlanningAuthorization() accepted a command ID that differs from the grant")
	}
	staleRequest := request
	staleRequest.Deadline = issuedAt.Add(20 * time.Minute)
	staleGrant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(),
		"cmd_33333333333333333333333333333333", staleRequest, issuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() for stale revision error = %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_33333333333333333333333333333333", staleGrant, principal); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("stale expected revision error = %v, want ErrIdempotencyConflict", err)
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
	if got.Revision != 2 || got.State != domain.JobDraft || len(got.PlanningAuthorizations) != 1 || got.PlanningAuthorizations[0].ID != grant.ID {
		t.Fatalf("persisted PlanningAuthorization = %#v", got)
	}
	if !got.PlanningAuthorizations[0].IssuedAt.Equal(first.CreatedAt) {
		t.Fatalf("persisted issue time = %s, receipt time = %s", got.PlanningAuthorizations[0].IssuedAt, first.CreatedAt)
	}
	replayed, err := reopened.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", grant, principal)
	if err != nil || replayed != first {
		t.Fatalf("reopened idempotent retry = %#v, %v; want %#v", replayed, err, first)
	}
	got.PlanningAuthorizations = nil
	tamperedPayload, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal altered aggregate: %v", err)
	}
	if _, err := reopened.db.ExecContext(ctx,
		"UPDATE job_aggregates SET payload_json = ?, integrity_sha256 = ? WHERE job_id = ?",
		string(tamperedPayload), digest(tamperedPayload), job.ID,
	); err != nil {
		t.Fatalf("remove persisted grant fixture: %v", err)
	}
	if _, err := reopened.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_11111111111111111111111111111111", grant, principal); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("retry after removing persisted grant error = %v, want ErrIntegrity", err)
	}
}

func TestStartTasklessPlannerPersistsAndIsIdempotent(t *testing.T) {
	database, path := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	jobID := "job_planner_start"
	createCommandID := "cmd_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	grantCommandID := "cmd_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	startCommandID := "cmd_cccccccccccccccccccccccccccccccc"
	if _, err := database.CreateTrustedDraftJob(ctx, jobID, "Plan this request.", createCommandID, principal.AuthenticatedAt(), principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	issuedAt := time.Now().UTC()
	request := domain.PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.primary",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode:      domain.PlanningReadOnlyBoundarySnapshot,
			Reference: "snapshot_start",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:   domain.PlanningBudget{MaxTurns: 8, MaxTokens: 100000},
		Deadline: issuedAt.Add(time.Hour),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), grantCommandID, request, issuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() error = %v", err)
	}
	grantReceipt, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, grantCommandID, grant, principal)
	if err != nil {
		t.Fatalf("AddPlanningAuthorization() error = %v", err)
	}
	if grantReceipt.Revision != 2 {
		t.Fatalf("grant receipt revision = %d, want 2", grantReceipt.Revision)
	}

	first, execution, intent, err := database.StartTasklessPlanner(
		ctx, jobID, grantReceipt.Revision, grant.ID, startCommandID, principal,
	)
	if err != nil {
		t.Fatalf("StartTasklessPlanner() error = %v", err)
	}
	if first.Revision != 3 || execution.Role != domain.RolePlanner || execution.TaskID != "" ||
		execution.PlanningAuthorizationID != grant.ID || execution.JobID != jobID {
		t.Fatalf("StartTasklessPlanner() result = receipt %#v, execution %#v", first, execution)
	}
	if intent.RoleExecutionID != execution.ID || intent.ID != execution.ProviderStartOperationID ||
		intent.ProviderID != grant.ProviderID || intent.AccountID != grant.AccountID ||
		intent.ConfigurationSHA256 != grant.ConfigurationSHA256 || intent.RequestSHA256 != first.PayloadSHA256 {
		t.Fatalf("ProviderStartIntent = %#v", intent)
	}
	second, retriedExecution, retriedIntent, err := database.StartTasklessPlanner(
		ctx, jobID, grantReceipt.Revision, grant.ID, startCommandID, principal,
	)
	if err != nil {
		t.Fatalf("idempotent StartTasklessPlanner() error = %v", err)
	}
	if second != first || retriedExecution != execution || retriedIntent != intent {
		t.Fatalf("idempotent retry = %#v / %#v / %#v, want original result", second, retriedExecution, retriedIntent)
	}

	got, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() after start error = %v", err)
	}
	if got.State != domain.JobDraft || got.Revision != 3 ||
		len(got.PlanningAuthorizations) != 1 || len(got.RoleExecutions) != 1 ||
		len(got.ProviderStartIntents) != 1 {
		t.Fatalf("persisted Job = %#v", got)
	}
	if _, _, _, err := database.StartTasklessPlanner(ctx, jobID, got.Revision, grant.ID,
		"cmd_dddddddddddddddddddddddddddddddd", principal); err == nil {
		t.Fatal("StartTasklessPlanner() reused a PlanningAuthorization")
	}
	if _, _, _, err := database.StartTasklessPlanner(ctx, jobID, got.Revision,
		"plan_auth_other", startCommandID, principal); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("StartTasklessPlanner() changed grant for existing Command ID: %v", err)
	}

	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	defer reopened.Close()
	replayed, replayedExecution, replayedIntent, err := reopened.StartTasklessPlanner(
		ctx, jobID, grantReceipt.Revision, grant.ID, startCommandID, principal,
	)
	if err != nil || replayed != first || replayedExecution != execution || replayedIntent != intent {
		t.Fatalf("reopened retry = %#v / %#v / %#v, %v", replayed, replayedExecution, replayedIntent, err)
	}
}

func TestStartTasklessPlannerRejectsWrongPrincipalAndRollsBack(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	otherPrincipal := newAuthenticatedTestPrincipal(t)
	jobID := "job_planner_start_atomic"
	createCommandID := "cmd_eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	grantCommandID := "cmd_ffffffffffffffffffffffffffffffff"
	startCommandID := "cmd_11111111111111111111111111111111"
	if _, err := database.CreateTrustedDraftJob(ctx, jobID, "Plan this request.", createCommandID, principal.AuthenticatedAt(), principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	job, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	issuedAt := time.Now().UTC()
	request := domain.PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.primary",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode:      domain.PlanningReadOnlyBoundarySnapshot,
			Reference: "snapshot_atomic",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:   domain.PlanningBudget{MaxTurns: 4, MaxTokens: 50000},
		Deadline: issuedAt.Add(time.Hour),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), grantCommandID, request, issuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() error = %v", err)
	}
	grantReceipt, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, grantCommandID, grant, principal)
	if err != nil {
		t.Fatalf("AddPlanningAuthorization() error = %v", err)
	}
	if _, _, _, err := database.StartTasklessPlanner(ctx, jobID, grantReceipt.Revision, grant.ID, startCommandID, otherPrincipal); err == nil {
		t.Fatal("StartTasklessPlanner() accepted a different HUMAN Principal")
	}
	if _, _, _, err := database.StartTasklessPlanner(ctx, jobID, grantReceipt.Revision+1, grant.ID,
		"cmd_22222222222222222222222222222222", principal); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("StartTasklessPlanner() accepted a stale expected revision: %v", err)
	}
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_planner_start_receipt
		BEFORE INSERT ON job_commands
		WHEN NEW.command_type = 'StartTasklessPlanner'
		BEGIN SELECT RAISE(ABORT, 'simulated receipt failure'); END;`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	if _, _, _, err := database.StartTasklessPlanner(ctx, jobID, grantReceipt.Revision, grant.ID, startCommandID, principal); err == nil {
		t.Fatal("StartTasklessPlanner() succeeded despite receipt insert failure")
	}
	got, err := database.GetJob(ctx, jobID)
	if err != nil {
		t.Fatalf("GetJob() after rollback error = %v", err)
	}
	if got.Revision != 2 || len(got.RoleExecutions) != 0 || len(got.ProviderStartIntents) != 0 {
		t.Fatalf("Job changed despite transaction rollback: %#v", got)
	}
}

func TestAddPlanningAuthorizationRejectsWrongPrincipalAndRollsBack(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	otherPrincipal := newAuthenticatedTestPrincipal(t)
	receivedAt := principal.AuthenticatedAt()
	if _, err := database.CreateTrustedDraftJob(ctx, "job_planning_atomic", "Plan this request.", "cmd_planning_atomic_create", receivedAt, principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	job, err := database.GetJob(ctx, "job_planning_atomic")
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	issuedAt := job.UpdatedAt.Add(time.Minute)
	request := domain.PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.test",
		ConfigurationSHA256: strings.Repeat("b", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode:      domain.PlanningReadOnlyBoundaryDocuments,
			Reference: "documents_atomic",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:   domain.PlanningBudget{MaxTurns: 4, MaxTokens: 50000},
		Deadline: issuedAt.Add(10 * time.Minute),
	}
	grant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(), "cmd_22222222222222222222222222222222", request, issuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() error = %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_55555555555555555555555555555555", grant, otherPrincipal); err == nil {
		t.Fatal("AddPlanningAuthorization() accepted a different HUMAN Principal")
	}
	if _, err := database.db.ExecContext(ctx, `CREATE TRIGGER fail_planning_receipt
		BEFORE INSERT ON job_commands
		WHEN NEW.command_type = 'AddPlanningAuthorization'
		BEGIN SELECT RAISE(ABORT, 'simulated receipt failure'); END;`); err != nil {
		t.Fatalf("create failure trigger: %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision, "cmd_22222222222222222222222222222222", grant, principal); err == nil {
		t.Fatal("AddPlanningAuthorization() succeeded despite receipt insert failure")
	}
	got, err := database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() after rollback error = %v", err)
	}
	if got.Revision != 1 || len(got.PlanningAuthorizations) != 0 {
		t.Fatalf("Job changed despite transaction rollback: %#v", got)
	}
}

func TestAddPlanningAuthorizationAppendsWithoutChangingExistingFacts(t *testing.T) {
	database, _ := newTestStore(t)
	ctx := context.Background()
	principal := newAuthenticatedTestPrincipal(t)
	if _, err := database.CreateTrustedDraftJob(ctx, "job_planning_append", "Plan this request.", "cmd_planning_append_create", principal.AuthenticatedAt(), principal); err != nil {
		t.Fatalf("CreateTrustedDraftJob() error = %v", err)
	}
	job, err := database.GetJob(ctx, "job_planning_append")
	if err != nil {
		t.Fatalf("GetJob() error = %v", err)
	}
	firstIssuedAt := time.Now().UTC()
	firstRequest := domain.PlanningAuthorizationRequest{
		ProviderID:          "provider.test",
		AccountID:           "account.test",
		ConfigurationSHA256: strings.Repeat("a", 64),
		RepositoryID:        "https://github.com/example/project@" + strings.Repeat("b", 40),
		ReadOnlyBoundary: domain.PlanningReadOnlyBoundary{
			Mode:      domain.PlanningReadOnlyBoundarySnapshot,
			Reference: "snapshot_first",
			SHA256:    strings.Repeat("c", 64),
		},
		Budget:   domain.PlanningBudget{MaxTurns: 4, MaxTokens: 50000},
		Deadline: firstIssuedAt.Add(time.Hour),
	}
	firstGrant, err := domain.NewPlanningAuthorization(job, principal.PrincipalID(),
		"cmd_66666666666666666666666666666666", firstRequest, firstIssuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() first error = %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, job.ID, job.Revision,
		firstGrant.CommandID, firstGrant, principal); err != nil {
		t.Fatalf("AddPlanningAuthorization() first error = %v", err)
	}
	afterFirst, err := database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() after first grant error = %v", err)
	}
	originalFirst := afterFirst.PlanningAuthorizations[0]

	secondIssuedAt := time.Now().UTC()
	secondRequest := firstRequest
	secondRequest.ConfigurationSHA256 = strings.Repeat("d", 64)
	secondRequest.ReadOnlyBoundary = domain.PlanningReadOnlyBoundary{
		Mode:      domain.PlanningReadOnlyBoundaryDocuments,
		Reference: "documents_second",
		SHA256:    strings.Repeat("e", 64),
	}
	secondRequest.Budget = domain.PlanningBudget{MaxTurns: 6, MaxTokens: 75000}
	secondRequest.Deadline = secondIssuedAt.Add(2 * time.Hour)
	secondGrant, err := domain.NewPlanningAuthorization(afterFirst, principal.PrincipalID(),
		"cmd_77777777777777777777777777777777", secondRequest, secondIssuedAt)
	if err != nil {
		t.Fatalf("NewPlanningAuthorization() second error = %v", err)
	}
	if _, err := database.AddPlanningAuthorization(ctx, afterFirst.ID, afterFirst.Revision,
		secondGrant.CommandID, secondGrant, principal); err != nil {
		t.Fatalf("AddPlanningAuthorization() second error = %v", err)
	}
	finalJob, err := database.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob() final error = %v", err)
	}
	if finalJob.State != domain.JobDraft || finalJob.Revision != 3 || len(finalJob.PlanningAuthorizations) != 2 {
		t.Fatalf("final Job = %#v", finalJob)
	}
	if !reflect.DeepEqual(finalJob.PlanningAuthorizations[0], originalFirst) {
		t.Fatalf("first PlanningAuthorization changed: got %#v, want %#v", finalJob.PlanningAuthorizations[0], originalFirst)
	}
}
