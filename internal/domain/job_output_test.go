package domain

import "testing"

func TestJobOutputRequiresLatestCoderTurn(t *testing.T) {
	first := TaskTurnIntent{ID: "turn_first", RoleExecutionID: "role_exec_coder", CreatedRevision: 10}
	later := TaskTurnIntent{ID: "turn_later", RoleExecutionID: "role_exec_coder", CreatedRevision: 20}
	job := Job{TaskTurnIntents: []TaskTurnIntent{first, later}}
	if err := validateJobOutputLatestTurn(job, first.RoleExecutionID, first); err == nil {
		t.Fatal("JobOutput accepted a prior Turn after a later Coder Turn was created")
	}
	if err := validateJobOutputLatestTurn(job, later.RoleExecutionID, later); err != nil {
		t.Fatalf("latest Coder Turn was rejected: %v", err)
	}
	single := Job{TaskTurnIntents: []TaskTurnIntent{first}}
	if err := validateJobOutputLatestTurn(single, first.RoleExecutionID, first); err != nil {
		t.Fatalf("single Coder Turn was rejected: %v", err)
	}
}
