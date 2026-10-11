package controller

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func approvalRequest(t *testing.T, db *store.Store, id string) DraftApprovalRequest {
	t.Helper()
	observation, err := ObserveFeedback(db, "flow")
	if err != nil {
		t.Fatal(err)
	}
	return DraftApprovalRequest{FlowID: "flow", DraftApproval: core.DraftApproval{
		ID: id, DraftID: observation.ExpectedDraftID, ExpectedChangeID: observation.ExpectedChangeID,
		Target: observation.Target, ContractHash: observation.DraftContractHash,
		Action: "APPROVE_SUPPLEMENT", Actor: "human", Source: "human:supplement-approval",
		Reason: "Approve this exact supplementary instruction without changing the authorized contract",
	}}
}

func TestDraftApprovalPublishesExactSupplementThroughReviewRepairAndRestart(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	coder := executeFlowDispatch(t, db, flow, "", true)
	if _, err := RecordFeedback(db, feedbackRequest(t, db, "feedback")); err != nil {
		t.Fatal(err)
	}
	draft := draftRequest(t, db, "review-supplement")
	draft.FeedbackIDs = []string{"feedback"}
	if _, err := RecordInstructionDraft(db, draft); err != nil {
		t.Fatal(err)
	}
	approval := approvalRequest(t, db, "review-approval")
	flow, err = ApproveInstructionDraft(db, approval)
	if err != nil || flow.InstructionDrafts[0].Approval == nil ||
		flow.InstructionDrafts[0].Publication != nil || !flow.HandoffHeld() || len(flow.PendingFeedbackIDs()) != 0 {
		t.Fatal("approval lost explicit state or implicitly issued/released", err)
	}
	observation, err := ObserveFeedback(db, flow.ID)
	if err != nil || observation.FeedbackDispositions["feedback"] != "APPROVED_FOR_HANDOFF" {
		t.Fatal("approval was shown as applied before instruction publication", err)
	}
	if _, err := ResolveFeedback(db, resolutionRequest(t, db, "feedback", "keep")); err == nil {
		t.Fatal("approved feedback could be rewritten as KEEP_CONTRACT")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	replay, err := ApproveInstructionDraft(db, approval)
	if err != nil || !reflect.DeepEqual(flow, replay) {
		t.Fatal("approval lost across restart", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "feedback:feedback", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 2 || flow.InstructionDrafts[0].Publication == nil {
		t.Fatal("approved review supplement was not published", err)
	}
	observation, err = ObserveFeedback(db, flow.ID)
	if err != nil || observation.FeedbackDispositions["feedback"] != "INSTRUCTION_PUBLISHED" {
		t.Fatal("published feedback lost instruction linkage", err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	review, _ := state.Graph.GraphDispatch(flow.DispatchIDs[1])
	decision, err := flow.InstructionDrafts[0].SupplementalDecision()
	if err != nil || !slices.Contains(review.Instruction.Decisions, decision) {
		t.Fatal("actual instruction omitted approved text", err)
	}
	prompt, err := core.RenderDispatchInstruction(review)
	if err != nil || !strings.Contains(prompt, draft.Text) || !strings.Contains(prompt, approval.Source) {
		t.Fatal("rendered worker instruction omitted supplement", err)
	}
	original, _ := state.Graph.GraphDispatch(coder.ID)
	if !reflect.DeepEqual(original, coder) {
		t.Fatal("previous dispatch was rewritten")
	}
	if !slices.Equal(review.Instruction.Acceptance, flow.Contract.Acceptance) ||
		!slices.Equal(review.Instruction.AuthorizedActions, flow.Contract.Scope) {
		t.Fatal("supplement changed approved execution contract")
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); err != nil {
		t.Fatal("repeated advance duplicated publication", err)
	}
	if _, err := DiscardInstructionDraft(db, discardRequest(t, db, "too-late")); err == nil {
		t.Fatal("published supplement was retroactively discarded")
	}
	review = executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	if _, err := RecordFeedback(db, feedbackRequest(t, db, "repair-feedback")); err != nil {
		t.Fatal(err)
	}
	repairDraft := draftRequest(t, db, "repair-supplement")
	repairDraft.NextRole, repairDraft.Text = "CODER", "Add the compatibility check while preserving every saved review finding."
	repairDraft.FeedbackIDs = []string{"repair-feedback"}
	if _, err := RecordInstructionDraft(db, repairDraft); err != nil {
		t.Fatal(err)
	}
	oldReplay, err := ApproveInstructionDraft(db, approval)
	if err != nil || !oldReplay.HandoffHeld() || oldReplay.PendingDraftID() != repairDraft.ID ||
		oldReplay.InstructionDrafts[1].Approval != nil || len(oldReplay.PendingFeedbackIDs()) != 1 {
		t.Fatal("old approval replay approved a new proposal or released a later hold", err)
	}
	repairApproval := approvalRequest(t, db, "repair-approval")
	if _, err := ApproveInstructionDraft(db, repairApproval); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("repair-release", "feedback:repair-feedback", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Repairs != 1 || len(flow.DispatchIDs) != 3 || flow.InstructionDrafts[1].Publication == nil {
		t.Fatal("approved repair supplement not published", err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 4 {
		t.Fatal("applied supplement caused contract drift at re-review", err)
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Status != "COMPLETED" {
		t.Fatal("supplement flow could not complete independent review", err)
	}
	if _, err := ApproveInstructionDraft(db, approval); err != nil {
		t.Fatal("old approval replay failed after completion", err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].InstructionDrafts[0].Publication.InstructionHash = "tampered"
		return nil
	}); err == nil {
		t.Fatal("publication link tampering accepted")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].InstructionDrafts[0].Text = "tampered supplement"
		return nil
	}); err == nil {
		t.Fatal("published supplement content tampering accepted")
	}
}

func TestDraftApprovalCannotBypassSafetyStopsOrPublicationFailure(t *testing.T) {
	for _, failure := range []string{"budget", "contract", "issue"} {
		t.Run(failure, func(t *testing.T) {
			db, ledger, request := reviewFlowFixture(t)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			executeFlowDispatch(t, db, flow, "", true)
			if _, err := RecordFeedback(db, feedbackRequest(t, db, "feedback")); err != nil {
				t.Fatal(err)
			}
			draft := draftRequest(t, db, "supplement")
			draft.FeedbackIDs = []string{"feedback"}
			if _, err := RecordInstructionDraft(db, draft); err != nil {
				t.Fatal(err)
			}
			if _, err := ApproveInstructionDraft(db, approvalRequest(t, db, "approval")); err != nil {
				t.Fatal(err)
			}
			if _, err := ChangeHandoff(db, handoffRequest("release", "feedback:feedback", "RELEASE")); err != nil {
				t.Fatal(err)
			}
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				switch failure {
				case "budget":
					dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
					input, output := flow.MaxTokens, int64(0)
					dispatch.RuntimeUsage = append(dispatch.RuntimeUsage, core.DispatchUsage{
						Model: "test", InputTokens: &input, OutputTokens: &output, ObservedAt: time.Now().UTC(),
					})
					for i := range state.Graph.Dispatches {
						if state.Graph.Dispatches[i].ID == dispatch.ID {
							state.Graph.Dispatches[i] = dispatch
						}
					}
				case "contract":
					task, _ := state.Graph.GraphTask(flow.TaskID)
					task.Objective = "Changed request"
					return state.Graph.Track(task, "human", "change requires new approval")
				case "issue":
					// Collide with the deterministic next Dispatch ID after Task preparation.
					task := flow.Contract
					task.ID, task.Title, task.Status = "unrelated", "Unrelated", core.Draft
					task.CreatedFrom = "human:other"
					if err := state.Graph.Track(task, "human", "separate task"); err != nil {
						return err
					}
					state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{
						ID: task.ID, Title: task.Title, Repo: task.Repo,
					})
					if err := state.Graph.Transition(task.ID, core.Ready, "human", "ready", core.TransitionProof{}); err != nil {
						return err
					}
					context := state.Graph.Dispatches[0].Instruction.MemoryContext
					dispatch, err := core.NewGraphDispatch(state.Graph, "flow-dispatch-1", task.ID, task.Repo,
						core.RelationshipNone, "", "", context)
					if err != nil {
						return err
					}
					if err := state.Graph.IssueDispatch(dispatch, "human", "collision fixture"); err != nil {
						return err
					}
					state.Ledger.Dispatches = append(state.Ledger.Dispatches, core.Dispatch{
						ID: dispatch.ID, TaskID: task.ID, Repo: task.Repo, Active: true,
					})
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
			if err == nil || flow.Status != "STOPPED" || flow.InstructionDrafts[0].Publication != nil {
				t.Fatal("supplement bypassed safety gate or falsely claimed publication", err)
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before.Graph.Tasks, after.Graph.Tasks) ||
				!reflect.DeepEqual(before.Graph.Dispatches, after.Graph.Dispatches) ||
				!reflect.DeepEqual(before.Ledger, after.Ledger) {
				t.Fatal("failed supplementary issuance partially changed Tasks, Dispatch or Ledger", err)
			}
		})
	}
}
func TestDraftApprovalDiscardAndInvalidInputsPreserveAuthority(t *testing.T) {
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RecordFeedback(db, feedbackRequest(t, db, "feedback")); err != nil {
		t.Fatal(err)
	}
	first := draftRequest(t, db, "draft")
	first.FeedbackIDs = []string{"feedback"}
	if _, err := RecordInstructionDraft(db, first); err != nil {
		t.Fatal(err)
	}
	if _, err := ApproveInstructionDraft(db, approvalRequest(t, db, "early")); err == nil {
		t.Fatal("approved pending execution before results")
	}
	if _, err := DiscardInstructionDraft(db, discardRequest(t, db, "discard")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveFeedback(db, resolutionRequest(t, db, "feedback", "keep")); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "feedback:feedback", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	if _, err := RecordFeedback(db, feedbackRequest(t, db, "fresh-feedback")); err != nil {
		t.Fatal(err)
	}
	next := draftRequest(t, db, "draft-2")
	next.FeedbackIDs = []string{"fresh-feedback"}
	if _, err := RecordInstructionDraft(db, next); err != nil {
		t.Fatal(err)
	}
	a := approvalRequest(t, db, "approve")
	for _, mutate := range []func(*DraftApprovalRequest){
		func(r *DraftApprovalRequest) { r.Action = "OVERRIDE" },
		func(r *DraftApprovalRequest) { r.Actor = "" },
		func(r *DraftApprovalRequest) { r.DraftID = "draft" },
		func(r *DraftApprovalRequest) { r.Target.ResultHash = "stale" },
		func(r *DraftApprovalRequest) { r.ContractHash = "stale" },
		func(r *DraftApprovalRequest) { r.ExpectedChangeID = "stale" },
		func(r *DraftApprovalRequest) { r.Source = "" },
		func(r *DraftApprovalRequest) { r.At = time.Now().UTC() },
	} {
		bad := a
		mutate(&bad)
		if _, err := ApproveInstructionDraft(db, bad); err == nil {
			t.Fatal("invalid approval accepted")
		}

	}
	if _, err := ApproveInstructionDraft(db, a); err != nil {
		t.Fatal(err)
	}
	if _, err := RecordInstructionDraft(db, draftRequest(t, db, "draft-3")); err == nil {
		t.Fatal("approved pending draft silently superseded")
	}
	if _, err := DiscardInstructionDraft(db, discardRequest(t, db, "discard-approved")); err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	current, _ := state.Graph.ReviewFlow(flow.ID)
	if len(current.PendingFeedbackIDs()) != 1 || current.Feedback[1].Resolution != nil {
		t.Fatal("discard falsely marked feedback applied")
	}
	if _, err := ChangeHandoff(db, handoffRequest("release-2", "feedback:fresh-feedback", "RELEASE")); err == nil {
		t.Fatal("discarded approval allowed unresolved feedback release")
	}
	if _, err := ApproveInstructionDraft(nil, a); err == nil {
		t.Fatal("missing workspace accepted")
	}
	if _, err := AdvanceReviewFlow(db, ledger, flow.ID); !errors.Is(err, core.ErrHandoffHeld) {
		t.Fatal("pending feedback bypassed hold", err)
	}
}

func TestDraftApprovalCannotChangeRoleOrOverrideVerdict(t *testing.T) {
	for _, verdict := range []string{"", "APPROVE", "BLOCKED"} {
		t.Run("verdict-"+verdict, func(t *testing.T) {
			db, ledger, request := reviewFlowFixture(t)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			executeFlowDispatch(t, db, flow, "", true)
			if verdict != "" {
				flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
				if err != nil {
					t.Fatal(err)
				}
				executeFlowDispatch(t, db, flow, verdict, true)
			}
			if _, err := ChangeHandoff(db, handoffRequest("hold", "", "HOLD")); err != nil {
				t.Fatal(err)
			}
			draft := draftRequest(t, db, "draft")
			draft.NextRole = "CODER"
			if _, err := RecordInstructionDraft(db, draft); err != nil {
				t.Fatal(err)
			}
			if _, err := ApproveInstructionDraft(db, approvalRequest(t, db, "approval")); err == nil {
				t.Fatal("supplement changed next role or overrode verdict")
			}
		})
	}
}
