package controller

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/store"
)

func TestClarificationAssessmentRechecksTheOwnedControllerIntake(t *testing.T) {
	for _, scenario := range []string{"current", "owner", "expired-owner", "held", "stopped"} {
		t.Run(scenario, func(t *testing.T) {
			db, control, binding, _, _ := clarificationSuccessorFixture(t)
			state, err := db.LoadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			dispatch, _ := state.Graph.GraphDispatch(binding.DispatchID)
			input := clarificationRequest(t, db, dispatch.TaskID, dispatch.ID, "CODER")
			input.ID = "owned-question"
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := control.ObserveRoleClarification(t.Context(), binding, "successor-session", body); err != nil {
				t.Fatal(err)
			}
			if _, err := AnswerClarification(db, input.ID, ClarificationAnswer{
				Role: "HUMAN", Actor: "owner", Source: "human:interpretation", Text: "Preserve the accepted scope.",
			}); err != nil {
				t.Fatal(err)
			}
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				if scenario == "owner" {
					state.Records.Operation.Owner = "another-owner"
				}
				if scenario == "expired-owner" {
					state.Records.Operation.Deadline = time.Now().Add(-time.Hour)
				}
				if scenario == "held" {
					_, err := state.Graph.ChangeHandoff(state.Records.Operation.FlowID, core.HandoffChange{
						ID: "hold-clarification", Action: "HOLD", Actor: "human", Source: "human:hold", Reason: "Retain the execution hold",
					})
					return err
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			if scenario == "stopped" {
				if err := stopFlowForBudget(db, state.Records.Operation.FlowID, "Retain the existing stop"); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := db.LoadWorkspace()
			report, err := AssessClarification(db, input.ID)
			expected := "clarification-owner"
			if scenario == "held" || scenario == "stopped" {
				expected = "clarification-flow"
			}
			if err != nil || report.Budget == nil || scenario == "current" && report.Decision.Verdict != "accept" ||
				scenario != "current" && !slices.ContainsFunc(report.Decision.Conditions, func(c core.InputCondition) bool {
					return c.Code == expected
				}) {
				t.Fatal("owned intake bypassed post-answer lease checks", report, err)
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("assessment changed operation ownership or execution state", err)
			}
		})
	}
}

func TestClarificationAssessmentPreservesUnknownControllerDelivery(t *testing.T) {
	db, control, binding, id, _ := clarificationSuccessorFixture(t)
	if _, err := HandoffCoderClarification(db, id, binding.DispatchID, "explicit transfer"); err != nil {
		t.Fatal(err)
	}
	if _, err := control.ReceiveCoderClarification(t.Context(), binding, "successor-session", "synthetic-receive", json.RawMessage(`{"action":"receive"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := AnswerClarification(db, id, ClarificationAnswer{
		Role: "HUMAN", Actor: "owner", Source: "human:manual-answer", Text: "Here is the retained implementation evidence.",
	}); err != nil {
		t.Fatal(err)
	}
	before, _ := db.LoadWorkspace()
	report, err := AssessClarification(db, id)
	if err != nil || !slices.ContainsFunc(report.Decision.Conditions, func(c core.InputCondition) bool {
		return c.Code == "clarification-delivery-unknown"
	}) {
		t.Fatal("manual answer invented delivery evidence", report, err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("assessment resent or promoted an unknown delivery", err)
	}
}
