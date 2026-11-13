package controller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestSuppliedReviewIntakeCarriesHumanArtifactsAndFailureDiagnosisWithoutCoder(t *testing.T) {
	for _, test := range []struct {
		name, kind, purpose, mode string
		commands                  []string
		committed                 bool
	}{
		{"quality-v1", core.ReviewInputKind, "quality", "", []string{"test -s internal/input-00.txt"}, false},
		{"diagnostic-v1", core.ReviewInputKind, "diagnostic", "", []string{"test -s internal/input-00.txt"}, false},
		{"failed-tests", core.ReviewInputKindV2, "diagnostic", "observe", []string{"printf 'expected 10 got 11'; exit 7", "printf 'second check observed'"}, false},
		{"no-edited-artifact", core.ReviewInputKindV2, "diagnostic", "observe", nil, true},
		{"document-review-only", core.ReviewInputKindV2, "quality", "identity-only", nil, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			purpose := test.purpose
			db, ledger, request, selected := artifactFixture(t, []string{"test -s internal/input-00.txt"}, test.committed)
			state, _ := db.LoadWorkspace()
			task, _ := state.Graph.GraphTask(request.TaskID)
			task.ID, task.Status, task.AuthorizationID, task.Decisions = "supplied-review", core.Draft, "", nil
			task.ValidationCommands = test.commands
			packet := core.ReviewInput{
				Kind: test.kind, Purpose: purpose, Source: "human:preserved-docs-tests-and-partial-code",
				ValidationMode:    test.mode,
				Questions:         []string{"Does the supplied evidence explain the observed behavior?"},
				ValidationSummary: "Product TestClamp failed, exit 1; input integrity check is separate; integration checks not run",
				Evidence:          []string{"TestClamp: expected 10, got 11; exit 1, repeated twice; full redacted log in internal/input-00.txt"},
				KnownFailures:     []string{"Coder failed twice to fix TestClamp; cause not yet classified"},
			}
			if !test.committed {
				if err := os.WriteFile(filepath.Join(task.Worktree, "internal/input-00.txt"),
					[]byte(packet.Evidence[0]), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				packet.Evidence = []string{"Recorded attempt 1 and 2: TestClamp expected 10, got 11; no edits were produced; cause unknown"}
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
			if test.committed && len(flow.Artifact.Target.ChangedFiles) != 0 {
				t.Fatal("review of recorded evidence invented an edited artifact")
			}
			if test.mode == "observe" && flow.Artifact.Validation.Status != "OBSERVED" {
				t.Fatal("diagnostic input was mislabeled as passed product validation")
			}
			if len(test.commands) != 0 && test.mode == "observe" &&
				(len(flow.Artifact.Validation.Commands) != 2 || flow.Artifact.Validation.Commands[0].ExitCode != 7) {
				t.Fatal("failed command or subsequent observation lost")
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
			result, _ := state.Graph.GraphDispatch(flow.LastDispatchID())
			if purpose == "diagnostic" && test.kind == core.ReviewInputKindV2 {
				empty := *result.Result
				empty.WorkReport = ""
				if err := result.ValidateResult(core.DispatchSucceeded, &empty); err == nil {
					t.Fatal("empty diagnostic approval was accepted without its report")
				}
			}
			if len(test.commands) != 0 && test.mode == "observe" && result.Result.Validations[0].ExitCode != 7 {
				t.Fatal("diagnostic result acceptance hid the failed product command")
			}
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

func TestDiagnosticObservationDoesNotWaiveIdentityOrUnknownExecution(t *testing.T) {
	for _, command := range []string{"printf drift >> internal/input-00.txt", "kill -TERM $$"} {
		t.Run(command, func(t *testing.T) {
			db, ledger, request, _ := artifactFixture(t, []string{command}, false)
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				task, _ := state.Graph.GraphTask(request.TaskID)
				if err := state.Graph.Transition(task.ID, core.Draft, "human", "approve explicit diagnosis", core.TransitionProof{}); err != nil {
					return err
				}
				packet := core.ReviewInput{Kind: core.ReviewInputKindV2, Purpose: "diagnostic",
					Source: request.ArtifactSource, ValidationMode: "observe",
					Questions: []string{"Explain the failed attempt"}, KnownFailures: []string{"Synthetic failed attempt"},
					ValidationSummary: "Observe outcomes; identity and delivery checks remain mandatory", Evidence: []string{"Synthetic recorded failure"}}
				task.Status = core.Draft
				task.Decisions = append(task.Decisions, core.TaskDecision{ID: "review-input:" + task.ID,
					Source: "human:diagnosis", Text: string(mustFlowJSON(t, packet))})
				if err := state.Graph.Track(task, "human", "bind diagnostic policy"); err != nil {
					return err
				}
				return state.Graph.Transition(task.ID, core.Ready, "human", "ready", core.TransitionProof{})
			}); err != nil {
				t.Fatal(err)
			}
			refreshArtifactPlan(t, db, &request)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			flow, err = ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, request.Configuration)
			if err == nil || flow.Status != "STOPPED" || len(flow.DispatchIDs) != 0 ||
				flow.Artifact.Validation.Status != "FAILED" {
				t.Fatal("diagnostic policy waived identity or execution integrity", err, flow)
			}
		})
	}
}
