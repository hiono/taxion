package controller

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

func plannerDraftFixture(t *testing.T) (*store.Store, string, PlannerDraftRequest) {
	t.Helper()
	db, ledger, request := reviewFlowFixture(t)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = ChangeHandoff(db, handoffRequest("hold", "", "HOLD"))
	if err != nil {
		t.Fatal(err)
	}
	context, err := observePlannerQuestion(db, ledger, flow.Contract.Repo, flow.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := ObserveFeedback(db, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	return db, ledger, PlannerDraftRequest{
		FlowID: flow.ID, ID: "planner-review", Target: observation.Target,
		ExpectedDraftID: observation.ExpectedDraftID, ExpectedChangeID: observation.ExpectedChangeID,
		Actor: "human", Source: "human:adopt-planning-advice",
		Reason: "Propose this exact review focus within the unchanged contract",
		Proposal: QuestionResult{
			SessionID: "test-planner-session", Context: context,
			Answer: &QuestionAnswer{
				Recommendation: "REVIEW",
				Confirmed:      []QuestionClaim{{Text: "Preserve the recorded acceptance during independent review.", Sources: []string{"/planner/memory/0", "/planner/results/0/target/outputSha"}}},
				Assumptions:    []string{}, Unconfirmed: []string{"Live state is not observed by this proposal."},
			},
		},
	}
}

func TestPlannerDraftRequiresExplicitApprovalAndPublishesWithoutRewritingResults(t *testing.T) {
	db, ledger, request := plannerDraftFixture(t)
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	flow, err := RecordPlannerDraft(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if len(flow.DispatchIDs) != 1 || !flow.HandoffHeld() || len(flow.InstructionDrafts) != 1 ||
		flow.InstructionDrafts[0].Approval != nil || flow.InstructionDrafts[0].Publication != nil ||
		!strings.Contains(flow.InstructionDrafts[0].Text, request.Proposal.Context.Observation.ContentHash) {
		t.Fatal("planner adoption inferred approval, lost provenance or issued work")
	}
	var provenance struct {
		Observation *PlanObservation      `json:"observation"`
		Targets     []core.FeedbackTarget `json:"targets"`
		Memory      []struct {
			Reference string   `json:"reference"`
			Version   int      `json:"version"`
			Sources   []string `json:"sources"`
		} `json:"memory"`
	}
	if err := json.Unmarshal([]byte(flow.InstructionDrafts[0].Text), &provenance); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(provenance.Observation, request.Proposal.Context.Observation) ||
		len(provenance.Targets) != len(request.Proposal.Context.Planner.Results) ||
		len(provenance.Memory) != len(request.Proposal.Context.Planner.Memory) {
		t.Fatal("draft lost the original observation or source targets")
	}
	for i, source := range provenance.Memory {
		entry := request.Proposal.Context.Planner.Memory[i]
		if source.Reference != entry.Reference || source.Version != entry.Version ||
			!reflect.DeepEqual(source.Sources, entry.SourceRefs) {
			t.Fatal("draft lost versioned Memory provenance")
		}
	}
	for i, target := range provenance.Targets {
		if target != request.Proposal.Context.Planner.Results[i].Target {
			t.Fatal("draft lost a source result/SHA target")
		}
	}
	replay, err := RecordPlannerDraft(db, ledger, request)
	if err != nil || !reflect.DeepEqual(flow, replay) {
		t.Fatal("exact planner adoption replay created a new version", err)
	}
	changed := request
	answer := *request.Proposal.Answer
	answer.Assumptions = []string{"different proposal"}
	changed.Proposal.Answer = &answer
	if _, err := RecordPlannerDraft(db, ledger, changed); err == nil {
		t.Fatal("same ID accepted different planning advice")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(filepath.Join(filepath.Dir(ledger), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := RecordPlannerDraft(db, ledger, request); err != nil {
		t.Fatal("planner provenance lost on restart", err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("premature", "hold", "RELEASE")); err == nil {
		t.Fatal("unapproved planner proposal permitted continuation")
	}
	if _, err := ApproveInstructionDraft(db, approvalRequest(t, db, "explicit-review-approval")); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("release", "hold", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 2 || flow.InstructionDrafts[0].Publication == nil {
		t.Fatal("explicitly approved planner supplement did not publish", err)
	}
	after, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before.Graph.Dispatches[0], after.Graph.Dispatches[0]) {
		t.Fatal("planner application rewrote original result or instruction")
	}
	review, _ := after.Graph.GraphDispatch(flow.DispatchIDs[1])
	prompt, err := core.RenderDispatchInstruction(review)
	if err != nil || !strings.Contains(prompt, "Preserve the recorded acceptance") ||
		!reflect.DeepEqual(review.Instruction.Acceptance, flow.Contract.Acceptance) {
		t.Fatal("review lost adopted reasoning or changed acceptance", err)
	}
	if _, err := RecordPlannerDraft(db, ledger, request); err != nil {
		t.Fatal("old replay affected the new role instead of returning its original record", err)
	}
	executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	if _, err := ChangeHandoff(db, handoffRequest("repair-hold", "release", "HOLD")); err != nil {
		t.Fatal(err)
	}
	observation, err := ObserveFeedback(db, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	context, err := observePlannerQuestion(db, ledger, flow.Contract.Repo, flow.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	repair := PlannerDraftRequest{
		FlowID: flow.ID, ID: "planner-repair", ExpectedDraftID: observation.ExpectedDraftID,
		ExpectedChangeID: observation.ExpectedChangeID, Target: observation.Target,
		Actor: "human", Source: "human:adopt-repair-advice", Reason: "Repair only the recorded findings",
		Proposal: QuestionResult{
			SessionID: "test-repair-planner", Context: context,
			Answer: &QuestionAnswer{
				Recommendation: "REPAIR",
				Confirmed: []QuestionClaim{{Text: "Address the independent review findings with unchanged acceptance.",
					Sources: []string{"/planner/results/1/result/findings", "/planner/memory/0"}}},
				Assumptions: []string{}, Unconfirmed: []string{},
			},
		},
	}
	if _, err := RecordPlannerDraft(db, ledger, repair); err != nil {
		t.Fatal(err)
	}
	if _, err := ApproveInstructionDraft(db, approvalRequest(t, db, "explicit-repair-approval")); err != nil {
		t.Fatal(err)
	}
	if _, err := ChangeHandoff(db, handoffRequest("repair-release", "repair-hold", "RELEASE")); err != nil {
		t.Fatal(err)
	}
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || len(flow.DispatchIDs) != 3 {
		t.Fatal("explicit planner repair did not publish", err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "APPROVE", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil || flow.Status != "COMPLETED" {
		t.Fatal("planner supplements broke exact-SHA completion", err)
	}
	if _, err := RecordPlannerDraft(db, ledger, repair); err != nil {
		t.Fatal("exact repair adoption replay changed completed flow", err)
	}
}

func TestPlannerDraftRejectsStaleForgedAndNonExecutableAdvice(t *testing.T) {
	for _, failure := range []string{"action", "changed", "target", "context", "new-hold", "wrong-flow", "missing-source", "repair-without-review", "memory"} {
		t.Run(failure, func(t *testing.T) {
			db, ledger, request := plannerDraftFixture(t)
			switch failure {
			case "action":
				request.Proposal.Answer.Recommendation = "REPLAN"
			case "changed":
				request.Proposal.Changed = true
			case "target":
				request.Target.OutputSHA = strings.Repeat("b", 40)
			case "context":
				request.Proposal.Context.Task.Task.Title = "forged title with copied content hash"
			case "new-hold":
				if _, err := ChangeHandoff(db, handoffRequest("new-release", "hold", "RELEASE")); err != nil {
					t.Fatal(err)
				}
				if _, err := ChangeHandoff(db, handoffRequest("new-hold", "new-release", "HOLD")); err != nil {
					t.Fatal(err)
				}
			case "wrong-flow":
				request.FlowID = "another"
			case "missing-source":
				request.Proposal.Answer.Confirmed[0].Sources = []string{"/planner/missing"}
			case "repair-without-review":
				request.Proposal.Answer.Recommendation = "REPAIR"
			case "memory":
				mem, err := memory.Open(memory.PathForLedger(ledger))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := mem.Revise(memory.KindDecision, 1, memory.Entry{
					Title: "Changed acceptance guidance", Body: "Human must reassess this newer version.",
					SourceRefs: []string{"human:updated-memory"},
				}, "human", "revise decision"); err != nil {
					t.Fatal(err)
				}
				if err := mem.Close(); err != nil {
					t.Fatal(err)
				}
			}
			before, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			_, err = RecordPlannerDraft(db, ledger, request)
			if err == nil {
				t.Fatal("invalid or stale advice was adopted")
			}
			if failure == "context" && !errors.Is(err, core.ErrFeedbackStale) {
				t.Fatal("copied hash bypassed actual context comparison", err)
			}
			after, loadErr := db.LoadWorkspace()
			if loadErr != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected advice changed saved records", loadErr)
			}
		})
	}
}
