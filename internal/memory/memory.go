// Package memory is the SQLite-backed memory store (M6 Memory).
package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
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

// Scopes distinguish repository-specific entries from cross-repository ones.
const (
	ScopeProject = "project"
	ScopeGlobal  = "global"
)

const schema = `
CREATE TABLE IF NOT EXISTS lessons (
  id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT 'project',
  status TEXT NOT NULL DEFAULT 'proposed', created_at TEXT NOT NULL,
  source_refs TEXT NOT NULL DEFAULT '[]', version INTEGER NOT NULL DEFAULT 1,
  supersedes_id INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS decisions (
  id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT 'project',
  status TEXT NOT NULL DEFAULT 'proposed', created_at TEXT NOT NULL,
  source_refs TEXT NOT NULL DEFAULT '[]', version INTEGER NOT NULL DEFAULT 1,
  supersedes_id INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS guardrails (
  id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT 'project',
  status TEXT NOT NULL DEFAULT 'proposed', created_at TEXT NOT NULL,
  source_refs TEXT NOT NULL DEFAULT '[]', version INTEGER NOT NULL DEFAULT 1,
  supersedes_id INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS concerns (
  id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '', scope TEXT NOT NULL DEFAULT 'project',
  status TEXT NOT NULL DEFAULT 'proposed', created_at TEXT NOT NULL,
  source_refs TEXT NOT NULL DEFAULT '[]', version INTEGER NOT NULL DEFAULT 1,
  supersedes_id INTEGER NOT NULL DEFAULT 0, updated_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS memory_entry_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, entry_id INTEGER NOT NULL,
  action TEXT NOT NULL, actor TEXT NOT NULL, reason TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS warning_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT, warning_id INTEGER NOT NULL,
  source_ref TEXT NOT NULL DEFAULT '', occurred_at TEXT NOT NULL,
  occurrence_count INTEGER NOT NULL DEFAULT 1);
`

// Entry is one memory row in any of the four tables.
type Entry struct {
	ID           int64
	Title        string
	Body         string
	Scope        string
	Status       string
	CreatedAt    time.Time
	SourceRefs   []string
	Version      int
	SupersedesID int64
	UpdatedAt    time.Time
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
	if err := migrateEntryColumns(db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(warningsSchema); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrateWarningEvents(db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(promotionSchema); err != nil {
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

func migrateEntryColumns(db *sql.DB) error {
	for _, table := range []string{"lessons", "decisions", "guardrails", "concerns"} {
		columns := map[string]struct{}{}
		rows, err := db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			return err
		}
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var defaultValue sql.NullString
			if err := rows.Scan(&cid, &name, &typ, &notnull, &defaultValue, &pk); err != nil {
				rows.Close()
				return err
			}
			columns[name] = struct{}{}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for name, definition := range map[string]string{
			"source_refs":   "TEXT NOT NULL DEFAULT '[]'",
			"version":       "INTEGER NOT NULL DEFAULT 1",
			"supersedes_id": "INTEGER NOT NULL DEFAULT 0",
			"updated_at":    "TEXT NOT NULL DEFAULT ''",
		} {
			if _, exists := columns[name]; exists {
				continue
			}
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, definition)); err != nil {
				return err
			}
		}
		if _, err := db.Exec(fmt.Sprintf("UPDATE %s SET updated_at=created_at WHERE updated_at=''", table)); err != nil {
			return err
		}
	}
	return nil
}

func migrateWarningEvents(db *sql.DB) error {
	columns := map[string]struct{}{}
	rows, err := db.Query("PRAGMA table_info(warning_events)")
	if err != nil {
		return err
	}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &defaultValue, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if _, exists := columns["occurrence_count"]; !exists {
		if _, err := db.Exec(`ALTER TABLE warning_events ADD COLUMN occurrence_count INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err = tx.Query(`SELECT id,body,scope,count,lesson_id,created_at,updated_at FROM warnings ORDER BY id`)
	if err != nil {
		return err
	}
	type warning struct {
		id, count, lessonID               int64
		body, scope, createdAt, updatedAt string
	}
	var warnings []warning
	for rows.Next() {
		var item warning
		if err := rows.Scan(&item.id, &item.body, &item.scope, &item.count, &item.lessonID,
			&item.createdAt, &item.updatedAt); err != nil {
			rows.Close()
			return err
		}
		warnings = append(warnings, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range warnings {
		var eventID int64
		err := tx.QueryRow(`SELECT id FROM warning_events WHERE warning_id=? ORDER BY id LIMIT 1`, item.id).Scan(&eventID)
		if err == sql.ErrNoRows {
			at := item.updatedAt
			if at == "" {
				at = item.createdAt
			}
			result, err := tx.Exec(
				`INSERT INTO warning_events(warning_id,source_ref,occurred_at,occurrence_count)
				 VALUES(?,'legacy:aggregate-count-only',?,?)`, item.id, at, item.count)
			if err != nil {
				return err
			}
			eventID, err = result.LastInsertId()
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if item.lessonID == 0 {
			continue
		}
		var refsJSON string
		if err := tx.QueryRow(`SELECT source_refs FROM lessons WHERE id=?`, item.lessonID).Scan(&refsJSON); err != nil {
			return err
		}
		var refs []string
		if err := json.Unmarshal([]byte(refsJSON), &refs); err != nil {
			return fmt.Errorf("decode legacy candidate sources: %w", err)
		}
		ref := fmt.Sprintf("warning:%d", eventID)
		if slices.Contains(refs, ref) {
			continue
		}
		refs = append(refs, ref)
		encoded, err := json.Marshal(refs)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lessons SET source_refs=? WHERE id=?`, string(encoded), item.lessonID); err != nil {
			return err
		}
		if err := recordEntryEvent(tx, KindLesson, item.lessonID, "migrated", "system",
			"linked legacy warning aggregate; individual source evidence was not recorded", time.Now().UTC()); err != nil {
			return err
		}
	}
	return tx.Commit()
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

// Validate kind before interpolating a table name; SQL parameters cannot
// bind identifiers.
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

func parseTs(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, fmt.Errorf("empty timestamp")
	}
	return time.Parse(time.RFC3339Nano, v)
}

func validSourceRefs(refs []string) bool {
	seen := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) == "" || ref != strings.TrimSpace(ref) {
			return false
		}
		if _, exists := seen[ref]; exists {
			return false
		}
		seen[ref] = struct{}{}
	}
	return true
}

func scanEntry(row interface{ Scan(...any) error }) (Entry, error) {
	var e Entry
	var createdAt, updatedAt, refs string
	err := row.Scan(&e.ID, &e.Title, &e.Body, &e.Scope, &e.Status, &createdAt,
		&refs, &e.Version, &e.SupersedesID, &updatedAt)
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal([]byte(refs), &e.SourceRefs); err != nil {
		return Entry{}, fmt.Errorf("decode Memory source references: %w", err)
	}
	e.CreatedAt, err = parseTs(createdAt)
	if err != nil {
		return Entry{}, fmt.Errorf("decode Memory creation time: %w", err)
	}
	e.UpdatedAt, err = parseTs(updatedAt)
	if err != nil {
		return Entry{}, fmt.Errorf("decode Memory update time: %w", err)
	}
	return e, nil
}

const entrySelect = `id,title,body,scope,status,created_at,source_refs,version,supersedes_id,updated_at`

func recordEntryEvent(tx *sql.Tx, kind string, id int64, action, actor, reason string, at time.Time) error {
	if actor == "" || reason == "" {
		return fmt.Errorf("taxion: Memory event requires actor and reason")
	}
	_, err := tx.Exec(
		`INSERT INTO memory_entry_events(kind,entry_id,action,actor,reason,created_at) VALUES(?,?,?,?,?,?)`,
		kind, id, action, actor, reason, ts(at))
	return err
}

// Put appends an entry and returns its id. Empty scope defaults to project
// to avoid making repository-specific knowledge global implicitly.
func (s *Store) Put(kind string, e Entry) (int64, error) {
	if err := checkKind(kind); err != nil {
		return 0, err
	}
	if e.Title == "" {
		return 0, fmt.Errorf("taxion: memory title must not be empty")
	}
	if !validSourceRefs(e.SourceRefs) {
		return 0, fmt.Errorf("taxion: Memory source references must be non-empty and unique")
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
	if kind == KindGuardrail && e.Status == "approved" {
		return 0, fmt.Errorf("taxion: approved guardrails require candidate approval with a structured check")
	}
	if e.Status != "proposed" {
		return 0, fmt.Errorf("taxion: new Memory entries must be proposed")
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	e.UpdatedAt = e.CreatedAt
	refs, err := json.Marshal(e.SourceRefs)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(fmt.Sprintf(
		`INSERT INTO %s(title,body,scope,status,created_at,source_refs,version,supersedes_id,updated_at)
		 VALUES(?,?,?,? ,?,?,1,0,?)`, tableFor(kind)),
		e.Title, e.Body, e.Scope, e.Status, ts(e.CreatedAt), string(refs), ts(e.UpdatedAt))
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := recordEntryEvent(tx, kind, id, "created", "cli", "entry created", e.CreatedAt); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// Get reads one entry by id. Missing ids fail closed with ErrNotFound.
func (s *Store) Get(kind string, id int64) (Entry, error) {
	if err := checkKind(kind); err != nil {
		return Entry{}, err
	}
	e, err := scanEntry(s.db.QueryRow(
		fmt.Sprintf(`SELECT %s FROM %s WHERE id=?`, entrySelect, tableFor(kind)), id))
	if err == sql.ErrNoRows {
		return Entry{}, fmt.Errorf("%w: %s %d", ErrNotFound, kind, id)
	}
	if err != nil {
		return Entry{}, err
	}
	return e, nil
}

// List reads a kind table in insertion order. Empty scope lists all;
// otherwise scope must be project|global.
func (s *Store) List(kind, scope string) ([]Entry, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`SELECT %s FROM %s ORDER BY id`, entrySelect, tableFor(kind))
	var args []any
	if scope != "" {
		if err := checkScope(scope); err != nil {
			return nil, err
		}
		q = fmt.Sprintf(`SELECT %s FROM %s WHERE scope=? ORDER BY id`, entrySelect, tableFor(kind))
		args = append(args, scope)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Revise appends a new proposed version and retains the previous content and
// its supersession event. Approved guardrails must be revoked and re-approved.
func (s *Store) Revise(kind string, id int64, next Entry, actor, reason string) (int64, error) {
	if err := checkKind(kind); err != nil {
		return 0, err
	}
	if id < 1 || next.Title == "" || actor == "" || reason == "" || !validSourceRefs(next.SourceRefs) {
		return 0, fmt.Errorf("taxion: revision requires an ID, title, actor, reason, and valid source references")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	old, err := scanEntry(tx.QueryRow(
		fmt.Sprintf(`SELECT %s FROM %s WHERE id=?`, entrySelect, tableFor(kind)), id))
	if err == sql.ErrNoRows {
		return 0, fmt.Errorf("%w: %s %d", ErrNotFound, kind, id)
	}
	if err != nil {
		return 0, err
	}
	if old.Status == "revoked" || old.Status == "superseded" ||
		(kind == KindGuardrail && old.Status == "approved") {
		return 0, fmt.Errorf("taxion: Memory %s %d is not directly revisable", kind, id)
	}
	if next.Scope == "" {
		next.Scope = old.Scope
	}
	if err := checkScope(next.Scope); err != nil {
		return 0, err
	}
	if next.SourceRefs == nil {
		next.SourceRefs = old.SourceRefs
	}
	if !validSourceRefs(next.SourceRefs) {
		return 0, fmt.Errorf("taxion: Memory source references must be non-empty and unique")
	}
	now := time.Now().UTC()
	refs, err := json.Marshal(next.SourceRefs)
	if err != nil {
		return 0, err
	}
	res, err := tx.Exec(fmt.Sprintf(
		`INSERT INTO %s(title,body,scope,status,created_at,source_refs,version,supersedes_id,updated_at)
		 VALUES(?,?,?,'proposed',?,?,?, ?,?)`, tableFor(kind)),
		next.Title, next.Body, next.Scope, ts(now), string(refs), old.Version+1, old.ID, ts(now))
	if err != nil {
		return 0, err
	}
	newID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if kind == KindLesson {
		if _, err := tx.Exec(`UPDATE warnings SET lesson_id=? WHERE lesson_id=?`, newID, old.ID); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(
		`UPDATE %s SET status='superseded',updated_at=? WHERE id=?`, tableFor(kind)), ts(now), old.ID); err != nil {
		return 0, err
	}
	if err := recordEntryEvent(tx, kind, old.ID, "superseded", actor, reason, now); err != nil {
		return 0, err
	}
	if kind == KindLesson {
		if err := revokeLinkedGuardrail(tx, old.ID, actor,
			fmt.Sprintf("source lesson superseded: %s", reason), now); err != nil {
			return 0, err
		}
	}
	if err := recordEntryEvent(tx, kind, newID, "created", actor, fmt.Sprintf("revision of %s:%d: %s", kind, old.ID, reason), now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return newID, nil
}

// Revoke disables an entry without deleting its content or audit history.
func (s *Store) Revoke(kind string, id int64, actor, reason string) error {
	if err := checkKind(kind); err != nil {
		return err
	}
	if id < 1 || actor == "" || reason == "" {
		return fmt.Errorf("taxion: revocation requires an ID, actor, and reason")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	e, err := scanEntry(tx.QueryRow(
		fmt.Sprintf(`SELECT %s FROM %s WHERE id=?`, entrySelect, tableFor(kind)), id))
	if err == sql.ErrNoRows {
		return fmt.Errorf("%w: %s %d", ErrNotFound, kind, id)
	}
	if err != nil {
		return err
	}
	if e.Status == "revoked" || e.Status == "superseded" {
		return fmt.Errorf("taxion: Memory %s %d is already inactive", kind, id)
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(fmt.Sprintf(
		`UPDATE %s SET status='revoked',updated_at=? WHERE id=?`, tableFor(kind)), ts(now), id); err != nil {
		return err
	}
	if err := recordEntryEvent(tx, kind, id, "revoked", actor, reason, now); err != nil {
		return err
	}
	if kind == KindLesson {
		if err := revokeLinkedGuardrail(tx, id, actor,
			fmt.Sprintf("source lesson revoked: %s", reason), now); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func revokeLinkedGuardrail(tx *sql.Tx, lessonID int64, actor, reason string, now time.Time) error {
	var guardrailID int64
	err := tx.QueryRow(
		`SELECT g.id FROM guardrails g JOIN memory_guardrail_checks c ON c.guardrail_id=g.id
		 WHERE c.lesson_id=? AND g.status='approved'`, lessonID).Scan(&guardrailID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE guardrails SET status='revoked',updated_at=? WHERE id=?`,
		ts(now), guardrailID); err != nil {
		return err
	}
	return recordEntryEvent(tx, KindGuardrail, guardrailID, "revoked", actor, reason, now)
}

// ResolveReference validates a stable kind:id Task reference and rejects stale
// records so a revoked or superseded decision cannot silently drive a dispatch.
func (s *Store) ResolveReference(ref string) (Entry, error) {
	kind, idText, ok := strings.Cut(ref, ":")
	if !ok {
		return Entry{}, fmt.Errorf("taxion: invalid Memory reference %q (want kind:id)", ref)
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || fmt.Sprintf("%s:%d", kind, id) != ref || id < 1 {
		return Entry{}, fmt.Errorf("taxion: invalid Memory reference %q (want kind:id)", ref)
	}
	e, err := s.Get(kind, id)
	if err != nil {
		return Entry{}, err
	}
	if e.Status == "revoked" || e.Status == "superseded" ||
		(kind == KindGuardrail && e.Status != "approved") {
		return Entry{}, fmt.Errorf("taxion: Memory reference %s is not active", ref)
	}
	return e, nil
}

type EntryEvent struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	EntryID   int64     `json:"entryId"`
	Action    string    `json:"action"`
	Actor     string    `json:"actor"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"createdAt"`
}

// Events returns the append-only audit trail for one entry.
func (s *Store) Events(kind string, id int64) ([]EntryEvent, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(
		`SELECT id,kind,entry_id,action,actor,reason,created_at FROM memory_entry_events
		 WHERE kind=? AND entry_id=? ORDER BY id`, kind, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []EntryEvent
	for rows.Next() {
		var event EntryEvent
		var created string
		if err := rows.Scan(&event.ID, &event.Kind, &event.EntryID, &event.Action,
			&event.Actor, &event.Reason, &created); err != nil {
			return nil, err
		}
		event.CreatedAt, err = parseTs(created)
		if err != nil {
			return nil, fmt.Errorf("decode Memory event time: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
