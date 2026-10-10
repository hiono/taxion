// Package memory is the SQLite-backed memory store (M6 Memory).
//
// Memory starts on SQLite, never file-first: lessons, decisions, guardrails,
// and concerns live in four tables with a shared shape. Raw warnings land in
// concerns (counted); distilled knowledge lands in lessons/decisions;
// human-approved, dispatch-checked rules land in guardrails. The promotion
// path between them (docs/08-memory-model.md, ADR-0017) is M6-3; this
// package is the durable floor it stands on.
//
// Pure-Go driver (modernc.org/sqlite, same as internal/store) — no cgo.
package memory

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when an entry id does not exist.
var ErrNotFound = errors.New("taxion: memory entry not found")

// Kinds are the four memory tables.
const (
	KindLesson    = "lesson"
	KindDecision  = "decision"
	KindGuardrail = "guardrail"
	KindConcern   = "concern"
)

// Scopes are the memory ranges (docs/08).
const (
	ScopeProject = "project"
	ScopeGlobal  = "global"
)

const schema = `
CREATE TABLE IF NOT EXISTS lessons (
  id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT 'project',
  status TEXT NOT NULL DEFAULT 'proposed', created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS decisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT 'project',
  status TEXT NOT NULL DEFAULT 'proposed', created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS guardrails (
  id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT 'project',
  status TEXT NOT NULL DEFAULT 'proposed', created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS concerns (
  id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT 'project',
  status TEXT NOT NULL DEFAULT 'proposed', created_at TEXT NOT NULL);
`

// Entry is one memory row in any of the four tables.
type Entry struct {
	ID        int64
	Title     string
	Body      string
	Scope     string
	Status    string
	CreatedAt time.Time
}

// Store is a SQLite-backed memory database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the memory file at path and migrates the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(warningsSchema); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.ensureFTS(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

func checkKind(kind string) error {
	switch kind {
	case KindLesson, KindDecision, KindGuardrail, KindConcern:
		return nil
	}
	return fmt.Errorf("taxion: unknown memory kind %q (lesson|decision|guardrail|concern)", kind)
}

// tableFor maps a kind to its table. checkKind must pass first.
func tableFor(kind string) string {
	return kind + "s"
}

func checkScope(scope string) error {
	switch scope {
	case ScopeProject, ScopeGlobal:
		return nil
	}
	return fmt.Errorf("taxion: unknown memory scope %q (project|global)", scope)
}

func ts(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTs(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

// Put appends an entry to the kind table and returns its id.
// Empty titles, unknown kinds, and unknown scopes fail closed.
func (s *Store) Put(kind string, e Entry) (int64, error) {
	if err := checkKind(kind); err != nil {
		return 0, err
	}
	if e.Title == "" {
		return 0, fmt.Errorf("taxion: memory title must not be empty")
	}
	if e.Scope == "" {
		e.Scope = ScopeProject
	}
	if err := checkScope(e.Scope); err != nil {
		return 0, err
	}
	if e.Status == "" {
		e.Status = "proposed"
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	res, err := s.db.Exec(fmt.Sprintf(
		`INSERT INTO %s(title,body,scope,status,created_at) VALUES(?,?,?,?,?)`, tableFor(kind)),
		e.Title, e.Body, e.Scope, e.Status, ts(e.CreatedAt))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Get reads one entry by id. Missing ids fail closed with ErrNotFound.
func (s *Store) Get(kind string, id int64) (Entry, error) {
	var e Entry
	if err := checkKind(kind); err != nil {
		return e, err
	}
	var created string
	err := s.db.QueryRow(fmt.Sprintf(
		`SELECT id,title,body,scope,status,created_at FROM %s WHERE id=?`, tableFor(kind)), id).
		Scan(&e.ID, &e.Title, &e.Body, &e.Scope, &e.Status, &created)
	if err == sql.ErrNoRows {
		return e, fmt.Errorf("%w: %s %d", ErrNotFound, kind, id)
	}
	if err != nil {
		return e, err
	}
	e.CreatedAt = parseTs(created)
	return e, nil
}

// List reads a kind table in insertion order. Empty scope lists all;
// otherwise scope must be project|global.
func (s *Store) List(kind, scope string) ([]Entry, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`SELECT id,title,body,scope,status,created_at FROM %s ORDER BY id`, tableFor(kind))
	var args []any
	if scope != "" {
		if err := checkScope(scope); err != nil {
			return nil, err
		}
		q = fmt.Sprintf(`SELECT id,title,body,scope,status,created_at FROM %s WHERE scope=? ORDER BY id`, tableFor(kind))
		args = append(args, scope)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var created string
		if err := rows.Scan(&e.ID, &e.Title, &e.Body, &e.Scope, &e.Status, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTs(created)
		out = append(out, e)
	}
	return out, rows.Err()
}
