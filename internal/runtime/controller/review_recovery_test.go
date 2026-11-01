package controller

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func TestReviewRecoveryOneApprovalStartsOnlyExactReviewSuccessor(t *testing.T) {
	testUnstartedReviewRecovery(t, false)
}

func TestReviewRecoveryOneApprovalRecontractsExpiredUnstartedReviewer(t *testing.T) {
	testUnstartedReviewRecovery(t, true)
}

func testUnstartedReviewRecovery(t *testing.T, expired bool) {
	counter := filepath.Join(t.TempDir(), "validation-count")
	db, ledger, initial, selected := artifactFixture(t, []string{"printf x >> " + counter}, false)
	packet := core.ReviewInput{
		Kind: core.ReviewInputKind, Purpose: "diagnostic", Source: initial.ArtifactSource,
		Questions:         []string{"Why did the saved Coder attempt fail repeatedly?"},
		KnownFailures:     []string{"Saved TestClamp assertion failed twice; cause is unknown"},
		ValidationSummary: "Product assertion failed; input receipt checks evidence integrity; integration checks not run",
		Evidence:          []string{"TestClamp expected 10, got 11; saved redacted assertion excerpt"},
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		task, _ := state.Graph.GraphTask(initial.TaskID)
		if err := state.Graph.Transition(task.ID, core.Draft, "human", "approve diagnostic input", core.TransitionProof{}); err != nil {
			return err
		}
		task.Status = core.Draft
		task.Decisions = append(task.Decisions, core.TaskDecision{ID: "review-input:" + task.ID, Source: "human:diagnostic", Text: string(mustFlowJSON(t, packet))})
		if err := state.Graph.Track(task, "human", "preserve failure diagnosis"); err != nil {
			return err
		}
		return state.Graph.Transition(task.ID, core.Ready, "human", "diagnostic contract ready", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	refreshArtifactPlan(t, db, &initial)
	old, err := StartReviewFlow(db, ledger, initial)
	if err != nil {
		t.Fatal(err)
	}
	old, err = ValidateReviewFlowInput(t.Context(), db, ledger, old.ID, initial.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	old, err = RunReviewFlow(t.Context(), db, ledger, old.ID, func(d core.GraphDispatch) (runtime.Identity, error) {
		if err := BindConfiguration(db, d.ID, selected); err != nil {
			return nil, err
		}
		return &configuredRuntime{
			fakeRuntime: &fakeRuntime{role: "REVIEWER", worktree: d.Instruction.Worktree, hash: "pre-session"},
			selection:   mustFlowJSON(t, selected.Config), repositoryErr: fmt.Errorf("identity query failed"),
		}, nil
	}, io.Discard, initial.Configuration)
	if err == nil || old.Status != "STOPPED" {
		t.Fatal("missing stopped prepared Reviewer", err)
	}
	if expired {
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			state.Graph.ReviewFlows[0].StartedAt = time.Now().UTC().Add(-2 * time.Minute)
			state.Graph.ReviewFlows[0].Deadline = time.Now().UTC().Add(-time.Minute)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		state, _ := db.LoadWorkspace()
		old, _ = state.Graph.ReviewFlow(old.ID)
	}
	request := ReviewRecoveryRequest{
		Reconciliation: DispatchReconciliationRequest{
			FlowID: old.ID, DispatchID: old.LastDispatchID(), Actor: "human", Source: "human:bounded-recovery",
			Reason:                     "Authorize reconciliation and same-input Reviewer-only successor within displayed limits",
			Evidence:                   []string{"test process quiescence check; accept uncertain delivery"},
			NoActiveExecutionConfirmed: true, AcceptDeliveryUncertainty: true,
		},
		TaskID: "single-recovery", FlowID: "single-recovery-flow", ReviewRoot: filepath.Join(t.TempDir(), "reviews"),
		Deadline: old.Deadline, TimeoutSeconds: 600,
		MaxNanoAIU: initial.MaxNanoAIU, StageBudget: *initial.StageBudget,
	}
	if expired {
		request.Deadline = time.Now().UTC().Add(5 * time.Minute)
		request.DeadlineRecontract = &store.ReviewDeadlineRecontract{
			PreviousDeadline: old.Deadline, Deadline: request.Deadline,
			Reason: "New bounded review window for preserved input, not a reset of usage",
		}
	}
	before, _ := db.LoadWorkspace()
	refs := func() string {
		t.Helper()
		output, err := exec.Command("git", "-C", old.Contract.Worktree, "for-each-ref", "--format=%(refname) %(objectname)").Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(output)
	}
	beforeRefs := refs()
	extended := request
	extended.Deadline = request.Deadline.Add(time.Second)
	if _, err := RecoverArtifactReview(db, ledger, extended, true); err == nil {
		t.Fatal("unstarted recovery extended the original deadline")
	}
	preview, err := RecoverArtifactReview(db, ledger, request, true)
	if err != nil {
		t.Fatal(err)
	}
	again, err := RecoverArtifactReview(db, ledger, request, true)
	if err != nil || preview.Hash != again.Hash ||
		preview.Plan.Plan.ReviewSeconds != again.Plan.Plan.ReviewSeconds ||
		preview.Plan.Plan.ReviewerSessionCredits != again.Plan.Plan.ReviewerSessionCredits {
		t.Fatal("combined approval basis was not stable", err)
	}
	afterPreview, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, afterPreview) {
		t.Fatal("preview changed the source store")
	}
	if refs() != beforeRefs {
		t.Fatal("preview changed persistent Git refs")
	}
	if _, err := os.Stat(request.ReviewRoot); !os.IsNotExist(err) {
		t.Fatal("preview created review root", err)
	}
	if preview.Flow.Budget.MaxCoderSessions != 0 || !preview.Flow.Deadline.Before(request.Deadline.Add(time.Nanosecond)) ||
		preview.Reconciliation.PreservedReviewWorktree == "" {
		t.Fatal("combined preview hid execution scope", preview)
	}
	if _, err := RecoverArtifactReview(db, ledger, request, false); err == nil {
		t.Fatal("missing approval accepted")
	}
	badPlan := request
	badPlan.StageBudget.ReviewSeconds = -1
	if _, err := RecoverArtifactReview(db, ledger, badPlan, true); err == nil {
		t.Fatal("invalid plan accepted after staged reconciliation")
	}
	afterFailure, _ := db.LoadWorkspace()
	if !reflect.DeepEqual(before, afterFailure) {
		t.Fatal("failed staged plan left partial reconciliation or contract")
	}
	if refs() != beforeRefs {
		t.Fatal("failed staged plan changed persistent Git refs")
	}
	request.ExpectedHash = preview.Hash
	for _, change := range []string{"budget", "deadline", "identity", "uncertainty", "input", "expired"} {
		t.Run(change, func(t *testing.T) {
			bad := request
			switch change {
			case "budget":
				bad.MaxNanoAIU++
			case "deadline":
				bad.Deadline = bad.Deadline.Add(time.Minute)
			case "identity":
				bad.FlowID += "-other"
			case "uncertainty":
				bad.Reconciliation.AcceptDeliveryUncertainty = false
			case "expired":
				bad.Deadline = time.Now().Add(-time.Second)
			case "input":
				path := filepath.Join(old.Contract.Worktree, "internal/input-00.txt")
				original, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("drift"), 0600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.WriteFile(path, original, 0600) })
			}
			if _, err := RecoverArtifactReview(db, ledger, bad, false); err == nil {
				t.Fatal("changed approval accepted", change)
			}
			state, _ := db.LoadWorkspace()
			if !reflect.DeepEqual(before, state) {
				t.Fatal("failed combined recovery partially committed")
			}
		})
	}
	applied, err := RecoverArtifactReview(db, ledger, request, false)
	if err != nil || !applied.StateChanged {
		t.Fatal("single exact approval failed", err)
	}
	if refs() != beforeRefs {
		t.Fatal("recovery needlessly retained a new snapshot instead of reusing the source")
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	savedInput, err := core.ReviewInputFromDecisions(applied.Flow.Contract.Decisions)
	if err != nil || savedInput == nil || !reflect.DeepEqual(*savedInput, packet) {
		t.Fatal("combined recovery discarded diagnostic purpose or evidence", err)
	}
	budget, err := ObserveFlowBudget(state, request.FlowID)
	if err != nil || len(budget.PriorFlows) != 1 || budget.PriorFlows[0].FlowID != old.ID {
		t.Fatal("successor budget observation discarded predecessor consumption", err)
	}
	preserved, _ := state.Graph.ReviewFlow(old.ID)
	if !reflect.DeepEqual(preserved, old) || len(state.Records.DispatchReconciliations) != 1 || len(state.Records.Events) != 0 {
		t.Fatal("recovery changed old stop or launched worker")
	}
	if _, err := RecoverArtifactReview(db, ledger, request, false); err == nil {
		t.Fatal("approval replay accepted")
	}
	next, err := ValidateReviewFlowInput(t.Context(), db, ledger, request.FlowID, initial.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	count, _ := os.ReadFile(counter)
	if string(count) != "x" {
		t.Fatal("input validation replayed")
	}
	executeFlowDispatch(t, db, next, "APPROVE", true, selected)
	next, err = AdvanceReviewFlow(db, ledger, next.ID, initial.Configuration)
	if err != nil || next.Status != "COMPLETED" {
		t.Fatal("approved recovery did not reach independent review", err)
	}
	count, _ = os.ReadFile(counter)
	if string(count) != "xx" {
		t.Fatal("result acceptance validation missing")
	}
	if _, err := os.Stat(preview.Reconciliation.PreservedReviewWorktree); err != nil {
		t.Fatal("old worktree lost", err)
	}
}
