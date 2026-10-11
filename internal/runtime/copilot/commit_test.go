package copilot

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func commitToolConfig(t *testing.T) Config {
	config := testConfig(t)
	config.ManagedCommit = &runtime.ManagedCommit{
		DispatchID: "dispatch-1", Scanner: filepath.Join(t.TempDir(), "scanner"),
		Commit: func(context.Context, string, string) (string, error) { return strings.Repeat("a", 40), nil },
	}
	return config
}

func TestManagedCommitToolCreateResumeAndIdentity(t *testing.T) {
	config := commitToolConfig(t)
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	hash := adapter.ConfigHash()
	for _, tools := range [][]sdk.Tool{adapter.sessionConfig(nil).Tools, adapter.resumeConfig(nil).Tools} {
		if len(tools) != 1 || tools[0].Name != ManagedCommitToolName || tools[0].Handler == nil ||
			tools[0].SkipPermission || tools[0].OverridesBuiltInTool || tools[0].IsTerminal || tools[0].Defer != sdk.ToolDeferNever {
			t.Fatal("managed tool declaration missing or bypasses ordinary permission/result handling")
		}
		if tools[0].Parameters["additionalProperties"] != false {
			t.Fatal("tool accepts agent-selected Git settings")
		}
	}
	if !slices.Contains(adapter.sessionConfig(nil).AvailableTools, ManagedCommitToolName) ||
		!slices.Contains(adapter.resumeConfig(nil).AvailableTools, ManagedCommitToolName) {
		t.Fatal("managed tool not allowlisted for creation and resume")
	}
	for _, change := range []string{"disabled", "dispatch", "scanner", "handler"} {
		changed := config
		binding := *config.ManagedCommit
		changed.ManagedCommit = &binding
		switch change {
		case "disabled":
			changed.ManagedCommit = nil
		case "dispatch":
			binding.DispatchID = "different"
		case "scanner":
			binding.Scanner = filepath.Join(t.TempDir(), "different")
		case "handler":
			binding.Commit = func(context.Context, string, string) (string, error) { return "", errors.New("different closure") }
		}
		other, err := New(changed)
		if err != nil {
			t.Fatal(err)
		}
		if (other.ConfigHash() == hash) != (change == "handler") {
			t.Fatal("configuration hash does not preserve the durable managed tool boundary", change)
		}
	}
	config.ManagedCommit.Scanner = "/changed-after-New"
	if adapter.ConfigHash() != hash {
		t.Fatal("caller changed adapter binding after creation")
	}
	for _, role := range []string{"REVIEWER", "PLANNER", "ORCHESTRATOR"} {
		config := commitToolConfig(t)
		config.Role = role
		if _, err := New(config); err == nil {
			t.Fatal("non-Coder registered managed commit", role)
		}
	}
	unbound := testConfig(t)
	unbound.AvailableTools = []string{ManagedCommitToolName}
	if _, err := New(unbound); err == nil {
		t.Fatal("allowlist alone enabled managed commit")
	}
}

func TestManagedCommitToolInvocationBoundary(t *testing.T) {
	config := commitToolConfig(t)
	calls := 0
	config.ManagedCommit.Commit = func(ctx context.Context, sessionID, message string) (string, error) {
		calls++
		if sessionID != "session-1" || message != "complete message" || ctx == nil {
			t.Fatal("SDK-neutral handler lost invocation identity or content")
		}
		return strings.Repeat("a", 40), nil
	}
	adapter, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	handle := &managedSession{sdk: &sdk.Session{}, busy: true, watchers: map[*subscription]struct{}{}}
	adapter.sessions["session-1"] = handle
	invocation := sdk.ToolInvocation{
		ToolName: ManagedCommitToolName, SessionID: "session-1", TraceContext: context.Background(),
		Arguments: map[string]any{"message": "complete message"},
	}
	for _, kind := range []string{"unknown-session", "subagent", "tool-name", "unknown-argument", "missing", "null", "type", "blank", "context", "cancelled", "idle", "stopped"} {
		t.Run(kind, func(t *testing.T) {
			bad := invocation
			switch kind {
			case "unknown-session":
				bad.SessionID = "different"
			case "subagent":
				bad.AgentID = "subagent"
			case "tool-name":
				bad.ToolName = "other"
			case "unknown-argument":
				bad.Arguments = map[string]any{"message": "complete message", "scanner": "different"}
			case "missing":
				bad.Arguments = map[string]any{}
			case "null":
				bad.Arguments = nil
			case "type":
				bad.Arguments = map[string]any{"message": 123}
			case "blank":
				bad.Arguments = map[string]any{"message": " "}
			case "context":
				bad.TraceContext = nil
			case "cancelled":
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				bad.TraceContext = ctx
			case "idle":
				handle.busy = false
				defer func() { handle.busy = true }()
			case "stopped":
				handle.stopped = true
				defer func() { handle.stopped = false }()
			}
			result, err := adapter.sessionConfig(nil).Tools[0].Handler(bad)
			if err == nil || result.ResultType == "success" || calls != 0 {
				t.Fatal("unsafe SDK invocation reached the host commit", kind, err)
			}
		})
	}
	result, err := adapter.resumeConfig(nil).Tools[0].Handler(invocation)
	if err != nil || result.ResultType != "success" || calls != 1 ||
		!strings.Contains(result.TextResultForLLM, strings.Repeat("a", 40)) ||
		!strings.Contains(result.TextResultForLLM, "Task is not complete") {
		t.Fatal("successful host commit result lost SHA or acceptance boundary", result, err)
	}
	adapter.config.ManagedCommit.Commit = func(context.Context, string, string) (string, error) {
		return "", errors.New("secret scanner unavailable")
	}
	result, err = adapter.commitTool(invocation)
	if err == nil || result.ResultType == "success" || !strings.Contains(err.Error(), "scanner unavailable") {
		t.Fatal("host failure became successful SDK result")
	}
}

func TestSDKManagedStageAndCommitInvokeControllerGitGate(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		output, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git fixture failed: %v: %s", err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-b", "work/task")
	git("config", "user.name", "Test")
	git("config", "user.email", "test@example.com")
	git("remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(root, "baseline.txt"), []byte("baseline"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", "baseline.txt")
	git("commit", "-qm", "baseline")
	baseline := git("rev-parse", "HEAD")
	task := core.GraphTask{
		ID: "task", Title: "approved work", Repo: "owner/repo", Type: "IMPLEMENTATION",
		Status: core.Draft, Owner: "CODER", Reviewer: "REVIEWER", Issue: "Issue #72",
		CreatedFrom: "human:1", Objective: "safe commit", Scope: []string{"src"},
		Acceptance: []string{"safe tree"}, RequiredEvidence: []string{"report"},
		ValidationCommands: []string{"test -f src/source.txt"}, ExitCondition: "safe tree",
		Worktree: root, Branch: "work/task", BaselineSHA: baseline, AuthorizationID: "decision:1",
		Decisions: []core.TaskDecision{{ID: "decision:1", Source: "human:1", Text: "approve"}},
	}
	graph := core.TaskGraph{Version: 1}
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
	db, err := store.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	config := commitToolConfig(t)
	config.WorkingDirectory = root
	scanner := config.ManagedCommit.Scanner
	if err := os.WriteFile(scanner, []byte("#!/bin/sh\nif [ \"$1\" = version ]; then printf '1.9.0\\n'; exit 0; fi\ncontent=$(cat)\ncase \"$content\" in *fake-private-payload*) printf '[{}]'; exit 10;; esac\nprintf '[]'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	var adapter *Adapter
	config.ManagedCommit.DispatchID = dispatch.ID
	config.ManagedCommit.Commit = func(ctx context.Context, sessionID, message string) (string, error) {
		control, err := controller.New(db, adapter)
		if err != nil {
			return "", err
		}
		return control.CommitForSession(ctx, dispatch.ID, sessionID, message, scanner)
	}
	config.ManagedCommit.Stage = func(ctx context.Context, sessionID string, paths []string) (string, error) {
		control, err := controller.New(db, adapter)
		if err != nil {
			return "", err
		}
		return control.StageForSession(ctx, dispatch.ID, sessionID, paths, scanner)
	}
	adapter, err = New(config)
	if err != nil {
		t.Fatal(err)
	}
	graph.Dispatches[0].ExecutionStartSHA = baseline
	if err := graph.BindRuntimeSession(dispatch.ID, "session", adapter.ConfigHash()); err != nil {
		t.Fatal(err)
	}
	for _, status := range []core.DispatchStatus{core.DispatchAcknowledged, core.DispatchRunning} {
		if err := graph.TransitionDispatch(dispatch.ID, status, "runtime", "execute", true, status == core.DispatchRunning, nil); err != nil {
			t.Fatal(err)
		}
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
	adapter.sessions["session"] = &managedSession{sdk: &sdk.Session{}, busy: true}
	if err := os.Mkdir(filepath.Join(root, "src"), 0700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "src", "source.txt")
	if err := os.WriteFile(source, []byte("approved content"), 0600); err != nil {
		t.Fatal(err)
	}
	stage := sdk.ToolInvocation{ToolName: ManagedStageToolName, SessionID: "session",
		TraceContext: context.Background(), Arguments: map[string]any{"paths": []string{"src/source.txt"}}}
	stageHandler := adapter.sessionConfig(nil).Tools[1].Handler
	if err := os.WriteFile(source, []byte("fake-private-payload"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := stageHandler(stage)
	if err == nil || result.ResultType == "success" || strings.Contains(err.Error(), "fake-private-payload") ||
		strings.Contains(git("ls-files"), "src/source.txt") {
		t.Fatal("SDK stage content scan failed to stop safely", err)
	}
	if err := os.WriteFile(source, []byte("approved content"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = stageHandler(stage)
	if err != nil || result.ResultType != "success" || git("rev-parse", "HEAD") != baseline {
		t.Fatal("SDK managed stage failed or created a commit", result, err)
	}
	message := "feat(src): implement approved change\n\nintent(src): satisfy the authorized criterion\n"
	invocation := sdk.ToolInvocation{ToolName: ManagedCommitToolName, SessionID: "session",
		TraceContext: context.Background(), Arguments: map[string]any{"message": message}}
	bad := invocation
	bad.Arguments = map[string]any{"message": message + "constraint(src): fake-private-payload\n"}
	result, err = adapter.sessionConfig(nil).Tools[0].Handler(bad)
	if err == nil || result.ResultType == "success" || git("rev-parse", "HEAD") != baseline ||
		strings.Contains(err.Error(), "fake-private-payload") {
		t.Fatal("SDK message scan failed to stop safely", err)
	}
	tree := git("write-tree")
	if err := os.WriteFile(source, []byte("different unstaged content"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err = adapter.resumeConfig(nil).Tools[0].Handler(invocation)
	if err != nil || result.ResultType != "success" || git("rev-parse", "HEAD^{tree}") != tree {
		t.Fatal("registered SDK tool did not commit the controller-scanned tree", result, err)
	}
	saved, err := db.LoadWorkspace()
	if err != nil || saved.Graph.Tasks[0].Status != core.Active || saved.Graph.Dispatches[0].Status != core.DispatchRunning {
		t.Fatal("SDK commit bypassed review or completed the Task", err)
	}
}
