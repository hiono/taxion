package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/auth"
	"github.com/hiono/taxion/internal/domain"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound            = errors.New("job not found")
	ErrJobExists           = errors.New("job already exists")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different content")
	ErrRevisionConflict    = errors.New("Job revision conflict")
	ErrIntegrity           = errors.New("job integrity check failed")
	ErrUnsupportedSchema   = errors.New("unsupported database schema version")
)

// schemaVersion identifies the single supported fresh schema. Older schemas are rejected; no migration path is retained.
const schemaVersion = 1

const createLocalDraftJobCommandType = "CreateJob"
const createTrustedDraftJobCommandType = "CreateTrustedDraftJob"
const addPlanningAuthorizationCommandType = "AddPlanningAuthorization"
const startTasklessPlannerCommandType = "StartTasklessPlanner"
const addProviderResultObservationCommandType = "AddProviderResultObservation"
const addProviderUsageObservationCommandType = "AddProviderUsageObservation"

type Store struct {
	db *sql.DB
}

type CommandReceipt struct {
	CommandID     string    `json:"command_id"`
	JobID         string    `json:"job_id"`
	Revision      uint64    `json:"revision"`
	PayloadSHA256 string    `json:"payload_sha256"`
	CreatedAt     time.Time `json:"created_at"`
}

func Open(ctx context.Context, path string) (*Store, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("database path is required")
	}
	if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve database path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
		if info, err := os.Lstat(absolute); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return nil, errors.New("database path must not be a symbolic link")
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("inspect database path: %w", err)
		}
		path = absolute
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open SQLite database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	store := &Store{db: db}
	if err := store.initialize(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	if path != ":memory:" && runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("restrict database permissions: %w", err)
		}
	}
	return store, nil
}

func (s *Store) initialize(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire SQLite schema connection: %w", err)
	}
	defer conn.Close()

	var version int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read SQLite schema version: %w", err)
	}
	if version != 0 && version != schemaVersion {
		return fmt.Errorf("%w: database=%d supported=%d", ErrUnsupportedSchema, version, schemaVersion)
	}
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA synchronous = FULL",
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure SQLite: %w", err)
		}
	}
	if version == schemaVersion {
		return nil
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA journal_mode = WAL"); err != nil {
		return fmt.Errorf("enable SQLite WAL: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin SQLite schema transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = rollbackSQLiteConnection(conn)
		}
	}()

	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("recheck SQLite schema version: %w", err)
	}
	if version != 0 {
		if version == schemaVersion {
			if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
				return fmt.Errorf("finish concurrent SQLite schema initialization: %w", err)
			}
			committed = true
			return nil
		}
		return fmt.Errorf("%w: database=%d supported=%d", ErrUnsupportedSchema, version, schemaVersion)
	}

	statements := []string{
		`CREATE TABLE job_aggregates (
			job_id TEXT PRIMARY KEY,
			revision INTEGER NOT NULL CHECK (revision > 0),
			payload_json TEXT NOT NULL,
			integrity_sha256 TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX job_aggregates_updated_at
			ON job_aggregates(updated_at DESC, job_id ASC)`,
		`CREATE TABLE job_commands (
			command_id TEXT PRIMARY KEY,
			job_id TEXT NOT NULL REFERENCES job_aggregates(job_id),
			command_type TEXT NOT NULL,
			payload_sha256 TEXT NOT NULL,
			resulting_revision INTEGER NOT NULL,
			receipt_json TEXT NOT NULL,
			created_at TEXT NOT NULL
		)`,
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create Job schema: %w", err)
		}
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return fmt.Errorf("set SQLite schema version: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit SQLite schema transaction: %w", err)
	}
	committed = true
	return nil
}

func rollbackSQLiteConnection(conn *sql.Conn) error {
	if conn == nil {
		return nil
	}
	_, err := conn.ExecContext(context.Background(), "ROLLBACK")
	if err == nil || strings.Contains(strings.ToLower(err.Error()), "no transaction is active") {
		return nil
	}
	return err
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) CreateLocalDraftJob(ctx context.Context, job domain.Job, commandID string) (CommandReceipt, error) {
	if job.IntakeProvenance != nil {
		return CommandReceipt{}, errors.New("local DRAFT Job must not contain IntakeProvenance")
	}
	return s.createJob(ctx, job, commandID, createLocalDraftJobCommandType)
}

func (s *Store) CreateTrustedDraftJob(ctx context.Context, jobID, request, commandID string, receivedAt time.Time, principal auth.AuthenticatedPrincipal) (CommandReceipt, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	provenance := domain.IntakeProvenance{
		PrincipalID:     principal.PrincipalID(),
		Issuer:          principal.Issuer(),
		Subject:         principal.Subject(),
		IngressSource:   domain.IngressSourceCLI,
		AuthenticatedAt: principal.AuthenticatedAt(),
		ReceivedAt:      receivedAt,
		RequestIdentity: commandID,
		SchemaVersion:   domain.IntakeProvenanceSchemaVersion,
	}
	jobCreatedAt := receivedAt
	if principal.AuthenticatedAt().After(jobCreatedAt) {
		jobCreatedAt = principal.AuthenticatedAt()
	}
	job, err := domain.NewDraftJobWithProvenance(jobID, request, provenance, jobCreatedAt)
	if err != nil {
		return CommandReceipt{}, err
	}
	return s.createJob(ctx, job, commandID, createTrustedDraftJobCommandType)
}

func (s *Store) createJob(ctx context.Context, job domain.Job, commandID, commandType string) (CommandReceipt, error) {
	if err := job.Validate(); err != nil {
		return CommandReceipt{}, err
	}
	if job.Revision != 1 {
		return CommandReceipt{}, fmt.Errorf("%w: a new Job must start at revision 1", domain.ErrInvalidJob)
	}
	if strings.TrimSpace(commandID) == "" {
		return CommandReceipt{}, errors.New("command id is required")
	}

	payload, err := json.Marshal(job)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("encode Job: %w", err)
	}
	payloadHash := digest(payload)
	createdAt := job.CreatedAt.UTC().Format(time.RFC3339Nano)
	receipt := CommandReceipt{
		CommandID:     commandID,
		JobID:         job.ID,
		Revision:      job.Revision,
		PayloadSHA256: payloadHash,
		CreatedAt:     job.CreatedAt.UTC(),
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("encode command receipt: %w", err)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("begin Job command: %w", err)
	}
	defer tx.Rollback()

	var existingType, existingJobID, existingHash, existingReceipt, existingCreatedAt string
	var existingRevision int64
	err = tx.QueryRowContext(ctx,
		"SELECT command_type, job_id, resulting_revision, payload_sha256, receipt_json, created_at FROM job_commands WHERE command_id = ?",
		commandID,
	).Scan(&existingType, &existingJobID, &existingRevision, &existingHash, &existingReceipt, &existingCreatedAt)
	if err == nil {
		if existingType != commandType || existingJobID != job.ID || existingRevision <= 0 ||
			uint64(existingRevision) != job.Revision || existingHash != payloadHash {
			return CommandReceipt{}, ErrIdempotencyConflict
		}
		var stored CommandReceipt
		if err := json.Unmarshal([]byte(existingReceipt), &stored); err != nil {
			return CommandReceipt{}, fmt.Errorf("%w: decode existing command receipt: %v", ErrIntegrity, err)
		}
		if stored.CommandID != commandID || stored.JobID != job.ID || stored.Revision != job.Revision ||
			stored.PayloadSHA256 != payloadHash || !stored.CreatedAt.Equal(job.CreatedAt.UTC()) ||
			existingCreatedAt != createdAt {
			return CommandReceipt{}, fmt.Errorf("%w: command receipt metadata mismatch", ErrIntegrity)
		}
		return stored, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommandReceipt{}, fmt.Errorf("read command receipt: %w", err)
	}

	var exists int
	err = tx.QueryRowContext(ctx, "SELECT 1 FROM job_aggregates WHERE job_id = ?", job.ID).Scan(&exists)
	if err == nil {
		return CommandReceipt{}, ErrJobExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommandReceipt{}, fmt.Errorf("check Job identity: %w", err)
	}

	_, err = tx.ExecContext(ctx,
		`INSERT INTO job_aggregates
			(job_id, revision, payload_json, integrity_sha256, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
		job.ID, job.Revision, string(payload), payloadHash, createdAt,
		job.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("insert Job aggregate: %w", err)
	}
	_, err = tx.ExecContext(ctx,
		`INSERT INTO job_commands
			(command_id, job_id, command_type, payload_sha256, resulting_revision, receipt_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
		commandID, job.ID, commandType, payloadHash, job.Revision, string(receiptJSON), createdAt,
	)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("insert command receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommandReceipt{}, fmt.Errorf("commit Job command: %w", err)
	}
	return receipt, nil
}

func (s *Store) AddPlanningAuthorization(ctx context.Context, jobID string, expectedRevision uint64, commandID string, grant domain.PlanningAuthorization, principal auth.AuthenticatedPrincipal) (CommandReceipt, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	if grant.PrincipalID != principal.PrincipalID() {
		return CommandReceipt{}, errors.New("PlanningAuthorization principal does not match authenticated HUMAN Principal")
	}
	if commandID != grant.CommandID || grant.ID != domain.PlanningAuthorizationID(commandID) {
		return CommandReceipt{}, errors.New("PlanningAuthorization identity does not match command ID")
	}
	if strings.TrimSpace(commandID) == "" {
		return CommandReceipt{}, errors.New("command id is required")
	}
	if grant.JobID != jobID {
		return CommandReceipt{}, errors.New("PlanningAuthorization Job identity mismatch")
	}
	if expectedRevision == 0 {
		return CommandReceipt{}, errors.New("expected Job revision is required")
	}

	request := struct {
		Authorization domain.PlanningAuthorization `json:"authorization"`
	}{Authorization: grant}
	request.Authorization.IssuedAt = time.Time{}
	requestPayload, err := json.Marshal(request)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("encode PlanningAuthorization request: %w", err)
	}
	requestHash := digest(requestPayload)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("begin PlanningAuthorization command: %w", err)
	}
	defer tx.Rollback()

	var existingType, existingJobID, existingHash, existingReceipt, existingCreatedAt string
	var existingRevision int64
	err = tx.QueryRowContext(ctx,
		"SELECT command_type, job_id, resulting_revision, payload_sha256, receipt_json, created_at FROM job_commands WHERE command_id = ?",
		commandID,
	).Scan(&existingType, &existingJobID, &existingRevision, &existingHash, &existingReceipt, &existingCreatedAt)
	if err == nil {
		if existingType != addPlanningAuthorizationCommandType || existingJobID != jobID ||
			existingRevision <= 1 || existingHash != requestHash {
			return CommandReceipt{}, ErrIdempotencyConflict
		}
		var stored CommandReceipt
		if err := json.Unmarshal([]byte(existingReceipt), &stored); err != nil {
			return CommandReceipt{}, fmt.Errorf("%w: decode existing command receipt: %v", ErrIntegrity, err)
		}
		existingTime, err := time.Parse(time.RFC3339Nano, existingCreatedAt)
		if err != nil {
			return CommandReceipt{}, fmt.Errorf("%w: invalid PlanningAuthorization receipt timestamp: %v", ErrIntegrity, err)
		}
		if stored.CommandID != commandID || stored.JobID != jobID || stored.Revision != uint64(existingRevision) ||
			stored.PayloadSHA256 != requestHash || !stored.CreatedAt.Equal(existingTime) {
			return CommandReceipt{}, fmt.Errorf("%w: PlanningAuthorization receipt metadata mismatch", ErrIntegrity)
		}
		var currentRevision int64
		var currentPayload, currentHash, currentCreatedAt, currentUpdatedAt string
		if err := tx.QueryRowContext(ctx,
			"SELECT revision, payload_json, integrity_sha256, created_at, updated_at FROM job_aggregates WHERE job_id = ?",
			jobID,
		).Scan(&currentRevision, &currentPayload, &currentHash, &currentCreatedAt, &currentUpdatedAt); err != nil {
			return CommandReceipt{}, fmt.Errorf("%w: read Job for PlanningAuthorization retry: %v", ErrIntegrity, err)
		}
		currentJob, err := decodeJobAggregate(jobID, currentRevision, currentPayload, currentHash, currentCreatedAt, currentUpdatedAt)
		if err != nil {
			return CommandReceipt{}, err
		}
		found := false
		for _, currentGrant := range currentJob.PlanningAuthorizations {
			if currentGrant.ID != grant.ID {
				continue
			}
			persistedRequest := struct {
				Authorization domain.PlanningAuthorization `json:"authorization"`
			}{Authorization: currentGrant}
			persistedRequest.Authorization.IssuedAt = time.Time{}
			currentGrantPayload, err := json.Marshal(persistedRequest)
			if err != nil || digest(currentGrantPayload) != requestHash {
				return CommandReceipt{}, fmt.Errorf("%w: persisted PlanningAuthorization differs from command", ErrIntegrity)
			}
			if err := currentGrant.Validate(currentJob); err != nil {
				return CommandReceipt{}, fmt.Errorf("%w: persisted PlanningAuthorization is invalid: %v", ErrIntegrity, err)
			}
			if !currentGrant.IssuedAt.Equal(existingTime) {
				return CommandReceipt{}, fmt.Errorf("%w: PlanningAuthorization issue time differs from receipt", ErrIntegrity)
			}
			found = true
			break
		}
		if !found {
			return CommandReceipt{}, fmt.Errorf("%w: PlanningAuthorization fact is missing", ErrIntegrity)
		}
		return stored, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommandReceipt{}, fmt.Errorf("read PlanningAuthorization receipt: %w", err)
	}

	var revision int64
	var payload, expectedHash, aggregateCreatedAt, aggregateUpdatedAt string
	err = tx.QueryRowContext(ctx,
		"SELECT revision, payload_json, integrity_sha256, created_at, updated_at FROM job_aggregates WHERE job_id = ?",
		jobID,
	).Scan(&revision, &payload, &expectedHash, &aggregateCreatedAt, &aggregateUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CommandReceipt{}, ErrNotFound
	}
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("read Job for PlanningAuthorization: %w", err)
	}
	job, err := decodeJobAggregate(jobID, revision, payload, expectedHash, aggregateCreatedAt, aggregateUpdatedAt)
	if err != nil {
		return CommandReceipt{}, err
	}
	if job.Revision != expectedRevision {
		return CommandReceipt{}, ErrIdempotencyConflict
	}
	issuedAt := time.Now().UTC().Round(0)
	if issuedAt.Before(job.UpdatedAt) {
		return CommandReceipt{}, errors.New("PlanningAuthorization issue time precedes current Job revision")
	}
	grant.IssuedAt = issuedAt
	if err := grant.ValidateFor(job, principal.PrincipalID(), issuedAt); err != nil {
		return CommandReceipt{}, err
	}
	for _, existing := range job.PlanningAuthorizations {
		if existing.ID == grant.ID {
			return CommandReceipt{}, errors.New("PlanningAuthorization ID already exists")
		}
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return CommandReceipt{}, errors.New("Job revision exhausted")
	}

	job.PlanningAuthorizations = append(job.PlanningAuthorizations, grant)
	job.Revision++
	job.UpdatedAt = issuedAt
	if err := job.Validate(); err != nil {
		return CommandReceipt{}, fmt.Errorf("validate Job with PlanningAuthorization: %w", err)
	}
	updatedPayload, err := json.Marshal(job)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("encode updated Job: %w", err)
	}
	aggregateHash := digest(updatedPayload)
	receipt := CommandReceipt{
		CommandID:     commandID,
		JobID:         jobID,
		Revision:      job.Revision,
		PayloadSHA256: requestHash,
		CreatedAt:     issuedAt,
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("encode PlanningAuthorization receipt: %w", err)
	}

	result, err := tx.ExecContext(ctx,
		"UPDATE job_aggregates SET revision = ?, payload_json = ?, integrity_sha256 = ?, updated_at = ? WHERE job_id = ? AND revision = ?",
		job.Revision, string(updatedPayload), aggregateHash, issuedAt.Format(time.RFC3339Nano), jobID, revision,
	)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("update Job with PlanningAuthorization: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("check Job update result: %w", err)
	}
	if affected != 1 {
		return CommandReceipt{}, ErrIdempotencyConflict
	}
	createdAt := issuedAt.Format(time.RFC3339Nano)
	_, err = tx.ExecContext(ctx,
		"INSERT INTO job_commands (command_id, job_id, command_type, payload_sha256, resulting_revision, receipt_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		commandID, jobID, addPlanningAuthorizationCommandType, requestHash, job.Revision, string(receiptJSON), createdAt,
	)
	if err != nil {
		return CommandReceipt{}, fmt.Errorf("insert PlanningAuthorization receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommandReceipt{}, fmt.Errorf("commit PlanningAuthorization: %w", err)
	}
	return receipt, nil
}

func (s *Store) StartTasklessPlanner(ctx context.Context, jobID string, expectedRevision uint64, authorizationID, commandID string, principal auth.AuthenticatedPrincipal) (CommandReceipt, domain.RoleExecution, domain.ProviderStartIntent, error) {
	if err := principal.Validate(); err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("validate authenticated HUMAN Principal: %w", err)
	}
	if strings.TrimSpace(jobID) == "" || strings.TrimSpace(authorizationID) == "" ||
		!strings.HasPrefix(commandID, "cmd_") || len(commandID) != len("cmd_")+32 ||
		expectedRevision == 0 || expectedRevision >= uint64(^uint64(0)>>1) {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, errors.New("Job, PlanningAuthorization, expected revision, and command identity are required")
	}
	request := struct {
		JobID           string `json:"job_id"`
		AuthorizationID string `json:"authorization_id"`
		PrincipalID     string `json:"principal_id"`
	}{
		JobID: jobID, AuthorizationID: authorizationID,
		PrincipalID: principal.PrincipalID(),
	}
	requestPayload, err := json.Marshal(request)
	if err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("encode Taskless PLANNER start request: %w", err)
	}
	requestHash := digest(requestPayload)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("begin Taskless PLANNER start: %w", err)
	}
	defer tx.Rollback()

	var existingType, existingJobID, existingHash, existingReceipt, existingCreatedAt string
	var existingRevision int64
	err = tx.QueryRowContext(ctx,
		"SELECT command_type, job_id, resulting_revision, payload_sha256, receipt_json, created_at FROM job_commands WHERE command_id = ?",
		commandID,
	).Scan(&existingType, &existingJobID, &existingRevision, &existingHash, &existingReceipt, &existingCreatedAt)
	if err == nil {
		if existingType != startTasklessPlannerCommandType || existingJobID != jobID ||
			existingRevision <= 1 || existingHash != requestHash {
			return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, ErrIdempotencyConflict
		}
		var stored CommandReceipt
		if err := json.Unmarshal([]byte(existingReceipt), &stored); err != nil {
			return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("%w: decode existing Taskless PLANNER receipt: %v", ErrIntegrity, err)
		}
		existingTime, err := time.Parse(time.RFC3339Nano, existingCreatedAt)
		if err != nil {
			return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("%w: invalid Taskless PLANNER receipt timestamp: %v", ErrIntegrity, err)
		}
		if stored.CommandID != commandID || stored.JobID != jobID ||
			stored.Revision != uint64(existingRevision) || stored.PayloadSHA256 != requestHash ||
			!stored.CreatedAt.Equal(existingTime) {
			return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("%w: Taskless PLANNER receipt metadata mismatch", ErrIntegrity)
		}
		var currentRevision int64
		var currentPayload, currentHash, currentCreatedAt, currentUpdatedAt string
		if err := tx.QueryRowContext(ctx,
			"SELECT revision, payload_json, integrity_sha256, created_at, updated_at FROM job_aggregates WHERE job_id = ?",
			jobID,
		).Scan(&currentRevision, &currentPayload, &currentHash, &currentCreatedAt, &currentUpdatedAt); err != nil {
			return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("%w: read Job for Taskless PLANNER retry: %v", ErrIntegrity, err)
		}
		currentJob, err := decodeJobAggregate(jobID, currentRevision, currentPayload, currentHash, currentCreatedAt, currentUpdatedAt)
		if err != nil {
			return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, err
		}
		var execution domain.RoleExecution
		foundExecution := false
		for _, candidate := range currentJob.RoleExecutions {
			if candidate.CommandID == commandID {
				execution = candidate
				foundExecution = true
				break
			}
		}
		if !foundExecution || execution.PlanningAuthorizationID != authorizationID ||
			execution.CreatedRevision != uint64(existingRevision) || !execution.CreatedAt.Equal(existingTime) {
			return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("%w: persisted RoleExecution differs from command receipt", ErrIntegrity)
		}
		var intent domain.ProviderStartIntent
		foundIntent := false
		for _, candidate := range currentJob.ProviderStartIntents {
			if candidate.RoleExecutionID == execution.ID {
				intent = candidate
				foundIntent = true
				break
			}
		}
		if !foundIntent || intent.RequestSHA256 != requestHash ||
			intent.CreatedRevision != uint64(existingRevision) || !intent.CreatedAt.Equal(existingTime) {
			return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("%w: persisted Provider Start intent differs from command receipt", ErrIntegrity)
		}
		return stored, execution, intent, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("read Taskless PLANNER receipt: %w", err)
	}

	var revision int64
	var payload, expectedHash, aggregateCreatedAt, aggregateUpdatedAt string
	err = tx.QueryRowContext(ctx,
		"SELECT revision, payload_json, integrity_sha256, created_at, updated_at FROM job_aggregates WHERE job_id = ?",
		jobID,
	).Scan(&revision, &payload, &expectedHash, &aggregateCreatedAt, &aggregateUpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, ErrNotFound
	}
	if err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("read Job for Taskless PLANNER start: %w", err)
	}
	job, err := decodeJobAggregate(jobID, revision, payload, expectedHash, aggregateCreatedAt, aggregateUpdatedAt)
	if err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, err
	}
	if job.Revision != expectedRevision {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, ErrIdempotencyConflict
	}
	createdAt := time.Now().UTC().Round(0)
	if createdAt.Before(job.UpdatedAt) {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, errors.New("Taskless PLANNER start time precedes current Job revision")
	}
	execution, intent, err := domain.NewTasklessPlannerStart(job, authorizationID, principal.PrincipalID(), commandID, requestHash, createdAt)
	if err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, err
	}
	if job.Revision >= uint64(^uint64(0)>>1) {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, errors.New("Job revision exhausted")
	}
	job.RoleExecutions = append(job.RoleExecutions, execution)
	job.ProviderStartIntents = append(job.ProviderStartIntents, intent)
	if job.ProviderLifecycleEnforcedFromRevision == 0 {
		job.ProviderLifecycleEnforcedFromRevision = intent.CreatedRevision
	}
	job.Revision++
	job.UpdatedAt = createdAt
	if err := job.Validate(); err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("validate Job with Taskless PLANNER start: %w", err)
	}
	updatedPayload, err := json.Marshal(job)
	if err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("encode Job with Taskless PLANNER start: %w", err)
	}
	aggregateHash := digest(updatedPayload)
	receipt := CommandReceipt{
		CommandID: commandID, JobID: jobID, Revision: job.Revision,
		PayloadSHA256: requestHash, CreatedAt: createdAt,
	}
	receiptJSON, err := json.Marshal(receipt)
	if err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("encode Taskless PLANNER receipt: %w", err)
	}
	result, err := tx.ExecContext(ctx,
		"UPDATE job_aggregates SET revision = ?, payload_json = ?, integrity_sha256 = ?, updated_at = ? WHERE job_id = ? AND revision = ?",
		job.Revision, string(updatedPayload), aggregateHash, createdAt.Format(time.RFC3339Nano), jobID, revision,
	)
	if err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("update Job with Taskless PLANNER start: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("check Taskless PLANNER Job update: %w", err)
	}
	if affected != 1 {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, ErrIdempotencyConflict
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO job_commands (command_id, job_id, command_type, payload_sha256, resulting_revision, receipt_json, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)",
		commandID, jobID, startTasklessPlannerCommandType, requestHash, job.Revision, string(receiptJSON), createdAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("insert Taskless PLANNER receipt: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommandReceipt{}, domain.RoleExecution{}, domain.ProviderStartIntent{}, fmt.Errorf("commit Taskless PLANNER start: %w", err)
	}
	return receipt, execution, intent, nil
}

func (s *Store) GetJob(ctx context.Context, id string) (domain.Job, error) {
	var revision int64
	var payload, expectedHash, createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx,
		`SELECT revision, payload_json, integrity_sha256, created_at, updated_at
			FROM job_aggregates WHERE job_id = ?`,
		id,
	).Scan(&revision, &payload, &expectedHash, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Job{}, ErrNotFound
	}
	if err != nil {
		return domain.Job{}, fmt.Errorf("read Job: %w", err)
	}
	return decodeJobAggregate(id, revision, payload, expectedHash, createdAt, updatedAt)
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]domain.Job, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("limit must be between 1 and 1000")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT job_id, revision, payload_json, integrity_sha256, created_at, updated_at
			FROM job_aggregates
			ORDER BY updated_at DESC, job_id ASC
			LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list Jobs: %w", err)
	}
	defer rows.Close()

	jobs := make([]domain.Job, 0)
	for rows.Next() {
		var id, payload, expectedHash, createdAt, updatedAt string
		var revision int64
		if err := rows.Scan(&id, &revision, &payload, &expectedHash, &createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan Job: %w", err)
		}
		job, err := decodeJobAggregate(id, revision, payload, expectedHash, createdAt, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("validate Job %s: %w", id, err)
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Jobs: %w", err)
	}
	return jobs, nil
}

func decodeJobAggregate(id string, revision int64, payload, expectedHash, createdAt, updatedAt string) (domain.Job, error) {
	if revision <= 0 {
		return domain.Job{}, fmt.Errorf("%w: Job %s has invalid stored revision", ErrIntegrity, id)
	}
	if digest([]byte(payload)) != expectedHash {
		return domain.Job{}, fmt.Errorf("%w: Job %s payload digest mismatch", ErrIntegrity, id)
	}
	var job domain.Job
	if err := json.Unmarshal([]byte(payload), &job); err != nil {
		return domain.Job{}, fmt.Errorf("%w: decode Job %s payload: %v", ErrIntegrity, id, err)
	}
	if err := job.Validate(); err != nil {
		return domain.Job{}, fmt.Errorf("%w: validate Job %s payload: %v", ErrIntegrity, id, err)
	}
	if job.ID != id || job.Revision != uint64(revision) {
		return domain.Job{}, fmt.Errorf("%w: Job %s identity or revision mismatch", ErrIntegrity, id)
	}
	if job.CreatedAt.UTC().Format(time.RFC3339Nano) != createdAt ||
		job.UpdatedAt.UTC().Format(time.RFC3339Nano) != updatedAt {
		return domain.Job{}, fmt.Errorf("%w: Job %s timestamp metadata mismatch", ErrIntegrity, id)
	}
	return job, nil
}

func digest(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}
