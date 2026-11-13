package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"modernc.org/sqlite"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestLedgerRoundTrip(t *testing.T) {
	s := openTest(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	want := core.Ledger{
		Tasks:       []core.Task{{ID: "t1", Title: "first", Repo: "hiono/taxion", CreatedAt: now}},
		Dispatches:  []core.Dispatch{{ID: "d1", TaskID: "t1", Repo: "hiono/taxion", Active: true, CreatedAt: now}},
		Evidence:    []core.Evidence{{SHA: "abc123", TaskID: "t1", CreatedAt: now}},
		Checkpoints: []core.Checkpoint{{Name: "plan", Done: true}, {Name: "review", Done: false}},
	}
	if err := s.SaveLedger(want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := s.LoadLedger()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].Repo != "hiono/taxion" {
		t.Fatalf("tasks mismatch: %+v", got.Tasks)
	}
	if len(got.Dispatches) != 1 || !got.Dispatches[0].Active {
		t.Fatalf("dispatches mismatch: %+v", got.Dispatches)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].SHA != "abc123" {
		t.Fatalf("evidence mismatch: %+v", got.Evidence)
	}
	if len(got.Checkpoints) != 2 || !got.Checkpoints[0].Done || got.Checkpoints[1].Done {
		t.Fatalf("checkpoints mismatch: %+v", got.Checkpoints)
	}
	if err := core.GuardActiveDispatch(got.Dispatches); err != core.ErrActiveDispatchExists {
		t.Fatalf("DSP-001 must survive sqlite round-trip, got %v", err)
	}
}

func TestSaveReplaces(t *testing.T) {
	s := openTest(t)
	now := time.Now().UTC()
	if err := s.SaveLedger(core.Ledger{Tasks: []core.Task{{ID: "t1", CreatedAt: now}}}); err != nil {
		t.Fatalf("save1: %v", err)
	}
	if err := s.SaveLedger(core.Ledger{}); err != nil {
		t.Fatalf("save2: %v", err)
	}
	got, err := s.LoadLedger()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got.Tasks) != 0 {
		t.Fatalf("expected empty ledger after replace, got %+v", got.Tasks)
	}
}

func TestWorkspaceInitializeAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Workspace{Ledger: core.Ledger{Tasks: []core.Task{{ID: "t1", Title: "task", Repo: "owner/repo"}}}}
	inserted, err := s.InitializeWorkspace(want)
	if err != nil || !inserted {
		t.Fatalf("initialize: inserted=%v err=%v", inserted, err)
	}
	inserted, err = s.InitializeWorkspace(Workspace{})
	if err != nil || inserted {
		t.Fatalf("initialize twice: inserted=%v err=%v", inserted, err)
	}
	if err := s.UpdateWorkspace(func(state *Workspace) error {
		state.Ledger.Tasks[0].Title = "uncommitted"
		return fmt.Errorf("reject update")
	}); err == nil {
		t.Fatal("expected callback failure")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Ledger.Tasks) != 1 || got.Ledger.Tasks[0].Title != "task" {
		t.Fatalf("rollback/reopen lost state: %+v", got)
	}
}

func TestRuntimeSessionBindingPersistsInWorkspaceSQLite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	now := time.Now().UTC()
	task := core.GraphTask{
		ID: "t1", Title: "task", Repo: "owner/repo", Type: "IMPLEMENTATION", Issue: "Issue #50",
		Owner: "CODER", Status: core.Draft, CreatedFrom: "request:1", Objective: "implement",
		Scope: []string{"src"}, Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"report"},
		ValidationCommands: []string{"go test ./..."},
		Worktree:           "/work/t1", Branch: "work/t1", BaselineSHA: strings.Repeat("a", 40),
		ExitCondition: "report", AuthorizationID: "decision:1",
		Decisions: []core.TaskDecision{{ID: "decision:1", Source: "human:1", Text: "approved"}},
	}
	graph := core.TaskGraph{Version: 1}
	if err := graph.Track(task, "human", "register"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition("t1", core.Ready, "human", "approve", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	dispatch, err := core.NewGraphDispatch(graph, "d1", "t1", "owner/repo", core.RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.IssueDispatch(dispatch, "controller", "issue"); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state := Workspace{
		Ledger: core.Ledger{
			Tasks: []core.Task{{ID: "t1", Title: "task", Repo: "owner/repo", CreatedAt: now}},
			Dispatches: []core.Dispatch{{
				ID: "d1", TaskID: "t1", Repo: "owner/repo", Active: true, CreatedAt: now,
			}},
		},
		Graph: graph, HasGraph: true,
	}
	if _, err := store.InitializeWorkspace(state); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.UpdateWorkspace(func(state *Workspace) error {
		return state.Graph.BindRuntimeSession("d1", "sdk-session-1", "role-config-hash")
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	got, err := store.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	bound, ok := got.Graph.GraphDispatch("d1")
	if !ok || bound.RuntimeSessionID != "sdk-session-1" || bound.RuntimeConfigHash != "role-config-hash" {
		t.Fatalf("runtime binding did not survive SQLite reopen: %+v", bound)
	}
}

func TestOutbox(t *testing.T) {
	s := openTest(t)
	id1, err := s.Enqueue("dispatch.created", `{"id":"d1"}`)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if _, err := s.Enqueue("evidence.bound", `{"sha":"abc"}`); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	claimed, err := s.Claim(10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 2 || claimed[0].ID != id1 || claimed[0].Type != "dispatch.created" {
		t.Fatalf("claim mismatch: %+v", claimed)
	}
	if err := s.MarkSent(id1); err != nil {
		t.Fatalf("mark: %v", err)
	}
	claimed, err = s.Claim(10)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(claimed) != 1 || claimed[0].Type != "evidence.bound" {
		t.Fatalf("expected one remaining entry, got %+v", claimed)
	}
}

func TestFailedReviewContextTransactionsClassifyPrewriteFailures(t *testing.T) {
	s := openTest(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.LoadWorkspaceContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled initial read lost cancellation identity: %v", err)
	}

	outcome, err := s.AppendFailedReviewSupplementContext(context.Background(), "stale-state-hash", &FailedReviewSupplement{})
	if err == nil || outcome != FailedReviewNotWritten {
		t.Fatalf("pre-write conflict was not safely classified: outcome=%s err=%v", outcome, err)
	}
}

func TestFailedReviewCancelledMutexWaitNeverWrites(t *testing.T) {
	s := openTest(t)
	before, err := s.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	s.mu.Lock()
	done := make(chan error, 1)
	go func() {
		outcome, err := s.AppendFailedReviewSupplementContext(ctx, "", &FailedReviewSupplement{})
		if outcome != FailedReviewNotWritten {
			done <- fmt.Errorf("cancelled wait persistence=%s: %w", outcome, err)
			return
		}
		done <- err
	}()
	cancel()
	s.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled mutex wait lost cancellation identity: %v", err)
	}
	after, err := s.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	beforeHash, _ := core.CanonicalSHA256(before)
	afterHash, _ := core.CanonicalSHA256(after)
	if beforeHash != afterHash {
		t.Fatal("cancelled mutex wait changed saved evidence")
	}
}

func TestFailedReviewTransactionCancellationDoesNotCommit(t *testing.T) {
	s := openTest(t)
	before, err := s.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := workspaceUpdateResult{}
	s.mu.Lock()
	err = s.updateWorkspaceLocked(ctx, func(state *Workspace) error {
		state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{
			ID: "cancelled-write", Title: "must not persist", Repo: "owner/repo", CreatedAt: time.Now().UTC(),
		})
		cancel()
		return nil
	}, &result)
	s.mu.Unlock()
	if err == nil {
		t.Fatal("cancelled transaction claimed commit success")
	}
	after, err := s.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	beforeHash, _ := core.CanonicalSHA256(before)
	afterHash, _ := core.CanonicalSHA256(after)
	if beforeHash != afterHash {
		t.Fatalf("cancelled transaction changed evidence: rollbackProven=%v", result.rollbackProven)
	}
}

type failedReviewFaultConnector struct {
	driver        driver.Driver
	location      string
	mode          string
	cancel        context.CancelFunc
	failure       error
	queryPrefix   string
	statement     string
	safetyContext context.Context
}

func (c *failedReviewFaultConnector) Driver() driver.Driver {
	if c.driver != nil {
		return c.driver
	}
	return &sqlite.Driver{}
}

func (c *failedReviewFaultConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.Driver().Open(c.location)
	if err != nil {
		return nil, err
	}
	return &failedReviewFaultConn{Conn: conn, fault: c}, nil
}

type failedReviewFaultConn struct {
	driver.Conn
	fault *failedReviewFaultConnector
}

func (c *failedReviewFaultConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	var tx driver.Tx
	var err error
	if conn, ok := c.Conn.(driver.ConnBeginTx); ok {
		tx, err = conn.BeginTx(ctx, options)
	} else {
		tx, err = c.Conn.Begin()
	}
	if err != nil {
		return nil, err
	}
	return &failedReviewFaultTx{Tx: tx, fault: c.fault}, nil
}

func (c *failedReviewFaultConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if c.fault.mode == "rollback-error" && strings.Contains(query, "INSERT INTO workspace_state") {
		return nil, c.fault.failure
	}
	ctx, query = c.statementContext(ctx, query)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *failedReviewFaultConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	ctx, query = c.statementContext(ctx, query)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *failedReviewFaultConn) statementContext(ctx context.Context, query string) (context.Context, string) {
	if c.fault.statement != "" && strings.HasPrefix(query, c.fault.queryPrefix) {
		if ctx.Done() == nil {
			ctx = c.fault.safetyContext
		}
		return ctx, c.fault.statement
	}
	return ctx, query
}

type failedReviewFaultTx struct {
	driver.Tx
	fault *failedReviewFaultConnector
}

func (tx *failedReviewFaultTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	if tx.fault.mode == "ambiguous-commit" {
		return tx.fault.failure
	}
	if tx.fault.mode == "cancel-after-commit" {
		tx.fault.cancel()
	}
	return nil
}

func (tx *failedReviewFaultTx) Rollback() error {
	if err := tx.Tx.Rollback(); err != nil {
		return err
	}
	if tx.fault.mode == "rollback-error" {
		return tx.fault.failure
	}
	return nil
}

var failedReviewStatementFunctionID atomic.Uint64

func TestFailedReviewInFlightSQLiteCancellation(t *testing.T) {
	for _, test := range []struct {
		name, queryPrefix string
		write             bool
		deadline          bool
	}{
		{"workspace-row", "SELECT ledger_json", false, false},
		{"workspace-row-deadline", "SELECT ledger_json", false, true},
		{"document-strings", "SELECT hash,content,bytes FROM workspace_strings", false, false},
		{"pooled-string-write", "INSERT INTO workspace_strings", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "inflight.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if _, err := s.InitializeWorkspace(Workspace{}); err != nil {
				t.Fatal(err)
			}
			before, err := s.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			var once sync.Once
			function := fmt.Sprintf("taxion_statement_started_%d", failedReviewStatementFunctionID.Add(1))
			if err := sqlite.RegisterScalarFunction(function, 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
				once.Do(func() { close(started) })
				return int64(0), nil
			}); err != nil {
				t.Fatal(err)
			}
			location, err := workspaceLocation(path)
			if err != nil {
				t.Fatal(err)
			}
			safety, stopSafety := context.WithTimeout(t.Context(), 3*time.Second)
			defer stopSafety()
			faultDB := sql.OpenDB(&failedReviewFaultConnector{
				driver: s.db.Driver(), location: location.String(), queryPrefix: test.queryPrefix, safetyContext: safety,
				statement: "WITH RECURSIVE n(x) AS (SELECT " + function +
					"() UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT sum(x) FROM n",
			})
			defer faultDB.Close()
			subject := &Store{db: faultDB}
			ctx, cancel := context.WithCancel(t.Context())
			wantError := context.Canceled
			if test.deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), 500*time.Millisecond)
				wantError = context.DeadlineExceeded
			}
			defer cancel()
			if !test.deadline {
				go func() {
					select {
					case <-started:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			result := workspaceUpdateResult{}
			if test.write {
				err = subject.updateWorkspaceLocked(ctx, func(state *Workspace) error {
					state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{
						ID: "cancelled-statement", Title: strings.Repeat("x", 600),
					})
					return nil
				}, &result)
			} else {
				_, err = subject.LoadWorkspaceContext(ctx)
			}
			select {
			case <-started:
			default:
				t.Fatalf("SQLite statement never began executing: %v", err)
			}
			if safety.Err() != nil {
				t.Fatalf("statement reached fixture safety timeout instead of caller cancellation: %v", err)
			}
			if !errors.Is(err, wantError) {
				t.Fatalf("in-flight statement did not honor caller cancellation: %v", err)
			}
			if test.write && result.failedReviewPersistence(err) == FailedReviewCommitted {
				t.Fatal("cancelled statement claimed a successful commit")
			}
			actual, err := s.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			beforeHash, _ := core.CanonicalSHA256(before)
			afterHash, _ := core.CanonicalSHA256(actual)
			if beforeHash != afterHash {
				t.Fatal("cancelled SQLite statement changed persisted workspace")
			}
		})
	}
}

func TestFailedReviewTransactionFaultOutcomes(t *testing.T) {
	for _, mode := range []string{"ambiguous-commit", "rollback-error", "cancel-after-commit"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "fault.db")
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}

			defer s.Close()
			original := s.db
			location, err := workspaceLocation(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			injected := errors.New("injected transaction fault")
			faultDB := sql.OpenDB(&failedReviewFaultConnector{
				location: location.String(), mode: mode, cancel: cancel, failure: injected,
			})
			defer faultDB.Close()
			s.db = faultDB
			result := workspaceUpdateResult{}
			s.mu.Lock()
			err = s.updateWorkspaceLocked(ctx, func(state *Workspace) error {
				state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{
					ID: "fault-write", Title: "transaction boundary", Repo: "owner/repo", CreatedAt: time.Now().UTC(),
				})
				return nil
			}, &result)
			s.mu.Unlock()
			s.db = original
			outcome := result.failedReviewPersistence(err)
			if mode == "cancel-after-commit" {
				if err != nil || outcome != FailedReviewCommitted || !errors.Is(ctx.Err(), context.Canceled) {
					t.Fatalf("successful commit was overwritten by cancellation: %s %v", outcome, err)
				}
			} else if !errors.Is(err, injected) || outcome != FailedReviewUnknown || result.rollbackProven {
				t.Fatalf("uncertain transaction claimed known persistence: %s %v %+v", outcome, err, result)
			}
			after, err := s.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			wantTasks := 1
			if mode == "rollback-error" {
				wantTasks = 0
			}
			if len(after.Ledger.Tasks) != wantTasks {
				t.Fatalf("actual committed/rolled-back state differs: %+v", after.Ledger.Tasks)
			}
		})
	}
}
