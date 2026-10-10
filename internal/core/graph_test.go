package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func graphTask(id string) GraphTask {
	return GraphTask{ID: id, Title: id, Repo: "owner/repo", Type: "IMPLEMENTATION", Issue: "Issue #47",
		Status: Draft, Owner: "CODER", Reviewer: "reviewer-run", CreatedFrom: "request:1",
		Objective: "fix", Scope: []string{"src"}, Acceptance: []string{"tests pass"},
		RequiredEvidence: []string{"test-report"}, ExitCondition: "report",
		AuthorizationID: "decision:1", Decisions: []TaskDecision{{ID: "decision:1", Source: "human:1", Text: "allow scope"}}}
}

func trackedGraph(t *testing.T, tasks ...GraphTask) TaskGraph {
	t.Helper()
	g := TaskGraph{Version: 1}
	for _, task := range tasks {
		if err := g.Track(task, "human", "register"); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func move(t *testing.T, g *TaskGraph, id string, status TaskStatus, proof TransitionProof) {
	t.Helper()
	if err := g.Transition(id, status, "controller", "advance", proof); err != nil {
		t.Fatal(err)
	}
}

func TestGraphLifecycleAndProof(t *testing.T) {
	g := trackedGraph(t, graphTask("task"))
	for _, status := range []TaskStatus{Completed, Approved, Active} {
		if err := g.Transition("task", status, "controller", "skip", TransitionProof{}); err == nil {
			t.Fatal("skipped gate")
		}
	}
	move(t, &g, "task", Ready, TransitionProof{})
	if err := g.CheckExecutable("task", "owner/other"); err == nil {
		t.Fatal("repo mismatch")
	}
	if err := g.CheckExecutable("task", "owner/repo"); err != nil {
		t.Fatal(err)
	}
	move(t, &g, "task", Active, TransitionProof{})
	move(t, &g, "task", WaitingForReview, TransitionProof{OutputSHA: strings.Repeat("a", 40)})
	before, _ := json.Marshal(g)
	proof := TransitionProof{ActionPerformed: true, Evidence: []string{"test-report"}, Acceptance: []string{"tests pass"}, ReviewedSHA: strings.Repeat("a", 40), Reviewer: "reviewer-run"}
	for _, bad := range []TransitionProof{{}, {ActionPerformed: true}, {ActionPerformed: true, Evidence: proof.Evidence, Acceptance: proof.Acceptance, ReviewedSHA: "old", Reviewer: proof.Reviewer}} {
		if err := g.Transition("task", Approved, "controller", "approve", bad); err == nil {
			t.Fatal("incomplete proof accepted")
		}
	}
	after, _ := json.Marshal(g)
	if string(before) != string(after) {
		t.Fatal("failed transitions mutated graph")
	}
	move(t, &g, "task", RequestChanges, TransitionProof{})
	move(t, &g, "task", Ready, TransitionProof{})
	move(t, &g, "task", Active, TransitionProof{})
	move(t, &g, "task", WaitingForReview, TransitionProof{OutputSHA: strings.Repeat("b", 40)})
	if err := g.Transition("task", Approved, "controller", "stale approval", proof); err == nil {
		t.Fatal("stale SHA accepted")
	}
	proof.ReviewedSHA = strings.Repeat("b", 40)
	move(t, &g, "task", Approved, proof)
	move(t, &g, "task", Completed, proof)
	if err := g.Transition("task", RequestChanges, "controller", "stale", TransitionProof{}); err == nil {
		t.Fatal("completed task rolled back")
	}
	path := filepath.Join(t.TempDir(), "graph.json")
	if err := g.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadTaskGraph(path)
	if err != nil || !equalJSON(g, loaded) {
		t.Fatalf("roundtrip: %v", err)
	}
	loaded.History[len(loaded.History)-1].Proof = nil
	if err := loaded.Validate(); err == nil {
		t.Fatal("missing proof accepted")
	}
}

func TestGraphDependenciesAndCycles(t *testing.T) {
	a, b := graphTask("a"), graphTask("b")
	b.DependsOn = []TaskDependency{{TaskID: "a", State: Approved}}
	g := trackedGraph(t, a, b)
	if err := g.Transition("b", Ready, "human", "start", TransitionProof{}); err == nil {
		t.Fatal("unmet dependency accepted")
	}
	for _, status := range []TaskStatus{Ready, Active, WaitingForReview} {
		move(t, &g, "a", status, TransitionProof{})
	}
	// Update the output through the evidence handoff before approval.
	move(t, &g, "a", Blocked, TransitionProof{})
	ta, _ := g.task("a")
	ta.TargetSHA = "sha"
	if err := g.Track(ta, "human", "target"); err != nil {
		t.Fatal(err)
	}
	move(t, &g, "a", Ready, TransitionProof{})
	move(t, &g, "a", Active, TransitionProof{})
	move(t, &g, "a", WaitingForReview, TransitionProof{})
	p := TransitionProof{ActionPerformed: true, Evidence: a.RequiredEvidence, Acceptance: a.Acceptance, Reviewer: a.Reviewer, ReviewedSHA: "sha"}
	move(t, &g, "a", Approved, p)
	move(t, &g, "b", Ready, TransitionProof{})
	move(t, &g, "a", Blocked, TransitionProof{})
	tb, _ := g.task("b")
	if tb.Status != Blocked {
		t.Fatal("dependent readiness not reevaluated")
	}
	ta, _ = g.task("a")
	ta.DependsOn = []TaskDependency{{TaskID: "b", State: Completed}}
	if err := g.Track(ta, "human", "cycle"); err == nil {
		t.Fatal("cycle accepted")
	}
	ta, _ = g.task("a")
	ta.ParentID = "b"
	if err := g.Track(ta, "human", "parent"); err != nil {
		t.Fatal(err)
	}
	tb, _ = g.task("b")
	tb.ParentID = "a"
	if err := g.Track(tb, "human", "parent cycle"); err == nil {
		t.Fatal("parent cycle accepted")
	}
}

func TestGraphScopeOutAndTaskLost(t *testing.T) {
	task := graphTask("concern-task")
	task.Status = Blocked
	task.DeferredReason = "outside current implementation"
	task.NextAction = "investigate later"
	task.ResumeCondition = "parent ready"
	g := trackedGraph(t, task)
	if err := g.AddConcern(Concern{ID: "concern", Source: "human", Text: "naming"}); err == nil {
		t.Fatal("unassigned concern accepted")
	}
	if err := g.AddConcern(Concern{ID: "concern", Source: "human", Text: "naming", TaskID: task.ID}); err != nil {
		t.Fatal(err)
	}
	if err := g.CheckExecutable(task.ID, task.Repo); err == nil {
		t.Fatal("scope-out executed")
	}
	if err := g.Track(graphTask("other"), "human", "other work"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "graph.json")
	if err := g.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadTaskGraph(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := loaded.task(task.ID)
	if !equalJSON(got, task) || loaded.Concerns[0].TaskID != task.ID {
		t.Fatal("lost deferred task")
	}
	got.Decisions = nil
	if err := loaded.Track(got, "human", "forget decision"); err == nil {
		t.Fatal("decision removed")
	}
	got = task
	got.ID = ""
	if err := loaded.Track(got, "human", "missing ID"); err == nil {
		t.Fatal("missing task ID")
	}
}

func TestGraphCompletionWaiversAndDeferredCancellation(t *testing.T) {
	parent, child := graphTask("parent"), graphTask("child")
	parent.TargetSHA = "target"
	parent.ExceptionIDs = []string{"waiver:1"}
	parent.Decisions = append(parent.Decisions, TaskDecision{ID: "waiver:1", Source: "human:2", Text: "waive tests and child"})
	child.ParentID = parent.ID
	g := trackedGraph(t, parent, child)
	proof := TransitionProof{ActionPerformed: true, Reviewer: parent.Reviewer, ReviewedSHA: parent.TargetSHA,
		Waivers: []TaskWaiver{{DecisionID: "waiver:1", Acceptance: parent.Acceptance, Evidence: parent.RequiredEvidence, Children: []string{child.ID}}}}
	for _, s := range []TaskStatus{Ready, Active, WaitingForReview} {
		move(t, &g, parent.ID, s, TransitionProof{})
	}
	bad := proof
	bad.Waivers = []TaskWaiver{{DecisionID: "unknown", Acceptance: parent.Acceptance, Evidence: parent.RequiredEvidence, Children: []string{child.ID}}}
	if err := g.Transition(parent.ID, Approved, "human", "unapproved waiver", bad); err == nil {
		t.Fatal("unapproved waiver accepted")
	}
	move(t, &g, parent.ID, Approved, proof)
	move(t, &g, parent.ID, Completed, proof)
	deferred := graphTask("deferred")
	deferred.Status = Blocked
	deferred.DeferredReason = "scope out"
	deferred.NextAction = "check later"
	deferred.ResumeCondition = "authorization"
	if err := g.Track(deferred, "human", "defer"); err != nil {
		t.Fatal(err)
	}
	move(t, &g, deferred.ID, Cancelled, TransitionProof{})
	got, _ := g.task(deferred.ID)
	if got.DeferredReason == "" {
		t.Fatal("lost deferral reason")
	}
}

func TestGraphGateMigrationAndBrokenStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.json")
	base := Task{ID: "task", Title: "task", Repo: "owner/repo"}
	if err := (Ledger{Tasks: []Task{base}}).Save(path); err != nil {
		t.Fatal(err)
	}
	if err := CheckGraphDispatch(path, base, base.Repo); err != nil {
		t.Fatal("legacy changed", err)
	}

	g := trackedGraph(t, graphTask("task"))
	move(t, &g, "task", Ready, TransitionProof{})
	if err := g.Save(GraphPath(path)); err != nil {
		t.Fatal(err)
	}
	if err := CheckGraphDispatch(path, base, base.Repo); err != nil {
		t.Fatal(err)
	}
	if err := CheckGraphDispatch(path, Task{ID: "lost"}, base.Repo); !errors.Is(err, ErrGraphRejected) {
		t.Fatal("untracked task accepted")
	}
	g.Tasks[0].Title = "changed outside history"
	if err := g.Save(GraphPath(path)); err == nil {
		t.Fatal("inconsistent history saved")
	}
	if err := os.WriteFile(GraphPath(path), []byte(`{"version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckGraphDispatch(path, base, base.Repo); err == nil {
		t.Fatal("future version accepted")
	}
	if err := os.WriteFile(GraphPath(path), []byte(`{"version":1,"typo":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTaskGraph(GraphPath(path)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if err := DecodeGraphInput(strings.NewReader(`{} {}`), &TaskGraph{}); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	if err := g.Save(filepath.Join(t.TempDir(), "missing", "graph.json")); err == nil {
		t.Fatal("save failure swallowed")
	}
}
