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

	"github.com/hiono/taxion/internal/domain"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound            = errors.New("job not found")
	ErrJobExists           = errors.New("job already exists")
	ErrIdempotencyConflict = errors.New("idempotency key reused with different content")
	ErrIntegrity           = errors.New("job integrity check failed")
	ErrUnsupportedSchema   = errors.New("unsupported database schema version")
)

const schemaVersion = 1
const createLocalDraftJobCommandType = "CreateJob"
const createTrustedDraftJobCommandType = "CreateTrustedDraftJob"

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
	if err := s.db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to SQLite database: %w", err)
	}
	for _, statement := range []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = FULL",
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("configure SQLite: %w", err)
		}
	}

	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("%w: database=%d supported=%d", ErrUnsupportedSchema, version, schemaVersion)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin schema transaction: %w", err)
	}
	defer tx.Rollback()

	statements := []string{
		`CREATE TABLE IF NOT EXISTS job_aggregates (
			job_id TEXT PRIMARY KEY,
			revision INTEGER NOT NULL CHECK (revision > 0),
			payload_json TEXT NOT NULL,
			integrity_sha256 TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS job_aggregates_updated_at
			ON job_aggregates(updated_at DESC, job_id ASC)`,
		`CREATE TABLE IF NOT EXISTS job_commands (
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
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("create Job schema: %w", err)
		}
	}
	if version == 0 {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
			return fmt.Errorf("set schema version: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit schema transaction: %w", err)
	}
	return nil
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

func (s *Store) CreateTrustedDraftJob(ctx context.Context, job domain.Job, commandID string) (CommandReceipt, error) {
	if job.IntakeProvenance == nil {
		return CommandReceipt{}, errors.New("trusted DRAFT Job requires IntakeProvenance")
	}
	if job.IntakeProvenance.RequestIdentity != commandID {
		return CommandReceipt{}, errors.New("IntakeProvenance request identity must match command ID")
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
