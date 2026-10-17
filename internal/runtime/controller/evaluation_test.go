package controller

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestFlowEvaluationRequiresDocumentedSameTargetAcceptance(t *testing.T) {
	for _, criterion := range []string{"application tested", core.DocumentationAcceptance} {
		t.Run(criterion, func(t *testing.T) {
			db, ledger, request := reviewFlowFixtureWithOutputMode(t, core.OutputModeWorktreeTreeSnapshot)
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				task, _ := state.Graph.GraphTask(request.TaskID)
				task.Acceptance = []string{criterion}
				task.Status = core.Draft
				state.Graph = core.TaskGraph{Version: 1}
				if err := state.Graph.Track(task, "human", "define explicit quality criterion"); err != nil {
					return err
				}
				return state.Graph.Transition(task.ID, core.Ready, "human", "approve fixture contract", core.TransitionProof{})
			}); err != nil {
				t.Fatal(err)
			}
			flow, err := StartReviewFlow(db, ledger, request)
			if err != nil {
				t.Fatal(err)
			}
			state, _ := db.LoadWorkspace()
			pending, err := EvaluateReviewFlow(state.Graph, flow.ID)
			if err != nil || pending.CostComparisonEligible || pending.InputOutputTokens != nil {
				t.Fatal("pending work was rewarded", pending, err)
			}
			executeFlowDispatch(t, db, flow, "", true)
			flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
			if err != nil {
				t.Fatal(err)
			}
			executeFlowDispatch(t, db, flow, "APPROVE", true)
			flow, err = AdvanceReviewFlow(db, ledger, flow.ID)
			if err != nil {
				t.Fatal(err)
			}
			state, err = db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			cache := int64(999)
			for i := range state.Graph.Dispatches {
				state.Graph.Dispatches[i].RuntimeUsage[0].CacheReadTokens = &cache
				state.Graph.Dispatches[i].RuntimeUsage[0].CacheWriteTokens = &cache
			}
			before := mustFlowJSON(t, state.Graph)
			report, err := EvaluateReviewFlow(state.Graph, flow.ID)
			if err != nil || report.CostComparisonEligible != (criterion == core.DocumentationAcceptance) ||
				report.InputOutputTokens == nil || *report.InputOutputTokens != 30 ||
				len(report.Dispatches) != 2 || report.ElapsedMilliseconds == nil ||
				!bytes.Equal(before, mustFlowJSON(t, state.Graph)) {
				t.Fatal("evaluation changed state or misclassified quality/cost", report, err)
			}
			for _, mutation := range []string{"missing", "unavailable", "partial", "overflow", "duplicate", "changed-target"} {
				t.Run(mutation, func(t *testing.T) {
					var graph core.TaskGraph
					if err := json.Unmarshal(before, &graph); err != nil {
						t.Fatal(err)
					}
					switch mutation {
					case "missing":
						graph.Dispatches[0].RuntimeUsage = nil
					case "unavailable":
						for i := range graph.Dispatches {
							graph.Dispatches[i].RuntimeUsage = nil
						}
					case "partial":
						graph.Dispatches[0].RuntimeUsage[0].Scope = "latest-assistant-message"
					case "overflow":
						maximum := int64(math.MaxInt64)
						graph.Dispatches[0].RuntimeUsage[0].InputTokens = &maximum
					case "duplicate":
						identity := &core.UsageAccountingIdentity{SourceSessionID: "source", Sequence: 1, UsageID: "call"}
						for i := range graph.Dispatches {
							graph.Dispatches[i].RuntimeUsage[0].Accounting = identity
						}
					case "changed-target":
						graph.Dispatches[len(graph.Dispatches)-1].Result.OutputTreeSHA = strings.Repeat("b", 40)
					}
					got, err := EvaluateReviewFlow(graph, flow.ID)
					if mutation == "changed-target" {
						if err == nil {
							t.Fatal("accepted stale review target")
						}
					} else if mutation == "unavailable" {
						if err != nil || got.InputOutputTokens != nil || got.TokenStatus != "unavailable" {
							t.Fatal("missing usage became zero or complete", got, err)
						}
					} else if err != nil || got.InputOutputTokens != nil || got.TokenStatus != "partial" {
						t.Fatal("invented a complete cost", got, err)
					}
				})
			}
		})
	}
}
