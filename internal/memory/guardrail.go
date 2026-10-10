package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
)

const (
	CheckRepoEquals       = "repo-equals"
	CheckTaskTitleMatches = "task-title-matches"
)

const promotionSchema = `
CREATE TABLE IF NOT EXISTS memory_settings (
  id INTEGER PRIMARY KEY CHECK(id=1),
  promote_after INTEGER NOT NULL CHECK(promote_after>0));
INSERT OR IGNORE INTO memory_settings(id,promote_after) VALUES(1,3);
CREATE TABLE IF NOT EXISTS memory_guardrail_checks (
  guardrail_id INTEGER PRIMARY KEY,
  lesson_id INTEGER NOT NULL UNIQUE,
  check_kind TEXT NOT NULL,
  check_value TEXT NOT NULL);
`

// ErrGuardrailRejected identifies a policy denial, not a database failure.
var ErrGuardrailRejected = errors.New("taxion: MEM-001 guardrail rejected dispatch")

// GuardrailCheck is the approved predicate, separate from the free-text lesson.
type GuardrailCheck struct {
	ID       int64
	LessonID int64
	Scope    string
	Kind     string
	Value    string
}

// PathForLedger keeps CLI, HTTP and ACP checks on the same workspace database.
func PathForLedger(ledgerPath string) string {
	return filepath.Join(filepath.Dir(ledgerPath), "memory.db")
}

// SetPromoteAfter persists a positive threshold for subsequent warnings.
// Existing candidates are never withdrawn when the threshold changes.
func (s *Store) SetPromoteAfter(n int) error {
	if n < 1 {
		return fmt.Errorf("taxion: promote-after must be positive")
	}
	result, err := s.db.Exec(`UPDATE memory_settings SET promote_after=? WHERE id=1`, n)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return fmt.Errorf("taxion: Memory promotion settings are missing")
	}
	return nil
}

func validateCheck(kind, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("taxion: guardrail check value must not be empty")
	}
	switch kind {
	case CheckRepoEquals:
		return nil
	case CheckTaskTitleMatches:
		if _, err := regexp.Compile(value); err != nil {
			return fmt.Errorf("taxion: invalid task-title-matches expression: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("taxion: unknown guardrail check %q (repo-equals|task-title-matches)", kind)
	}
}

// Approve promotes a recorded warning candidate using an explicitly selected
// predicate. Free-text lessons must not become executable policy implicitly.
// Repeating the same approval returns its guardrail; changing it is rejected.
func (s *Store) Approve(lessonID int64, kind, value string) (int64, error) {
	if err := validateCheck(kind, value); err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var existing int64
	var previousKind, previousValue string
	err = tx.QueryRow(
		`SELECT c.guardrail_id,c.check_kind,c.check_value FROM memory_guardrail_checks c
		 JOIN guardrails g ON g.id=c.guardrail_id AND g.status='approved'
		 JOIN lessons l ON l.id=c.lesson_id AND l.status='approved' WHERE c.lesson_id=?`,
		lessonID).Scan(&existing, &previousKind, &previousValue)
	if err == nil {
		if previousKind != kind || previousValue != value {
			return 0, fmt.Errorf("taxion: candidate %d already approved with a different check", lessonID)
		}
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return 0, err
	}
	var title, body, scope, status string
	err = tx.QueryRow(
		`SELECT title,body,scope,status FROM lessons
		 WHERE id=? AND EXISTS(SELECT 1 FROM warnings WHERE lesson_id=lessons.id)`,
		lessonID).Scan(&title, &body, &scope, &status)
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("%w: warning candidate %d", ErrNotFound, lessonID)
	}
	if err != nil {
		return 0, err
	}
	if status != "proposed" {
		return 0, fmt.Errorf("taxion: candidate %d is not proposed", lessonID)
	}
	if err := checkScope(scope); err != nil {
		return 0, err
	}
	res, err := tx.Exec(
		`INSERT INTO guardrails(title,body,scope,status,created_at) VALUES(?,?,?,'approved',?)`,
		title, body, scope, ts(time.Now()))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(
		`INSERT INTO memory_guardrail_checks(guardrail_id,lesson_id,check_kind,check_value) VALUES(?,?,?,?)`,
		id, lessonID, kind, value); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`UPDATE lessons SET status='approved' WHERE id=?`, lessonID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// CheckDispatch evaluates all approved checks in both workspace scopes.
// Absence preserves pre-Memory workspaces; unreadable or malformed policy
// fails explicitly rather than silently disabling enforcement.
func CheckDispatch(ledgerPath string, task core.Task, repo string) error {
	path := PathForLedger(ledgerPath)
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("taxion: inspect Memory policy: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("taxion: read Memory policy: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("taxion: Memory policy is not a regular database file")
	}
	s, err := Open(path)
	if err != nil {
		return fmt.Errorf("taxion: open Memory policy: %w", err)
	}
	defer s.Close()
	checks, err := s.ApprovedChecks()
	if err != nil {
		return err
	}
	for _, check := range checks {
		var matches bool
		switch check.Kind {
		case CheckRepoEquals:
			matches = repo == check.Value
		case CheckTaskTitleMatches:
			pattern, err := regexp.Compile(check.Value)
			if err != nil {
				return err
			}
			matches = pattern.MatchString(task.Title)
		}
		if !matches {
			return fmt.Errorf("%w: guardrail %d (%s)", ErrGuardrailRejected, check.ID, check.Kind)
		}
	}
	return nil
}

// ApprovedChecks lists validated policy in stable order. Missing predicates
// are errors so inspection and dispatch never silently omit approved policy.
func (s *Store) ApprovedChecks() ([]GuardrailCheck, error) {
	rows, err := s.db.Query(
		`SELECT g.id,g.scope,c.lesson_id,c.check_kind,c.check_value FROM guardrails g
		 LEFT JOIN memory_guardrail_checks c ON c.guardrail_id=g.id
		 WHERE g.status='approved' ORDER BY g.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var checks []GuardrailCheck
	for rows.Next() {
		var check GuardrailCheck
		var lesson sql.NullInt64
		var kind, value sql.NullString
		if err := rows.Scan(&check.ID, &check.Scope, &lesson, &kind, &value); err != nil {
			return nil, err
		}
		if !lesson.Valid || !kind.Valid || !value.Valid {
			return nil, fmt.Errorf("taxion: approved guardrail %d has no structured check", check.ID)
		}
		if err := checkScope(check.Scope); err != nil {
			return nil, err
		}
		if err := validateCheck(kind.String, value.String); err != nil {
			return nil, fmt.Errorf("taxion: guardrail %d: %w", check.ID, err)
		}
		check.LessonID = lesson.Int64
		check.Kind, check.Value = kind.String, value.String
		checks = append(checks, check)
	}
	return checks, rows.Err()
}
