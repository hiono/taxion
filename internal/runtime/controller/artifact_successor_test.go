package controller

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestFailedArtifactSuccessorBindsReceiptAndRunsCoderThenReview(t *testing.T) {
	db, ledger, request, selected := artifactFixture(t, []string{"test -s internal/app.go"}, false, "CODER")
	old, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	old, err = ValidateReviewFlowInput(t.Context(), db, ledger, old.ID, request.Configuration)
	if err == nil || old.Status != "STOPPED" || old.Artifact.Validation.Status != "FAILED" || len(old.DispatchIDs) != 0 {
		t.Fatalf("expected failed non-model input: %+v %v", old, err)
	}
	task := old.Contract
	task.ID, task.Status, task.AuthorizationID, task.Decisions = "repair-input", core.Draft, "", nil
	task.Supersedes, task.CreatedFrom = old.TaskID, "flow:"+old.ID
	task.Worktree, task.Branch = filepath.Join(t.TempDir(), "repair"), "work/repair-input"
	task.TargetSHA, task.TargetTreeSHA = old.Artifact.Target.CommitSHA, old.Artifact.Target.TreeSHA
	task.TargetChangedFiles = old.Artifact.Target.ChangedFiles
	gitTest(t, old.Contract.Worktree, "worktree", "add", "-b", task.Branch, task.Worktree, task.BaselineSHA)
	t.Run("reject predecessor alias", func(t *testing.T) {
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(old.Contract.Worktree, alias); err != nil {
			t.Skip("symlink unavailable:", err)
		}
		aliased := task
		aliased.Worktree = alias
		if _, err := artifactPredecessorDecision(core.TaskGraph{ReviewFlows: []core.ReviewFlow{old}}, aliased); err == nil {
			t.Fatal("accepted predecessor alias as a separate worktree")
		}
		aliased.Worktree = old.Contract.Worktree
		if _, err := artifactPredecessorDecision(core.TaskGraph{ReviewFlows: []core.ReviewFlow{old}}, aliased); err == nil {
			t.Fatal("accepted original worktree as a separate worktree")
		}
	})
	if err := os.MkdirAll(filepath.Join(task.Worktree, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, path := range task.TargetChangedFiles {
		data, err := os.ReadFile(filepath.Join(old.Contract.Worktree, path))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(task.Worktree, path), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, mutate := range []func(*core.ReviewFlow){
		func(f *core.ReviewFlow) { f.RunnerID = "live-runner" },
		func(f *core.ReviewFlow) { f.Status = "RUNNING" },
		func(f *core.ReviewFlow) { f.Artifact.Validation.Status = "RUNNING" },
		func(f *core.ReviewFlow) { f.Artifact.Validation.Status = "PENDING" },
		func(f *core.ReviewFlow) { f.Artifact.Validation.Status = "PASSED" },
	} {
		bad := old
		artifact := *old.Artifact
		bad.Artifact = &artifact
		mutate(&bad)
		if _, err := artifactPredecessorDecision(core.TaskGraph{ReviewFlows: []core.ReviewFlow{bad}}, task); err == nil {
			t.Fatal("accepted active or non-failed predecessor")
		}
	}
	boundReceipt, _ := artifactPredecessorDecision(core.TaskGraph{ReviewFlows: []core.ReviewFlow{old}}, task)
	changed := old
	artifact := *old.Artifact
	changed.Artifact = &artifact
	changed.Artifact.Validation.Error = "different receipt"
	staleReceipt, _ := artifactPredecessorDecision(core.TaskGraph{ReviewFlows: []core.ReviewFlow{changed}}, task)
	if reflect.DeepEqual(boundReceipt, staleReceipt) {
		t.Fatal("receipt change did not invalidate proposal binding")
	}
	proposal, err := ProposeIntake(db, IntakeRequest{Task: task, Request: "Repair supplied input, preserve acceptance and validation.",
		Actor: "human", Source: "human:repair-input"})
	if err != nil {
		t.Fatal(err)
	}
	var bound bool
	for _, decision := range proposal.Decisions {
		bound = bound || strings.HasPrefix(decision.ID, "artifact-predecessor:") &&
			strings.Contains(decision.Text, old.Artifact.Target.Hash) && strings.Contains(decision.Text, "receiptHash")
	}
	if !bound {
		t.Fatal("proposal hash omitted failed receipt and artifact identity")
	}
	hash, _ := IntakeHash(proposal)
	confirm := IntakeConfirmation{TaskID: proposal.ID, ExpectedHash: hash, Actor: "human", Source: "human:repair-input",
		Reason: "Approve bounded Coder repair and same-target review", Selection: selected.Config,
		ConfigurationDecisionID: selected.Config.Execution.ApprovalRef + "@task:" + proposal.ID,
		AdditionalTaskIDs:       []string{"repair-flow-review-0"}, ReplacesFlow: old.ID}
	for _, mutate := range []func(*IntakeConfirmation){
		func(c *IntakeConfirmation) { c.ExpectedHash = "sha256:stale" },
		func(c *IntakeConfirmation) { c.Target = &core.FeedbackTarget{} },
		func(c *IntakeConfirmation) { c.ReplacesFlow = "" },
	} {
		bad := confirm
		mutate(&bad)
		if _, err := ConfirmIntake(db, bad); err == nil {
			t.Fatal("accepted stale approval, invented target or absent predecessor")
		}
	}
	path := filepath.Join(task.Worktree, task.TargetChangedFiles[0])
	original, _ := os.ReadFile(path)
	if err := os.WriteFile(path, []byte("drift"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfirmIntake(db, confirm); err == nil {
		t.Fatal("confirmed changed successor input")
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	oldPath := filepath.Join(old.Contract.Worktree, task.TargetChangedFiles[0])
	if err := os.WriteFile(oldPath, []byte("predecessor drift"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfirmIntake(db, confirm); err == nil {
		t.Fatal("confirmed changed predecessor input")
	}
	if err := os.WriteFile(oldPath, original, 0600); err != nil {
		t.Fatal(err)
	}
	approved, err := ConfirmIntake(db, confirm)
	if err != nil {
		t.Fatal(err)
	}
	next := request
	budget := *request.StageBudget
	budget.ReviewNanoAIU = 10
	next.StageBudget = &budget
	next.ID, next.TaskID, next.ArtifactSource, next.StagePlanHash = "repair-flow", approved.ID, "", ""
	next.ContinuationChange, next.SavedOutputDecision = "authorize previously excluded implementation", "retain failed unverified input and receipt"
	state, _ := db.LoadWorkspace()
	preview, err := PreviewStagePlan(state.Graph, next)
	if err != nil {
		t.Fatal(err)
	}
	next.StagePlanHash = preview.Plan.Hash
	flow, err := StartReviewFlow(db, ledger, next)
	if err != nil || flow.Artifact != nil || flow.Budget.MaxCoderSessions != 1 {
		t.Fatalf("expected bounded normal Coder successor: %+v %v", flow, err)
	}
	executeFlowDispatch(t, db, flow, "", true, selected)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true, selected)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Status != "COMPLETED" || len(flow.DispatchIDs) != 2 {
		t.Fatalf("repair and independent review failed: %+v %v", flow, err)
	}
	state, _ = db.LoadWorkspace()
	preserved, _ := state.Graph.ReviewFlow(old.ID)
	if !reflect.DeepEqual(old, preserved) {
		t.Fatal("successor rewrote failed predecessor or its validation receipt")
	}
}
