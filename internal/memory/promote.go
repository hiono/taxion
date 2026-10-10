package memory

import (
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
	if count >= threshold && lesson == 0 {
		// Reuse the recorded candidate on later warnings to avoid duplicate
		// proposals. A proposal is not an automatically enforced guardrail.
		title := body
		if len([]rune(title)) > 80 {
			title = string([]rune(title)[:80]) + "…"
		}
		// The counter and candidate link must commit together; a failed proposal
		// must not leave a counted warning or an unlinked duplicate lesson.
		res, err := tx.Exec(
			`INSERT INTO lessons(title,body,scope,status,created_at) VALUES(?,?,?,'proposed',?)`,
			"guideline candidate: "+title, body, scope, now)
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
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
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
