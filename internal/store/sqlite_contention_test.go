package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"modernc.org/sqlite"
)

func TestWorkspaceConcurrentWriterWaitsBeforeReading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace ?#.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if _, err := first.InitializeWorkspace(Workspace{Ledger: core.Ledger{Tasks: []core.Task{{ID: "task", Title: "initial"}}}}); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	tx, err := first.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := first.loadWorkspace(tx); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- second.UpdateWorkspace(func(state *Workspace) error {
			close(entered)
			state.Ledger.Tasks[0].Title = "updated"
			return nil
		})
	}()
	select {
	case <-entered:
		t.Fatal("competing writer read before acquiring its writer reservation")
	case err := <-done:
		t.Fatalf("competing writer failed instead of waiting: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := reader.LoadWorkspace(); err != nil {
		t.Fatalf("reader could not observe state while writer reserved: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("writer did not finish after lock release")
	}
	state, err := reader.LoadWorkspace()
	if err != nil || state.Ledger.Tasks[0].Title != "updated" {
		t.Fatal("committed update was lost", err)
	}
}

func TestWorkspaceBusyTimeoutAppliesToEveryConnection(t *testing.T) {
	s := openTest(t)
	for range 2 {
		conn, err := s.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var timeout int
		if err := conn.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&timeout); err != nil || timeout != 5000 {
			t.Fatalf("connection timeout=%d: %v", timeout, err)
		}
	}
}

func TestWorkspaceCommitWaitsForShortReader(t *testing.T) {
	s := openTest(t)
	if _, err := s.InitializeWorkspace(Workspace{}); err != nil {
		t.Fatal(err)
	}
	reader, err := s.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var revision int
	if err := reader.QueryRow("SELECT revision FROM workspace_state").Scan(&revision); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.UpdateWorkspace(func(state *Workspace) error {
			close(entered)
			state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: "after-reader"})
			return nil
		})
	}()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("writer failed before callback: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("writer did not reach callback")
	}
	select {
	case err := <-done:
		t.Fatalf("commit did not wait for reader: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := reader.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("commit did not finish after reader released its lock")
	}
}

func TestWorkspacePersistentContentionFailsWithoutCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	tx, err := first.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	calls := 0
	start := time.Now()
	err = second.UpdateWorkspace(func(*Workspace) error {
		calls++
		return nil
	})
	var busy *sqlite.Error
	if !errors.As(err, &busy) || busy.Code() != 5 || calls != 0 {
		t.Fatalf("persistent contention did not fail before callback: calls=%d err=%v", calls, err)
	}
	if elapsed := time.Since(start); elapsed < 4*time.Second || elapsed > 15*time.Second {
		t.Fatalf("contention wait was not bounded near five seconds: %v", elapsed)
	}
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Fatal(err)
	}
}
