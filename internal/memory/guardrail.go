package memory

import (
	"database/sql"
	"encoding/json"
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
	candidate, err := scanEntry(tx.QueryRow(
		`SELECT `+entrySelect+` FROM lessons
		 WHERE id=? AND EXISTS(SELECT 1 FROM warnings WHERE lesson_id=lessons.id)`,
		lessonID))
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("%w: warning candidate %d", ErrNotFound, lessonID)
	}
	if err != nil {
		return 0, err
	}
	if candidate.Status != "proposed" {
		return 0, fmt.Errorf("taxion: candidate %d is not proposed", lessonID)
	}
	if err := checkScope(candidate.Scope); err != nil {
		return 0, err
	}
	refs, err := json.Marshal(candidate.SourceRefs)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	res, err := tx.Exec(
		`INSERT INTO guardrails(title,body,scope,status,created_at,source_refs,version,supersedes_id,updated_at)
		 VALUES(?,?,?,'approved',?,?,1,0,?)`,
		candidate.Title, candidate.Body, candidate.Scope, ts(now), string(refs), ts(now))
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
	if _, err := tx.Exec(`UPDATE lessons SET status='approved',updated_at=? WHERE id=?`, ts(now), lessonID); err != nil {
		return 0, err
	}
	if err := recordEntryEvent(tx, KindLesson, lessonID, "approved", "human",
		fmt.Sprintf("guardrail %d approved with %s=%s", id, kind, value), now); err != nil {
		return 0, err
	}
	if err := recordEntryEvent(tx, KindGuardrail, id, "approved", "human",
		fmt.Sprintf("candidate lesson %d approved with %s=%s", lessonID, kind, value), now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// CheckReferences rejects missing, revoked, or superseded records before
// dispatch. Task Graph remains authoritative for the list of references.
func (s *Store) CheckReferences(refs []string) error {
	for _, ref := range refs {
		if _, err := s.ResolveReference(ref); err != nil {
			return fmt.Errorf("taxion: resolve Task Memory reference %q: %w", ref, err)
		}
	}
	return nil
}

// CheckDispatch evaluates all approved checks in both workspace scopes.
// Absence preserves pre-Memory workspaces; unreadable or malformed policy
// fails explicitly rather than silently disabling enforcement.
func CheckDispatch(ledgerPath string, task core.Task, repo string, refs ...string) error {
	path := PathForLedger(ledgerPath)
	if _, err := os.Lstat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if len(refs) != 0 {
				return fmt.Errorf("taxion: Task has Memory references but workspace Memory database is missing")
			}
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
	if err := s.CheckReferences(refs); err != nil {
		return err
	}
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

// ResolveDispatchContext captures the explicitly referenced Memory entries
// with their provenance and approved guardrail predicates.
func ResolveDispatchContext(ledgerPath string, refs []string) ([]core.DispatchMemoryEntry, error) {
	return resolveDispatchContext(ledgerPath, refs, false)
}

// ResolveDispatchContextReadOnly observes referenced entries without migrations.
func ResolveDispatchContextReadOnly(ledgerPath string, refs []string) ([]core.DispatchMemoryEntry, error) {
	return resolveDispatchContext(ledgerPath, refs, true)
}

func resolveDispatchContext(ledgerPath string, refs []string, readOnly bool) ([]core.DispatchMemoryEntry, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	path := PathForLedger(ledgerPath)
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("taxion: inspect Memory context: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("taxion: Memory context is not a regular database file")
	}
	var s *Store
	if readOnly {
		s, err = OpenReadOnly(path)
	} else {
		s, err = Open(path)
	}
	if err != nil {
		return nil, fmt.Errorf("taxion: open Memory context: %w", err)
	}
	defer s.Close()
	return s.DispatchContext(refs)
}

// DispatchContext resolves each Task reference in order and captures its
// immutable content, version, sources, and structured guardrail check.
func (s *Store) DispatchContext(refs []string) ([]core.DispatchMemoryEntry, error) {
	checks, err := s.ApprovedChecks()
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]GuardrailCheck, len(checks))
	for _, check := range checks {
		byID[check.ID] = check
	}
	entries := make([]core.DispatchMemoryEntry, 0, len(refs))
	for _, ref := range refs {
		kind, id, err := parseReference(ref)
		if err != nil {
			return nil, err
		}
		entry, err := s.ResolveReference(ref)
		if err != nil {
			return nil, err
		}
		if len(entry.SourceRefs) == 0 {
			return nil, fmt.Errorf("taxion: Memory reference %s has no source references", ref)
		}
		snapshot := core.DispatchMemoryEntry{
			Reference: ref, Version: entry.Version, Scope: entry.Scope, Status: entry.Status,
			Title: entry.Title, Body: entry.Body, SourceRefs: append([]string(nil), entry.SourceRefs...),
		}
		if kind == KindGuardrail {
			check, ok := byID[id]
			if !ok {
				return nil, fmt.Errorf("taxion: approved guardrail %s has no structured check", ref)
			}
			snapshot.Guardrail = &core.DispatchMemoryCheck{
				Kind: check.Kind, Value: check.Value,
				SourceLessonReference: fmt.Sprintf("%s:%d", KindLesson, check.LessonID),
			}
		}
		entries = append(entries, snapshot)
	}
	return entries, nil
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
