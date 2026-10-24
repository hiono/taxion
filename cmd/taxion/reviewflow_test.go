package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestWorkflowEvaluationDoesNotCreateWorkspace(t *testing.T) {
	previous := storePath
	storePath = filepath.Join(t.TempDir(), "absent", "ledger.json")
	t.Cleanup(func() { storePath = previous })
	var output bytes.Buffer
	if err := workflowCommand(t.Context(), []string{"evaluate", "--id", "missing"}, &output); err == nil {
		t.Fatal("evaluation accepted an absent workspace")
	}
	if _, err := os.Stat(filepath.Dir(storePath)); !os.IsNotExist(err) {
		t.Fatal("evaluation created state", err)
	}
}

func TestWorkflowCommandsPersistAuthorizedCeilings(t *testing.T) {
	old := storePath
	dir := t.TempDir()
	storePath = filepath.Join(dir, "ledger.json")
	t.Cleanup(func() { storePath = old })
	task := core.GraphTask{
		ID: "work", Title: "Application", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Status: core.Draft, Issue: "Issue #53", CreatedFrom: "human:1",
		Owner: "CODER", Reviewer: "REVIEWER", Objective: "Implement application",
		Scope: []string{"src"}, Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"test-report"},
		ValidationCommands: []string{"go test ./..."}, ExitCondition: "tests pass",
		Worktree: filepath.Join(dir, "repo"), Branch: "work", BaselineSHA: strings.Repeat("a", 40),
		AuthorizationID: "authorize", Decisions: []core.TaskDecision{{ID: "authorize", Source: "human:1", Text: "approve"}},
	}

	if err := (core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}}).Save(storePath); err != nil {
		t.Fatal(err)
	}
	graph := core.TaskGraph{Version: 1}
	if err := graph.Track(task, "human", "track"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(task.ID, core.Ready, "human", "ready", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	if err := updateWorkspace(func(state *store.Workspace) error {
		state.HasGraph, state.Graph = true, graph
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	repairs := 2
	request := controller.ReviewFlowRequest{
		ID: "flow", TaskID: task.ID, Repository: task.Repo, ReviewRoot: filepath.Join(dir, "reviews"),
		MaxRepairs: &repairs, TimeoutSeconds: 60, MaxNanoAIU: 1e12,
		Authorization: core.TaskDecision{ID: "flow-authorized", Source: "human:1", Text: "authorize bounded independent reviews and repairs"},
	}
	file := filepath.Join(dir, "flow.json")
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"start", "--file", file}); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"show", "--id", request.ID}); err != nil {
		t.Fatal(err)
	}
	var evaluation bytes.Buffer
	if err := workflowCommand(t.Context(), []string{"evaluate", "--id", request.ID}, &evaluation); err != nil {
		t.Fatal(err)
	}
	var report controller.FlowEvaluation
	if err := json.Unmarshal(evaluation.Bytes(), &report); err != nil || report.QualityGate != "UNMET" ||
		report.CostComparisonEligible || report.InputOutputTokens != nil {
		t.Fatal("pending Flow acquired a quality score", report, err)
	}
	if err := workflowCommand(t.Context(), []string{"evaluate", "--id", request.ID, "--require-quality"}, &evaluation); err == nil {
		t.Fatal("quality gate allowed a pending Flow")
	}
	if err := workflowCmd([]string{"planning-context", "--id", request.ID}); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"planning-context", "--id", "missing"}); err == nil {
		t.Fatal("planner context accepted a missing flow")
	}
	if err := workflowCmd([]string{"advance", "--id", request.ID}); err != nil {
		t.Fatal(err)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	flow, ok := state.Graph.ReviewFlow(request.ID)
	if !ok || flow.MaxRepairs != *request.MaxRepairs || flow.MaxTokens != 0 ||
		flow.Authorization != request.Authorization || len(flow.DispatchIDs) != 1 || len(state.Graph.Dispatches) != 1 {
		t.Fatalf("workflow contract lost or active execution duplicated: %+v", flow)
	}
	handoffFile := filepath.Join(dir, "handoff.json")
	writeHandoff := func(change controller.HandoffRequest) {
		t.Helper()
		data, err := json.Marshal(change)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(handoffFile, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeHandoff(controller.HandoffRequest{
		FlowID: flow.ID, HandoffChange: core.HandoffChange{
			ID: "hold", Action: "HOLD", Actor: "human", Source: "human:question",
			Reason: "Discuss before execution",
		},
	})
	if err := workflowCmd([]string{"handoff", "--file", handoffFile}); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"advance", "--id", flow.ID}); !errors.Is(err, core.ErrHandoffHeld) {
		t.Fatal("CLI advance bypassed hold", err)
	}
	writeHandoff(controller.HandoffRequest{
		FlowID: flow.ID, HandoffChange: core.HandoffChange{
			ID: "release", ExpectedChangeID: "hold", Action: "RELEASE",
			Actor: "human", Source: "human:continue", Reason: "Continue the unchanged authorized contract",
		},
	})
	if err := workflowCmd([]string{"handoff", "--file", handoffFile}); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"advance", "--id", flow.ID}); err != nil {
		t.Fatal("release did not allow pending execution", err)
	}
	if err := workflowCmd([]string{"feedback-target", "--id", flow.ID}); err != nil {
		t.Fatal(err)
	}
	var repairOutput bytes.Buffer
	if err := workflowCommand(context.Background(), []string{"repair-status", "--id", flow.ID}, &repairOutput); err != nil {
		t.Fatal(err)
	}
	var repair controller.RepairStatus
	if err := json.Unmarshal(repairOutput.Bytes(), &repair); err != nil || repair.Status != controller.RepairNotApplicable ||
		repair.FlowID != flow.ID || repair.StateChanged {
		t.Fatalf("repair-status output = %s, %v", repairOutput.String(), err)
	}
	if err := workflowCmd([]string{"repair-status"}); err == nil {
		t.Fatal("repair-status without --id accepted")
	}
	observed, err := controller.ObserveFeedback(db, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	feedbackFile := filepath.Join(dir, "feedback.json")
	feedback, err := json.Marshal(controller.FeedbackRequest{
		FlowID: flow.ID, FlowFeedback: core.FlowFeedback{
			ID: "feedback", ExpectedChangeID: observed.ExpectedChangeID, Target: observed.Target,
			Actor: "human", Source: "human:feedback", Reason: "Inspect saved instructions",
			Text: "Explain how compatibility will be checked.",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(feedbackFile, feedback, 0600); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"feedback", "--file", feedbackFile}); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"feedback", "--file", feedbackFile}); err != nil {
		t.Fatal("CLI replay failed", err)
	}
	current, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	recorded, _ := current.Graph.ReviewFlow(flow.ID)
	if len(recorded.Feedback) != 1 || !recorded.HandoffHeld() ||
		recorded.Feedback[0].Target != observed.Target || len(current.Graph.Dispatches) != 1 {
		t.Fatal("CLI feedback lost target, duplicated record or advanced execution")
	}
	resolutionFile := filepath.Join(dir, "resolution.json")
	observed, err = controller.ObserveFeedback(db, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	resolution, err := json.Marshal(controller.FeedbackResolutionRequest{
		FlowID: flow.ID, FeedbackID: "feedback",
		FeedbackResolution: core.FeedbackResolution{
			ID: "resolution", ExpectedChangeID: observed.ExpectedChangeID, Target: observed.Target,
			Action: "KEEP_CONTRACT", Actor: "human", Source: "human:decision",
			Reason: "Keep the approved contract after clarification",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resolutionFile, resolution, 0600); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"feedback-resolve", "--file", resolutionFile}); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"feedback-resolve", "--file", resolutionFile}); err != nil {
		t.Fatal("resolution replay failed", err)
	}
	draftFile := filepath.Join(dir, "draft.json")
	observed, err = controller.ObserveFeedback(db, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := json.Marshal(controller.InstructionDraftRequest{
		FlowID: flow.ID, InstructionDraft: core.InstructionDraft{
			ID: "draft-1", ExpectedDraftID: observed.ExpectedDraftID,
			ExpectedChangeID: observed.ExpectedChangeID, Target: observed.Target,
			NextRole: "REVIEWER", Operation: "Review compatibility",
			Text:  "Check compatibility evidence before the next handoff",
			Actor: "human", Source: "human:draft", Reason: "Clarify review instructions",
			Evidence: []string{}, Unconfirmed: []string{"Compatibility evidence remains unconfirmed"},
			FeedbackIDs: []string{"feedback"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draftFile, draft, 0600); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"draft", "--file", draftFile}); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"draft", "--file", draftFile}); err != nil {
		t.Fatal("draft replay failed", err)
	}
	observed, err = controller.ObserveFeedback(db, flow.ID)
	if err != nil || observed.ExpectedDraftID != "draft-1" || observed.PendingDraftID != "draft-1" {
		t.Fatal("CLI draft missing from readout", err)
	}
	approvalFile := filepath.Join(dir, "approval.json")
	approval, err := json.Marshal(controller.DraftApprovalRequest{
		FlowID: flow.ID, DraftApproval: core.DraftApproval{
			ID: "approval-1", DraftID: observed.ExpectedDraftID,
			ExpectedChangeID: observed.ExpectedChangeID, Target: observed.Target,
			ContractHash: observed.DraftContractHash, Action: "APPROVE_SUPPLEMENT",
			Actor: "human", Source: "human:approval", Reason: "Approve the supplementary instruction",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(approvalFile, approval, 0600); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"draft-approve", "--file", approvalFile}); err == nil ||
		!strings.Contains(err.Error(), "successful saved result") {
		t.Fatal("CLI approval did not enforce completed-result boundary", err)
	}
	writeHandoff(controller.HandoffRequest{
		FlowID: flow.ID, HandoffChange: core.HandoffChange{
			ID: "feedback-release", ExpectedChangeID: "feedback:feedback", Action: "RELEASE",
			Actor: "human", Source: "human:continue", Reason: "Continue unchanged instructions",
		},
	})
	if err := workflowCmd([]string{"handoff", "--file", handoffFile}); err == nil {
		t.Fatal("CLI silently released an unissued proposal")
	}
	discardFile := filepath.Join(dir, "discard.json")
	discard, err := json.Marshal(controller.DraftDiscardRequest{
		FlowID: flow.ID, DraftDiscard: core.DraftDiscard{
			ID: "discard-1", DraftID: observed.ExpectedDraftID,
			ExpectedChangeID: observed.ExpectedChangeID, Target: observed.Target,
			Actor: "human", Source: "human:discard", Reason: "Keep unchanged instructions after clarification",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(discardFile, discard, 0600); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"draft-discard", "--file", discardFile}); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"draft-discard", "--file", discardFile}); err != nil {
		t.Fatal("discard replay failed", err)
	}
	current, err = db.LoadWorkspace()
	if err != nil || len(current.Graph.ReviewFlows[0].InstructionDrafts) != 1 ||
		current.Graph.ReviewFlows[0].InstructionDrafts[0].Version != 1 ||
		current.Graph.ReviewFlows[0].InstructionDrafts[0].Discard == nil ||
		len(current.Graph.Dispatches) != 1 {
		t.Fatal("CLI proposal/discard changed issued instructions or lost version", err)
	}
	if err := workflowCmd([]string{"handoff", "--file", handoffFile}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{}, {"start"}, {"advance"}, {"show", "--id", "missing"}, {"start", "--file", file},
		{"show", "--id", request.ID, "--file", file},
		{"run", "--id", request.ID},
		{"run", "--id", request.ID, "--coder-prompt-file", filepath.Join(dir, "missing"), "--reviewer-prompt-file", file},
		{"recover"}, {"recover", "--file", file}, {"handoff"}, {"handoff", "--file", file},
		{"feedback"}, {"feedback", "--file", file}, {"feedback-target"},
		{"feedback-target", "--id", "missing"},
		{"feedback-resolve"}, {"feedback-resolve", "--file", file},
		{"draft"}, {"draft", "--file", file}, {"draft", "--file", file, "--id", request.ID},
		{"draft-discard"}, {"draft-discard", "--file", draftFile},
		{"draft-approve"}, {"draft-approve", "--file", draftFile},
	} {
		if err := workflowCmd(args); err == nil {
			t.Fatalf("invalid workflow invocation accepted: %v", args)
		}
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Graph.ReviewFlows[0].RunnerID = "interrupted-owner"
		dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[0])
		result := &core.DispatchResult{
			DispatchID: dispatch.ID, TaskID: task.ID, Recipient: "CODER", Repository: task.Repo,
			Status: core.DispatchCancelled, StartedSHA: dispatch.ExpectedStartSHA(),
			Evidence: []string{"human:cancel-before-start"}, NextTransition: core.Blocked,
		}
		if err := state.Graph.TransitionDispatch(dispatch.ID, core.DispatchCancelled,
			"human", "cancel issued execution", false, false, result); err != nil {
			return err
		}
		state.Ledger.Dispatches[0].Active = false
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recovery := controller.RecoveryRequest{
		FlowID: request.ID, ExpectedRunnerID: "interrupted-owner", PreviousProcessStopped: true,
		Actor: "human", Reason: "Confirm old process ended",
		Evidence: []string{"human:process-ended", "human:cancel-before-start"},
	}
	data, err = json.Marshal(recovery)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := workflowCmd([]string{"recover", "--file", file}); err != nil {
		t.Fatal(err)
	}
	state, err = db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	recovered, _ := state.Graph.ReviewFlow(request.ID)
	if recovered.RunnerID != "" || len(recovered.Recoveries) != 1 ||
		recovered.Recoveries[0].Actor != recovery.Actor {
		t.Fatal("CLI did not persist recovery")
	}
	if err := workflowCmd([]string{"recover", "--file", file}); err == nil {
		t.Fatal("CLI accepted stale owner recovery")
	}
}

func TestRuntimeToolAllowlist(t *testing.T) {
	for _, invalid := range []string{"read,", ",write", "read, read", "read, ,write"} {
		if _, err := runtimeTools(invalid); err == nil {
			t.Fatalf("invalid allowlist accepted: %q", invalid)
		}
	}
	tools, err := runtimeTools(" read, write ")
	if err != nil || len(tools) != 2 || tools[0] != "read" || tools[1] != "write" {
		t.Fatalf("allowlist: %v %v", tools, err)
	}
	if tools, err := runtimeTools(""); err != nil || len(tools) != 0 {
		t.Fatal("empty allowlist enabled tools")
	}
}

func TestWorkflowLanesRequireTask(t *testing.T) {
	var output bytes.Buffer
	if err := workflowCommand(t.Context(), []string{"lanes"}, &output); err == nil ||
		!strings.Contains(err.Error(), "--task") {
		t.Fatalf("workflow lanes accepted a missing --task: %v", err)
	}
	if output.Len() != 0 {
		t.Fatal("workflow lanes wrote output before validating --task")
	}
}
