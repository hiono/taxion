// Promotion implements the Nth-warning → guideline path (M6-3).
//
// Raw repeated warnings land in the warnings table, counted by exact
// (body, scope) match. When a warning's count reaches PromoteAfter, the
// store distills it into a lesson with status "proposed" — a guideline
// candidate. Guideline → guardrail stays a human decision (out of scope:
// nothing here writes guardrails).
//
// Promotion fires exactly once per (body, scope): the warning row records
// the lesson id, later repeats only bump the count.
package memory

import (
	"database/sql"
	"fmt"
	"time"
)

// PromoteAfter is the warning count that distills a guideline candidate.
const PromoteAfter = 3

const warningsSchema = `
CREATE TABLE IF NOT EXISTS warnings (
  id INTEGER PRIMARY KEY AUTOINCREMENT, body TEXT NOT NULL,
  scope TEXT NOT NULL DEFAULT 'project', count INTEGER NOT NULL DEFAULT 1,
  lesson_id INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS idx_warnings_body_scope ON warnings(body, scope);
`

// Warn records one warning occurrence and returns the running count.
// On reaching PromoteAfter it also returns the new guideline-candidate
// lesson id (0 when not promoted by this call). Empty bodies fail closed;
// empty scope defaults to project.
func (s *Store) Warn(body, scope string) (count int, lessonID int64, err error) {
	if body == "" {
		return 0, 0, fmt.Errorf("taxion: warning body must not be empty")
	}
	if scope == "" {
		scope = ScopeProject
	}
	if err := checkScope(scope); err != nil {
		return 0, 0, err
	}
	now := ts(time.Now().UTC())
	var id int64
	var lesson int64
	var created string
	qerr := s.db.QueryRow(
		`SELECT id, count, lesson_id, created_at FROM warnings WHERE body=? AND scope=?`,
		body, scope).Scan(&id, &count, &lesson, &created)
	switch qerr {
	case sql.ErrNoRows:
		res, err := s.db.Exec(
			`INSERT INTO warnings(body,scope,count,created_at,updated_at) VALUES(?,?,1,?,?)`,
			body, scope, now, now)
		if err != nil {
			return 0, 0, err
		}
		if _, err := res.LastInsertId(); err != nil {
			return 0, 0, err
		}
		count = 1
	case nil:
		count++
		if _, err := s.db.Exec(
			`UPDATE warnings SET count=?, updated_at=? WHERE id=?`, count, now, id); err != nil {
			return 0, 0, err
		}
	default:
		return 0, 0, qerr
	}
	if count >= PromoteAfter && lesson == 0 {
		title := body
		if len([]rune(title)) > 80 {
			title = string([]rune(title)[:80]) + "…"
		}
		lid, err := s.Put(KindLesson, Entry{
			Title: "guideline candidate: " + title, Body: body, Scope: scope, Status: "proposed",
		})
		if err != nil {
			return count, 0, err
		}
		if id == 0 {
			var wrow int64
			if err := s.db.QueryRow(
				`SELECT id FROM warnings WHERE body=? AND scope=?`, body, scope).Scan(&wrow); err != nil {
				return count, lid, err
			}
			id = wrow
		}
		if _, err := s.db.Exec(`UPDATE warnings SET lesson_id=? WHERE id=?`, lid, id); err != nil {
			return count, lid, err
		}
		return count, lid, nil
	}
	return count, lesson, nil
}

// WarningCount returns the current count for a (body, scope), or 0.
func (s *Store) WarningCount(body, scope string) int {
	var n int
	if err := s.db.QueryRow(
		`SELECT count FROM warnings WHERE body=? AND scope=?`, body, scope).Scan(&n); err != nil {
		return 0
	}
	return n
}
