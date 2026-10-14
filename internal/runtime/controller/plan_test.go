package controller

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/store"
)

func TestPlanRestoresDeferredWorkAndDecisionsAfterOtherWork(t *testing.T) {
	db, path, dispatchID := dispatchStore(t)
	ledgerPath := filepath.Join(filepath.Dir(path), "ledger.json")
	mem, err := memory.Open(memory.PathForLedger(ledgerPath))
	if err != nil {
		t.Fatal(err)
	}
	id, err := mem.Put(memory.KindDecision, memory.Entry{
		Title: "Keep the original concern", Body: "Investigate after the parser contract is approved.",
		SourceRefs: []string{"human:scope-out"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Close(); err != nil {
		t.Fatal(err)
	}
	deferred := core.GraphTask{
		ID: "deferred", Title: "Parser concern", Repo: "owner/repo", Type: "INVESTIGATION",
		Issue: "Issue #67", Status: core.Blocked, CreatedFrom: "human:scope-out",
		DeferredReason:  "Separate investigation after current implementation",
		ResumeCondition: "Parser contract approved", NextAction: "Investigate saved parser concern",
		Decisions:  []core.TaskDecision{{ID: "scope-decision", Source: "human:scope-out", Text: "Retain investigation for the next plan"}},
		MemoryRefs: []string{fmt.Sprintf("decision:%d", id)},
		DependsOn:  []core.TaskDependency{{TaskID: "other-repo", State: core.Completed}},
		FileAccess: core.DefaultFileAccessPolicy(),
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		for _, task := range []core.GraphTask{{
			ID: "other-repo", Title: "Other repository", Repo: "other/repo", Type: "DESIGN",
			Issue: "Issue #68", Status: core.Draft, CreatedFrom: "human:other",
		}, deferred} {
			if err := state.Graph.Track(task, "human", "retain work"); err != nil {
				return err
			}
			state.Ledger.Tasks = append(state.Ledger.Tasks, core.Task{ID: task.ID, Title: task.Title, Repo: task.Repo})
		}
		return state.Graph.AddConcern(core.Concern{
			ID: "parser-concern", TaskID: deferred.ID, Source: deferred.CreatedFrom, Text: "Parser needs investigation",
		})
	}); err != nil {
		t.Fatal(err)
	}
	before, err := Plan(db, ledgerPath, "owner/repo")
	if err != nil || len(before.Tasks) != 2 || before.Tasks[0].Action != "RECONCILE_DISPATCH" {
		t.Fatalf("initial plan: %+v %v", before, err)
	}
	worker, err := New(db, &fakeRuntime{role: "CODER", hash: "config-v1", worktree: "/tmp/task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Start(t.Context(), dispatchID); err != nil {
		t.Fatal(err)
	}
	if err := worker.Send(t.Context(), dispatchID, "run authorized work"); err != nil {
		t.Fatal(err)
	}
	if err := worker.Fail(dispatchID, "observed failure"); err != nil {
		t.Fatal(err)
	}
	expected, err := Plan(db, ledgerPath, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if expected.Tasks[0].Action != "REASSESS_DEFERRED" ||
		len(expected.Tasks[0].Dispatches) != 1 || expected.Tasks[0].Dispatches[0].Result.Blockers[0] != "observed failure" {
		t.Fatalf("other work did not advance independently: %+v", expected.Tasks[0])
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	actual, err := Plan(reopened, ledgerPath, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if expected.Observation.ContentHash != actual.Observation.ContentHash {
		t.Fatalf("observation content changed after session loss: %+v %+v", expected.Observation, actual.Observation)
	}
	expected.Observation = actual.Observation
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("plan changed after session loss: %+v %v", actual, err)
	}
	step := actual.Tasks[1]
	if step.Action != "REASSESS_DEFERRED" || step.Task.ID != deferred.ID ||
		step.Task.CreatedFrom != deferred.CreatedFrom || step.Task.ResumeCondition != deferred.ResumeCondition ||
		step.Task.NextAction != deferred.NextAction || step.Task.DeferredReason != deferred.DeferredReason ||
		len(step.UnmetDependencies) != 1 || step.UnmetDependencies[0].Repository != "other/repo" ||
		step.UnmetDependencies[0].ObservedState != core.Draft || step.UnmetDependencies[0].RequiredState != core.Completed ||
		len(step.Concerns) != 1 || step.Concerns[0].Source != deferred.CreatedFrom ||
		len(step.Task.Decisions) != 1 || len(step.MemoryContext) != 1 ||
		step.MemoryContext[0].Version != 1 || step.MemoryContext[0].Body != "Investigate after the parser contract is approved." {
		t.Fatalf("deferred work missing from follow-up planning: %+v", step)
	}
	state, err := reopened.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	retained, _ := state.Graph.GraphTask(deferred.ID)
	if !reflect.DeepEqual(retained, deferred) {
		t.Fatal("planning changed the saved task or authorization")
	}
	if _, err := Plan(reopened, ledgerPath, ""); err == nil {
		t.Fatal("implicit repository accepted")
	}
	if _, err := Plan(reopened, ledgerPath, "missing/repo"); err == nil {
		t.Fatal("empty success-shaped plan accepted")
	}
	if err := reopened.UpdateWorkspace(func(state *store.Workspace) error {
		return state.Graph.Transition(deferred.ID, core.Cancelled, "human", "cancel investigation", core.TransitionProof{})
	}); err != nil {
		t.Fatal(err)
	}
	cancelled, err := Plan(reopened, ledgerPath, "owner/repo")
	if err != nil || cancelled.Tasks[1].Action != "RETAIN" ||
		cancelled.Tasks[1].Task.DeferredReason != deferred.DeferredReason ||
		len(cancelled.Tasks[1].History) != 2 || cancelled.Tasks[1].History[1].Reason != "cancel investigation" {
		t.Fatalf("cancelled work lost its outcome or became executable: %+v %v", cancelled, err)
	}
	mem, err = memory.Open(memory.PathForLedger(ledgerPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Revoke(memory.KindDecision, id, "human", "invalidated"); err != nil {
		t.Fatal(err)
	}
	if err := mem.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Plan(reopened, ledgerPath, "owner/repo"); err == nil || !strings.Contains(err.Error(), deferred.ID) {
		t.Fatalf("stale required decision was used for planning: %v", err)
	}
}

func TestPlanDistinguishesNewHumanDecisionsAndContractGaps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := Plan(db, filepath.Join(filepath.Dir(path), "ledger.json"), "owner/repo"); err == nil {
		t.Fatal("planning without a graph accepted")
	}
	graph := core.TaskGraph{Version: 1}
	task := core.GraphTask{
		ID: "decision-task", Title: "Choose parser", Repo: "owner/repo", Type: "DESIGN",
		Status: core.Draft, CreatedFrom: "human:question", Issue: "Issue #67",
		HumanDecision: &core.HumanDecisionRequest{
			ID: "parser-choice", Question: "Which parser?", Options: []core.HumanDecisionOption{
				{ID: "native", Description: "Use the standard library"},
				{ID: "custom", Description: "Implement a parser"},
			}, RecommendedOptionID: "native", RecommendationReason: "Supported input",
		},
	}
	if err := graph.Track(task, "human", "track choice"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(task.ID, core.HumanDecisionRequired, "controller", "selection needed", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.InitializeWorkspace(store.Workspace{
		Ledger: core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}},
		Graph:  graph, HasGraph: true,
	}); err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(filepath.Dir(path), "ledger.json")
	plan, err := Plan(db, ledger, task.Repo)
	if err != nil || plan.Tasks[0].Action != "ASK_HUMAN" || plan.Tasks[0].Task.HumanDecision.Question != "Which parser?" {
		t.Fatalf("new decision not presented: %+v %v", plan, err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		task, _ := state.Graph.GraphTask(task.ID)
		task.HumanDecision.SelectedOptionID = "native"
		task.HumanDecision.DecisionRecordID = "selected-parser"
		task.Decisions = []core.TaskDecision{{ID: "selected-parser", Source: "human:answer", Text: "Use the standard library"}}
		return state.Graph.Track(task, "human", "select parser")
	}); err != nil {
		t.Fatal(err)
	}
	plan, err = Plan(db, ledger, task.Repo)
	if err != nil || plan.Tasks[0].Action != "RESTORE_CONTRACT" ||
		!slices.Contains(plan.Tasks[0].Gaps, "scope") || !slices.Contains(plan.Tasks[0].Gaps, "acceptance") ||
		plan.Tasks[0].Task.Decisions[0].Source != "human:answer" || len(plan.Tasks[0].History) != 3 {
		t.Fatalf("known decision was requested again or gaps omitted: %+v %v", plan, err)
	}
	encoded, err := json.Marshal(plan)
	if err != nil || !strings.Contains(string(encoded), "selected-parser") {
		t.Fatal("selected decision not usable by the next planner", err)
	}
}
