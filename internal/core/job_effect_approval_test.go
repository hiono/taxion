package core

import (
	"testing"
	"time"
)

func effectApprovalFixture(t *testing.T) (*TaskGraph, JobEffectObservation, time.Time) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	job := Job{
		ID: "effect-job", Request: "request", Goal: "goal", Status: JobActive,
		CreatedAt: now, UpdatedAt: now, Contract: validJobContract(t),
	}
	graph := &TaskGraph{Version: 1, Jobs: []Job{job}}
	proposal, err := JobProposal(job)
	if err != nil {
		t.Fatal(err)
	}
	approval, err := NewJobApproval("effect-job-approval", "human", "user", "approve bounded Job", proposal, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobApproval(approval); err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendRoleExecution(RoleExecution{
		ID: "effect-execution", JobID: job.ID, Role: "CODER",
		Status: RoleExecutionCreated, CreatedAt: now.Add(2 * time.Second), UpdatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("effect-execution", RoleExecutionRunning, "controller", "start", now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	observation := JobEffectObservation{
		Provider: "copilot", Capability: "web-search", Action: "network",
		Effects: []JobEffect{JobEffectNetworkAccess},
		Targets: []JobEffectTarget{{Resource: "network:https://api.example.com/search"}},
		Inspectable: true,
	}
	return graph, observation, now.Add(4 * time.Second)
}

func TestJobEffectApprovalRequestIsBoundToExactExecutionAndObservation(t *testing.T) {
	graph, observation, at := effectApprovalFixture(t)
	if got := graph.EvaluateJobEffect("effect-execution", observation); got != JobEffectRequireApproval {
		t.Fatalf("unapproved external effect decision = %q, want require_approval", got)
	}
	request, err := NewJobEffectApprovalRequest(
		"effect-request", "effect-job", 1, "effect-execution", observation, at, *graph.Jobs[0].Contract,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobEffectApprovalRequest(request); err != nil {
		t.Fatal(err)
	}
	if got := graph.EvaluateJobEffect("effect-execution", observation); got != JobEffectRequireApproval {
		t.Fatalf("pending approval decision = %q, want require_approval", got)
	}
	changed := observation
	changed.Targets = []JobEffectTarget{{Resource: "network:https://other.example.com/search"}}
	if got := graph.EvaluateJobEffect("effect-execution", changed); got != JobEffectRequireApproval {
		t.Fatalf("different target decision = %q, want a new approval request", got)
	}
	if err := graph.TransitionJob("effect-job", JobWaiting, "controller", "effect approval requested", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("effect-execution", RoleExecutionWaiting, "controller", "effect approval requested", at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	decision, err := NewJobEffectApprovalDecision("effect-decision", request, true, "human", "approve this exact network request", at.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobEffectApprovalDecision(decision); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionJob("effect-job", JobActive, "human", "effect explicitly approved", at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("effect-execution", RoleExecutionRunning, "controller", "resume after effect approval", at.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := graph.EvaluateJobEffect("effect-execution", observation); got != JobEffectAllow {
		t.Fatalf("approved exact effect decision = %q, want allow", got)
	}
	if got := graph.EvaluateJobEffect("effect-execution", changed); got != JobEffectRequireApproval {
		t.Fatalf("different target inherited approval: decision = %q", got)
	}
}

func TestJobEffectApprovalRejectionDoesNotGrantEffect(t *testing.T) {
	graph, observation, at := effectApprovalFixture(t)
	request, err := NewJobEffectApprovalRequest(
		"effect-request", "effect-job", 1, "effect-execution", observation, at, *graph.Jobs[0].Contract,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobEffectApprovalRequest(request); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionJob("effect-job", JobWaiting, "controller", "effect approval requested", at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("effect-execution", RoleExecutionWaiting, "controller", "effect approval requested", at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	decision, err := NewJobEffectApprovalDecision("effect-decision", request, false, "human", "deny external access", at.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobEffectApprovalDecision(decision); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionJob("effect-job", JobActive, "human", "effect explicitly denied", at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := graph.TransitionRoleExecution("effect-execution", RoleExecutionRunning, "controller", "resume after effect denial", at.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := graph.EvaluateJobEffect("effect-execution", observation); got != JobEffectDeny {
		t.Fatalf("rejected effect decision = %q, want deny", got)
	}
}

func TestJobEffectApprovalRecordsAreDeepCloned(t *testing.T) {
	graph, observation, at := effectApprovalFixture(t)
	request, err := NewJobEffectApprovalRequest(
		"effect-request", "effect-job", 1, "effect-execution", observation, at, *graph.Jobs[0].Contract,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.AppendJobEffectApprovalRequest(request); err != nil {
		t.Fatal(err)
	}
	clone := cloneGraph(*graph)
	clone.JobEffectApprovalRequests[0].Observation.Effects[0] = JobEffectSecretDisclosure
	clone.JobEffectApprovalRequests[0].Observation.Targets[0].Resource = "credential:changed"
	if graph.JobEffectApprovalRequests[0].Observation.Effects[0] != JobEffectNetworkAccess ||
		graph.JobEffectApprovalRequests[0].Observation.Targets[0].Resource != observation.Targets[0].Resource {
		t.Fatal("Job effect approval request clone shares mutable observation data")
	}
}
