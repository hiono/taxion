package controller

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
)

func laneTestGraph(t *testing.T) (*core.TaskGraph, LaneRequest) {
	t.Helper()
	root := t.TempDir()
	worktree := filepath.Join(root, ".taxion", "worktrees", "task-1")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	task := core.GraphTask{
		ID: "task-1", Repo: "https://example.com/org/repo.git", Owner: "CODER", Status: core.Active,
		Objective: "ship the change", Scope: []string{"src/app.go"},
		Acceptance: []string{"tests pass"}, RequiredEvidence: []string{"go test ./..."},
		Branch: "feat/lane", BaselineSHA: strings.Repeat("a", 40), Worktree: worktree,
	}
	instruction := core.DispatchInstruction{
		TaskID: "task-1", DispatchTo: "CODER", Repository: task.Repo,
		Objective: task.Objective, AuthorizedActions: task.Scope, ForbiddenActions: task.OutOfScope,
		Acceptance: task.Acceptance, RequiredEvidence: task.RequiredEvidence,
		Worktree: task.Worktree, Branch: task.Branch, BaselineSHA: task.BaselineSHA,
		ReturnFormat: []string{"result"},
	}
	graph := &core.TaskGraph{
		Tasks: []core.GraphTask{task},
		Dispatches: []core.GraphDispatch{{
			ID: "dispatch-1", TaskID: task.ID, Repository: task.Repo, Recipient: "CODER",
			Status: core.DispatchRunning, Instruction: instruction, InstructionHash: strings.Repeat("b", 64),
		}},
		ReviewFlows: []core.ReviewFlow{{
			ID: "flow-1", TaskID: task.ID, Status: "RUNNING", DispatchIDs: []string{"dispatch-1"},
		}},
	}
	return graph, LaneRequest{Graph: graph, TaskID: task.ID, RepositoryRoot: root, Timeout: time.Second}
}

func TestReadOnlyLanesRunConcurrentlyAndAggregateDeterministically(t *testing.T) {
	_, request := laneTestGraph(t)
	started := make(chan struct{}, len(ReadOnlyLaneNames))
	release := make(chan struct{})
	laneStartHook = func(ctx context.Context, name LaneName) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
	}
	defer func() { laneStartHook = nil }()

	type outcome struct {
		report LaneReport
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		report, err := RunReadOnlyLanes(context.Background(), request)
		done <- outcome{report, err}
	}()
	for range ReadOnlyLaneNames {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("read-only lanes did not start concurrently")
		}
	}
	close(release)
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	if !result.report.ExecutionGateOpen || result.report.Verdict != LaneVerdictPass {
		t.Fatalf("expected an open execution gate, got %+v", result.report)
	}
	if len(result.report.Lanes) != len(ReadOnlyLaneNames) {
		t.Fatalf("expected %d lanes, got %d", len(ReadOnlyLaneNames), len(result.report.Lanes))
	}

	sequential := make([]LaneResult, 0, len(ReadOnlyLaneNames))
	for _, name := range ReadOnlyLaneNames {
		sequential = append(sequential, sanitizeLaneResult(runReadOnlyLane(context.Background(), request, name)))
	}
	for index, name := range ReadOnlyLaneNames {
		if result.report.Lanes[index].Lane != name {
			t.Fatalf("lane order changed: %v", result.report.Lanes)
		}
		if !reflect.DeepEqual(result.report.Lanes[index], sequential[index]) {
			t.Fatalf("concurrent lane %s differs from sequential execution: %+v vs %+v",
				name, result.report.Lanes[index], sequential[index])
		}
	}
}

func TestReadOnlyLanesFailClosedWithoutMutation(t *testing.T) {
	graph, request := laneTestGraph(t)
	graph.Tasks[0].BaselineSHA = "not-a-sha"
	before := deepCopyGraph(t, *graph)
	report, err := RunReadOnlyLanes(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != LaneVerdictFail || report.ExecutionGateOpen {
		t.Fatalf("a failing lane must close the execution gate: %+v", report)
	}
	failed := false
	for _, lane := range report.Lanes {
		if lane.Lane == LaneBaselineWorktree {
			failed = lane.Verdict == LaneVerdictFail && len(lane.Diagnostics) != 0
		}
	}
	if !failed {
		t.Fatal("baseline lane did not report the invalid baseline")
	}
	if after := deepCopyGraph(t, *graph); !reflect.DeepEqual(before, after) {
		t.Fatal("read-only lanes mutated the Task Graph")
	}
	if graph.Dispatches[0].Status != core.DispatchRunning || len(graph.ReviewFlows) != 1 {
		t.Fatal("read-only lanes changed Dispatch or Flow state")
	}
}

func deepCopyGraph(t *testing.T, graph core.TaskGraph) core.TaskGraph {
	t.Helper()
	encoded, err := core.CanonicalJSON(graph)
	if err != nil {
		t.Fatal(err)
	}
	var copy core.TaskGraph
	if err := core.DecodeGraphInput(strings.NewReader(string(encoded)), &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}

func TestReadOnlyLanesRespectPerLaneTimeout(t *testing.T) {
	_, request := laneTestGraph(t)
	request.Timeout = 20 * time.Millisecond
	laneStartHook = func(ctx context.Context, name LaneName) {
		if name == LaneHandoffPreparation {
			<-ctx.Done()
		}
	}
	defer func() { laneStartHook = nil }()
	report, err := RunReadOnlyLanes(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if report.ExecutionGateOpen {
		t.Fatal("a timed-out lane must close the execution gate")
	}
	for _, lane := range report.Lanes {
		if lane.Lane == LaneHandoffPreparation && lane.Verdict != LaneVerdictFail {
			t.Fatalf("timed-out lane reported %+v", lane)
		}
	}
}

func TestReadOnlyLanesRedactCredentials(t *testing.T) {
	graph, request := laneTestGraph(t)
	secret := "glpat-" + strings.Repeat("a", 20)
	graph.Tasks[0].Repo = "https://user:" + secret + "@example.com/org/repo.git"
	report, err := RunReadOnlyLanes(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if report.ExecutionGateOpen {
		t.Fatal("embedded credentials must fail the repository identity lane")
	}
	encoded, err := core.CanonicalJSON(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{secret, "user:" + secret[:6]} {
		if strings.Contains(string(encoded), leaked) {
			t.Fatalf("lane report leaked a credential: %s", encoded)
		}
	}

	token := "ghp_" + strings.Repeat("b", 24)
	sanitized := sanitizeLaneResult(LaneResult{
		Lane: LaneContractConsistency, Verdict: LaneVerdictFail,
		Summary:     "failed with " + token,
		Diagnostics: []string{"token " + token},
	})
	if strings.Contains(sanitized.Summary+sanitized.Diagnostics[0], token) {
		t.Fatal("diagnostic sanitizer did not redact a credential")
	}
}

func TestReadOnlyLanesRequireExistingTask(t *testing.T) {
	graph, request := laneTestGraph(t)
	request.TaskID = "missing"
	if _, err := RunReadOnlyLanes(context.Background(), request); err == nil {
		t.Fatal("expected an error for an unknown Task")
	}
	_ = graph
}
