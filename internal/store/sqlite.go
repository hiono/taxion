// Package store provides SQLite-backed workspace state and the durable ledger.
//
// The schema preserves core.Ledger so changing persistence does not change
// governance types. The pure-Go driver avoids a cgo build requirement
// (ADR-0009). Record storage preserves the existing Ledger JSON wire shape.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync"
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
CREATE TABLE IF NOT EXISTS workspace_state (
  id INTEGER PRIMARY KEY CHECK (id = 1),
  revision INTEGER NOT NULL,
  ledger_json TEXT NOT NULL,
  graph_json TEXT NOT NULL,
  graph_present INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS workspace_records (
  id INTEGER PRIMARY KEY CHECK (id = 1), records_json TEXT NOT NULL);
`

// Store is a sqlite-backed ledger.
type Store struct {
	db          *sql.DB
	mu          sync.Mutex
	operationMu sync.Mutex
}

type Workspace struct {
	Ledger   core.Ledger
	Graph    core.TaskGraph
	HasGraph bool
	Records  Records
}

// Open opens (creating if needed) the sqlite file at path and migrates the schema.
func Open(path string) (*Store, error) {
	location, err := workspaceLocation(path)
	if err != nil {
		return nil, err
	}
	query := location.Query()
	query.Set("_txlock", "immediate")
	query.Add("_pragma", "busy_timeout(5000)")
	location.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema + documentSchema); err != nil {
		db.Close()
		return nil, err
	}
	result := &Store{db: db}
	if err := result.migrateDocuments(); err != nil {
		db.Close()
		return nil, err
	}
	return result, nil
}

func workspaceLocation(path string) (*url.URL, error) {
	if strings.HasPrefix(path, "file:") {
		return url.Parse(path)
	}
	if path == ":memory:" {
		return &url.URL{Scheme: "file", Opaque: ":memory:"}, nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return absoluteWorkspaceLocation(absolute, runtime.GOOS == "windows"), nil
}

func absoluteWorkspaceLocation(absolute string, windows bool) *url.URL {
	if windows {
		absolute = strings.ReplaceAll(absolute, `\`, "/")
		if !strings.HasPrefix(absolute, "/") {
			absolute = "/" + absolute
		}
	}
	return &url.URL{Scheme: "file", Path: absolute}
}

// OpenReadOnly observes existing state without creating or migrating a workspace.
func OpenReadOnly(path string) (*Store, error) {
	location, err := workspaceLocation(path)
	if err != nil {
		return nil, err
	}
	query := location.Query()
	query.Set("mode", "ro")
	query.Add("_pragma", "busy_timeout(5000)")
	location.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) LoadWorkspace() (Workspace, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Workspace{}, err
	}
	defer tx.Rollback()
	state, err := s.loadWorkspace(tx)
	if err != nil {
		return Workspace{}, err
	}
	return state, tx.Commit()
}

func (s *Store) WorkspaceInitialized() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var exists int
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM workspace_state WHERE id=1)`).Scan(&exists)
	return exists != 0, err
}

func (s *Store) InitializeWorkspace(state Workspace) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := state.Records.validate(); err != nil {
		return false, err
	}
	if err := state.Records.VerifyFlowAudits(state.Graph); err != nil {
		return false, err
	}
	if state.HasGraph {
		if err := state.Graph.Validate(); err != nil {
			return false, err
		}
		if err := state.Graph.CheckLedger(state.Ledger); err != nil {
			return false, err
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`INSERT INTO workspace_state(id,revision,ledger_json,graph_json,graph_present)
		VALUES(1,1,'{}','{}',?) ON CONFLICT(id) DO NOTHING`, boolInt(state.HasGraph))
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if inserted == 1 {
		if _, err := tx.Exec(`INSERT INTO workspace_records(id,records_json) VALUES(1,'{}')`); err != nil {
			return false, err
		}
		if err := saveWorkspaceDocuments(tx, state); err != nil {
			return false, err
		}
	}
	return inserted == 1, tx.Commit()
}

func (s *Store) loadWorkspace(q workspaceReader) (Workspace, error) {
	var state Workspace
	var ledgerJSON, graphJSON string
	var present int
	err := q.QueryRow(`SELECT ledger_json,graph_json,graph_present FROM workspace_state WHERE id=1`).
		Scan(&ledgerJSON, &graphJSON, &present)
	if err == sql.ErrNoRows {
		return Workspace{Ledger: core.Ledger{Tasks: []core.Task{}, Dispatches: []core.Dispatch{}, Evidence: []core.Evidence{}, Checkpoints: []core.Checkpoint{}}}, nil
	}
	if err != nil {
		return Workspace{}, err
	}
	normalized, err := documentFormat(q)
	if err != nil {
		return Workspace{}, err
	}
	if normalized {
		var legacyRecords string
		if err := q.QueryRow(`SELECT records_json FROM workspace_records WHERE id=1`).Scan(&legacyRecords); err != nil {
			return Workspace{}, err
		}
		if ledgerJSON != "{}" || graphJSON != "{}" || legacyRecords != "{}" {
			return Workspace{}, fmt.Errorf("normalized workspace has conflicting legacy state")
		}
		if err := loadWorkspaceDocuments(q, &state); err != nil {
			return Workspace{}, err
		}
		state.HasGraph = present != 0
		if state.HasGraph {
			if err := state.Graph.Validate(); err != nil {
				return Workspace{}, err
			}
			if err := state.Graph.CheckLedger(state.Ledger); err != nil {
				return Workspace{}, err
			}
		}
		if err := state.Records.VerifyFlowAudits(state.Graph); err != nil {
			return Workspace{}, err
		}
		return state, nil
	}
	if err := json.Unmarshal([]byte(ledgerJSON), &state.Ledger); err != nil {
		return Workspace{}, fmt.Errorf("decode workspace ledger: %w", err)
	}
	if err := json.Unmarshal([]byte(graphJSON), &state.Graph); err != nil {
		return Workspace{}, fmt.Errorf("decode workspace graph: %w", err)
	}
	state.HasGraph = present != 0
	if state.HasGraph {
		if err := state.Graph.Validate(); err != nil {
			return Workspace{}, err
		}
		if err := state.Graph.CheckLedger(state.Ledger); err != nil {
			return Workspace{}, err
		}
	}
	var recordsJSON string
	err = q.QueryRow(`SELECT records_json FROM workspace_records WHERE id=1`).Scan(&recordsJSON)
	if err != nil && err != sql.ErrNoRows {
		return Workspace{}, err
	}
	if err == nil {
		state.Records, err = decodeRecords(recordsJSON)
		if err != nil {
			return Workspace{}, err
		}
	}
	if err := state.Records.VerifyFlowAudits(state.Graph); err != nil {
		return Workspace{}, err
	}
	return state, nil
}

// UpdateWorkspace commits the ledger projection and Task Graph in one SQLite
// transaction. The callback must not perform external side effects.
func (s *Store) UpdateWorkspace(update func(*Workspace) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := s.loadWorkspace(tx)
	if err != nil {
		return err
	}
	archives := slices.Clone(state.Graph.Archives)
	archivedIDs := map[string]bool{}
	for i := range archives {
		archives[i].TaskIDs = slices.Clone(archives[i].TaskIDs)
		for _, id := range archives[i].TaskIDs {
			archivedIDs[id] = true
		}
	}
	archivedBefore, err := archivedGraphContent(state.Graph, archivedIDs)
	if err != nil {
		return err
	}
	var restore *RestoreRecord
	if state.Records.Restore != nil {
		copy := *state.Records.Restore
		copy.Paths = slices.Clone(copy.Paths)
		copy.TaskIDs = slices.Clone(copy.TaskIDs)
		restore = &copy
	}
	history := slices.Clone(state.Records.Restores)
	for i := range history {
		history[i].Paths = slices.Clone(history[i].Paths)
		history[i].TaskIDs = slices.Clone(history[i].TaskIDs)
	}
	budgets := slices.Clone(state.Records.Budgets)
	cancellations := slices.Clone(state.Records.Cancellations)
	approved := map[string]string{}
	for _, update := range state.Records.Updates {
		data, err := json.Marshal(update)
		if err != nil {
			return err
		}
		approved[update.ID] = string(data)
	}
	packets := slices.Clone(state.Records.Packets)
	packetAuthorizations := slices.Clone(state.Records.PacketAuthorizations)
	for i := range packetAuthorizations {
		packetAuthorizations[i].Unavailable = slices.Clone(packetAuthorizations[i].Unavailable)
	}
	packetAudits := slices.Clone(state.Records.PacketAudits)
	acceptances := make([]string, len(state.Records.ReviewAcceptances))
	for i, acceptance := range state.Records.ReviewAcceptances {
		content, err := core.CanonicalJSON(acceptance)
		if err != nil {
			return err
		}
		acceptances[i] = string(content)
	}
	existing := map[string]bool{}
	if restore != nil {
		for _, dispatch := range state.Graph.Dispatches {
			existing[dispatch.ID] = true
		}
	}
	if err := update(&state); err != nil {
		return err
	}
	if len(state.Records.ReviewAcceptances) < len(acceptances) {
		return fmt.Errorf("saved review acceptance records are append-only")
	}
	for i, previous := range acceptances {
		content, err := core.CanonicalJSON(state.Records.ReviewAcceptances[i])
		if err != nil || string(content) != previous {
			return fmt.Errorf("saved review acceptance records are immutable")
		}
	}
	if len(state.Records.Packets) < len(packets) ||
		!slices.Equal(packets, state.Records.Packets[:len(packets)]) ||
		len(state.Records.PacketAuthorizations) < len(packetAuthorizations) ||
		len(packetAuthorizations) != 0 && !reflect.DeepEqual(packetAuthorizations, state.Records.PacketAuthorizations[:len(packetAuthorizations)]) ||
		len(state.Records.PacketAudits) < len(packetAudits) ||
		!slices.Equal(packetAudits, state.Records.PacketAudits[:len(packetAudits)]) {
		return fmt.Errorf("packet artifacts, authorizations and audit records are append-only")
	}
	if len(state.Graph.Archives) < len(archives) ||
		len(archives) != 0 && !reflect.DeepEqual(archives, state.Graph.Archives[:len(archives)]) {
		return fmt.Errorf("Task archive receipts are immutable")
	}
	if len(archives) != 0 && !state.HasGraph {
		return fmt.Errorf("archived Task Graph cannot be removed")
	}
	archivedAfter, err := archivedGraphContent(state.Graph, archivedIDs)
	if err != nil {
		return err
	}
	if archivedBefore != archivedAfter {
		return fmt.Errorf("archived contracts and execution history are immutable; use a newly approved successor")
	}
	if !reflect.DeepEqual(restore, state.Records.Restore) || !reflect.DeepEqual(history, state.Records.Restores) {
		return fmt.Errorf("workspace restore receipts are immutable")
	}
	if len(state.Records.Budgets) < len(budgets) ||
		!slices.Equal(budgets, state.Records.Budgets[:len(budgets)]) {
		return fmt.Errorf("task budget audit records are append-only")
	}
	if len(state.Records.Cancellations) < len(cancellations) ||
		!slices.Equal(cancellations, state.Records.Cancellations[:len(cancellations)]) {
		return fmt.Errorf("publication cancellation audits are append-only")
	}
	for _, audit := range state.Records.Cancellations {
		var previous ExternalUpdate
		if err := json.Unmarshal([]byte(approved[audit.ID]), &previous); err != nil {
			return fmt.Errorf("cancellation requires an existing approved publication")
		}
		if previous.Status != "PENDING" && previous.Status != "CANCELED" {
			return fmt.Errorf("only unsent publications can be canceled")
		}
		expected := previous
		expected.Status = "CANCELED"
		found := false
		for _, update := range state.Records.Updates {
			if update.ID == audit.ID {
				found = reflect.DeepEqual(expected, update)
			}
		}
		if !found {
			return fmt.Errorf("canceled publication content and approval are immutable")
		}
	}
	if restore != nil {
		for _, dispatch := range state.Graph.Dispatches {
			if !existing[dispatch.ID] && slices.Contains(restore.TaskIDs, dispatch.TaskID) {
				return fmt.Errorf("restored Task requires a newly approved successor before execution")
			}
		}
	}
	if err := state.Records.validate(); err != nil {
		return err
	}
	if err := state.Records.VerifyFlowAudits(state.Graph); err != nil {
		return err
	}
	if state.HasGraph {
		if err := state.Graph.Validate(); err != nil {
			return err
		}
		if err := state.Graph.CheckLedger(state.Ledger); err != nil {
			return err
		}
	}
	if err := saveWorkspaceDocuments(tx, state); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO workspace_state(id,revision,ledger_json,graph_json,graph_present)
		VALUES(1,1,'{}','{}',?) ON CONFLICT(id)
		DO UPDATE SET revision=workspace_state.revision+1,graph_present=excluded.graph_present`, boolInt(state.HasGraph)); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO workspace_records VALUES(1,'{}') ON CONFLICT(id) DO NOTHING`); err != nil {
		return err
	}
	return tx.Commit()
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
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

// Enqueue records an event for later delivery in a separate transaction
// from SaveLedger. Callers must not assume the state and event are atomic.
func (s *Store) Enqueue(typ, payload string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO outbox(type,payload,created_at) VALUES(?,?,?)`,
		typ, payload, ts(time.Now().UTC()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Claim returns up to limit unsent entries in FIFO order. It does not reserve
// them; concurrent consumers can receive the same entries.
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
