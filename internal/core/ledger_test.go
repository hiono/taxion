package core

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLedgerRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	want := Ledger{
		Tasks:       []Task{{ID: "t1", Title: "first", CreatedAt: now}},
		Dispatches:  []Dispatch{{ID: "d1", TaskID: "t1", Active: true, CreatedAt: now}},
		Evidence:    []Evidence{{SHA: "abc123", TaskID: "t1", CreatedAt: now}},
		Checkpoints: []Checkpoint{{Name: "plan", Done: true}},
	}
	path := filepath.Join(t.TempDir(), "ledger.json")
	if err := want.Save(path); err != nil {
		t.Fatalf("save: unexpected error %v", err)
	}
	got, err := LoadLedger(path)
	if err != nil {
		t.Fatalf("load: unexpected error %v", err)
	}
	if len(got.Tasks) != 1 || got.Tasks[0].ID != "t1" {
		t.Fatalf("tasks mismatch: %+v", got.Tasks)
	}
	if len(got.Dispatches) != 1 || !got.Dispatches[0].Active {
		t.Fatalf("dispatches mismatch: %+v", got.Dispatches)
	}
	if len(got.Evidence) != 1 || got.Evidence[0].SHA != "abc123" {
		t.Fatalf("evidence mismatch: %+v", got.Evidence)
	}
	if err := CheckReady(got.Checkpoints); err != nil {
		t.Fatalf("loaded checkpoints should be ready: %v", err)
	}
}

func TestLoadLedgerMissing(t *testing.T) {
	if _, err := LoadLedger(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected error for missing ledger file")
	}
}
