package memory

import (
	"database/sql"
	"fmt"
	"time"
)

// PromoteAfter is the fixed occurrence count for proposing a guideline.
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
// It returns the candidate lesson id once one exists, including on later
// repeats (0 before promotion). Empty scope defaults to project.
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
		// Reuse the recorded candidate on later warnings to avoid duplicate
		// proposals. A proposal is not an automatically enforced guardrail.
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
