package memory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PromoteAfter is the default occurrence count for proposing a guideline.
const PromoteAfter = 3

const warningsSchema = `
CREATE TABLE IF NOT EXISTS warnings (
  id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL,
  scope TEXT NOT NULL DEFAULT 'project', count INTEGER NOT NULL DEFAULT 1,
  lesson_id INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_warnings_body_scope ON warnings(body, scope);
CREATE UNIQUE INDEX IF NOT EXISTS idx_warnings_unique ON warnings(body, scope);
`

type WarningOccurrence struct {
	ID              int64     `json:"id"`
	Body            string    `json:"body"`
	Scope           string    `json:"scope"`
	SourceRef       string    `json:"sourceRef,omitempty"`
	OccurredAt      time.Time `json:"occurredAt"`
	OccurrenceCount int       `json:"occurrenceCount"`
}

// WarningOccurrence resolves a warning:<id> provenance reference.
func (s *Store) WarningOccurrence(id int64) (WarningOccurrence, error) {
	var event WarningOccurrence
	var occurred string
	err := s.db.QueryRow(
		`SELECT e.id,w.body,w.scope,e.source_ref,e.occurred_at,e.occurrence_count
		 FROM warning_events e JOIN warnings w ON w.id=e.warning_id WHERE e.id=?`, id).
		Scan(&event.ID, &event.Body, &event.Scope, &event.SourceRef, &occurred, &event.OccurrenceCount)
	if errors.Is(err, sql.ErrNoRows) {
		return WarningOccurrence{}, fmt.Errorf("%w: warning occurrence %d", ErrNotFound, id)
	}
	if err != nil {
		return WarningOccurrence{}, err
	}
	event.OccurredAt, err = parseTs(occurred)
	if err != nil {
		return WarningOccurrence{}, fmt.Errorf("decode warning occurrence time: %w", err)
	}
	return event, nil
}

// Warn records one warning occurrence and returns the running count.
// It returns the candidate lesson id once one exists, including on later
// repeats (0 before promotion). Empty scope defaults to project.
func (s *Store) Warn(body, scope string) (count int, lessonID int64, err error) {
	return s.WarnWithSource(body, scope, "")
}

// WarnWithSource appends an occurrence to the warning log and links an
// automatically proposed lesson back to every recorded occurrence.
func (s *Store) WarnWithSource(body, scope, sourceRef string) (count int, lessonID int64, err error) {
	if body == "" {
		return 0, 0, fmt.Errorf("taxion: warning body must not be empty")
	}
	if sourceRef != "" && !validSourceRefs([]string{sourceRef}) {
		return 0, 0, fmt.Errorf("taxion: warning source reference must not be blank")
	}
	if scope == "" {
		scope = ScopeProject
	}
	if err := checkScope(scope); err != nil {
		return 0, 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	now := ts(time.Now().UTC())
	var threshold int
	if err := tx.QueryRow(`SELECT promote_after FROM memory_settings WHERE id=1`).Scan(&threshold); err != nil {
		return 0, 0, err
	}
	if threshold < 1 {
		return 0, 0, fmt.Errorf("taxion: invalid Memory promotion threshold %d", threshold)
	}
	var id int64
	var lesson int64
	if err := tx.QueryRow(
		`INSERT INTO warnings(body,scope,count,created_at,updated_at) VALUES(?,?,1,?,?)
		 ON CONFLICT(body,scope) DO UPDATE SET count=count+1,updated_at=excluded.updated_at
		 RETURNING id,count,lesson_id`, body, scope, now, now).Scan(&id, &count, &lesson); err != nil {
		return 0, 0, err
	}
	if _, err := tx.Exec(`INSERT INTO warning_events(warning_id,source_ref,occurred_at) VALUES(?,?,?)`,
		id, sourceRef, now); err != nil {
		return 0, 0, err
	}
	if count >= threshold && lesson == 0 {
		// Reuse the recorded candidate on later warnings to avoid duplicate
		// proposals. A proposal is not an automatically enforced guardrail.
		title := body
		if len([]rune(title)) > 80 {
			title = string([]rune(title)[:80]) + "…"
		}
		rows, err := tx.Query(`SELECT id,source_ref FROM warning_events WHERE warning_id=? ORDER BY id`, id)
		if err != nil {
			return 0, 0, err
		}
		refs := make([]string, 0, count*2)
		for rows.Next() {
			var occurrenceID int64
			var source string
			if err := rows.Scan(&occurrenceID, &source); err != nil {
				rows.Close()
				return 0, 0, err
			}
			refs = append(refs, fmt.Sprintf("warning:%d", occurrenceID))
			if source != "" {
				refs = append(refs, source)
			}
		}
		if err := rows.Close(); err != nil {
			return 0, 0, err
		}
		refs = uniqueSourceRefs(refs)
		encodedRefs, err := json.Marshal(refs)
		if err != nil {
			return 0, 0, err
		}
		// The counter and candidate link must commit together; a failed proposal
		// must not leave a counted warning or an unlinked duplicate lesson.
		res, err := tx.Exec(
			`INSERT INTO lessons(title,body,scope,status,created_at,source_refs,version,supersedes_id,updated_at)
			 VALUES(?,?,?,'proposed',?,?,1,0,?)`,
			"guideline candidate: "+title, body, scope, now, string(encodedRefs), now, now)
		if err != nil {
			return 0, 0, err
		}
		lesson, err = res.LastInsertId()
		if err != nil {
			return 0, 0, err
		}
		if _, err := tx.Exec(`UPDATE warnings SET lesson_id=? WHERE id=?`, lesson, id); err != nil {
			return 0, 0, err
		}
		if err := recordEntryEvent(tx, KindLesson, lesson, "created", "system",
			fmt.Sprintf("warning threshold reached for warning %d", id), time.Now().UTC()); err != nil {
			return 0, 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return count, lesson, nil
}

func uniqueSourceRefs(refs []string) []string {
	seen := make(map[string]bool, len(refs))
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref != "" && !seen[ref] {
			seen[ref] = true
			out = append(out, ref)
		}
	}
	return out
}

// WarningCount returns the current count for a (body, scope). Missing rows
// and query errors both return 0; callers cannot use it to prove database health.
func (s *Store) WarningCount(body, scope string) int {
	var n int
	if err := s.db.QueryRow(
		`SELECT count FROM warnings WHERE body=? AND scope=?`, body, scope).Scan(&n); err != nil {
		return 0
	}
	return n
}
