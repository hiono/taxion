package core

import "testing"

func TestGuardActiveDispatch(t *testing.T) {
	if err := GuardActiveDispatch(nil); err != nil {
		t.Fatalf("empty ledger should allow dispatch, got %v", err)
	}
	existing := []Dispatch{{ID: "d1", TaskID: "t1", Active: true}}
	if err := GuardActiveDispatch(existing); err != ErrActiveDispatchExists {
		t.Fatalf("expected DSP-001 error, got %v", err)
	}
	inactive := []Dispatch{{ID: "d1", TaskID: "t1", Active: false}}
	if err := GuardActiveDispatch(inactive); err != nil {
		t.Fatalf("inactive ledger should allow dispatch, got %v", err)
	}
}

func TestBindEvidence(t *testing.T) {
	if _, err := BindEvidence("t1", ""); err != ErrEmptySHA {
		t.Fatalf("expected SHA-001 error, got %v", err)
	}
	ev, err := BindEvidence("t1", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev.SHA != "abc123" || ev.TaskID != "t1" {
		t.Fatalf("unexpected evidence: %+v", ev)
	}
}

func TestCheckReady(t *testing.T) {
	if err := CheckReady([]Checkpoint{{Name: "plan", Done: true}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := CheckReady([]Checkpoint{{Name: "plan", Done: false}}); err != ErrNotReady {
		t.Fatalf("expected CHK-001 error, got %v", err)
	}
}
