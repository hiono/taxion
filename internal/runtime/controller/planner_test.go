package controller

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPlannerUsesIsolatedBoundedQuestionWithoutApplyingRecommendation(t *testing.T) {
	db, databasePath, worker, resolved := questionFixture(t)
	worker.role = "ORCHESTRATOR"
	answer := `{"recommendation":"HUMAN_REQUIRED","confirmed":[{"text":"The Task is active; confirm the saved decision before changing work.","sources":["/planner/task/status"]}],"assumptions":[],"unconfirmed":["No live investigation was performed."]}`
	q := &questionRuntime{configuredRuntime: worker, events: questionEvents(answer)}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	result, err := AskPlanner(context.Background(), db, filepath.Join(filepath.Dir(databasePath), "ledger.json"),
		"task", "Evaluate whether to repair or revisit the plan using saved decisions.", resolved.Config, q)
	if err != nil {
		t.Fatal(err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("planner advice applied a decision or changed main history", err)
	}
	if result.Answer == nil || result.Answer.Recommendation != "HUMAN_REQUIRED" ||
		result.Context.Planner == nil || result.Changed || len(q.sends) != 1 ||
		q.starts != 1 || q.stops != 1 || len(result.Usage) != 1 {
		t.Fatal("planner lost isolation, target, token accounting or cleanup")
	}
	var input struct {
		Context map[string]json.RawMessage `json:"context"`
	}
	if err := json.Unmarshal([]byte(q.sends[0]), &input); err != nil {
		t.Fatal(err)
	}
	planning, err := json.Marshal(result.Context.Planner)
	if err != nil {
		t.Fatal(err)
	}
	if _, duplicate := input.Context["task"]; duplicate ||
		!reflect.DeepEqual([]byte(input.Context["planner"]), planning) || input.Context["observation"] == nil {
		t.Fatal("model context duplicates the Task or loses Planner evidence and observation")
	}
	if result.Context.Task.Task.ID != result.Context.Planner.Task.ID {
		t.Fatal("model projection discarded the saved context")
	}
	full, err := json.Marshal(result.Context)
	if err != nil {
		t.Fatal(err)
	}
	compact, err := json.Marshal(result.Context.ModelContext())
	if err != nil || len(compact)*100 > len(full)*75 {
		t.Fatalf("Planner model context saved less than 25%%: %d versus %d bytes, %v", len(compact), len(full), err)
	}
	for _, action := range []string{"APPROVE", "EXECUTE", ""} {
		invalid := *result.Answer
		invalid.Recommendation = action
		if err := validateQuestionAnswer(invalid, result.Context); err == nil {
			t.Fatal("planner accepted an approval/execution verdict or missing recommendation", action)
		}
	}
	invalid := *result.Answer
	invalid.Confirmed = []QuestionClaim{{Text: "invented", Sources: []string{"/planner/missing"}}}
	if err := validateQuestionAnswer(invalid, result.Context); err == nil {
		t.Fatal("planner accepted nonexistent evidence")
	}
	invalid.Confirmed = []QuestionClaim{{Text: "Task state", Sources: []string{"/planner/task/task/status"}}}
	if err := validateQuestionAnswer(invalid, result.Context); err == nil {
		t.Fatal("planner accepted the legacy PlanningTask wrapper inside its GraphTask")
	}
	invalid.Confirmed = []QuestionClaim{{Text: "Task state", Sources: []string{"/task/task/status"}}}
	if err := validateQuestionAnswer(invalid, result.Context); err == nil {
		t.Fatal("planner accepted a source present only in the saved, unsent duplicate")
	}
	ordinary := result.Context
	ordinary.Planner = nil
	if err := validateQuestionAnswer(*result.Answer, ordinary); err == nil {
		t.Fatal("a side question adopted a planning recommendation")
	}
}
