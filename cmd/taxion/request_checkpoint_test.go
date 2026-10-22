package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestRequestCancellationPreservesTaskAndRefusesActiveWorkflow(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "waiting", true: "active-flow"}[active], func(t *testing.T) {
			_, args := requestFixture(t)
			question := []string{"Which behavior?"}
			input := "/plan\n"
			if active {
				question, input = []string{}, "/plan\n/approve\n"
			}
			runner := func(context.Context, []string, io.Writer) error { return nil }
			if err := requestCommand(append(args, "add search"), strings.NewReader(input), io.Discard,
				fixturePlan(t, requestFixtureAnswer(question)), runner); err != nil {
				t.Fatal(err)
			}
			db, before, err := loadWorkspace()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var output bytes.Buffer
			err = requestCommand([]string{"--cancel", before.Records.Operation.ID}, nil, &output, nil, nil)
			if active == (err == nil) {
				t.Fatal("cancellation did not distinguish idle and active execution", err)
			}
			after, err := db.LoadWorkspace()
			if err != nil || !reflect.DeepEqual(before.Graph, after.Graph) || !reflect.DeepEqual(before.Ledger, after.Ledger) {
				t.Fatal("cancellation changed a Task, decision, approval or Dispatch", err)
			}
			if !active && (after.Records.Operation.State != "CANCELLED" || after.Records.Operation.Owner != "") {
				t.Fatal("idle cancellation retained the active operation")
			}
		})
	}
}

func TestRequestResumesUnansweredCheckpointWithoutRepeatingPlanner(t *testing.T) {
	_, args := requestFixture(t)
	calls := 0
	planner := func(ctx context.Context, db *store.Store, selection config.Config, task core.GraphTask,
		text string, credits float64, cli, base string) (controller.QuestionResult, error) {
		calls++
		if calls == 2 && !strings.Contains(text, "partial match") {
			t.Fatal("resumed Planner did not receive the answer")
		}

		return fixturePlan(t, requestFixtureAnswer([]string{"Which match behavior?"}))(ctx, db, selection, task, text, credits, cli, base)
	}
	var out bytes.Buffer
	if err := requestCommand(append(args, "add search"), strings.NewReader("/plan\n"), &out, planner, nil); err != nil {
		t.Fatal(err)
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	operation := state.Records.Operation
	if operation == nil || operation.State != "WAITING_FOR_ANSWER" || operation.Owner != "" {
		t.Fatal("EOF lost the waiting operation", operation)
	}
	if err := requestCommand(append(args, "different workflow"), strings.NewReader("/plan\n"), io.Discard, planner, nil); err == nil {
		t.Fatal("a different request displaced the pending question")
	}
	if err := requestCommand([]string{"--resume", operation.ID}, strings.NewReader("/plan\n"), io.Discard, planner, nil); err == nil {
		t.Fatal("answer-wait accepted a workflow command")
	}
	if calls != 1 {
		t.Fatal("resume repeated a paid Planner before receiving the answer")
	}
	out.Reset()
	if err := requestCommand([]string{"--status"}, nil, &out, nil, nil); err != nil {
		t.Fatal(err)
	}
	var status struct {
		Operation store.Operation `json:"operation"`
	}
	if err := json.Unmarshal(out.Bytes(), &status); err != nil ||
		status.Operation.State != "WAITING_FOR_ANSWER" || status.Operation.MaxNanoAIU != 1e9 ||
		status.Operation.Owner != "" || status.Operation.Checkpoint != nil {
		t.Fatal("status lacks machine-readable ceilings or discloses the owner/checkpoint", out.String(), err)
	}
	if err := requestCommand([]string{"--resume", operation.ID}, strings.NewReader("partial match\n/quit\n"), io.Discard, planner, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("answer did not continue the saved Planner conversation", calls)
	}
	state, _ = db.LoadWorkspace()
	if state.Records.Operation.State != "CANCELLED" || len(state.Graph.Dispatches) != 0 {
		t.Fatal("answer or cancellation authorized execution")
	}
}

func TestRequestExpiredCheckpointStopsWithoutRepeatingPlanner(t *testing.T) {
	_, args := requestFixture(t)
	args = append(args, "--timeout", "1s")
	calls := 0
	planner := func(ctx context.Context, db *store.Store, selection config.Config, task core.GraphTask,
		text string, credits float64, cli, base string) (controller.QuestionResult, error) {
		calls++
		return fixturePlan(t, requestFixtureAnswer([]string{"Which behavior?"}))(ctx, db, selection, task, text, credits, cli, base)
	}
	if err := requestCommand(append(args, "add search"), strings.NewReader("/plan\n"), io.Discard, planner, nil); err != nil {
		t.Fatal(err)
	}
	db, before, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	operation := before.Records.Operation
	time.Sleep(time.Until(operation.Deadline) + time.Millisecond)
	if err := requestCommand([]string{"--resume", operation.ID}, strings.NewReader("answer\n"), io.Discard, planner, nil); err == nil {
		t.Fatal("expired checkpoint allowed another paid call")
	}
	after, err := db.LoadWorkspace()
	if err != nil || calls != 1 || after.Records.Operation.State != "USAGE_EXCEEDED" ||
		!after.Records.Operation.Deadline.Equal(operation.Deadline) ||
		!reflect.DeepEqual(before.Graph, after.Graph) {
		t.Fatal("deadline stop was not durable or changed the contract", err)
	}
}

func TestRequestUnknownPlanningUsageStopsBeforeAnotherPaidCall(t *testing.T) {
	_, args := requestFixture(t)
	calls := 0
	planner := func(context.Context, *store.Store, config.Config, core.GraphTask, string, float64, string, string) (controller.QuestionResult, error) {
		calls++
		answer := requestFixtureAnswer([]string{"Which behavior?"})
		return controller.QuestionResult{Answer: &answer}, nil
	}
	if err := requestCommand(append(args, "add search"), strings.NewReader("/plan\nanswer\n"), io.Discard, planner, nil); err == nil {
		t.Fatal("unknown usage allowed another paid call")
	}
	db, state, err := loadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if calls != 1 || state.Records.Operation.State != "USAGE_EXCEEDED" ||
		state.Records.Operation.PlanningUsageState != "unknown" || len(state.Graph.Dispatches) != 0 {
		t.Fatal("hard stop lost its state or issued work", calls)
	}
}
