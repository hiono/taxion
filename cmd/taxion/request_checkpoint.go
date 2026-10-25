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

	"github.com/hiono/taxion/internal/billing"
	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

type requestCheckpoint struct {
	Version         int                        `json:"version"`
	OperationID     string                     `json:"operationId"`
	Args            []string                   `json:"args"`
	Task            core.GraphTask             `json:"task"`
	Selection       config.Config              `json:"selection"`
	RequestHash     string                     `json:"requestHash"`
	ProposalHash    string                     `json:"proposalHash,omitempty"`
	AuthorizationID string                     `json:"authorizationId,omitempty"`
	MaxRepairs      int                        `json:"maxRepairs"`
	Answer          *controller.QuestionAnswer `json:"answer,omitempty"`
	Approved        bool                       `json:"approved"`
	PlannerStarted  bool                       `json:"plannerStarted"`
	Preflight       *controller.LaneReport     `json:"preflight,omitempty"`
}

func saveRequestCheckpoint(db *store.Store, operation *store.Operation, checkpoint requestCheckpoint) error {
	if checkpoint.Answer != nil {
		answer := minimalRequestAnswer(*checkpoint.Answer)
		checkpoint.Answer = &answer
	}
	data, err := json.Marshal(checkpoint)
	if err != nil {
		return err
	}
	if core.Redact(string(data)) != string(data) {
		return fmt.Errorf("request checkpoint contains a credential-like value")
	}
	operation.Checkpoint = data
	operation.CheckpointHash = core.SHA256Hex(data)
	return db.SaveOperation(*operation)
}

// Legacy transcripts are discarded during decoding, never migrated into a new
// checkpoint. They cannot supply the approval binding introduced in version 2.
func (c *requestCheckpoint) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, field := range []string{"conversation", "request"} {
		delete(fields, field)
	}
	filtered, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	type wire requestCheckpoint
	var decoded wire
	if err := core.DecodeGraphInput(bytes.NewReader(filtered), &decoded); err != nil {
		return err
	}
	*c = requestCheckpoint(decoded)
	if c.Version != 2 {
		// Do not retain a legacy Task/answer/args copy containing transcript text.
		*c = requestCheckpoint{Version: c.Version, PlannerStarted: c.PlannerStarted}
	} else if c.Answer != nil {
		answer := minimalRequestAnswer(*c.Answer)
		c.Answer = &answer
	}
	return nil
}

func requestApprovalBasis(operation store.Operation, checkpoint requestCheckpoint) (string, error) {
	return core.CanonicalSHA256(struct {
		Version          int                        `json:"version"`
		OperationID      string                     `json:"operationId"`
		Task             core.GraphTask             `json:"planningTask"`
		Args             []string                   `json:"options"`
		Selection        config.Config              `json:"selection"`
		RequestHash      string                     `json:"requestHash"`
		Plan             *controller.QuestionAnswer `json:"plan"`
		Deadline         time.Time                  `json:"deadline"`
		Credits          float64                    `json:"sessionSoftCreditLimit"`
		Limit            float64                    `json:"maxNanoAiu"`
		Consumed         float64                    `json:"planningNanoAiu"`
		UsageState       string                     `json:"planningUsageState"`
		MaxRepairs       int                        `json:"maxRepairs"`
		AccountQuotaOnly bool                       `json:"accountQuotaOnly,omitempty"`
		BudgetSource     string                     `json:"budgetSource,omitempty"`
	}{2, operation.ID, checkpoint.Task, checkpoint.Args, checkpoint.Selection, checkpoint.RequestHash,
		checkpoint.Answer, operation.Deadline, operation.SessionCreditLimit, operation.MaxNanoAIU, operation.PlanningNanoAIU,
		operation.PlanningUsageState, checkpoint.MaxRepairs, operation.AccountQuotaOnly, operation.BudgetSource})
}

func verifyRequestCheckpoint(state store.Workspace, operation store.Operation, checkpoint requestCheckpoint) (bool, error) {
	if checkpoint.Version != 2 {
		return false, fmt.Errorf("legacy request checkpoint has no verifiable approval binding; retained without migration")
	}
	if operation.CheckpointHash == "" || core.SHA256Hex(operation.Checkpoint) != operation.CheckpointHash ||
		checkpoint.OperationID != operation.ID || checkpoint.Task.ID != operation.ID+"-planning" {
		return false, fmt.Errorf("request checkpoint integrity or operation identity mismatch")
	}
	task, exists := state.Graph.GraphTask(checkpoint.Task.ID)
	current, err := controller.IntakeHash(task)
	expected, hashErr := controller.IntakeHash(checkpoint.Task)
	if !exists || err != nil || hashErr != nil || current != expected {
		return false, fmt.Errorf("request planning Task changed")
	}
	if operation.AccountQuotaOnly && (operation.MaxNanoAIU != 0 || !core.AccountQuotaBudget(task.BudgetGuidance)) {
		return false, fmt.Errorf("request account-only budget lacks its approved reset-daily policy")
	}
	if operation.BudgetSource != "" && billing.ValidateBudget(operation.BudgetSource, operation.AccountQuotaOnly,
		operation.MaxNanoAIU, core.AccountQuotaBudget(task.BudgetGuidance), core.ProviderBudget(task.BudgetGuidance)) != nil {
		return false, fmt.Errorf("request provider-only budget lacks its declared billing contract")
	}
	if checkpoint.Answer == nil && !checkpoint.PlannerStarted {
		return false, fmt.Errorf("request checkpoint has no minimal plan or pending question; transient input is unavailable; reconcile before continuation")
	}
	approved := checkpoint.AuthorizationID != "" || checkpoint.Approved ||
		operation.Phase == "EXECUTION" || operation.Phase == "PREFLIGHT"
	if !approved {
		if operation.TaskID != checkpoint.Task.ID || operation.FlowID != "" || checkpoint.ProposalHash != "" {
			return false, fmt.Errorf("unapproved request checkpoint has execution identity")
		}
		return false, nil
	}
	if operation.TaskID != operation.ID || operation.FlowID != operation.ID+"-flow" {
		return false, fmt.Errorf("approved request operation Task or Flow mismatch")
	}
	if !operation.ExternalBudgetOnly() && (operation.PlanningUsageState != "complete" || operation.PlanningNanoAIU >= operation.MaxNanoAIU) ||
		checkpoint.MaxRepairs < 0 || checkpoint.MaxRepairs > 100 {
		return false, fmt.Errorf("approved request has unknown usage or invalid limits")
	}
	basis, err := requestApprovalBasis(operation, checkpoint)
	if err != nil {
		return false, err
	}
	if err := controller.VerifyRequestConfirmation(state.Graph, operation.TaskID, operation.ID,
		checkpoint.ProposalHash, checkpoint.AuthorizationID, basis, checkpoint.Selection); err != nil {
		return false, err
	}
	if flow, exists := state.Graph.ReviewFlow(operation.FlowID); exists &&
		(flow.Deadline.After(operation.Deadline) || flow.Budget.AccountQuotaOnly != operation.AccountQuotaOnly ||
			flow.Budget.BudgetSource != operation.BudgetSource ||
			!operation.ExternalBudgetOnly() && flow.Budget.MaxNanoAIU > operation.MaxNanoAIU-operation.PlanningNanoAIU ||
			operation.AccountQuotaOnly && !core.AccountQuotaBudget(flow.Contract.BudgetGuidance) ||
			operation.BudgetSource == billing.ProviderStop && !core.ProviderBudget(flow.Contract.BudgetGuidance) ||
			flow.MaxRepairs != checkpoint.MaxRepairs || flow.Budget.MaxDispatches > 2+2*checkpoint.MaxRepairs ||
			flow.Budget.MaxCoderSessions > 1+checkpoint.MaxRepairs || flow.Budget.MaxReviewerSessions > 1+checkpoint.MaxRepairs) {
		return false, fmt.Errorf("request execution budget or deadline exceeds its approval")
	}
	if checkpoint.Preflight != nil && checkpoint.Preflight.ExecutionGateOpen {
		payload, err := requestPreflightReceipt(operation, checkpoint)
		if err != nil {
			return false, err
		}
		found := false
		for _, record := range state.Records.Requests {
			found = found || record.RequestID == operation.ID && record.Kind == "execution" && record.Payload == payload
		}
		if !found {
			return false, fmt.Errorf("request preflight checkpoint lacks matching saved evidence")
		}
	}
	return true, nil
}

func requestPreflightReceipt(operation store.Operation, checkpoint requestCheckpoint) (string, error) {
	basis, err := requestApprovalBasis(operation, checkpoint)
	if err != nil {
		return "", err
	}
	hash, err := core.CanonicalSHA256(checkpoint.Preflight)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(struct {
		Version      int    `json:"version"`
		FlowID       string `json:"flowId"`
		Stage        string `json:"stage"`
		BasisHash    string `json:"basisHash"`
		EvidenceHash string `json:"evidenceHash"`
	}{2, operation.FlowID, "preflight", basis, hash})
	return string(data), err
}

func recordRequestPreflight(db *store.Store, operation store.Operation, checkpoint requestCheckpoint) error {
	payload, err := requestPreflightReceipt(operation, checkpoint)
	if err != nil {
		return err
	}
	return db.AppendRequestRecord(operation.ID, "execution", payload)
}

func minimalRequestAnswer(answer controller.QuestionAnswer) controller.QuestionAnswer {
	return controller.QuestionAnswer{Recommendation: answer.Recommendation, Questions: answer.Questions, Proposal: answer.Proposal}
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
	if checkpoint.Version != 2 {
		return fmt.Errorf("legacy request checkpoint requires explicit reconciliation before cancellation")
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
		FlowID    string `json:"flowId"`
		ErrorHash string `json:"errorHash,omitempty"`
	}{operation.FlowID, core.SHA256Hex([]byte(requestError(runErr)))})
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
