package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func graphTask(id string) GraphTask {
	return GraphTask{ID: id, Title: id, Repo: "owner/repo", Type: "IMPLEMENTATION", Issue: "Issue #47",
		Status: Draft, Owner: "CODER", Reviewer: "reviewer-run", CreatedFrom: "request:1",
		Objective: "fix", Scope: []string{"src"}, Acceptance: []string{"tests pass"},
		RequiredEvidence: []string{"test-report"}, ExitCondition: "report", Worktree: "/work/task",
		ValidationCommands: []string{"go test ./..."},
		Branch:             "work/task", BaselineSHA: strings.Repeat("a", 40),
		AuthorizationID: "decision:1", Decisions: []TaskDecision{{ID: "decision:1", Source: "human:1", Text: "allow scope"}},
		FileAccess: DefaultFileAccessPolicy()}
}

func TestGraphRepositoryPreservesNestedNamespaces(t *testing.T) {
	for _, repo := range []string{"owner/repo", "group/subgroup/project", strings.Repeat("group/", 21) + "project"} {
		task := graphTask("nested")
		task.Repo = repo
		graph := trackedGraph(t, task)
		if graph.Tasks[0].Repo != repo {
			t.Fatal("repository namespace changed", graph.Tasks[0].Repo)
		}
	}
	for _, repo := range []string{"repo", "/owner/repo", "owner/repo/", "group//repo", "group/./repo",
		"group/../repo", "group/sub group/repo", "group/sub\r/repo", `group\sub/repo`,
		"group/sub%2Frepo", "group/sub?query/repo", "https://host/group/repo"} {
		graph := TaskGraph{Version: 1}
		task := graphTask("invalid")
		task.Repo = repo
		if err := graph.Track(task, "human", "register"); err == nil {
			t.Fatal("accepted noncanonical repository", repo)
		}
	}
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

func TestSnapshotTaskCompletionRequiresExactTreeReview(t *testing.T) {
	task := graphTask("snapshot-task")
	task.OutputMode = OutputModeWorktreeTreeSnapshot
	g := trackedGraph(t, task)
	move(t, &g, task.ID, Ready, TransitionProof{})
	dispatch, err := NewGraphDispatch(g, "snapshot-dispatch", task.ID, task.Repo,
		RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if dispatch.Instruction.OutputMode != OutputModeWorktreeTreeSnapshot {
		t.Fatalf("snapshot mode missing from approved Dispatch: %+v", dispatch.Instruction)
	}
	move(t, &g, task.ID, Active, TransitionProof{})
	tree := strings.Repeat("b", 40)
	move(t, &g, task.ID, WaitingForReview, TransitionProof{
		OutputSHA: task.BaselineSHA, OutputTreeSHA: tree,
		OutputChangedFiles: []string{"src/file.go"},
	})
	saved, _ := g.GraphTask(task.ID)
	if saved.TargetSHA != task.BaselineSHA || saved.TargetTreeSHA != tree ||
		!slices.Equal(saved.TargetChangedFiles, []string{"src/file.go"}) {
		t.Fatalf("snapshot handoff was not retained: %+v", saved)
	}
	proof := TransitionProof{
		ActionPerformed: true, Evidence: []string{"test-report"}, Acceptance: task.Acceptance,
		ReviewedSHA: task.BaselineSHA, ReviewedTreeSHA: tree, Reviewer: task.Reviewer,
	}
	bad := proof
	bad.ReviewedTreeSHA = strings.Repeat("c", 40)
	if err := g.Transition(task.ID, Approved, "controller", "wrong tree", bad); err == nil {
		t.Fatal("accepted review of a different worktree tree")
	}
	move(t, &g, task.ID, Approved, proof)
	move(t, &g, task.ID, Completed, proof)
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchLifecycleRequiresAcknowledgement(t *testing.T) {
	g := trackedGraph(t, graphTask("task"))
	move(t, &g, "task", Ready, TransitionProof{})
	dispatch, err := NewGraphDispatch(g, "dispatch", "task", "owner/repo", RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}

	if err := g.IssueDispatch(dispatch, "controller", "issue"); err != nil {
		t.Fatal(err)
	}
	if err := g.TransitionDispatch("dispatch", DispatchAcknowledged, "worker", "ack", false, false, nil); err == nil {
		t.Fatal("accepted acknowledgement without confirmation")
	}
	if err := g.TransitionDispatch("dispatch", DispatchRunning, "worker", "start", true, true, nil); err == nil {
		t.Fatal("accepted start without persisted acknowledgement")
	}
	if err := g.TransitionDispatch("dispatch", DispatchAcknowledged, "worker", "ack", true, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := g.TransitionDispatch("dispatch", DispatchRunning, "worker", "start", true, true, nil); err != nil {
		t.Fatal(err)
	}
	suspended := &DispatchResult{
		DispatchID: "dispatch", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		Status: DispatchSuspended, StartedSHA: strings.Repeat("a", 40), Evidence: []string{"safe-stop:1"},
		NextTransition: Active, ResumeCondition: "human authorizes continued execution",
	}
	if err := g.TransitionDispatch("dispatch", DispatchSuspended, "worker", "safe stop", false, false, suspended); err != nil {
		t.Fatal(err)
	}
	stopped := cloneGraph(g)
	cancelled := *suspended
	cancelled.Status, cancelled.NextTransition = DispatchCancelled, Blocked
	cancelled.Evidence = []string{"runtime-stop:session"}
	if err := stopped.TransitionDispatch("dispatch", DispatchCancelled, "worker", "confirmed stop", false, false, &cancelled); err != nil {
		t.Fatal("terminal stop lost its result when leaving SUSPENDED", err)
	}
	savedStop, _ := stopped.GraphDispatch("dispatch")
	if savedStop.Result == nil || savedStop.Result.Status != DispatchCancelled ||
		stopped.DispatchHistory[len(stopped.DispatchHistory)-2].Result.Status != DispatchSuspended {
		t.Fatal("cancelled result or original suspension history was lost")
	}
	if err := g.TransitionDispatch("dispatch", DispatchRunning, "worker", "resume", true, true, nil); err != nil {
		t.Fatal(err)
	}
	result := &DispatchResult{
		DispatchID: "dispatch", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		Status: DispatchSucceeded, StartedSHA: strings.Repeat("a", 40), OutputSHA: strings.Repeat("b", 40),
		ChangedFiles: []string{"src/file.go"}, Validations: []DispatchValidation{{
			Command: "go test ./...", ExitCode: 0, Worktree: "/work/task", SHA: strings.Repeat("b", 40),
			StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
		}},
		Evidence: []string{"test-report"}, NextTransition: WaitingForEvidence,
	}
	result.GitEvidence = testGitEvidence(dispatch, result)
	if err := g.TransitionDispatch("dispatch", DispatchSucceeded, "worker", "finish", false, false, result); err != nil {
		t.Fatal(err)
	}
	if err := g.TransitionDispatch("dispatch", DispatchSucceeded, "worker", "stale result", false, false, result); err == nil {
		t.Fatal("accepted stale result")
	}
	task := g.Tasks[0]
	task.DeferredReason = ""
	task.ResumeCondition = ""
	task.Objective = "complete the remaining scoped work"
	task.AuthorizationID = "decision:2"
	task.Decisions = append(task.Decisions, TaskDecision{ID: "decision:2", Source: "human:2", Text: "continue with the remaining scope"})
	if err := g.Track(task, "human", "authorize follow-up"); err != nil {
		t.Fatal(err)
	}
	move(t, &g, "task", Ready, TransitionProof{})
	next, err := NewGraphDispatch(g, "dispatch-2", "task", "owner/repo", RelationshipAmends, "dispatch", "Clarified remaining scope")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(next.Instruction.ChangedFields, "objective") ||
		!slices.Contains(next.Instruction.ChangedFields, "authorizationId") ||
		!slices.Contains(next.Instruction.PreservedFields, "baselineSha") {
		t.Fatalf("amendment comparison missing changes or preserved fields: %+v", next.Instruction)
	}
	if err := g.IssueDispatch(next, "controller", "issue amended work"); err != nil {
		t.Fatal(err)
	}
	if err := g.TransitionDispatch("dispatch-2", DispatchAcknowledged, "worker", "ack", true, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := g.TransitionDispatch("dispatch-2", DispatchRunning, "worker", "start", true, true, nil); err != nil {
		t.Fatal(err)
	}
	if err := g.TransitionDispatch("dispatch-2", DispatchSucceeded, "worker", "stale result", false, false, result); err == nil {
		t.Fatal("old Dispatch result completed a newer Dispatch")
	}
	rendered, err := RenderDispatchInstruction(dispatch)
	if err != nil || !strings.HasPrefix(rendered, "DISPATCH TO: \"CODER\"\n") {
		t.Fatalf("recipient must be first: %q %v", rendered, err)
	}
	for _, field := range []string{"AUTHORIZED ACTIONS:", "FORBIDDEN ACTIONS:", "WORKTREE:", "BASELINE SHA:", "RETURN FORMAT:"} {
		if !strings.Contains(rendered, field) {
			t.Fatalf("instruction missing %s: %s", field, rendered)
		}
	}
	g.Dispatches[0].Instruction.Objective = "tampered"
	if err := g.Validate(); err == nil {
		t.Fatal("tampered instruction accepted")
	}
}

func TestDispatchRequiresVersionedMemorySnapshot(t *testing.T) {
	task := graphTask("task")
	task.MemoryRefs = []string{"decision:4"}
	g := trackedGraph(t, task)
	move(t, &g, task.ID, Ready, TransitionProof{})
	if _, err := NewGraphDispatch(g, "dispatch", task.ID, task.Repo, RelationshipNone, "", ""); err == nil {
		t.Fatal("dispatch without the referenced Memory snapshot was accepted")
	}
	context := []DispatchMemoryEntry{{
		Reference: "decision:4", Version: 2, Scope: "project", Status: "approved",
		Title: "Review output SHA", Body: "Bind the review to the exact commit.",
		SourceRefs: []string{"issue:52"},
	}}
	dispatch, err := NewGraphDispatch(g, "dispatch", task.ID, task.Repo,
		RelationshipNone, "", "", context)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.IssueDispatch(dispatch, "controller", "issue"); err != nil {
		t.Fatal(err)
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	context[0].SourceRefs[0] = "mutated"
	if g.Dispatches[0].Instruction.MemoryContext[0].SourceRefs[0] != "issue:52" {
		t.Fatal("Dispatch Memory source references were not cloned")
	}
}

func TestRuntimeSessionBindingPersistsTaskRoleAndConfiguration(t *testing.T) {
	coderTask := graphTask("coder-task")
	reviewerTask := graphTask("reviewer-task")
	reviewerTask.Owner = "REVIEWER"
	g := trackedGraph(t, coderTask, reviewerTask)
	move(t, &g, "coder-task", Ready, TransitionProof{})
	move(t, &g, "reviewer-task", Ready, TransitionProof{})
	coderDispatch, err := NewGraphDispatch(g, "coder-dispatch", "coder-task", "owner/repo", RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.IssueDispatch(coderDispatch, "controller", "issue coder"); err != nil {
		t.Fatal(err)
	}
	if err := g.BindRuntimeSession("coder-dispatch", "sdk-session", "config-hash"); err != nil {
		t.Fatal(err)
	}
	if err := g.BindRuntimeSession("coder-dispatch", "sdk-session", "config-hash"); err != nil {
		t.Fatalf("idempotent bind: %v", err)
	}
	if err := g.BindRuntimeSession("coder-dispatch", "sdk-session", "changed-config"); err == nil {
		t.Fatal("changed role configuration reused an existing session")
	}
	reviewDispatch, err := NewGraphDispatch(g, "review-dispatch", "reviewer-task", "owner/repo",
		RelationshipIndependent, "coder-dispatch", "independent review")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.IssueDispatch(reviewDispatch, "controller", "issue reviewer"); err != nil {
		t.Fatal(err)
	}
	if err := g.BindRuntimeSession("review-dispatch", "sdk-session", "config-hash"); err == nil {
		t.Fatal("runtime session crossed Task and role boundaries")
	}
	if err := g.BindRuntimeSession("review-dispatch", "review-session", "review-config"); err != nil {
		t.Fatal(err)
	}
	if err := g.Validate(); err != nil {
		t.Fatalf("valid bindings rejected: %v", err)
	}
}

func TestLegacyDispatchResultRemainsReadable(t *testing.T) {
	dispatch := GraphDispatch{
		ID: "legacy-dispatch", TaskID: "task", Recipient: "CODER", Repository: "owner/repo",
		Instruction: DispatchInstruction{Version: 3, BaselineSHA: strings.Repeat("a", 40)},
	}
	result := &DispatchResult{
		DispatchID: dispatch.ID, TaskID: dispatch.TaskID, Recipient: dispatch.Recipient,
		Repository: dispatch.Repository, Status: DispatchSucceeded,
		StartedSHA: dispatch.Instruction.BaselineSHA, OutputSHA: strings.Repeat("b", 40),
		ChangedFiles: []string{"src/file.go"},
		Validations:  []DispatchValidation{{Command: "go test ./...", ExitCode: 0}},
		Evidence:     []string{"test-report"}, NextTransition: WaitingForEvidence,
	}
	if err := dispatch.ValidateResult(DispatchSucceeded, result); err != nil {
		t.Fatalf("legacy Dispatch result rejected: %v", err)
	}
}

func TestReviewerDispatchResultValidatesAcceptanceFindingsAndWorktree(t *testing.T) {
	task := graphTask("review")
	task.Owner = "REVIEWER"
	g := trackedGraph(t, task)
	move(t, &g, task.ID, Ready, TransitionProof{})
	dispatch, err := NewGraphDispatch(g, "review-dispatch", task.ID, task.Repo, RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.IssueDispatch(dispatch, "controller", "issue review"); err != nil {
		t.Fatal(err)
	}
	if err := g.TransitionDispatch(dispatch.ID, DispatchAcknowledged, "reviewer", "ack", true, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := g.TransitionDispatch(dispatch.ID, DispatchRunning, "reviewer", "start", true, true, nil); err != nil {
		t.Fatal(err)
	}
	worktreeChanged := false
	result := &DispatchResult{
		DispatchID: dispatch.ID, TaskID: task.ID, Recipient: "REVIEWER", Repository: task.Repo,
		Status: DispatchSucceeded, StartedSHA: task.BaselineSHA, OutputSHA: task.BaselineSHA,
		ChangedFiles: []string{}, Validations: []DispatchValidation{{
			Command: "go test ./...", ExitCode: 0, Worktree: "/work/task", SHA: task.BaselineSHA,
			StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC(),
		}},
		Evidence: []string{"test-report", "review-report:1"}, Blockers: []string{}, ReviewVerdict: "APPROVE",
		Acceptance: []DispatchAcceptance{{Criterion: task.Acceptance[0], Status: "PASS", Evidence: []string{"test-report"}}},
		Findings:   []DispatchFinding{}, WorktreeChanged: &worktreeChanged,
		NextTransition: WaitingForEvidence,
	}
	result.GitEvidence = testGitEvidence(dispatch, result)
	incomplete := *result
	incomplete.Acceptance = nil
	if err := g.TransitionDispatch(dispatch.ID, DispatchSucceeded, "reviewer", "incomplete result", false, false, &incomplete); err == nil {
		t.Fatal("accepted a Reviewer result without acceptance decisions")
	}
	result.GitEvidence.WorktreeReleased = false
	if err := g.TransitionDispatch(dispatch.ID, DispatchSucceeded, "reviewer", "unreleased worktree", false, false, result); err == nil {
		t.Fatal("adopted Reviewer success before worktree release")
	}
	result.GitEvidence.WorktreeReleased = true
	if err := g.TransitionDispatch(dispatch.ID, DispatchSucceeded, "reviewer", "verified result", false, false, result); err != nil {
		t.Fatal(err)
	}
	accepted, _ := g.GraphDispatch(dispatch.ID)
	if accepted.Result == nil || accepted.Result.WorktreeChanged == nil || *accepted.Result.WorktreeChanged ||
		len(accepted.Result.Acceptance) != 1 || accepted.Result.ReviewVerdict != "APPROVE" {
		t.Fatalf("Reviewer result was not preserved: %+v", accepted.Result)
	}
}

func testGitEvidence(dispatch GraphDispatch, result *DispatchResult) *DispatchGitEvidence {
	return &DispatchGitEvidence{
		Worktree: "/work/task", Repository: dispatch.Repository, Branch: dispatch.Instruction.Branch,
		BaselineSHA: dispatch.Instruction.BaselineSHA, HeadSHA: result.OutputSHA,
		ParentSHAs: []string{}, ChangedFiles: slices.Clone(result.ChangedFiles),
		WorktreeClean: true, VerifiedAt: time.Now().UTC(),
		WorktreeReleased: dispatch.Recipient == "REVIEWER",
	}
}

func TestDispatchRelationshipAndDerivedTaskLinks(t *testing.T) {
	parent := graphTask("parent")
	first := graphTask("first")
	first.ParentID = parent.ID
	second := graphTask("second")
	second.ParentID = parent.ID
	blocked := graphTask("blocked")
	blocked.ParentID = parent.ID
	blocked.DependsOn = []TaskDependency{{TaskID: first.ID, State: Approved}}
	g := trackedGraph(t, parent, first, second, blocked)
	move(t, &g, "first", Ready, TransitionProof{})
	move(t, &g, "second", Ready, TransitionProof{})
	firstDispatch, err := NewGraphDispatch(g, "d-first", "first", "owner/repo", RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(firstDispatch.Instruction.SiblingTaskIDs, []string{"second", "blocked"}) ||
		!slices.Equal(firstDispatch.Instruction.Blocks, []string{"blocked"}) {
		t.Fatalf("task relations not included in instruction: %+v", firstDispatch.Instruction)
	}
	if err := g.IssueDispatch(firstDispatch, "controller", "issue"); err != nil {
		t.Fatal(err)
	}
	secondDispatch, err := NewGraphDispatch(g, "d-second", "second", "owner/repo", RelationshipIndependent,
		"d-first", "Separate Task with independent scope")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.IssueDispatch(secondDispatch, "controller", "issue"); err != nil {
		t.Fatal(err)
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGraphDispatch(g, "d-retry", "first", "owner/repo", RelationshipNone, "", ""); err == nil {
		t.Fatal("reissued Task accepted without explicit relation")
	}
}

func TestHumanDecisionMustBeRecordedBeforeDispatch(t *testing.T) {
	task := graphTask("task")
	task.HumanDecision = &HumanDecisionRequest{
		ID: "question:1", Question: "Which package should be changed?",
		Options: []HumanDecisionOption{
			{ID: "a", Description: "Change package A"},
			{ID: "b", Description: "Change package B"},
		},
		RecommendedOptionID: "a", RecommendationReason: "A owns the affected behavior",
	}
	g := trackedGraph(t, task)
	move(t, &g, task.ID, HumanDecisionRequired, TransitionProof{})
	if err := g.CheckExecutable(task.ID, task.Repo); err == nil {
		t.Fatal("dispatched while a decision was pending")
	}
	task = g.Tasks[0]
	task.HumanDecision.SelectedOptionID = "a"
	task.HumanDecision.DecisionRecordID = "decision:human"
	task.Decisions = append(task.Decisions, TaskDecision{
		ID: "decision:human", Source: "human:1", Text: "Selected option a",
	})
	if err := g.Track(task, "human", "recorded decision"); err != nil {
		t.Fatal(err)
	}
	move(t, &g, task.ID, Ready, TransitionProof{})
	if err := g.CheckExecutable(task.ID, task.Repo); err != nil {
		t.Fatalf("recorded decision did not unblock task: %v", err)
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
