package controller

import (
	"reflect"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestJobContractProposalRequiresExactUserApproval(t *testing.T) {
	db, _, _, _ := executionConfigurationFixture(t)
	_, fixtureJob, _, _ := jobRoleFixture(t, true)
	contract := *fixtureJob.Contract

	job, err := ProposeJob(db, JobIntakeRequest{
		JobID: "job-approval-flow", Request: "Deliver a feature.",
		Goal: "Complete the feature with independent review.",
		Actor: "human", Source: "human:job-proposal",
	})
	if err != nil {
		t.Fatal(err)
	}
	proposal, proposalHash, err := ProposeJobContract(db, JobContractProposalRequest{
		JobID: job.ID, Contract: contract, Actor: "planner",
		Source: "planner", Reason: "Propose the bounded execution contract",
	})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.JobID != job.ID || proposal.Revision != 1 || proposalHash == "" {
		t.Fatalf("unexpected Job contract proposal: %+v hash=%q", proposal, proposalHash)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := state.Graph.Job(job.ID)
	if !ok || stored.Status != core.JobDraft || stored.Contract == nil {
		t.Fatalf("Job proposal should remain unapproved and DRAFT: %+v", stored)
	}
	if _, ok := state.Graph.CurrentJobApproval(job.ID); ok || len(state.Graph.JobApprovals) != 0 {
		t.Fatal("proposing a Job contract must not create an approval")
	}

	if _, err := ConfirmJob(db, JobApprovalConfirmationRequest{
		JobID: job.ID, ExpectedHash: "stale-hash", Actor: "human", Reason: "approve",
	}); err == nil {
		t.Fatal("stale proposal hash must not be approved")
	}
	approval, err := ConfirmJob(db, JobApprovalConfirmationRequest{
		JobID: job.ID, ExpectedHash: proposalHash, Actor: "human", Reason: "approve exact proposal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approval.Source != "user" || approval.ProposalHash != proposalHash {
		t.Fatalf("approval does not bind the exact user decision: %+v", approval)
	}
	if _, err := ConfirmJob(db, JobApprovalConfirmationRequest{
		JobID: job.ID, ExpectedHash: proposalHash, Actor: "human", Reason: "retry",
	}); err != nil {
		t.Fatalf("exact approval retry should be idempotent: %v", err)
	}

	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	stored, ok = state.Graph.Job(job.ID)
	if !ok || stored.Status != core.JobActive {
		t.Fatalf("approved Job should be ACTIVE: %+v", stored)
	}
	if _, ok := state.Graph.CurrentJobApproval(job.ID); !ok {
		t.Fatal("explicitly approved Job has no current approval")
	}
	if len(state.Graph.JobApprovals) != 1 {
		t.Fatalf("approval retry created %d records, want 1", len(state.Graph.JobApprovals))
	}

	updatedContract := *stored.Contract
	updatedContract.Budget.MaxNanoAIU += 100
	updatedProposal, updatedHash, err := ProposeJobContract(db, JobContractProposalRequest{
		JobID: job.ID, Contract: updatedContract, Actor: "planner",
		Source: "planner", Reason: "increase approved budget",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updatedProposal.Revision != 2 || updatedHash == proposalHash {
		t.Fatalf("contract revision was not advanced: revision=%d hash=%q", updatedProposal.Revision, updatedHash)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	stored, _ = state.Graph.Job(job.ID)
	if stored.Status != core.JobWaiting {
		t.Fatalf("revised active Job should wait for reapproval, got %s", stored.Status)
	}
	if _, ok := state.Graph.CurrentJobApproval(job.ID); ok {
		t.Fatal("previous approval must not authorize the revised contract")
	}
	if _, err := ConfirmJob(db, JobApprovalConfirmationRequest{
		JobID: job.ID, ExpectedHash: proposalHash, Actor: "human", Reason: "approve old revision",
	}); err == nil {
		t.Fatal("old proposal hash must not approve the revised contract")
	}
	if _, err := ConfirmJob(db, JobApprovalConfirmationRequest{
		JobID: job.ID, ExpectedHash: updatedHash, Actor: "human", Reason: "approve revised contract",
	}); err != nil {
		t.Fatal(err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	stored, _ = state.Graph.Job(job.ID)
	if stored.Status != core.JobActive || len(state.Graph.JobApprovals) != 2 {
		t.Fatalf("reapproved Job state/history mismatch: status=%s approvals=%d", stored.Status, len(state.Graph.JobApprovals))
	}
}

func TestProposeJobContractRejectsExistingTaskOutsideRoleScope(t *testing.T) {
	db, job, task, _ := jobRoleFixture(t, true)
	contract := *job.Contract
	contract.Roles = []string{"PLANNER", "REVIEWER"}
	if _, _, err := ProposeJobContract(db, JobContractProposalRequest{
		JobID: job.ID, Contract: contract, Actor: "planner",
		Source: "planner", Reason: "remove Coder authority",
	}); err == nil {
		t.Fatal("Job contract revision must not exclude an existing Task owner")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := state.Graph.Job(job.ID)
	if !ok || stored.Status != core.JobActive || stored.Contract == nil ||
		!reflect.DeepEqual(stored.Contract.Roles, job.Contract.Roles) {
		t.Fatalf("rejected Job contract proposal changed current authority: %+v", stored)
	}
	if _, ok := state.Graph.CurrentJobApproval(job.ID); !ok {
		t.Fatal("rejected Job contract proposal invalidated the current approval")
	}
	if task.Owner == "" {
		t.Fatal("fixture Task must identify an owner")
	}
}


func TestProposeJobContractReturnsExactProposalAndHash(t *testing.T) {
	db, confirmation := intakeApprovalFixture(t, false)
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	job, ok := state.Graph.JobForTask(confirmation.TaskID)
	if !ok || confirmation.JobContract == nil {
		t.Fatal("fixture must provide a Job and explicit contract")
	}
	proposal, hash, err := ProposeJobContract(db, JobContractProposalRequest{
		JobID: job.ID, Contract: *confirmation.JobContract,
		Actor: "human", Source: "human:job-contract",
		Reason: "propose exact contract for approval",
	})
	if err != nil {
		t.Fatal(err)
	}
	storedState, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := storedState.Graph.Job(job.ID)
	if !ok {
		t.Fatal("proposed Job was not persisted")
	}
	want, err := core.JobProposal(stored)
	if err != nil {
		t.Fatal(err)
	}
	wantHash, err := want.Hash()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(proposal, want) || hash != wantHash ||
		proposal.JobID != job.ID || proposal.Contract.Revision != 1 {
		t.Fatalf("returned proposal does not match persisted contract: proposal=%+v want=%+v hash=%q wantHash=%q",
			proposal, want, hash, wantHash)
	}
}
