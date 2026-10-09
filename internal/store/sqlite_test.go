package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
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
