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
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
	"github.com/hiono/taxion/internal/tools"
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
		ProtocolVersion: 3,
		DispatchID:      dispatch.ID, OperationID: "operation", Owner: "owner",
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
	oldBinding := binding
	oldBinding.ProtocolVersion = 2
	oldConfig := cfg
	oldConfig.ManagedClarification = &oldBinding
	oldAdapter, err := New(oldConfig)
	if err != nil || oldAdapter.ConfigHash() == adapter.ConfigHash() {
		t.Fatal("answer-read protocol was not bound to a new session configuration", err)
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
	readInvocation := invocation
	readInvocation.Arguments = map[string]any{"action": "read", "id": request.ID}
	read := func() controller.ClarificationAssessment {
		t.Helper()
		before, err := db.LoadWorkspace()
		if err != nil {
			t.Fatal(err)
		}
		result, err := handler(readInvocation)
		var report controller.ClarificationAssessment
		if err != nil || result.ResultType != "success" ||
			json.Unmarshal([]byte(result.TextResultForLLM), &report) != nil {
			t.Fatal("bound SDK tool did not return canonical answer evidence", result, err)
		}
		after, err := db.LoadWorkspace()
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatal("answer read changed records, authority, HOLD or ownership", err)
		}
		return report
	}
	pending := read()
	if pending.ID != request.ID || len(pending.Candidates) != 0 || pending.Decision.Verdict != "blocked" {
		t.Fatal("unanswered question was treated as resolved", pending)
	}
	control, err := controller.New(db, adapter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.ObserveRoleClarification(t.Context(), oldBinding, "session",
		json.RawMessage(`{"action":"read","id":"question"}`)); err == nil {
		t.Fatal("legacy protocol silently gained answer-read access")
	}
	for _, args := range []map[string]any{
		{"action": "read", "id": "missing"},
		{"action": "read", "id": request.ID, "question": "replace the question"},
		{"action": "read"},
		{"action": "approve", "id": request.ID},
	} {
		bad := readInvocation
		bad.Arguments = args
		if _, err := handler(bad); err == nil {
			t.Fatal("invalid answer read accepted", args)
		}
	}
	answer := controller.ClarificationAnswer{
		Role: "PLANNER", Actor: "planner-session", Source: "spec:behavior", Text: "Preserve the current behavior.",
	}
	if _, err := controller.AnswerClarification(db, request.ID, answer); err != nil {
		t.Fatal(err)
	}
	answered := read()
	if answered.State != "OBSERVED" || answered.Decision.Verdict != "blocked" ||
		!slices.ContainsFunc(answered.Decision.Conditions, func(c core.InputCondition) bool { return c.Code == "clarification-budget" }) ||
		slices.ContainsFunc(answered.Decision.Conditions, func(c core.InputCondition) bool { return c.Code == "clarification-unanswered" }) ||
		len(answered.Candidates) != 1 ||
		answered.Candidates[0].Answer.Text != answer.Text || answered.Candidates[0].Answer.Source != answer.Source ||
		answered.Candidates[0].Answer.At.IsZero() || answered.Candidates[0].Receipt.Hash == "" {
		t.Fatal("answer source, receipt or observation boundary missing", answered)
	}
	// No question is resent when independent work reaches another safe tool call.
	for range 2 {
		if got := read(); got.Receipt != answered.Receipt {
			t.Fatal("repeated answer reads created new evidence")
		}
	}
	for range cap(results) {
		go func() {
			result, err := handler(readInvocation)
			var report controller.ClarificationAssessment
			if err == nil && (json.Unmarshal([]byte(result.TextResultForLLM), &report) != nil || report.Receipt != answered.Receipt) {
				err = errors.New("concurrent read changed canonical evidence")
			}
			results <- err
		}()
	}
	for range cap(results) {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	conflict := answer
	conflict.Source, conflict.Text = "spec:alternative", "Replace the behavior."
	if _, err := controller.AnswerClarification(db, request.ID, conflict); err == nil {
		t.Fatal("contradictory answer was accepted")
	}
	disputed := read()
	if disputed.Decision.Verdict != "blocked" || len(disputed.Candidates) != 2 || disputed.Resolution != nil {
		t.Fatal("running Role did not observe the conflicting evidence", disputed)
	}
	if _, err := controller.ResolveClarificationConflict(db, controller.ClarificationResolutionRequest{
		ID: request.ID, ExpectedHash: disputed.Decision.BasisHash, Selected: disputed.Candidates[1].Receipt,
		Role: "HUMAN", Actor: "owner", Source: "decision:behavior", Reason: "Use the updated specification.",
	}); err != nil {
		t.Fatal(err)
	}
	selected := read()
	if selected.State != "OBSERVED" || selected.Resolution == nil ||
		selected.Resolution.Selected != disputed.Candidates[1].Receipt || selected.Candidates[0].Answer.Text != answer.Text {
		t.Fatal("selection overwrote the original evidence or granted authority", selected)
	}
	foreign := request
	foreign.ID, foreign.Uncertainty = "foreign", "a different uncertainty"
	observedForeign, err := controller.ObserveClarification(db, task.ID, dispatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreign.TaskID, foreign.DispatchID, foreign.Role, foreign.BasisHash = task.ID, dispatch.ID, "CODER", observedForeign.Hash
	if _, err := controller.RequestClarification(db, foreign); err != nil {
		t.Fatal(err)
	}
	other := readInvocation
	other.Arguments = map[string]any{"action": "read", "id": foreign.ID}
	if _, err := handler(other); err == nil {
		t.Fatal("answer read became a recipient inbox for a question from another origin")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Records.Operation.Owner = "new-owner"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := handler(readInvocation); err == nil {
		t.Fatal("former owner read answer evidence")
	}
	if err := db.UpdateWorkspace(func(state *store.Workspace) error {
		state.Records.Operation.Owner = binding.Owner
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	history, err = controller.ClarificationHistory(db)
	if err != nil {
		t.Fatal(err)
	}
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
	adapter.config.ManagedClarification.Request = func(ctx context.Context, session string, body json.RawMessage) (json.RawMessage, error) {
		control, err := controller.New(reopened, adapter)
		if err != nil {
			return nil, err
		}
		return control.ObserveRoleClarification(ctx, binding, session, body)
	}
	result, err := handler(readInvocation)
	var restored controller.ClarificationAssessment
	if err != nil || json.Unmarshal([]byte(result.TextResultForLLM), &restored) != nil ||
		restored.Receipt != selected.Receipt || !reflect.DeepEqual(restored.Candidates, selected.Candidates) ||
		!reflect.DeepEqual(restored.Resolution, selected.Resolution) {
		t.Fatal("store reopen changed or lost the selected answer evidence", result, err)
	}
	newEvidence := answer
	newEvidence.Source, newEvidence.Text = "spec:third", "A third interpretation."
	if _, err := controller.AnswerClarification(reopened, request.ID, newEvidence); err == nil {
		t.Fatal("new conflict accepted")
	}
	result, err = handler(readInvocation)
	restored = controller.ClarificationAssessment{}
	if err != nil || json.Unmarshal([]byte(result.TextResultForLLM), &restored) != nil ||
		restored.Resolution != nil || len(restored.Candidates) != 3 ||
		!slices.ContainsFunc(restored.Decision.Conditions, func(c core.InputCondition) bool { return c.Code == "clarification-conflict" }) {
		t.Fatal("new evidence did not invalidate the answer selection visible to the Role", result, err)
	}
	newEvidence.Source, newEvidence.Text = "spec:long", strings.Repeat("Additional evidence. ", 4000)
	if _, err := controller.AnswerClarification(reopened, request.ID, newEvidence); err == nil {
		t.Fatal("oversized conflicting answer was accepted")
	}
	if result, err := handler(readInvocation); err == nil || result.TextResultForLLM != "" {
		t.Fatal("bounded answer read returned silently truncated evidence", result, err)
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

func TestClarificationInboxFailsClosedWithoutRootAttribution(t *testing.T) {
	cfg := testConfig(t)
	calls := 0
	cfg.ManagedClarification = &runtime.ManagedClarification{
		ProtocolVersion: 2, DispatchID: "repair", ScopeHash: "scope", OperationID: "operation",
		Owner: "owner", FlowID: "flow", RunnerID: "runner",
		Request: func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, errors.New("not an intake request")
		},
		Inbox: func(ctx context.Context, session, call string, body json.RawMessage) (json.RawMessage, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if session != "coder" || call != "receive-call" || !json.Valid(body) {
				t.Fatal("SDK inbox lost its host invocation identity", session, call)
			}
			calls++
			return json.RawMessage(`{"id":"question","handoff":{"phase":"clarification.handoff","status":"REQUESTED"},"receipt":{"hash":"receipt"},"question":{"question":"Which evidence is missing?"}}`), nil
		},
	}
	for _, policy := range []*tools.Policy{nil, {EvidenceDirectory: t.TempDir()}} {
		cfg.ToolPolicy = policy
		adapter, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		adapter.sessions["coder"] = &managedSession{sdk: &sdk.Session{}, busy: true}
		for _, names := range [][]string{adapter.sessionConfig(nil).AvailableTools, adapter.resumeConfig(nil).AvailableTools} {
			if slices.Contains(names, ClarificationInboxToolName) {
				t.Fatal("disabled inbox allowlisted")
			}
		}
		for _, registered := range [][]sdk.Tool{adapter.sessionConfig(nil).Tools, adapter.resumeConfig(nil).Tools} {
			intakeRegistered := false
			for _, tool := range registered {
				if tool.Name == ClarificationInboxToolName {
					t.Fatal("unattributed inbox registered")
				}
				intakeRegistered = intakeRegistered || tool.Name == ClarificationToolName
			}
			if !intakeRegistered {
				t.Fatal("inbox disablement removed clarification intake")
			}
		}
		for _, agentID := range []string{"", "child"} {
			for _, action := range []string{"receive", "ack"} {
				invocation := sdk.ToolInvocation{ToolName: ClarificationInboxToolName, ToolCallID: "call", SessionID: "coder",
					AgentID: agentID, TraceContext: t.Context(), Arguments: map[string]any{"action": action}}
				for range 2 {
					if result, err := adapter.clarificationInboxTool(invocation); err == nil || result.ResultType == "success" {
						t.Fatal("disabled inbox invocation or replay reached its callback", agentID, action)
					}
				}
			}
		}
		if calls != 0 {
			t.Fatal("unverified attribution reached Controller callback")
		}
		injected := cfg
		injected.AvailableTools = append(slices.Clone(cfg.AvailableTools), ClarificationInboxToolName)
		if _, err := New(injected); err == nil {
			t.Fatal("disabled inbox manually allowlisted")
		}
	}
}

func TestClarificationNativeCompletionPreservesContentEvidence(t *testing.T) {
	content := `{"handoff":{"phase":"clarification.handoff"},"receipt":{"hash":"receipt"}}`
	handle := &managedSession{}
	handle.onEvent(sdk.SessionEvent{ID: "native-complete", Timestamp: time.Now(),
		Data: &sdk.ToolExecutionCompleteData{ToolCallID: "receive-call", Success: true,
			Result: &rpc.ToolExecutionCompleteResult{Content: content}}})
	if len(handle.history) != 1 || handle.history[0].Type != runtime.EventExecution || handle.history[0].Sequence == 0 ||
		!strings.Contains(handle.history[0].Payload, `"callId":"receive-call"`) {
		t.Fatal("SDK completion did not retain the exact tool correlation", handle.history)
	}
	var observation core.ExecutionObservation
	if err := json.Unmarshal([]byte(handle.history[0].Payload), &observation); err != nil || observation.ContentHash == "" {
		t.Fatal("model-facing result hash was not retained", observation, err)
	}
}
