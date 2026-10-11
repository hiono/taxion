package controller

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func testCompactPlanningContext(t *testing.T, db *store.Store, ledger string, before store.Workspace, review core.GraphDispatch, approval core.TaskDecision) {
	t.Helper()
	legacy, err := RenderDispatchContext(db, review, false)
	if err != nil {
		t.Fatal(err)
	}
	original, err := core.RenderDispatchInstruction(review)
	if err != nil || legacy != original {
		t.Fatal("default rendering changed saved session configuration", err)
	}
	compacted, err := RenderDispatchContext(db, review, true)
	if err != nil || len(compacted)*2 >= len(original) {
		t.Fatalf("nested instruction cost not reduced by at least half: %d versus %d, %v", len(compacted), len(original), err)
	}
	if !strings.Contains(compacted, "Correct acceptance coverage") ||
		!strings.Contains(compacted, review.Instruction.BaselineSHA) ||
		!strings.Contains(compacted, "Repair the existing criterion and rerun independent review.") {
		t.Fatal("compact rendering lost actual findings, Memory or current SHA")
	}
	decisions, err := compactResultDecisions(before.Graph, review.Instruction.Decisions)
	if err != nil {
		t.Fatal(err)
	}
	for _, decision := range decisions {
		if decision.ID == approval.ID && decision != approval {
			t.Fatal("compact rendering changed Human approval")
		}
		if !strings.HasPrefix(decision.ID, "result:") {
			continue
		}
		var projection struct {
			InstructionHash string               `json:"instructionHash"`
			ResultHash      string               `json:"resultHash"`
			Result          *core.DispatchResult `json:"result"`
		}
		if err := json.Unmarshal([]byte(decision.Text), &projection); err != nil {
			t.Fatal(err)
		}
		source, _ := before.Graph.GraphDispatch(strings.TrimPrefix(decision.ID, "result:"))
		if !reflect.DeepEqual(projection.Result, source.Result) ||
			projection.InstructionHash != source.InstructionHash ||
			!strings.HasPrefix(projection.ResultHash, "sha256:") {
			t.Fatal("projection changed actual results or lost immutable source hashes")
		}
	}
	planning, err := ObservePlannerContext(db, ledger, review.Repository, before.Graph.ReviewFlows[0].TaskID)
	if err != nil || len(planning.Results) != 4 || len(planning.Flows) != 1 ||
		planning.Observation == nil || planning.Flows[0].Status != "COMPLETED" ||
		planning.Task.Status != core.Completed {
		t.Fatalf("planner input lost repair history, bounds or current state: %+v %v", planning, err)
	}
	if planning.Results[1].Result.ReviewVerdict != "REQUEST_CHANGES" ||
		planning.Results[3].Result.ReviewVerdict != "APPROVE" ||
		planning.Results[3].Target.OutputSHA != review.Result.OutputSHA ||
		!reflect.DeepEqual(planning.Memory, review.Instruction.MemoryContext) {
		t.Fatal("planner input lost independent verdicts, Memory or target")
	}
	repeated, err := ObservePlannerContext(db, ledger, review.Repository, before.Graph.ReviewFlows[0].TaskID)
	if err != nil || repeated.Observation.ContentHash != planning.Observation.ContentHash {
		t.Fatal("observation times changed the content identity", err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("context generation mutated durable history or approval", err)
	}
	for _, bad := range []core.TaskDecision{
		{ID: "result:missing", Source: "dispatch:missing", Text: "{}"},
		{ID: "result:" + review.ID, Source: "dispatch:" + review.ID, Text: "forged result"},
	} {
		if _, err := compactResultDecisions(before.Graph, []core.TaskDecision{bad}); err == nil {
			t.Fatal("compact context accepted a missing or forged result source")
		}
	}
}
