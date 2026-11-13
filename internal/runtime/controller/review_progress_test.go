package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func reviewProgressFixture(t *testing.T) (*store.Store, string, core.ReviewFlow, core.GraphDispatch, core.GraphDispatch, *Controller, *scriptedRuntime) {
	t.Helper()
	db, ledger, request := reviewFlowFixtureWithOutputMode(t, core.OutputModeWorktreeTreeSnapshot)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := prepareReview(t.Context(), db, flow.DispatchIDs[1], flow.Contract.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	worker := &scriptedRuntime{fakeRuntime: fakeRuntime{
		role: "REVIEWER", hash: "progress-review", worktree: reviewer.Instruction.Worktree,
	}, script: func() error { return nil }}
	control, err := New(db, worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(t.Context(), reviewer.ID); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(t.Context(), reviewer.ID, "Review the exact saved result"); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	reviewer, _ = state.Graph.GraphDispatch(reviewer.ID)
	return db, ledger, flow, coder, reviewer, control, worker
}

func reportedReviewProgress(reviewer, coder core.GraphDispatch) core.ReviewProgress {
	return core.ReviewProgress{
		Version: core.ReviewProgressVersion, DispatchID: reviewer.ID, InstructionHash: reviewer.InstructionHash,
		CoderDispatchID: coder.ID, TargetSHA: coder.Result.OutputSHA, TargetTreeSHA: coder.Result.OutputTreeSHA,
		ReviewedScope: []string{"internal/app.go:1-3"}, UnreviewedScope: []string{"acceptance:behavior"},
		Findings: []core.DispatchFinding{}, Evidence: []string{"read:internal/app.go:1-3"},
	}
}

func TestReviewProgressPersistsBeforeStopAndTransfersWithoutCoder(t *testing.T) {
	for _, withProgress := range []bool{true, false} {
		t.Run(map[bool]string{true: "reported", false: "missing"}[withProgress], func(t *testing.T) {
			db, ledger, flow, coder, reviewer, control, _ := reviewProgressFixture(t)
			progress := reportedReviewProgress(reviewer, coder)
			if withProgress {
				if _, err := control.SaveReviewProgressForSession(t.Context(), reviewer.ID, reviewer.RuntimeSessionID, progress); err != nil {
					t.Fatal(err)
				}
				// Runtime events and controller progress use independent receipt streams.
				if err := control.recordEvent(reviewer.ID, "runtime-stream", runtime.Event{
					Type: runtime.EventOutput, Payload: "not a final verdict", Sequence: 1,
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := stopFlowForBudget(db, flow.ID, "elapsed budget reached"); err != nil {
				t.Fatal(err)
			}
			if _, err := control.SaveReviewProgressForSession(t.Context(), reviewer.ID, reviewer.RuntimeSessionID, progress); err == nil {
				t.Fatal("stopped flow accepted another progress report")
			}
			state, _ := db.LoadWorkspace()
			stopped, _ := state.Graph.ReviewFlow(flow.ID)
			if _, err := reviewProgressDecision(state, stopped, coder); err == nil {
				t.Fatal("unconfirmed cancellation permitted transfer")
			}
			if err := control.Stop(t.Context(), reviewer.ID); err != nil {
				t.Fatal(err)
			}
			state, _ = db.LoadWorkspace()
			stopped, _ = state.Graph.ReviewFlow(flow.ID)
			decision, err := reviewProgressDecision(state, stopped, coder)
			if err != nil {
				t.Fatal(err)
			}
			if withProgress && !strings.Contains(decision.Text, `"status":"reported"`) ||
				!withProgress && !strings.Contains(decision.Text, `"status":"missing"`) {
				t.Fatal("missing progress or reported coverage misclassified", decision.Text)
			}
			status, err := ObserveRepairStatus(state, flow.ID, core.ApprovalConfiguration{})
			if err != nil || status.StopCause != "time-limit" {
				t.Fatal(status, err)
			}

			task := flow.Contract
			task.ID, task.Status, task.AuthorizationID, task.Decisions = "progress-successor", core.Draft, "", nil
			task.Supersedes, task.CreatedFrom = flow.TaskID, "flow:"+flow.ID
			task.Worktree, task.Branch = filepath.Join(t.TempDir(), "successor"), "work/progress-successor"
			gitTest(t, flow.Contract.Worktree, "worktree", "add", "-b", task.Branch, task.Worktree, coder.Result.OutputSHA)
			data, err := os.ReadFile(filepath.Join(flow.Contract.Worktree, "internal/app.go"))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(task.Worktree, "internal"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(task.Worktree, "internal/app.go"), data, 0600); err != nil {
				t.Fatal(err)
			}
			task.TargetSHA, task.TargetTreeSHA, task.TargetChangedFiles = coder.Result.OutputSHA, coder.Result.OutputTreeSHA, coder.Result.ChangedFiles
			proposal, err := ProposeIntake(db, IntakeRequest{Request: "Review retained target and reported progress", Task: task, Actor: "human", Source: "human:successor"})
			if err != nil {
				t.Fatal(err)
			}
			hash, _ := IntakeHash(proposal)
			target, err := ObserveFeedback(db, flow.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, _, _, selection := executionConfigurationFixture(t)
			if _, err := ConfirmIntake(db, IntakeConfirmation{
				TaskID: task.ID, ExpectedHash: hash, Actor: "human", Source: "human:successor", Reason: "Explicit new review budget",
				Selection: selection.Config, ReplacesFlow: flow.ID, Target: &target.Target,
				AdditionalTaskIDs: []string{"progress-successor-flow-review-0"},
			}); err != nil {
				t.Fatal(err)
			}
			zero := 0
			next, err := StartReviewFlow(db, ledger, ReviewFlowRequest{
				ID: "progress-successor-flow", TaskID: task.ID, Repository: task.Repo,
				ReviewRoot: filepath.Join(t.TempDir(), "reviews"), MaxRepairs: &zero, TimeoutSeconds: 60, MaxNanoAIU: 1e12,
				ContinueFromDispatchID: coder.ID, Authorization: core.TaskDecision{ID: "next-budget", Source: "human:new-budget", Text: "Approve independent review only"},
			})
			if err != nil {
				t.Fatal(err)
			}
			state, _ = db.LoadWorkspace()
			nextDispatch, _ := state.Graph.GraphDispatch(next.DispatchIDs[0])
			if len(next.DispatchIDs) != 1 || nextDispatch.Recipient != "REVIEWER" || nextDispatch.RelatedID != coder.ID {
				t.Fatal("continuation repeated Coder", next)
			}
			found := false
			for _, item := range nextDispatch.Instruction.Decisions {
				if item.ID == decision.ID {
					found = true
					if item.Text != decision.Text {
						t.Fatal("progress changed in transfer")
					}
				}
			}
			if !found {
				t.Fatal("successor instruction omitted progress disposition")
			}
		})
	}
}

func TestReviewProgressRejectsChangedTargetCancelledSessionAndInvalidReports(t *testing.T) {
	db, _, flow, coder, reviewer, control, worker := reviewProgressFixture(t)
	for _, failure := range []string{"target", "instruction", "version", "missing-scope", "overlap", "oversized", "missing-evidence", "wrong-session", "cancelled-context"} {
		t.Run(failure, func(t *testing.T) {
			progress := reportedReviewProgress(reviewer, coder)
			session := reviewer.RuntimeSessionID
			ctx := t.Context()
			switch failure {
			case "target":
				progress.TargetTreeSHA = strings.Repeat("f", 40)
			case "instruction":
				progress.InstructionHash = "different"
			case "version":
				progress.Version++
			case "missing-scope":
				progress.UnreviewedScope = nil
			case "overlap":
				progress.UnreviewedScope = progress.ReviewedScope
			case "oversized":
				progress.Evidence = []string{strings.Repeat("x", core.ReviewProgressMaxBytes)}
			case "missing-evidence":
				progress.Evidence = []string{}
			case "wrong-session":
				session = "other"
			case "cancelled-context":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			before, _ := db.LoadWorkspace()
			if _, err := control.SaveReviewProgressForSession(ctx, reviewer.ID, session, progress); err == nil {
				t.Fatal("invalid report saved")
			}
			after, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected report changed state")
			}
		})
	}
	path := filepath.Join(reviewer.Instruction.Worktree, "internal/app.go")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed target\n"), 0600); err != nil {
		t.Fatal(err)
	}
	progress := reportedReviewProgress(reviewer, coder)
	if _, err := control.SaveReviewProgressForSession(t.Context(), reviewer.ID, reviewer.RuntimeSessionID, progress); err == nil {
		t.Fatal("changed worktree accepted")
	}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := control.SaveReviewProgressForSession(t.Context(), reviewer.ID, reviewer.RuntimeSessionID, progress); err != nil {
		t.Fatal(err)
	}
	if err := stopFlowForBudget(db, flow.ID, "elapsed budget reached"); err != nil {
		t.Fatal(err)
	}
	worker.stopErr = errors.New("cannot confirm stop")
	if err := control.Stop(t.Context(), reviewer.ID); err == nil {
		t.Fatal("failed stop succeeded")
	}
	state, _ := db.LoadWorkspace()
	stopped, _ := state.Graph.ReviewFlow(flow.ID)
	if _, err := reviewProgressDecision(state, stopped, coder); err == nil {
		t.Fatal("cancel failure allowed transfer")
	}
	worker.stopErr = nil
	if err := control.Stop(t.Context(), reviewer.ID); err != nil {
		t.Fatal(err)
	}
	state, _ = db.LoadWorkspace()
	for i := range state.Records.Events {
		if state.Records.Events[i].Type == reviewProgressReceipt {
			state.Records.Events[i].Payload += " "
			break
		}
	}
	if _, err := reviewProgressDecision(state, stopped, coder); err == nil {
		t.Fatal("corrupt progress silently ignored")
	}
}

func TestReviewProgressRedactsAndBoundsSavedReceipts(t *testing.T) {
	db, _, _, coder, reviewer, control, _ := reviewProgressFixture(t)
	progress := reportedReviewProgress(reviewer, coder)
	progress.Evidence = []string{"Authorization: Bearer synthetic-secret-value"}
	for i := 0; i < core.ReviewProgressMaxRecords; i++ {
		if _, err := control.SaveReviewProgressForSession(t.Context(), reviewer.ID, reviewer.RuntimeSessionID, progress); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := control.SaveReviewProgressForSession(t.Context(), reviewer.ID, reviewer.RuntimeSessionID, progress); err == nil {
		t.Fatal("unbounded receipts")
	}
	state, _ := db.LoadWorkspace()
	receipt, count, err := savedReviewProgress(state, reviewer, coder)
	if err != nil || count != core.ReviewProgressMaxRecords {
		t.Fatal(count, err)
	}
	if strings.Contains(receipt.Payload, "synthetic-secret-value") {
		t.Fatal("credential persisted")
	}
	var saved core.ReviewProgress
	if err := json.Unmarshal([]byte(receipt.Payload), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.TargetTreeSHA != progress.TargetTreeSHA {
		t.Fatal("redaction changed target")
	}
}

func TestReviewProgressRejectsExpiredFlowBeforeCancellation(t *testing.T) {
	db, _, flow, coder, reviewer, control, _ := reviewProgressFixture(t)
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		expired, _ := state.Graph.ReviewFlow(flow.ID)
		expired.Deadline = time.Now().UTC().Add(-time.Millisecond)
		storeFlow(state, expired)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := control.SaveReviewProgressForSession(t.Context(), reviewer.ID, reviewer.RuntimeSessionID,
		reportedReviewProgress(reviewer, coder)); err == nil {
		t.Fatal("deadline exceeded but progress accepted")
	}
}
