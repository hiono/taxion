package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func approvalTestConfiguration(t *testing.T, endpoint string) core.ApprovalConfiguration {
	t.Helper()
	selection, err := json.Marshal(map[string]string{"endpoint": endpoint})
	if err != nil {
		t.Fatal(err)
	}
	configuration, err := core.SelectApprovalConfiguration(selection, map[string]string{"endpoint": "user"})
	if err != nil {
		t.Fatal(err)
	}
	return configuration
}

func approvalEntryGraph(t *testing.T, configuration core.ApprovalConfiguration) (core.TaskGraph, core.GraphTask) {
	t.Helper()
	task := core.GraphTask{
		ID: "approval-entry", Title: "Approval entry", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Status: core.Ready, Owner: "CODER", Reviewer: "REVIEWER",
		Objective: "Exercise approval entry", Scope: []string{"internal"}, OutOfScope: []string{"README.md"},
		Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"test-report"},
		ValidationCommands: []string{"go test ./..."}, ExitCondition: "verified",
		Branch: "work/task", Worktree: filepath.Join(t.TempDir(), "worktree"), BaselineSHA: strings.Repeat("a", 40),
		CreatedFrom: "human:1", AuthorizationID: "approval-entry-authorization",
		Decisions: []core.TaskDecision{{ID: "approval-entry-authorization", Source: "human:1", Text: "authorize approval entry"}},
	}
	bundle, err := core.ComposeApprovalBundle("approval-entry-1", task, task, configuration, "human", "human:1", "approve entry test", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	graph := core.TaskGraph{
		Version: 1, ApprovalSchemaVersion: core.CurrentApprovalSchemaVersion,
		Tasks: []core.GraphTask{task}, Approvals: []core.ApprovalBundle{bundle},
	}
	return graph, task
}

func TestCheckEntryApprovalAtEveryStage(t *testing.T) {
	configuration := approvalTestConfiguration(t, "https://api.example.test")
	drifted := approvalTestConfiguration(t, "https://drifted.example.test")
	for _, stage := range core.ApprovalEntryStages() {
		t.Run(string(stage), func(t *testing.T) {
			graph, task := approvalEntryGraph(t, configuration)
			if err := checkEntryApproval(graph, task, stage, configuration); err != nil {
				t.Fatalf("matching bundle rejected at %s: %v", stage, err)
			}

			var failure *core.EntryCheckError
			if err := checkEntryApproval(graph, task, stage, drifted); !errors.As(err, &failure) || failure.Stage != stage {
				t.Fatalf("configuration drift accepted at %s: %v", stage, err)
			}
			found := false
			for _, drift := range failure.Drifts {
				found = found || drift.Dimension == "configuration"
			}
			if !found {
				t.Fatalf("configuration drift was not reported at %s: %+v", stage, failure.Drifts)
			}

			missing := graph
			missing.Approvals = nil
			var absent *core.EntryCheckError
			if err := checkEntryApproval(missing, task, stage, configuration); !errors.As(err, &absent) || absent.Stage != stage {
				t.Fatalf("missing bundle accepted at %s: %v", stage, err)
			}

			if err := checkEntryApproval(graph, task, stage, core.ApprovalConfiguration{}); err == nil ||
				!strings.Contains(err.Error(), "approval configuration is required") {
				t.Fatalf("empty configuration accepted at %s: %v", stage, err)
			}
		})
	}
}

func TestCheckEntryApprovalAllowsVerifiedLegacyGraph(t *testing.T) {
	legacy := core.TaskGraph{Version: 1, Tasks: []core.GraphTask{{
		ID: "legacy", Status: core.Ready, Owner: "CODER", Reviewer: "REVIEWER",
	}}}
	if legacy.ApprovalRequirement() != core.ApprovalVerifiedLegacy {
		t.Fatalf("fixture is not a verified-legacy graph: %s", legacy.ApprovalRequirement())
	}
	for _, stage := range core.ApprovalEntryStages() {
		if err := checkEntryApproval(legacy, legacy.Tasks[0], stage, core.ApprovalConfiguration{}); err != nil {
			t.Fatalf("verified legacy entry rejected at %s: %v", stage, err)
		}
	}
}

func TestCheckEntryApprovalRejectsAmbiguousSchema(t *testing.T) {
	ambiguous := core.TaskGraph{
		Version: 1, Tasks: []core.GraphTask{{ID: "task", Status: core.Ready}},
		Approvals: []core.ApprovalBundle{{ID: "unmarked", TaskID: "task"}},
	}
	if ambiguous.ApprovalRequirement() != core.ApprovalInvalidOrAmbiguous {
		t.Fatalf("fixture is not ambiguous: %s", ambiguous.ApprovalRequirement())
	}
	if err := checkEntryApproval(ambiguous, ambiguous.Tasks[0], core.EntryRepairDispatch, core.ApprovalConfiguration{}); err == nil {
		t.Fatal("ambiguous approval schema was allowed")
	}
}

func appendApprovalForTask(t *testing.T, db *store.Store, task core.GraphTask, configuration core.ApprovalConfiguration, id string) {
	t.Helper()
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		bundle, err := core.ComposeApprovalBundle(id, task, task, configuration, "human", "human:1", "approve entry test", time.Now().UTC())
		if err != nil {
			return err
		}
		return state.Graph.AppendApproval(bundle)
	}); err != nil {
		t.Fatal(err)
	}
}

func reachRepairBoundary(t *testing.T, db *store.Store, ledger string, request ReviewFlowRequest) (core.ReviewFlow, core.GraphDispatch) {
	t.Helper()
	flow, err := StartReviewFlow(db, ledger, request)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	reviewer := executeFlowDispatch(t, db, flow, "REQUEST_CHANGES", true)
	return flow, reviewer
}

func reachReReviewBoundary(t *testing.T, db *store.Store, ledger string, request ReviewFlowRequest) core.ReviewFlow {
	t.Helper()
	flow, _ := reachRepairBoundary(t, db, ledger, request)
	flow, err := AdvanceReviewFlow(db, ledger, flow.ID)
	if err != nil {
		t.Fatal(err)
	}
	executeFlowDispatch(t, db, flow, "", true)
	return flow
}

type approvalWorkspaceObservation struct {
	Flows            int
	Dispatches       []string
	ActiveDispatches int
	Repairs          int
	Amends           int
	Independent      int
	ReviewTasks      int
	Tasks            []string
	Approvals        int
	ReviewRoot       []string
	SourceHEAD       string
	SourceFiles      map[string]string
}

func approvalDirectoryNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}

func observeApprovalWorkspace(t *testing.T, db *store.Store, flowID string) approvalWorkspaceObservation {
	t.Helper()
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	flow, ok := state.Graph.ReviewFlow(flowID)
	if !ok {
		t.Fatal("review flow disappeared")
	}
	observed := approvalWorkspaceObservation{
		Flows: len(state.Graph.ReviewFlows), Repairs: flow.Repairs,
		Approvals: len(state.Graph.Approvals), SourceFiles: map[string]string{},
	}
	for _, dispatch := range state.Graph.Dispatches {
		observed.Dispatches = append(observed.Dispatches, fmt.Sprintf("%s:%s:%s:%t",
			dispatch.ID, dispatch.Status, dispatch.RuntimeSessionID, dispatch.ExecutionStarted))
		if active(dispatch.Status) {
			observed.ActiveDispatches++
		}
		switch dispatch.Relationship {
		case core.RelationshipAmends:
			observed.Amends++
		case core.RelationshipIndependent:
			observed.Independent++
		}
	}
	for _, task := range state.Graph.Tasks {
		observed.Tasks = append(observed.Tasks, fmt.Sprintf("%s:%s:%s:%s:%d",
			task.ID, task.Status, task.TargetSHA, task.TargetTreeSHA, len(task.Decisions)))
		if strings.Contains(task.ID, "-review-") {
			observed.ReviewTasks++
		}
	}
	observed.ReviewRoot = approvalDirectoryNames(t, flow.ReviewRoot)
	root := flow.Contract.Worktree
	observed.SourceHEAD = gitTest(t, root, "rev-parse", "HEAD")
	for _, name := range []string{"README.md", "internal/app.go"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err == nil {
			observed.SourceFiles[name] = string(data)
		}
	}
	return observed
}

func runOrStepReviewFlow(t *testing.T, driver string, db *store.Store, ledger, flowID string,
	configuration core.ApprovalConfiguration, factory WorkerFactory) (core.ReviewFlow, error) {
	t.Helper()
	out := &bytes.Buffer{}
	if driver == "step" {
		return StepReviewFlow(t.Context(), db, ledger, flowID, factory, out, configuration)
	}
	return RunReviewFlow(t.Context(), db, ledger, flowID, factory, out, configuration)
}

func TestReviewFlowRejectsRepairAndReReviewEntryDrift(t *testing.T) {
	for _, driver := range []string{"run", "step"} {
		for _, stage := range []string{"repair", "re-review"} {
			t.Run(driver+"/"+stage, func(t *testing.T) {
				db, ledger, request := reviewFlowFixture(t)
				matching := approvalTestConfiguration(t, "https://api.example.test")
				drifted := approvalTestConfiguration(t, "https://drifted.example.test")
				var flow core.ReviewFlow
				if stage == "repair" {
					var reviewer core.GraphDispatch
					flow, reviewer = reachRepairBoundary(t, db, ledger, request)
					decision, err := flowResultDecision(reviewer)
					if err != nil {
						t.Fatal(err)
					}
					state, err := db.LoadWorkspace()
					if err != nil {
						t.Fatal(err)
					}
					task, _ := state.Graph.GraphTask(flow.TaskID)
					task.Decisions = append(slices.Clone(task.Decisions), decision)
					appendApprovalForTask(t, db, task, matching, "approval-repair")
				} else {
					flow = reachReReviewBoundary(t, db, ledger, request)
					state, err := db.LoadWorkspace()
					if err != nil {
						t.Fatal(err)
					}
					task, _ := state.Graph.GraphTask(flow.TaskID)
					appendApprovalForTask(t, db, task, matching, "approval-re-review")
				}
				before := observeApprovalWorkspace(t, db, flow.ID)
				started := 0
				factory := func(core.GraphDispatch) (runtime.Identity, error) {
					started++
					return nil, fmt.Errorf("unexpected runtime start")
				}
				_, err := runOrStepReviewFlow(t, driver, db, ledger, flow.ID, drifted, factory)
				var failure *core.EntryCheckError
				expected := core.EntryRepairDispatch
				if stage == "re-review" {
					expected = core.EntryReReviewDispatch
				}
				if !errors.As(err, &failure) || failure.Stage != expected {
					t.Fatalf("%s/%s did not fail closed at %s: %v", driver, stage, expected, err)
				}
				if stage == "repair" {
					found := false
					for _, drift := range failure.Drifts {
						found = found || drift.Dimension == "configuration"
					}
					if !found {
						t.Fatalf("repair entry did not report configuration drift: %+v", failure.Drifts)
					}
				}
				if started != 0 {
					t.Fatal("runtime started after entry rejection")
				}
				after := observeApprovalWorkspace(t, db, flow.ID)
				if !reflect.DeepEqual(before, after) {
					t.Fatalf("entry rejection changed workspace:\nbefore=%+v\nafter=%+v", before, after)
				}
			})
		}
	}
}
