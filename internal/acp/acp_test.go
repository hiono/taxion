package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

type msg = map[string]any

func TestInitializeReportsReleaseVersion(t *testing.T) {
	messages := run(t, New(filepath.Join(t.TempDir(), "ledger.json")), req(1, "initialize", msg{}))
	if len(messages) != 1 {
		t.Fatalf("initialize returned %d messages", len(messages))
	}
	result := messages[0]["result"].(map[string]any)
	info := result["agentInfo"].(map[string]any)
	if info["name"] != "taxion" || info["version"] != "0.3.0" {
		t.Fatalf("incorrect release identity: %v", info)
	}
}

type fakeRuntime struct {
	role     string
	worktree string
	events   chan runtime.Event
	sent     chan struct{}
	stops    int
	resumes  int
	closed   bool
	wait     bool
	prompts  []string
}

func (f *fakeRuntime) Role() string       { return f.role }
func (f *fakeRuntime) ConfigHash() string { return "config-hash" }
func (f *fakeRuntime) Worktree() string   { return f.worktree }
func (f *fakeRuntime) Start(context.Context, string) (string, error) {
	return "sdk-session", nil
}
func (f *fakeRuntime) Send(ctx context.Context, _, prompt string) error {
	f.prompts = append(f.prompts, prompt)
	if f.sent != nil {
		f.sent <- struct{}{}
	}
	if f.wait {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func (f *fakeRuntime) Resume(context.Context, string) error { f.resumes++; return nil }
func (f *fakeRuntime) Stop(context.Context, string) error   { f.stops++; return nil }
func (f *fakeRuntime) Events(context.Context, string) (<-chan runtime.Event, error) {
	return f.events, nil
}
func (f *fakeRuntime) EventCursor(string) (uint64, error) { return 0, nil }
func (f *fakeRuntime) EventsAfter(context.Context, string, uint64) (<-chan runtime.Event, error) {
	return f.events, nil
}
func (f *fakeRuntime) Close() error { f.closed = true; return nil }

func run(t *testing.T, srv *Server, lines ...string) []msg {
	t.Helper()
	var out bytes.Buffer
	if err := srv.Serve(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out); err != nil {
		t.Fatalf("serve: %v", err)
	}
	trimmed := strings.TrimSpace(out.String())
	if trimmed == "" {
		return nil
	}
	var messages []msg
	for _, line := range strings.Split(trimmed, "\n") {
		var message msg
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatalf("bad output line %q: %v", line, err)
		}
		messages = append(messages, message)
	}
	return messages
}

func req(id int, method string, params msg) string {
	encoded, _ := json.Marshal(params)
	return string(mustJSON(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": json.RawMessage(encoded),
	}))
}

func notif(method string, params msg) string {
	encoded, _ := json.Marshal(params)
	return string(mustJSON(map[string]any{
		"jsonrpc": "2.0", "method": method, "params": json.RawMessage(encoded),
	}))
}

func mustJSON(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func result(t *testing.T, message msg) msg {
	t.Helper()
	r, ok := message["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result, got %v", message)
	}
	return r
}

func TestPlanRestoresWorkWithoutSessionHistory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.json")
	workspace := filepath.Join(dir, "workspace.db")
	task := core.GraphTask{
		ID: "deferred", Title: "Retained concern", Repo: "owner/repo", Type: "INVESTIGATION",
		Status: core.Blocked, Issue: "Issue #67", CreatedFrom: "human:1",
		DeferredReason: "Follow-up investigation", ResumeCondition: "Approved scope", NextAction: "Investigate",
	}
	graph := core.TaskGraph{Version: 1}
	if err := graph.Track(task, "human", "scope out"); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.InitializeWorkspace(store.Workspace{
		HasGraph: true, Graph: graph,
		Ledger: core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	server := NewWithWorkspace(path, workspace)
	messages := run(t, server, req(1, "taxion/plan", msg{"repository": task.Repo}))
	plan := result(t, messages[0])
	steps := plan["tasks"].([]any)
	step := steps[0].(map[string]any)
	contract := step["task"].(map[string]any)
	if step["action"] != "REASSESS_DEFERRED" || contract["id"] != task.ID || contract["resumeCondition"] != task.ResumeCondition {
		t.Fatalf("durable planning required old ACP session: %+v", plan)
	}
	observation, ok := plan["observation"].(map[string]any)
	if !ok {
		t.Fatalf("planning observation missing: %+v", plan)
	}
	hash, ok := observation["contentHash"].(string)
	if !ok || !strings.HasPrefix(hash, "sha256:") || observation["startedAt"] == nil || observation["completedAt"] == nil {
		t.Fatalf("planning observation incomplete: %+v", observation)
	}
	repeated := result(t, run(t, server, req(3, "taxion/plan", msg{"repository": task.Repo}))[0])
	if repeated["observation"].(map[string]any)["contentHash"] != hash {
		t.Fatalf("ACP observation changed unchanged records: %+v", repeated)
	}
}

func TestSessionRequiresFreshApprovedDispatch(t *testing.T) {
	db, dispatchID, worktree := dispatchWorkspace(t, false)
	defer db.Close()
	factoryCalls := 0
	srv := NewRuntime(filepath.Join(t.TempDir(), "ledger.json"), db, time.Minute, func(core.GraphDispatch) (runtime.Identity, error) {
		factoryCalls++
		return &fakeRuntime{role: "CODER", worktree: worktree}, nil
	})

	messages := run(t, srv, req(1, "session/new", msg{"cwd": worktree, "dispatchId": dispatchID}))
	if messages[0]["error"] == nil || factoryCalls != 0 {
		t.Fatalf("unissued Dispatch reached runtime factory: %#v", messages)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Graph.Dispatches) != 0 || len(state.Ledger.Dispatches) != 0 {
		t.Fatalf("ACP created a Dispatch without CLI authorization: %+v", state.Graph.Dispatches)
	}
}

func TestSessionBindsOnlyTheApprovedDispatch(t *testing.T) {
	db, dispatchID, worktree := dispatchWorkspace(t, true)
	defer db.Close()
	var received string
	worker := &fakeRuntime{role: "CODER", worktree: worktree}
	srv := NewRuntime(filepath.Join(t.TempDir(), "ledger.json"), db, time.Minute, func(dispatch core.GraphDispatch) (runtime.Identity, error) {
		received = dispatch.ID
		return worker, nil
	})
	if messages := run(t, srv, notif("session/new", msg{"cwd": worktree, "dispatchId": dispatchID})); len(messages) != 0 || received != "" {
		t.Fatalf("notification started an unobservable runtime session: messages=%v dispatch=%q", messages, received)
	}
	messages := run(t, srv, req(1, "session/new", msg{"cwd": worktree, "dispatchId": dispatchID}))
	if messages[0]["error"] != nil || result(t, messages[0])["sessionId"] == "" {
		t.Fatalf("approved Dispatch was not attached: %#v", messages)
	}
	if received != dispatchID || !worker.closed {
		t.Fatalf("session used a different Dispatch or leaked its worker: dispatch=%q closed=%v", received, worker.closed)
	}
}

func TestPromptUsesControllerAndPersistsRuntimeFailure(t *testing.T) {
	db, dispatchID, worktree := dispatchWorkspace(t, true)
	defer db.Close()
	worker := &fakeRuntime{
		role: "CODER", worktree: worktree, events: make(chan runtime.Event, 1),
		sent: make(chan struct{}, 1),
	}
	worker.events <- runtime.Event{Type: runtime.EventFailed, Payload: "simulated runtime failure"}
	server := NewRuntime(filepath.Join(t.TempDir(), "ledger.json"), db, time.Minute, func(core.GraphDispatch) (runtime.Identity, error) {
		return worker, nil
	})
	var output bytes.Buffer
	out := bufio.NewWriter(&output)
	server.handle(context.Background(), decodeRequest(t, req(1, "session/new", msg{
		"cwd": worktree, "dispatchId": dispatchID,
	})), out)
	messages := decodeMessages(t, output.String())
	sid := result(t, messages[0])["sessionId"].(string)
	server.handle(context.Background(), decodeRequest(t, req(2, "session/prompt", msg{
		"sessionId": sid, "prompt": []any{msg{"type": "text", "text": "implement the approved task"}},
	})), out)
	server.wg.Wait()
	messages = decodeMessages(t, output.String())
	last := messages[len(messages)-1]
	if last["error"] == nil || !strings.Contains(last["error"].(map[string]any)["message"].(string), "simulated runtime failure") {
		t.Fatalf("runtime failure was not returned to ACP: %+v", last)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, ok := state.Graph.GraphDispatch(dispatchID)
	if !ok || dispatch.Status != core.DispatchFailed || dispatch.RuntimeSessionID == "" ||
		len(state.Records.Events) < 2 || len(state.Ledger.Dispatches) != 1 || state.Ledger.Dispatches[0].Active || !worker.closed {
		t.Fatalf("runtime failure was not persisted through the controller: %+v", dispatch)
	}
	if len(worker.prompts) != 1 || !strings.Contains(worker.prompts[0], "additional task context only") ||
		!strings.Contains(worker.prompts[0], "implement the approved task") {
		t.Fatalf("ACP prompt was not constrained to the approved Dispatch: %+v", worker.prompts)
	}
}

func TestPromptCancellationRecordsControllerSafeStop(t *testing.T) {
	db, dispatchID, worktree := dispatchWorkspace(t, true)
	defer db.Close()
	worker := &fakeRuntime{
		role: "CODER", worktree: worktree, events: make(chan runtime.Event),
		sent: make(chan struct{}, 1), wait: true,
	}
	server := NewRuntime(filepath.Join(t.TempDir(), "ledger.json"), db, time.Minute, func(core.GraphDispatch) (runtime.Identity, error) {
		return worker, nil
	})
	var output bytes.Buffer
	out := bufio.NewWriter(&output)
	server.handle(context.Background(), decodeRequest(t, req(1, "session/new", msg{
		"cwd": worktree, "dispatchId": dispatchID,
	})), out)
	sid := result(t, decodeMessages(t, output.String())[0])["sessionId"].(string)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server.handle(ctx, decodeRequest(t, req(2, "session/prompt", msg{
		"sessionId": sid, "prompt": []any{msg{"type": "text", "text": "run the approved task"}},
	})), out)
	<-worker.sent
	server.handle(ctx, decodeRequest(t, notif("session/cancel", msg{"sessionId": sid})), out)
	server.wg.Wait()
	messages := decodeMessages(t, output.String())
	if result(t, messages[len(messages)-1])["stopReason"] != "cancelled" {
		t.Fatalf("cancelled prompt did not return a cancelled stop reason: %+v", messages[len(messages)-1])
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, ok := state.Graph.GraphDispatch(dispatchID)
	if !ok || dispatch.Status != core.DispatchCancelled || worker.stops != 1 || !worker.closed {
		t.Fatalf("ACP cancellation did not record a controller safe stop: dispatch=%+v stops=%d", dispatch, worker.stops)
	}
}

func TestReconnectObservesBoundRuntimeWithoutResendingPrompt(t *testing.T) {
	db, dispatchID, worktree := dispatchWorkspace(t, true)
	defer db.Close()
	original := &fakeRuntime{
		role: "CODER", worktree: worktree, events: make(chan runtime.Event, 1),
		sent: make(chan struct{}, 1),
	}
	control, err := controller.New(db, original)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := control.Start(context.Background(), dispatchID); err != nil {
		t.Fatal(err)
	}
	if err := control.Send(context.Background(), dispatchID, "original approved turn"); err != nil {
		t.Fatal(err)
	}
	resumed := &fakeRuntime{
		role: "CODER", worktree: worktree, events: make(chan runtime.Event, 1),
		sent: make(chan struct{}, 1),
	}
	resumed.events <- runtime.Event{Type: runtime.EventFailed, Payload: "recovered failure"}
	server := NewRuntime(filepath.Join(t.TempDir(), "ledger.json"), db, time.Minute, func(core.GraphDispatch) (runtime.Identity, error) {
		return resumed, nil
	})
	var output bytes.Buffer
	out := bufio.NewWriter(&output)
	server.handle(context.Background(), decodeRequest(t, req(1, "session/new", msg{
		"cwd": worktree, "dispatchId": dispatchID,
	})), out)
	sid := result(t, decodeMessages(t, output.String())[0])["sessionId"].(string)
	server.handle(context.Background(), decodeRequest(t, req(2, "session/prompt", msg{
		"sessionId": sid, "prompt": []any{msg{"type": "text", "text": "do not submit this prompt again"}},
	})), out)
	server.wg.Wait()
	messages := decodeMessages(t, output.String())
	if messages[len(messages)-1]["error"] == nil || resumed.resumes != 1 || len(resumed.prompts) != 0 {
		t.Fatalf("reconnect resent work instead of observing the saved session: responses=%+v resumes=%d prompts=%v",
			messages, resumed.resumes, resumed.prompts)
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		t.Fatal(err)
	}
	dispatch, ok := state.Graph.GraphDispatch(dispatchID)
	if !ok || dispatch.Status != core.DispatchFailed || dispatch.RuntimeSessionID != "sdk-session" {
		t.Fatalf("reconnected runtime state was not reconciled: %+v", dispatch)
	}
}

func TestACPDoesNotIssueOrReviewDispatches(t *testing.T) {
	server := New(filepath.Join(t.TempDir(), "ledger.json"))
	for _, method := range []string{"taxion/dispatch", "taxion/review"} {
		messages := run(t, server, req(1, method, msg{}))
		if messages[0]["error"] == nil {
			t.Fatalf("removed implicit authorization method %q was accepted", method)
		}
	}
}

func TestSessionCancelCancelsActivePrompt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	worker := &fakeRuntime{role: "CODER", worktree: t.TempDir()}
	server := New(filepath.Join(t.TempDir(), "ledger.json"))
	server.sessions["active"] = &acpSession{worker: worker, cancel: cancel, active: true}
	messages := run(t, server, notif("session/cancel", msg{"sessionId": "active"}))
	if len(messages) != 0 || ctx.Err() != context.Canceled {
		t.Fatalf("cancel notification did not cancel the active prompt: messages=%v context=%v", messages, ctx.Err())
	}
	if !worker.closed {
		t.Fatal("server did not close the cancelled session worker")
	}
}

func TestRuntimeOutputDeltasBecomeACPProgressUpdates(t *testing.T) {
	server := New(filepath.Join(t.TempDir(), "ledger.json"))
	var output bytes.Buffer
	writer := &eventWriter{server: server, out: bufio.NewWriter(&output), sessionID: "session"}
	for _, event := range []runtime.Event{
		{Type: runtime.EventOutputDelta, Payload: "first "},
		{Type: runtime.EventOutputDelta, Payload: "second"},
		{Type: runtime.EventOutput, Payload: "first second"},
	} {
		line := append(mustJSON(event), '\n')
		if _, err := writer.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	messages := decodeMessages(t, output.String())
	if len(messages) != 2 ||
		messages[0]["params"].(map[string]any)["update"].(map[string]any)["content"].(map[string]any)["text"] != "first " ||
		messages[1]["params"].(map[string]any)["update"].(map[string]any)["content"].(map[string]any)["text"] != "second" {
		t.Fatalf("ACP did not stream output deltas without duplicating the final message: %+v", messages)
	}
}

func dispatchWorkspace(t *testing.T, issue bool) (*store.Store, string, string) {
	t.Helper()
	worktree := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	task := core.GraphTask{
		ID: "task-1", Title: "Approved task", Repo: "owner/repo", Type: "IMPLEMENTATION", Issue: "Issue #50",
		Owner: "CODER", Reviewer: "reviewer", Status: core.Draft, Objective: "test ACP authorization",
		Scope: []string{"internal/acp"}, Acceptance: []string{"ACP uses the approved Dispatch"},
		ValidationCommands: []string{"go test ./internal/acp"}, RequiredEvidence: []string{"test evidence"},
		ExitCondition: "tests pass", Branch: "work/task", Worktree: worktree,
		Decisions: []core.TaskDecision{{ID: "decision:1", Source: "human:1", Text: "approved"}},
	}
	git(t, worktree, "init", "-b", task.Branch)
	git(t, worktree, "config", "user.name", "Taxion Test")
	git(t, worktree, "config", "user.email", "taxion-test@example.com")
	git(t, worktree, "remote", "add", "origin", "https://github.com/owner/repo.git")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, worktree, "add", "README.md")
	git(t, worktree, "commit", "-m", "baseline")
	task.BaselineSHA = git(t, worktree, "rev-parse", "HEAD")
	task.CreatedFrom, task.AuthorizationID = "request:1", "decision:1"
	graph := core.TaskGraph{Version: 1}
	if err := graph.Track(task, "human", "register task"); err != nil {
		t.Fatal(err)
	}
	if err := graph.Transition(task.ID, core.Ready, "human", "approve task", core.TransitionProof{}); err != nil {
		t.Fatal(err)
	}
	var dispatch core.GraphDispatch
	if issue {
		dispatch, err = core.NewGraphDispatch(graph, "dispatch-1", task.ID, task.Repo, core.RelationshipNone, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := graph.IssueDispatch(dispatch, "cli", "human-approved dispatch"); err != nil {
			t.Fatal(err)
		}
	}
	ledger := core.Ledger{Tasks: []core.Task{{ID: task.ID, Title: task.Title, Repo: task.Repo}}}
	if issue {
		ledger.Dispatches = []core.Dispatch{{ID: dispatch.ID, TaskID: task.ID, Repo: task.Repo, Active: true, CreatedAt: dispatch.CreatedAt}}
	}
	if _, err := db.InitializeWorkspace(store.Workspace{HasGraph: true, Graph: graph, Ledger: ledger}); err != nil {
		t.Fatal(err)
	}
	if !issue {
		dispatch.ID = "dispatch-1"
	}
	return db, dispatch.ID, worktree
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}

func decodeRequest(t *testing.T, line string) Request {
	t.Helper()
	var request Request
	if err := json.Unmarshal([]byte(line), &request); err != nil {
		t.Fatal(err)
	}
	return request
}

func decodeMessages(t *testing.T, output string) []msg {
	t.Helper()
	var messages []msg
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		var message msg
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatalf("bad output line %q: %v", line, err)
		}
		messages = append(messages, message)
	}
	return messages
}
