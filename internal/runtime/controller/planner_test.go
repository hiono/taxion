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
		"task", "Evaluate whether to repair or revisit the plan using saved decisions.", resolved.Config, 1000, q)
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
		Context QuestionContext `json:"context"`
	}
	if err := json.Unmarshal([]byte(q.sends[0]), &input); err != nil ||
		input.Context.Planner == nil || len(input.Context.Task.Dispatches) != 0 {
		t.Fatal("planner used full nested question history rather than compact input", err)
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
	ordinary := result.Context
	ordinary.Planner = nil
	if err := validateQuestionAnswer(*result.Answer, ordinary); err == nil {
		t.Fatal("a side question adopted a planning recommendation")
	}
}
