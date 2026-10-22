package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

type requestCheckpoint struct {
	Args           []string                   `json:"args"`
	Task           core.GraphTask             `json:"task"`
	Selection      config.Config              `json:"selection"`
	Request        string                     `json:"request"`
	Conversation   []requestMessage           `json:"conversation"`
	Answer         *controller.QuestionAnswer `json:"answer,omitempty"`
	Approved       bool                       `json:"approved"`
	PlannerStarted bool                       `json:"plannerStarted"`
	Preflight      *controller.LaneReport     `json:"preflight,omitempty"`
}

func saveRequestCheckpoint(db *store.Store, operation *store.Operation, checkpoint requestCheckpoint) error {
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	operation.Checkpoint = data
	return db.SaveOperation(*operation)
}

func requestOperationStatus(output io.Writer) error {
	db, err := store.OpenReadOnly(workspaceDBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	state, err := db.LoadWorkspace()
	if err != nil {
		return err
	}
	operation := state.Records.Operation
	owned := operation != nil && operation.Owner != ""
	var budget *controller.TaskBudgetReport
	if operation != nil {
		// Status never exports the owner capability or private planning inputs.
		copy := *operation
		operation = &copy
		operation.Owner, operation.Checkpoint = "", nil
		if _, exists := state.Graph.ReviewFlow(operation.FlowID); exists {
			report, err := controller.EvaluateTaskBudget(state.Graph, operation.FlowID, time.Now().UTC())
			if err != nil {
				return err
			}
			budget = &report
		}
	}
	return json.NewEncoder(output).Encode(struct {
		Operation *store.Operation             `json:"operation"`
		Budget    *controller.TaskBudgetReport `json:"budget"`
		Owned     bool                         `json:"claimRecorded"`
	}{operation, budget, owned})
}

func cancelRequestOperation(id string, output io.Writer) (returnErr error) {
	db, _, err := loadWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	lease, err := db.LockOperation()
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, lease.Close()) }()
	state, err := db.LoadWorkspace()
	if err != nil {
		return err
	}
	operation := state.Records.Operation
	if operation == nil || operation.ID != id || operation.Kind != "request" {
		return fmt.Errorf("cancel requires an existing request operation")
	}
	var checkpoint requestCheckpoint
	if err := core.DecodeGraphInput(bytes.NewReader(operation.Checkpoint), &checkpoint); err != nil {
		return err
	}
	if checkpoint.PlannerStarted {
		return fmt.Errorf("uncertain Planner execution must be reconciled before cancellation")
	}
	claimed, err := lease.Claim(store.Operation{ID: id, Owner: rand.Text()}, true)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, db.ReleaseOperation(claimed.ID, claimed.Owner)) }()
	err = db.UpdateWorkspace(func(state *store.Workspace) error {
		for _, flow := range state.Graph.ReviewFlows {
			if flow.RunnerID != "" || flow.Status == "RUNNING" {
				return fmt.Errorf("stop and reconcile the Flow before cancelling its operation")
			}
		}
		for _, dispatch := range state.Graph.Dispatches {
			switch dispatch.Status {
			case core.DispatchSucceeded, core.DispatchFailed, core.DispatchCancelled, core.DispatchSuperseded:
			default:
				return fmt.Errorf("reconcile every active Dispatch before cancelling its operation")
			}
		}
		if state.Records.Operation == nil || state.Records.Operation.Owner != claimed.Owner {
			return fmt.Errorf("operation ownership changed")
		}
		state.Records.Operation.State = "CANCELLED"
		return nil
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(struct {
		Gate string `json:"gate"`
		ID   string `json:"operationId"`
	}{"CANCELLED", id})
}

func finishRequestExecution(ctx context.Context, db *store.Store, operation *store.Operation,
	checkpoint *requestCheckpoint, runner func(context.Context, []string, io.Writer) error,
	cli, base string, output io.Writer) (returnErr error) {
	reported := false
	defer func() {
		if returnErr != nil && !reported {
			returnErr = errors.Join(returnErr, json.NewEncoder(output).Encode(struct {
				Gate   string `json:"gate"`
				Reason string `json:"reason"`
			}{"BLOCKED", returnErr.Error()}))
		}
	}()
	operation.Phase, operation.State = "EXECUTION", "RUNNING"
	if err := saveRequestCheckpoint(db, operation, *checkpoint); err != nil {
		return err
	}
	runErr := runner(controller.WithOperation(ctx, *operation), []string{"run", "--id", operation.FlowID, "--builtin-prompts",
		"--max-ai-credits", fmt.Sprint(operation.SessionCreditLimit), "--cli", cli, "--base-directory", base}, io.Discard)
	summary, err := json.Marshal(struct {
		FlowID string `json:"flowId"`
		Error  string `json:"error,omitempty"`
	}{operation.FlowID, requestError(runErr)})
	if err != nil {
		return errors.Join(runErr, err)
	}
	if err := db.AppendRequestRecord(operation.ID, "execution", string(summary)); err != nil {
		return errors.Join(runErr, err)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return errors.Join(runErr, err)
	}
	flow, exists := state.Graph.ReviewFlow(operation.FlowID)
	if !exists {
		return errors.Join(runErr, fmt.Errorf("request flow disappeared"))
	}
	gate := "BLOCKED"
	operation.State = "BLOCKED"
	if flow.Status == "COMPLETED" && runErr == nil {
		gate, operation.State = "COMPLETED", "COMPLETED"
	} else if flow.StopCode == "BUDGET_STOP" {
		gate, operation.State = "USAGE_EXCEEDED", "USAGE_EXCEEDED"
	}
	if err := saveRequestCheckpoint(db, operation, *checkpoint); err != nil {
		return errors.Join(runErr, err)
	}
	budget, err := controller.EvaluateTaskBudget(state.Graph, flow.ID, time.Now().UTC())
	if err != nil {
		return errors.Join(runErr, err)
	}
	reported = true
	printErr := json.NewEncoder(output).Encode(struct {
		Gate      string                      `json:"gate"`
		Operation string                      `json:"operationId"`
		FlowID    string                      `json:"flowId"`
		Status    string                      `json:"status"`
		Reason    string                      `json:"reason,omitempty"`
		Budget    controller.TaskBudgetReport `json:"budget"`
	}{gate, operation.ID, flow.ID, flow.Status, flow.Reason, budget})
	return errors.Join(runErr, printErr)
}
