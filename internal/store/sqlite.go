// Package store provides the sqlite-backed durable ledger (Server phase).
//
// It preserves the core.Ledger shape (LED-001): the JSON file backend and
// this sqlite backend are interchangeable at the Ledger level. Pure-Go
// driver (modernc.org/sqlite) — no cgo toolchain required.
//
// The outbox table implements the outbox pattern for future server delivery:
// writers enqueue events in the same transaction as state changes; a relay
// claims unsent rows and marks them sent after delivery.
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/hiono/taxion/internal/core"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS tasks (
  id TEXT PRIMARY KEY, title TEXT NOT NULL,
  repo TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS dispatches (
  id TEXT PRIMARY KEY, task_id TEXT NOT NULL,
  repo TEXT NOT NULL DEFAULT '', active INTEGER NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS evidence (
  id INTEGER PRIMARY KEY AUTOINCREMENT, sha TEXT NOT NULL,
  task_id TEXT NOT NULL, created_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS checkpoints (
  name TEXT PRIMARY KEY, done INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS outbox (
  id INTEGER PRIMARY KEY AUTOINCREMENT, type TEXT NOT NULL,
  payload TEXT NOT NULL, created_at TEXT NOT NULL, sent_at TEXT);
`

// Store is a sqlite-backed ledger.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the sqlite file at path and migrates the schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

func ts(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTs(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

// SaveLedger replaces all rows with the given ledger in one transaction.
func (s *Store) SaveLedger(l core.Ledger) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"tasks", "dispatches", "evidence", "checkpoints"} {
		if _, err := tx.Exec(fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			return err
		}
	}
	for _, t := range l.Tasks {
		if _, err := tx.Exec(`INSERT INTO tasks(id,title,repo,created_at) VALUES(?,?,?,?)`,
			t.ID, t.Title, t.Repo, ts(t.CreatedAt)); err != nil {
			return err
		}
	}
	for _, d := range l.Dispatches {
		active := 0
		if d.Active {
			active = 1
		}
		if _, err := tx.Exec(`INSERT INTO dispatches(id,task_id,repo,active,created_at) VALUES(?,?,?,?,?)`,
			d.ID, d.TaskID, d.Repo, active, ts(d.CreatedAt)); err != nil {
			return err
		}
	}
	for _, e := range l.Evidence {
		if _, err := tx.Exec(`INSERT INTO evidence(sha,task_id,created_at) VALUES(?,?,?)`,
			e.SHA, e.TaskID, ts(e.CreatedAt)); err != nil {
			return err
		}
	}
	for _, c := range l.Checkpoints {
		done := 0
		if c.Done {
			done = 1
		}
		if _, err := tx.Exec(`INSERT INTO checkpoints(name,done) VALUES(?,?)`, c.Name, done); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// LoadLedger reads the full ledger back in insertion order.
func (s *Store) LoadLedger() (core.Ledger, error) {
	var l core.Ledger
	rows, err := s.db.Query(`SELECT id,title,repo,created_at FROM tasks ORDER BY rowid`)
	if err != nil {
		return l, err
	}
	for rows.Next() {
		var t core.Task
		var created string
		if err := rows.Scan(&t.ID, &t.Title, &t.Repo, &created); err != nil {
			rows.Close()
			return l, err
		}
		t.CreatedAt = parseTs(created)
		l.Tasks = append(l.Tasks, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return l, err
	}

	drows, err := s.db.Query(`SELECT id,task_id,repo,active,created_at FROM dispatches ORDER BY rowid`)
	if err != nil {
		return l, err
	}
	for drows.Next() {
		var d core.Dispatch
		var created string
		var active int
		if err := drows.Scan(&d.ID, &d.TaskID, &d.Repo, &active, &created); err != nil {
			drows.Close()
			return l, err
		}
		d.Active = active != 0
		d.CreatedAt = parseTs(created)
		l.Dispatches = append(l.Dispatches, d)
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return l, err
	}

	erows, err := s.db.Query(`SELECT sha,task_id,created_at FROM evidence ORDER BY id`)
	if err != nil {
		return l, err
	}
	for erows.Next() {
		var e core.Evidence
		var created string
		if err := erows.Scan(&e.SHA, &e.TaskID, &created); err != nil {
			erows.Close()
			return l, err
		}
		e.CreatedAt = parseTs(created)
		l.Evidence = append(l.Evidence, e)
	}
	erows.Close()
	if err := erows.Err(); err != nil {
		return l, err
	}

	crows, err := s.db.Query(`SELECT name,done FROM checkpoints ORDER BY rowid`)
	if err != nil {
		return l, err
	}
	for crows.Next() {
		var c core.Checkpoint
		var done int
		if err := crows.Scan(&c.Name, &done); err != nil {
			crows.Close()
			return l, err
		}
		c.Done = done != 0
		l.Checkpoints = append(l.Checkpoints, c)
	}
	crows.Close()
	return l, crows.Err()
}

// OutboxEntry is one undelivered outbox row.
type OutboxEntry struct {
	ID        int64
	Type      string
	Payload   string
	CreatedAt time.Time
}

// Enqueue records an event for later relay delivery.
func (s *Store) Enqueue(typ, payload string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO outbox(type,payload,created_at) VALUES(?,?,?)`,
		typ, payload, ts(time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Claim returns up to limit unsent entries in FIFO order.
func (s *Store) Claim(limit int) ([]OutboxEntry, error) {
	rows, err := s.db.Query(`SELECT id,type,payload,created_at FROM outbox WHERE sent_at IS NULL ORDER BY id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OutboxEntry
	for rows.Next() {
		var e OutboxEntry
		var created string
		if err := rows.Scan(&e.ID, &e.Type, &e.Payload, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTs(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkSent records successful relay delivery of an entry.
func (s *Store) MarkSent(id int64) error {
	_, err := s.db.Exec(`UPDATE outbox SET sent_at=? WHERE id=?`, ts(time.Now().UTC()), id)
	return err
}
