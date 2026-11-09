package copilot

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func TestClarificationHostToolPersistsOnlyBoundRunningRoleObservation(t *testing.T) {
	cfg := testConfig(t)
	graph := core.TaskGraph{Version: 1}
	task := core.GraphTask{
		ID: "task", Title: "approved work", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Status: core.Draft, Owner: "CODER", Reviewer: "REVIEWER", Issue: "Issue #283",
		CreatedFrom: "human:1", Objective: "safe clarification", Scope: []string{"src"},
		Acceptance: []string{"bound questions"}, RequiredEvidence: []string{"report"},
		ValidationCommands: []string{"true"}, ExitCondition: "bound questions",
		Worktree: cfg.WorkingDirectory, Branch: "work/task", BaselineSHA: strings.Repeat("a", 40),
		AuthorizationID: "decision:1",
		Decisions:       []core.TaskDecision{{ID: "decision:1", Source: "human:1", Text: "approve"}},
	}

	if err := graph.Track(task, "human", "register"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(task.ID, core.Ready, "human", "approve", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	dispatch, err := core.NewGraphDispatch(graph, "dispatch", task.ID, task.Repo, core.RelationshipNone, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.IssueDispatch(dispatch, "controller", "issue"); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "workspace.db")
	db, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	deadline := time.Now().UTC().Add(time.Hour)
	contract, _ := graph.GraphTask(task.ID)
	contract.Status = core.Ready
	flow := core.ReviewFlow{
		ID: "flow", TaskID: task.ID, Status: "RUNNING", RunnerID: "runner",
		DispatchIDs: []string{dispatch.ID}, StartedAt: time.Now().UTC(), Deadline: deadline,
		Contract: contract, ReviewRoot: t.TempDir(),
		Authorization: core.TaskDecision{ID: "flow-approval", Source: "human:1", Text: "approved flow"},
	}
	graph.ReviewFlows = []core.ReviewFlow{flow}
	var adapter *Adapter
	binding := runtime.ManagedClarification{
		DispatchID: dispatch.ID, OperationID: "operation", Owner: "owner",
		FlowID: flow.ID, RunnerID: flow.RunnerID,
	}
	if _, err := db.InitializeWorkspace(store.Workspace{
		HasGraph: true, Graph: graph,
		Ledger: core.Ledger{
			Tasks:      []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}},
			Dispatches: []core.Dispatch{{ID: dispatch.ID, TaskID: task.ID, Repo: task.Repo, Active: true, CreatedAt: dispatch.CreatedAt}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	observed, err := controller.ObserveClarification(db, task.ID, dispatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding.ScopeHash = observed.ScopeHash
	binding.Request = func(ctx context.Context, session string, body json.RawMessage) (json.RawMessage, error) {
		control, err := controller.New(db, adapter)
		if err != nil {
			return nil, err
		}
		return control.ObserveRoleClarification(ctx, binding, session, body)
	}
	cfg.ManagedClarification = &binding
	adapter, err = New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	disabled := cfg
	disabled.ManagedClarification = nil
	plain, err := New(disabled)
	if err != nil || plain.ConfigHash() == adapter.ConfigHash() {
		t.Fatal("opt-in is not configuration-bound", err)
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Records.Operation = &store.Operation{
			Version: 1, ID: "operation", Kind: "request", TaskID: task.ID, FlowID: flow.ID,
			State: "RUNNING", Phase: "EXECUTION", Owner: "owner", Deadline: deadline,
			PlanningUsageState: "complete",
		}
		state.Graph.Dispatches[0].ExecutionStartSHA = task.BaselineSHA
		if err := state.Graph.BindRuntimeSession(dispatch.ID, "session", adapter.ConfigHash()); err != nil {
			return err
		}
		for _, status := range []core.DispatchStatus{core.DispatchAcknowledged, core.DispatchRunning} {
			if err := state.Graph.TransitionDispatch(dispatch.ID, status, "runtime", "execute", true, status == core.DispatchRunning, nil); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	handle := &managedSession{sdk: &sdk.Session{}, busy: true}
	adapter.sessions["session"] = handle
	var handler sdk.ToolHandler
	for _, tool := range adapter.sessionConfig(nil).Tools {
		if tool.Name == ClarificationToolName {
			handler = tool.Handler
			if tool.SkipPermission || tool.IsTerminal {
				t.Fatal("clarification bypasses permissions or ends the turn")
			}
		}
	}
	if handler == nil || !slices.Contains(adapter.resumeConfig(nil).AvailableTools, ClarificationToolName) {
		t.Fatal("clarification missing from create/resume registration")
	}
	request := controller.ClarificationRequest{
		ID: "question", Category: "specification", Question: "Which behavior is intended?",
		Reason: "Two acceptance interpretations", Uncertainty: "behavior", References: []string{"decision:1"},
		AnswerFormat: "text", DecisionImpact: "Select the implementation", StopCondition: "Do not execute dependent work",
		Deadline: deadline,
	}
	body, _ := json.Marshal(request)
	var arguments map[string]any
	if err := json.Unmarshal(body, &arguments); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"taskId", "dispatchId", "role", "basisHash"} {
		delete(arguments, field)
	}
	invocation := sdk.ToolInvocation{ToolName: ClarificationToolName, SessionID: "session", TraceContext: t.Context(), Arguments: arguments}
	before, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"session", "nested", "stopped", "idle", "cancelled", "owner", "operation", "dispatch", "task", "role", "configuration", "runner", "source", "expired", "stop-requested", "phase", "unknown-field"} {
		t.Run(name, func(t *testing.T) {
			bad := invocation
			switch name {
			case "session":
				bad.SessionID = "unknown"
			case "nested":
				bad.AgentID = "child"
			case "stopped":
				handle.stopped = true
				defer func() { handle.stopped = false }()
			case "idle":
				handle.busy = false
				defer func() { handle.busy = true }()
			case "cancelled":
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				bad.TraceContext = ctx
			case "owner":
				binding.Owner = "other"
				defer func() { binding.Owner = "owner" }()
			case "operation":
				binding.OperationID = "other"
				defer func() { binding.OperationID = "operation" }()
			case "runner":
				binding.RunnerID = "other"
				defer func() { binding.RunnerID = "runner" }()
			case "source":
				binding.ScopeHash = "changed"
				defer func() { binding.ScopeHash = observed.ScopeHash }()
			case "dispatch", "task", "role", "unknown-field":
				copy := make(map[string]any, len(arguments)+1)
				for key, value := range arguments {
					copy[key] = value
				}
				key := map[string]string{"dispatch": "dispatchId", "task": "taskId", "role": "role", "unknown-field": "notAField"}[name]
				copy[key] = "other"
				bad.Arguments = copy
			case "configuration":
				adapter.config.SystemPrompt += "changed"
				defer func() { adapter.config.SystemPrompt = cfg.SystemPrompt }()
			case "expired":
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					state.Records.Operation.Deadline = time.Now().Add(-time.Second)
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := db.UpdateWorkspace(func(state *store.Workspace) error {
						state.Records.Operation.Deadline = deadline
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}()
			case "stop-requested", "phase":
				if err := db.UpdateWorkspace(func(state *store.Workspace) error {
					if name == "stop-requested" {
						return state.Graph.TransitionDispatch(dispatch.ID, core.DispatchCancelRequested, "runtime", "stop requested", false, false, nil)
					}
					state.Records.Operation.Phase = "PLANNING"
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := db.UpdateWorkspace(func(state *store.Workspace) error {
						state.Graph, state.Ledger = before.Graph, before.Ledger
						state.Records.Operation.Phase = "EXECUTION"
						return nil
					}); err != nil {
						t.Fatal(err)
					}
				}()
			}
			if result, err := handler(bad); err == nil || result.ResultType == "success" {
				t.Fatal("invalid origin reached canonical observation", result, err)
			}
		})
	}
	for range 2 {
		if result, err := handler(invocation); err != nil || result.ResultType != "success" {
			t.Fatal("registered SDK handler did not reach Controller", result, err)
		}
	}
	results := make(chan error, 8)
	for range cap(results) {
		go func() {
			_, err := handler(invocation)
			results <- err
		}()
	}
	for range cap(results) {
		if err := <-results; err != nil {
			t.Error("concurrent replay failed", err)
		}
	}
	changed := make(map[string]any, len(arguments))
	for key, value := range arguments {
		changed[key] = value
	}
	changed["question"] = "A different question with the same ID"
	bad := invocation
	bad.Arguments = changed
	if _, err := handler(bad); err == nil {
		t.Fatal("same ID overwrote a different question")
	}
	decision, err := adapter.permissionHandler()(&sdk.PermissionRequestCustomTool{ToolName: ClarificationToolName}, sdk.PermissionInvocation{SessionID: "session"})
	if err != nil || decision.Kind() != "approve-once" {
		t.Fatal("bound observation tool lost its managed permission", decision, err)
	}
	required := true
	decision, err = adapter.permissionHandler()(&sdk.PermissionRequestCustomTool{
		ToolName: ClarificationToolName, ManagedApprovalRequired: &required,
	}, sdk.PermissionInvocation{SessionID: "session"})
	if err != nil || decision.Kind() == "approve-once" {
		t.Fatal("observation bypassed required approval", decision, err)
	}
	reviewerConfig := cfg
	reviewerConfig.Role = "REVIEWER"
	reviewer, err := New(reviewerConfig)
	if err != nil {
		t.Fatal(err)
	}
	decision, err = reviewer.permissionHandler()(&sdk.PermissionRequestWrite{FileName: "file"}, sdk.PermissionInvocation{SessionID: "session"})
	if err != nil || decision.Kind() == "approve-once" {
		t.Fatal("clarification granted reviewer write permissions", decision, err)
	}
	history, err := controller.ClarificationHistory(db)
	if err != nil || len(history) != 1 || history[0].State != "OBSERVED" || history[0].Answer != nil ||
		history[0].Request.TaskID != task.ID || history[0].Request.DispatchID != dispatch.ID ||
		history[0].Request.Role != "CODER" || history[0].Request.Question != request.Question ||
		history[0].Origin == nil || history[0].Origin.Owner != "owner" || history[0].Origin.SessionID != "session" {
		t.Fatal("canonical origin, original text or observation boundary lost", history, err)
	}
	after, err := db.LoadWorkspace()
	if err != nil || len(after.Records.Requests) != len(before.Records.Requests)+1 {
		t.Fatal("replay or invalid invocation appended duplicate receipts", err)
	}
	after.Records.Requests = before.Records.Requests
	if !reflect.DeepEqual(before, after) {
		t.Fatal("intake changed approval, Dispatch, HOLD, operation or ownership")
	}
	t.Run("owner-renewal", func(t *testing.T) {
		renewedBinding := binding
		renewedBinding.Owner, renewedBinding.RunnerID = "renewed-owner", "renewed-runner"
		renewedConfig := cfg
		renewedConfig.ManagedClarification = &renewedBinding
		renewed, err := New(renewedConfig)
		if err != nil || renewed.ConfigHash() != adapter.ConfigHash() {
			t.Fatal("ephemeral owner or runner prevents otherwise unchanged session recovery", err)
		}
		if err := db.UpdateWorkspace(func(state *store.Workspace) error {
			state.Records.Operation.Owner = renewedBinding.Owner
			state.Graph.ReviewFlows[0].RunnerID = renewedBinding.RunnerID
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := db.UpdateWorkspace(func(state *store.Workspace) error {
				state.Records.Operation.Owner = binding.Owner
				state.Graph.ReviewFlows[0].RunnerID = binding.RunnerID
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		}()
		if _, err := handler(invocation); err == nil {
			t.Fatal("previous owner's callback survived ownership release")
		}
		control, err := controller.New(db, renewed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := control.ObserveRoleClarification(t.Context(), renewedBinding, "session", body); err == nil {
			t.Fatal("previous execution's question ID was silently rebound to the renewed owner")
		}
	})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := controller.ClarificationHistory(reopened)
	if err != nil || !reflect.DeepEqual(recovered, history) {
		t.Fatal("reopen lost the canonical question or execution origin", recovered, err)
	}
	adapter.config.ManagedClarification.Request = func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return nil, errors.New("save failed")
	}
	if result, err := handler(invocation); err == nil || result.ResultType == "success" {
		t.Fatal("storage failure was hidden")
	}
}

func TestClarificationDeliveryCannotBypassBusyOrStoppedCoder(t *testing.T) {
	adapter, err := New(testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, busy := range []bool{true, false} {
		adapter.sessions["coder"] = &managedSession{sdk: &sdk.Session{}, busy: busy, stopped: !busy}
		want := ErrStoppedSession
		if busy {
			want = ErrSessionBusy
		}
		if err := adapter.Send(t.Context(), "coder", "A saved clarification needs an answer"); !errors.Is(err, want) {
			t.Fatal("notification bypassed the existing Coder input boundary", err, want)
		}
	}
}
