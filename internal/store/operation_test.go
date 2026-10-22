package store

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func operationFixture() Operation {
	return Operation{Version: 1, ID: "request", Kind: "request", TaskID: "planning",
		State: "WAITING_FOR_ANSWER", Phase: "PLANNING", Owner: "original-owner",
		Deadline: time.Now().UTC().Add(time.Hour), SessionCreditLimit: 30, MaxNanoAIU: 1000,
		PlanningUsageState: "complete", Checkpoint: json.RawMessage(`{"question":"Which behavior?"}`)}
}

func TestOperationLeaseSerializesStoresAndPreservesCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.InitializeWorkspace(Workspace{}); err != nil {
		t.Fatal(err)
	}
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	lease, err := db.LockOperation()
	if err != nil {
		t.Fatal(err)
	}
	operation, err := lease.Claim(operationFixture(), false)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := other.LockOperation(); err == nil {
		duplicate.Close()
		t.Fatal("two stores acquired the same active operation")
	}
	if err := db.ReleaseOperation(operation.ID, operation.Owner); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err = other.LockOperation()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if _, err := lease.Claim(operationFixture(), false); err == nil {
		t.Fatal("a new request replaced an unanswered operation")
	}
	resumed, err := lease.Claim(Operation{ID: operation.ID, Owner: "new-owner", MaxNanoAIU: 999999}, true)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Owner != "new-owner" || resumed.MaxNanoAIU != 1000 ||
		string(resumed.Checkpoint) != string(operation.Checkpoint) || !resumed.Deadline.Equal(operation.Deadline) {
		t.Fatal("resume changed the approved ceilings or lost the question")
	}
	resumed.MaxNanoAIU++
	if err := other.SaveOperation(resumed); err == nil {
		t.Fatal("a resume raised the approved budget")
	}
	resumed.MaxNanoAIU--
	resumed.State = "FUTURE"
	if err := other.SaveOperation(resumed); err == nil {
		t.Fatal("an unknown operation state was accepted")
	}
}

func TestOperationLeaseReleasesAfterProcessExit(t *testing.T) {
	if path := os.Getenv("TAXION_OPERATION_CRASH_FIXTURE"); path != "" {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		lease, err := db.LockOperation()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lease.Claim(operationFixture(), false); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.InitializeWorkspace(Workspace{}); err != nil {
		t.Fatal(err)
	}
	process := exec.Command(os.Args[0], "-test.run=^TestOperationLeaseReleasesAfterProcessExit$")
	process.Env = append(os.Environ(), "TAXION_OPERATION_CRASH_FIXTURE="+path)
	if output, err := process.CombinedOutput(); err != nil {
		t.Fatalf("crash fixture: %s: %v", output, err)
	}
	lease, err := db.LockOperation()
	if err != nil {
		t.Fatal("process exit retained the reservation", err)
	}
	defer lease.Close()
	resumed, err := lease.Claim(Operation{ID: "request", Owner: "resumed"}, true)
	if err != nil || resumed.Owner != "resumed" || resumed.State != "WAITING_FOR_ANSWER" {
		t.Fatal("could not resume the exact checkpoint after process exit", err)
	}
}
