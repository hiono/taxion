package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/portable"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func artifactFixture(t *testing.T, commands []string, committed bool, owners ...string) (*store.Store, string, ReviewFlowRequest, config.Resolution) {
	t.Helper()
	selected, err := config.Resolve([]config.Layer{{Source: "user", Input: []byte(`version: 1
project: {id: demo}
repository: {provider: github, host: github.com, path: owner/repo, account: scm-user, credentialRef: scm}
runtime: {provider: copilot, account: ai-user, credentialRef: ai, contractRef: contract}
execution: {requester: requester, worker: local, approvalRef: configuration-approval}
`)}})
	if err != nil {
		t.Fatal(err)
	}
	decision := core.TaskDecision{ID: selected.Config.Execution.ApprovalRef, Source: "human:input-review",
		Text: string(mustFlowJSON(t, config.Approval{
			Kind: config.ApprovalKind, TaskID: "implementation", Reason: "review existing user-authored input without a Coder",
			AdditionalTaskIDs: []string{"flow-review-0"}, Allowed: []config.Config{selected.Config},
		})),
	}
	db, ledger, request := reviewFlowFixtureWithValidations(t, "owner/repo", core.OutputModeWorktreeTreeSnapshot, nil, commands, decision)
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask(request.TaskID)
	if err := os.MkdirAll(filepath.Join(task.Worktree, "internal"), 0755); err != nil {
		t.Fatal(err)
	}
	for i := range 29 {
		if err := os.WriteFile(filepath.Join(task.Worktree, fmt.Sprintf("internal/input-%02d.txt", i)), []byte("user implementation\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if committed {
		gitTest(t, task.Worktree, "add", "internal")
		gitTest(t, task.Worktree, "commit", "-m", "user implementation")
		task.BaselineSHA = gitTest(t, task.Worktree, "rev-parse", "HEAD")
	}
	task.Type, task.Owner = "VERIFICATION", "USER"
	if len(owners) != 0 && owners[0] == "CODER" {
		task.Type, task.Owner = "IMPLEMENTATION", "CODER"
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		if err := state.Graph.Transition(task.ID, core.Draft, "human", "approve input review contract", core.TransitionProof{}); err != nil {
			return err
		}
		task.Status = core.Draft
		if err := state.Graph.Track(task, "human", "authorize verification of user input"); err != nil {
			return err
		}
		return state.Graph.Transition(task.ID, core.Ready, "human", "input review ready", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	request.Configuration, err = core.SelectApprovalConfiguration(mustFlowJSON(t, selected.Config), selected.Sources)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0
	request.MaxRepairs, request.ArtifactSource = &zero, "human:preserved-unverified-input"
	request.StageBudget = &core.StageBudget{Version: 1, ValidationSeconds: 30, ReviewSeconds: 60, RepairSeconds: 30}
	request.Authorization.Text = "authorize actual Controller validation, then same-input independent review; zero Coder sessions and repairs"
	refreshArtifactPlan(t, db, &request)
	return db, ledger, request, selected
}

func refreshArtifactPlan(t *testing.T, db *store.Store, request *ReviewFlowRequest) {
	t.Helper()
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewStagePlan(state.Graph, *request)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Artifact == nil {
		t.Fatal("preview omitted artifact identity")
	}
	request.StagePlanHash = preview.Plan.Hash
}

func TestArtifactInputReachesIndependentReviewWithoutCoder(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprintf("baselineEqualsTarget=%v", committed), func(t *testing.T) {
			db, ledger, request, selected := artifactFixture(t, []string{"test -s internal/input-00.txt"}, committed)
			state, _ := db.LoadWorkspace()
			task, _ := state.Graph.GraphTask(request.TaskID)
			status := gitTest(t, task.Worktree, "status", "--porcelain")
			index := gitTest(t, task.Worktree, "ls-files", "--stage")
			refreshArtifactPlan(t, db, &request)
			if gitTest(t, task.Worktree, "status", "--porcelain") != status ||
				gitTest(t, task.Worktree, "ls-files", "--stage") != index {
				t.Fatal("model-free preview changed the worktree or real index")
			}
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			wantPaths := 29
			if committed {
				wantPaths = 0
			}
			if len(flow.DispatchIDs) != 0 || flow.Budget.MaxCoderSessions != 0 ||
				len(flow.Artifact.Target.ChangedFiles) != wantPaths || flow.Artifact.Validation.Status != "PENDING" {
				t.Fatalf("input was mislabeled as a model result: %+v", flow)
			}
			if err := CheckReviewFlowConfiguration(db, flow.ID, selected.Config,
				map[string][]string{"REVIEWER": {"view"}}); err != nil {
				t.Fatal(err)
			}
			if _, err := ObserveFeedback(db, flow.ID); err == nil {
				t.Fatal("model feedback is not applicable before a Dispatch exists")
			}
			calls := 0
			flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, func(d core.GraphDispatch) (runtime.Identity, error) {
				calls++
				state, err := db.LoadWorkspace()
				if err != nil {
					return nil, err
				}
				saved, _ := state.Graph.ReviewFlow(flow.ID)
				if d.Recipient != "REVIEWER" || saved.Artifact.Validation.Status != "PASSED" ||
					len(saved.Artifact.Validation.Commands) != 1 || d.Instruction.TargetTreeSHA != saved.Artifact.Target.TreeSHA {
					return nil, errors.New("worker started before actual same-target validation or attempted Coder execution")
				}
				text, err := RenderDispatchContext(db, d, true)
				if err != nil || !strings.Contains(text, "validated") && !strings.Contains(text, "PASSED") {
					return nil, fmt.Errorf("Reviewer omitted input provenance/validation: %v", err)
				}
				if err := BindConfiguration(db, d.ID, selected); err != nil {
					return nil, err
				}
				worker := &scriptedRuntime{
					fakeRuntime: fakeRuntime{role: "REVIEWER", worktree: d.Instruction.Worktree,
						hash: "config:" + d.ID, sessionID: "sdk:" + d.ID, bound: true},
					dispatch: d, events: make(chan runtime.Event, 4),
				}
				worker.script = func() error {
					changed := false
					result := core.DispatchResult{
						DispatchID: d.ID, TaskID: d.TaskID, Recipient: d.Recipient, Repository: d.Repository,
						Status: core.DispatchSucceeded, StartedSHA: d.ExpectedStartSHA(), OutputSHA: d.ExpectedStartSHA(),
						ChangedFiles: []string{}, Evidence: []string{"test-report"}, ReviewVerdict: "APPROVE",
						Acceptance: []core.DispatchAcceptance{{Criterion: "application tested", Status: "PASS", Evidence: []string{"test-report"}}},
						Findings:   []core.DispatchFinding{}, WorktreeChanged: &changed, NextTransition: core.Approved,
					}
					worker.events <- runtime.Event{Type: runtime.EventUsage, Payload: `{"model":"test","inputTokens":1,"outputTokens":1,"totalNanoAIU":1,"aiCreditsStatus":"complete"}`}
					worker.events <- runtime.Event{Type: runtime.EventOutput, Payload: string(mustFlowJSON(t, result))}
					worker.events <- runtime.Event{Type: runtime.EventTurnFinished}
					return nil
				}
				return &configuredScriptedRuntime{scriptedRuntime: worker, selection: mustFlowJSON(t, selected.Config)}, nil
			}, io.Discard, request.Configuration)
			if err != nil || flow.Status != "COMPLETED" || calls != 1 || len(flow.DispatchIDs) != 1 {
				t.Fatalf("input review failed: status=%s reason=%s calls=%d err=%v", flow.Status, flow.Reason, calls, err)
			}
			state, _ = db.LoadWorkspace()
			report, err := EvaluateTaskBudget(state.Graph, flow.ID, time.Now().UTC())
			if err != nil || report.CoderSessionLimit != 0 || report.CoderSessions != 0 || report.ReviewerSessions != 1 ||
				len(report.Unknown) != 0 {
				t.Fatalf("zero Coder accounting failed: %+v %v", report, err)
			}
			if gitTest(t, task.Worktree, "status", "--porcelain") != status ||
				gitTest(t, task.Worktree, "ls-files", "--stage") != index {
				t.Fatal("verification/review changed the supplied input")
			}
		})
	}
}

func TestArtifactValidationStopsBeforeAnyModelOnFailureOrDrift(t *testing.T) {
	for _, command := range []string{"exit 7", "printf changed >> internal/input-00.txt"} {
		t.Run(command, func(t *testing.T) {
			db, ledger, request, _ := artifactFixture(t, []string{command}, false)
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			flow, err = RunReviewFlow(t.Context(), db, ledger, flow.ID, func(core.GraphDispatch) (runtime.Identity, error) {
				calls++
				return nil, errors.New("must not create a worker")
			}, io.Discard, request.Configuration)
			if err == nil || flow.Status != "STOPPED" || !flow.HandoffHeld() || flow.RunnerID != "" ||
				flow.Artifact.Validation.Status != "FAILED" || len(flow.Artifact.Validation.Commands) != 1 ||
				len(flow.DispatchIDs) != 0 || calls != 0 {
				t.Fatalf("failed validation was not preserved before model execution: %+v, %v", flow, err)
			}
			state, _ := db.LoadWorkspace()
			status, err := ObserveRepairStatus(state, flow.ID, request.Configuration)
			if err != nil || status.Status != RepairHumanDecision {
				t.Fatalf("terminal input stop was not classified: %+v %v", status, err)
			}
			saved := flow.Artifact.Validation
			if _, err := RunReviewFlow(t.Context(), db, ledger, flow.ID,
				func(core.GraphDispatch) (runtime.Identity, error) { t.Fatal("terminal replay"); return nil, nil },
				io.Discard, request.Configuration); err == nil {
				t.Fatal("terminal validation was replayed")
			}
			state, _ = db.LoadWorkspace()
			again, _ := state.Graph.ReviewFlow(flow.ID)
			if !reflect.DeepEqual(saved, again.Artifact.Validation) {
				t.Fatal("rejected terminal operation modified its receipt")
			}
		})
	}
}

func TestArtifactInterruptedValidationRecoveryNeverReplays(t *testing.T) {
	db, ledger, request, _ := artifactFixture(t, []string{"test -s internal/input-00.txt"}, false)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		f, _ := state.Graph.ReviewFlow(flow.ID)
		f.RunnerID = "interrupted-controller"
		f.Artifact.Validation.Status, f.Artifact.Validation.StartedAt = "RUNNING", time.Now().UTC()
		storeFlow(state, f)
		return appendBudgetAudit(state, f, "INPUT_VALIDATION_STARTED", taskBudgetReport(state.Graph, f, time.Now().UTC()))
	}); err != nil {
		t.Fatal(err)
	}
	flow, err = RecoverReviewFlow(db, RecoveryRequest{
		FlowID: flow.ID, ExpectedRunnerID: "interrupted-controller", PreviousProcessStopped: true,
		Actor: "human", Reason: "confirmed old process exit", Evidence: []string{"human:process-exit"},
	})
	if err != nil || flow.Status != "STOPPED" || flow.Artifact.Validation.Status != "RUNNING" || flow.RunnerID != "" {
		t.Fatalf("unknown command delivery was relabeled or permitted replay: %+v %v", flow, err)
	}
	_, err = RunReviewFlow(context.Background(), db, ledger, flow.ID,
		func(core.GraphDispatch) (runtime.Identity, error) { t.Fatal("replayed"); return nil, nil },
		io.Discard, request.Configuration)
	if err == nil {
		t.Fatal("unknown execution was replayed")
	}
}

func TestArtifactValidateInputOnlyIsBoundedAndNeverReplays(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "executed")
	command := "printf x >> " + marker
	db, ledger, request, _ := artifactFixture(t, []string{command}, false)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("model-free preview executed a validation command", err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	task, _ := state.Graph.GraphTask(request.TaskID)
	appendApprovalForTask(t, db, task, request.Configuration, "approved-input-contract")
	refreshArtifactPlan(t, db, &request)
	state, _ = db.LoadWorkspace()
	originalBundle := state.Graph.ApprovalsForTask(task.ID)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("start executed an unrequested validation stage", err)
	}
	flow, err = ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, request.Configuration)
	if err != nil || flow.Status != "RUNNING" || flow.RunnerID != "" || len(flow.DispatchIDs) != 1 ||
		flow.Artifact.Validation.Status != "PASSED" {
		t.Fatalf("validation-only failed or created no inspectable review boundary: %+v %v", flow, err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "x" {
		t.Fatalf("command did not execute exactly once: %s %v", data, err)
	}
	state, _ = db.LoadWorkspace()
	if !reflect.DeepEqual(originalBundle, state.Graph.ApprovalsForTask(task.ID)) ||
		len(state.Graph.ApprovalsForTask("flow-review-0")) != 1 {
		t.Fatal("derived Reviewer bundle replaced the original approval or is missing")
	}
	for _, d := range state.Graph.Dispatches {
		if d.Recipient != "REVIEWER" || d.RuntimeSessionID != "" || d.ExecutionStarted {
			t.Fatal("validation-only created a model session or a Coder")
		}
	}
	snapshot := mustFlowJSON(t, state)
	if _, err := ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, request.Configuration); err == nil {
		t.Fatal("validate-input accepted a repeated command execution")
	}
	state, _ = db.LoadWorkspace()
	if !reflect.DeepEqual(snapshot, mustFlowJSON(t, state)) {
		t.Fatal("rejected repeated validation mutated saved state")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		f, _ := state.Graph.ReviewFlow(flow.ID)
		f.Artifact.Validation.Commands[0].Output = "fabricated output"
		storeFlow(state, f)
		return nil
	}); err == nil {
		t.Fatal("saved validation could be rewritten without its append-only execution receipt")
	}
}

func TestArtifactStoppedBeforeValidationPreservesPortableInput(t *testing.T) {
	db, ledger, request, _ := artifactFixture(t, []string{"test -s internal/input-00.txt"}, false)
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := stopFlowForBudget(db, flow.ID, "stop before any command or model execution"); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "input.db")
	report, err := portable.Create(t.Context(), filepath.Join(filepath.Dir(ledger), "workspace.db"),
		filepath.Join(filepath.Dir(ledger), "memory.db"), archive)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored")
	if _, err := portable.Restore(t.Context(), archive, portable.Approval{
		Hash: report.Hash, Destination: destination, Actor: "human", Source: "human:preserve-input",
	}); err != nil {
		t.Fatal(err)
	}
	dbRestored, err := store.Open(filepath.Join(destination, ".taxion/workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer dbRestored.Close()
	state, err := dbRestored.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	f, _ := state.Graph.ReviewFlow(flow.ID)
	if f.Artifact.Target.Hash != flow.Artifact.Target.Hash || f.Artifact.Validation.Status != "PENDING" ||
		len(f.DispatchIDs) != 0 {
		t.Fatal("snapshot changed input provenance or invented a result")
	}
	found := false
	for _, mapping := range state.Records.Restore.Paths {
		if mapping.Original != flow.Contract.Worktree {
			continue
		}
		found = true
		data, err := os.ReadFile(filepath.Join(mapping.Local, "internal/input-28.txt"))
		if err != nil || string(data) != "user implementation\n" {
			t.Fatalf("snapshot lost the undispatched input: %q %v", data, err)
		}
		if gitTest(t, mapping.Local, "cat-file", "-t", f.Artifact.Target.TreeSHA) != "tree" {
			t.Fatal("snapshot omitted the input tree object")
		}
	}
	if !found {
		t.Fatal("snapshot omitted input worktree mapping")
	}
}

func TestArtifactChangedInputInvalidatesPlanBeforeStart(t *testing.T) {
	db, ledger, request, _ := artifactFixture(t, []string{"test -s internal/input-00.txt"}, false)
	state, _ := db.LoadWorkspace()
	task, _ := state.Graph.GraphTask(request.TaskID)
	if err := os.WriteFile(filepath.Join(task.Worktree, "internal/input-00.txt"), []byte("different input"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := StartReviewFlow(db, ledger, request); err == nil {
		t.Fatal("old exact plan authorized a changed input tree")
	}
	state, _ = db.LoadWorkspace()
	if len(state.Graph.ReviewFlows) != 0 || len(state.Graph.Dispatches) != 0 {
		t.Fatal("rejected start persisted a Flow or Dispatch")
	}
}

func TestArtifactExistingCoderContractStillHasZeroCoderPermission(t *testing.T) {
	db, ledger, request, _ := artifactFixture(t, []string{"test -s internal/input-00.txt"}, false, "CODER")
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	graph := state.Graph
	if err := graph.Transition(flow.TaskID, core.Blocked, "human", "inspect input boundary", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(flow.TaskID, core.Ready, "human", "return to approved boundary", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.NewGraphDispatch(graph, "forbidden-coder", flow.TaskID, flow.Contract.Repo,
		core.RelationshipNone, "", ""); err == nil || !strings.Contains(err.Error(), "artifact input Tasks") {
		t.Fatal("direct Dispatch issuance bypassed zero Coder permission", err)
	}
	flow, err = ValidateReviewFlowInput(t.Context(), db, ledger, flow.ID, request.Configuration)
	if err != nil || len(flow.DispatchIDs) != 1 || flow.Budget.MaxCoderSessions != 0 {
		t.Fatalf("existing Coder contract could not use artifact-only validation: %+v %v", flow, err)
	}
}
