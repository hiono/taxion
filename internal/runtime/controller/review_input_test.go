package controller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestSuppliedReviewIntakeCarriesHumanArtifactsAndFailureDiagnosisWithoutCoder(t *testing.T) {
	for _, purpose := range []string{"quality", "diagnostic"} {
		t.Run(purpose, func(t *testing.T) {
			db, ledger, request, selected := artifactFixture(t, []string{"test -s internal/input-00.txt"}, false)
			state, _ := db.LoadWorkspace()
			task, _ := state.Graph.GraphTask(request.TaskID)
			task.ID, task.Status, task.AuthorizationID, task.Decisions = "supplied-review", core.Draft, "", nil
			packet := core.ReviewInput{
				Kind: core.ReviewInputKind, Purpose: purpose, Source: "human:preserved-docs-tests-and-partial-code",
				Questions:         []string{"Does the supplied evidence explain the observed behavior?"},
				ValidationSummary: "Product TestClamp failed, exit 1; input integrity check is separate; integration checks not run",
				Evidence:          []string{"TestClamp: expected 10, got 11; exit 1, repeated twice; full redacted log in internal/input-00.txt"},
				KnownFailures:     []string{"Coder failed twice to fix TestClamp; cause not yet classified"},
			}
			if err := os.WriteFile(filepath.Join(task.Worktree, "internal/input-00.txt"),
				[]byte(packet.Evidence[0]), 0600); err != nil {
				t.Fatal(err)
			}
			proposal, err := ProposeIntake(db, IntakeRequest{Task: task, ReviewInput: &packet,
				Request: "Review supplied human edits and saved partial failures; do not run Coder",
				Actor:   "human", Source: "human:review-input"})
			if err != nil {
				t.Fatal(err)
			}
			hash, _ := IntakeHash(proposal)
			if _, err := ConfirmIntake(db, IntakeConfirmation{
				TaskID: task.ID, ExpectedHash: hash, Actor: "human", Source: "human:review-input",
				Reason: "Authorize exactly the supplied-review contract and configuration", Selection: selected.Config,
				ConfigurationDecisionID: selected.Config.Execution.ApprovalRef + "@task:" + task.ID,
				AdditionalTaskIDs:       []string{request.ID + "-review-0"},
			}); err != nil {
				t.Fatal(err)
			}
			request.TaskID, request.ArtifactSource = task.ID, packet.Source
			refreshArtifactPlan(t, db, &request)
			bad := request
			bad.ArtifactSource = ""
			state, _ = db.LoadWorkspace()
			if _, err := PreviewStagePlan(state.Graph, bad); err == nil {
				t.Fatal("supplied review became a Coder plan")
			}
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			flow, err = ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, request.Configuration)
			if err != nil {
				t.Fatal(err)
			}
			state, _ = db.LoadWorkspace()
			dispatch, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
			for _, compact := range []bool{false, true} {
				text, err := RenderDispatchContext(db, dispatch, compact)
				if err != nil || !strings.Contains(text, packet.KnownFailures[0]) ||
					!strings.Contains(text, packet.ValidationSummary) || !strings.Contains(text, "REVIEW PURPOSE: "+purpose) {
					t.Fatal("actual Reviewer context lost purpose or diagnostics", compact, err)
				}
			}
			if dispatch.Recipient != "REVIEWER" || len(flow.DispatchIDs) != 1 || flow.Budget.MaxCoderSessions != 0 {
				t.Fatal("review incorrectly required successful Coder")
			}
			executeFlowDispatch(t, db, flow, "APPROVE", true, selected)
			flow, err = AdvanceReviewFlow(db, ledger, flow.ID, request.Configuration)
			if err != nil || flow.Status != "COMPLETED" {
				t.Fatal("review did not complete", err)
			}
			state, _ = db.LoadWorkspace()
			evaluation, err := EvaluateReviewFlow(state.Graph, flow.ID)
			if err != nil {
				t.Fatal(err)
			}
			if purpose == "diagnostic" {
				if !evaluation.DiagnosticComplete || evaluation.QualityGate != "NOT_ASSESSED_DIAGNOSTIC" ||
					evaluation.CostComparisonEligible || !strings.Contains(flow.Reason, "not approved") {
					t.Fatal("diagnosis falsely approved product", evaluation)
				}
				gate, err := PublicationGateReport(state.Graph, flow.ID)
				if err != nil || gate.Verdict == GateVerdictPass {
					t.Fatal("diagnosis used as product publication approval", err)
				}
			}
			tampered := packet
			tampered.Purpose = "other"
			data, _ := json.Marshal(tampered)
			if _, err := core.ReviewInputFromDecisions([]core.TaskDecision{{Text: string(data), Source: "human"}}); err == nil {
				t.Fatal("unknown review purpose accepted")
			}
		})
	}
}
