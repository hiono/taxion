// Command taxion is the human-facing CLI for the Taxion control plane.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/hiono/taxion/internal/acp"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/server"
	"github.com/hiono/taxion/internal/store"
)

const usage = `taxion — Evidence-governed control plane for coding agents

Usage:
  taxion [--store PATH] <command> [options]

Commands:
  config check        Validate fixed Global/Project YAML and environment references ([--sources], [--task])
  git check-index     Reject local Taxion and dotenv paths in the actual commit index
  git hook            Install (--scanner PATH, or --paths-only), inspect, or remove the Taxion pre-commit hook
  init                Initialize a local workspace ledger
  task create         Create a task (--title and --repo required)
  task list           List tasks ([--repo] filter, [--json])
  task track          Register/update a durable Task Graph contract (--file JSON)
  task transition     Validate and record a Task Graph transition (--file JSON)
  task graph          Print the versioned Task Graph and history
  task plan           Rebuild a follow-up plan from durable contracts and evidence (--repo required)
  dispatch            Dispatch a task (--task and --repo required; revised Dispatches need an explicit relationship)
  dispatch transition Record a Dispatch lifecycle transition (--id, --status, --reason)
  dispatch list       List dispatches ([--repo] filter, [--json])
  runtime start       Start the Copilot session bound to an active Dispatch and send its authorized instruction
  runtime send        Send a follow-up prompt to the Dispatch session (--prompt-file required)
  runtime resume      Resume the Dispatch session
  runtime events      Stream events from the Dispatch session
  runtime stop        Stop the Dispatch session
  runtime commit      Publish scanned staged changes for the bound running Coder (--message-file, --scanner)
  workflow start      Authorize a bounded review flow and issue its Coder Dispatch (--file JSON)
  workflow advance    Issue review/repair from the latest verified result (--id required)
  workflow run        Execute review/repair continuously with saved ceilings (--id and role prompt files required)
  workflow show       Read the saved review flow and ceilings (--id required)
  workflow recover    Release interrupted runner ownership after recorded execution stop (--file JSON)
  review              Bind coder-output evidence (--task, --sha and --repo required)
  resume              Print a resume acknowledgement (local stub; --session required)
  acp                 Serve ACP v1 over stdio (spike)
  serve               Serve HTTP API over the ledger ([--addr], [--mode], [--token]/TAXION_TOKEN, [--audit])
  gen-token           Print a random Bearer token for serve (M5 auth)
  audit verify        Verify the audit log hash chain ([--audit])
  memory put          Store a memory entry (--kind, --title, [--body, --scope, --source])
  memory get          Fetch one memory entry (--kind and --id required)
  memory list         List memory entries (--kind required, [--scope] filter)
  memory revise       Append a new version (--kind, --id, --title, --actor, --reason)
  memory revoke       Disable an entry without deleting its history
  memory history      Show entry lifecycle events (--kind and --id required)
  memory conflict     Record, inspect, or resolve explicit Memory conflicts
  memory source       Resolve a warning source (--warning-id required)
  memory warn         Record a repeated warning (--body required, [--scope])
  memory configure    Set the warning threshold (--promote-after required)
  memory approve      Approve a candidate (--id, --check, --value required; MEM-001)
  memory checks       List approved structured dispatch checks
  memory search       Search memory entries (--query required, [--kind, --scope])
  memory export       Print memory entries ([--scope], [--sanitize], no transmission)

Identity gate (REP-001): task create, dispatch and review require an explicit --repo.
The target is never inferred from the working directory.
`

var storePath = ".taxion/ledger.json"

func main() {
	args := os.Args[1:]
	if len(args) >= 2 && args[0] == "--store" {
		storePath = args[1]
		args = args[2:]
	}
	if len(args) < 1 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch args[0] {
	case "config":
		err = configCmd(args[1:])
	case "git":
		err = checkGitIndex(args[1:], os.Stdout)
	case "init":
		err = initCmd()
	case "task":
		err = taskCmd(args[1:])
	case "dispatch":
		err = dispatchCmd(args[1:])
	case "runtime":
		err = runtimeCmd(args[1:])
	case "workflow":
		err = workflowCmd(args[1:])
	case "review":
		err = reviewCmd(args[1:])
	case "resume":
		err = resumeCmd(args[1:])
	case "acp":
		err = runACP()
	case "serve":
		err = serveCmd(args[1:])
	case "gen-token":
		err = genTokenCmd()
	case "audit":
		err = auditCmd(args[1:])
	case "memory":
		err = memoryCmd(args[1:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n%s", args[0], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "taxion: %v\n", err)
		os.Exit(1)
	}
}

func loadStore() (core.Ledger, error) {
	db, state, err := loadWorkspace()
	if err != nil {
		return core.Ledger{}, err
	}
	defer db.Close()
	return state.Ledger, nil
}

func saveLedger(l core.Ledger) error {
	return updateWorkspace(func(state *store.Workspace) error {
		state.Ledger = l
		return nil
	})
}

func initCmd() error {
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		return err
	}
	db, err := store.Open(workspaceDBPath())
	if err != nil {
		return err
	}
	initialized, err := db.WorkspaceInitialized()
	if err != nil {
		db.Close()
		return err
	}
	if initialized {
		db.Close()
		fmt.Printf("taxion: workspace already initialized at %s\n", workspaceDBPath())
		return nil
	}
	db.Close()
	if _, err := os.Stat(storePath); err == nil {
		db, err := openWorkspace()
		if err != nil {
			return err
		}
		defer db.Close()
		fmt.Printf("taxion: workspace already initialized at %s\n", storePath)
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	db, err = store.Open(workspaceDBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.InitializeWorkspace(store.Workspace{
		Ledger: core.Ledger{Tasks: []core.Task{}, Dispatches: []core.Dispatch{}, Evidence: []core.Evidence{}, Checkpoints: []core.Checkpoint{}},
	})
	return err
}

func newID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func taskCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: taxion task create|list …")
	}
	switch args[0] {
	case "track", "transition", "graph", "plan":
		return taskGraphCmd(args[0], args[1:])
	case "create":
		fs := flag.NewFlagSet("task create", flag.ContinueOnError)
		title := fs.String("title", "", "task title")
		repo := fs.String("repo", "", "repository identity (required, REP-001)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *title == "" {
			return fmt.Errorf("task create: --title is required")
		}
		if err := core.CheckRepo(*repo); err != nil {
			return err
		}
		t := core.Task{ID: newID("t"), Title: *title, Repo: *repo, CreatedAt: time.Now().UTC()}
		if err := updateWorkspace(func(state *store.Workspace) error {
			state.Ledger.Tasks = append(state.Ledger.Tasks, t)
			return nil
		}); err != nil {
			return err
		}
		fmt.Printf("taxion: task %s created in %s\n", t.ID, *repo)
		return nil
	case "list":
		fs := flag.NewFlagSet("task list", flag.ContinueOnError)
		repo := fs.String("repo", "", "filter by repository identity")
		asJSON := fs.Bool("json", false, "JSON output")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		db, state, err := loadWorkspace()
		if err != nil {
			return err
		}
		defer db.Close()
		l := state.Ledger
		var tasks []core.Task
		for _, t := range l.Tasks {
			if *repo == "" || t.Repo == *repo {
				tasks = append(tasks, t)
			}
		}
		if *asJSON {
			return printJSON(tasks)
		}
		if len(tasks) == 0 {
			fmt.Println("taxion: no tasks")
			return nil
		}
		for _, t := range tasks {
			fmt.Printf("%s\t%s\t%s\n", t.ID, t.Repo, t.Title)
		}
		return nil
	default:
		return fmt.Errorf("usage: taxion task create|list …")
	}
}

func dispatchCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: taxion dispatch --task ID --repo REPO | taxion dispatch list …")
	}
	if args[0] == "transition" {
		return dispatchTransitionCmd(args[1:])
	}
	if args[0] == "list" {
		fs := flag.NewFlagSet("dispatch list", flag.ContinueOnError)
		repo := fs.String("repo", "", "filter by repository identity")
		asJSON := fs.Bool("json", false, "JSON output")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		l, err := loadStore()
		if err != nil {
			return err
		}
		var ds []core.Dispatch
		for _, d := range l.Dispatches {
			if *repo == "" || d.Repo == *repo {
				ds = append(ds, d)
			}
		}
		if *asJSON {
			return printJSON(ds)
		}
		if len(ds) == 0 {
			fmt.Println("taxion: no dispatches")
			return nil
		}
		for _, d := range ds {
			fmt.Printf("%s\ttask=%s\trepo=%s\tactive=%v\n", d.ID, d.TaskID, d.Repo, d.Active)
		}
		return nil
	}
	fs := flag.NewFlagSet("dispatch", flag.ContinueOnError)
	task := fs.String("task", "", "task id")
	repo := fs.String("repo", "", "repository identity (required, REP-001)")
	relationship := fs.String("relationship", string(core.RelationshipNone), "relationship to prior Dispatch")
	relatedDispatch := fs.String("related-dispatch", "", "related prior Dispatch ID")
	relationshipReason := fs.String("relationship-reason", "", "reason for the Dispatch relationship")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *task == "" {
		return fmt.Errorf("dispatch: --task is required")
	}
	if err := core.CheckRepo(*repo); err != nil {
		return err
	}
	preflightDB, preflightState, err := loadWorkspace()
	if err != nil {
		return err
	}
	if err := preflightDB.Close(); err != nil {
		return err
	}
	preflightTask, found := findTask(preflightState.Ledger, *task)
	if !found {
		return fmt.Errorf("dispatch: unknown task %q", *task)
	}
	var preflightRefs []string
	if preflightState.HasGraph {
		preflightRefs, _ = preflightState.Graph.TaskMemoryRefs(*task)
	}
	if err := memory.CheckDispatch(storePath, preflightTask, *repo, preflightRefs...); err != nil {
		return err
	}
	if preflightTask.Repo != *repo {
		return fmt.Errorf("dispatch: repository does not match task")
	}
	var did string
	selected := core.Task{}
	var instruction string
	if err := updateWorkspace(func(state *store.Workspace) error {
		var found bool
		for _, t := range state.Ledger.Tasks {
			if t.ID == *task {
				selected = t
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("dispatch: unknown task %q", *task)
		}
		if err := core.GuardActiveDispatch(state.Ledger.Dispatches); err != nil {
			return err
		}
		if selected != preflightTask {
			return fmt.Errorf("dispatch: task changed during policy validation; retry")
		}
		if state.HasGraph != preflightState.HasGraph {
			return fmt.Errorf("dispatch: Task Graph changed during policy validation; retry")
		}
		var refs []string
		if state.HasGraph {
			refs, _ = state.Graph.TaskMemoryRefs(*task)
		}
		if !slices.Equal(refs, preflightRefs) {
			return fmt.Errorf("dispatch: Task Memory references changed during policy validation; retry")
		}
		if err := memory.CheckDispatch(storePath, selected, *repo, refs...); err != nil {
			return err
		}
		if selected.Repo != *repo {
			return fmt.Errorf("dispatch: repository does not match task")
		}
		if state.HasGraph {
			if err := state.Graph.CheckExecutable(*task, *repo); err != nil {
				return err
			}
			memoryContext, err := memory.ResolveDispatchContext(storePath, refs)
			if err != nil {
				return err
			}

			did = newID("d")
			dispatch, err := core.NewGraphDispatch(state.Graph, did, *task, *repo,
				core.DispatchRelationship(*relationship), *relatedDispatch, *relationshipReason, memoryContext)
			if err != nil {
				return err
			}
			if err := state.Graph.IssueDispatch(dispatch, "controller", "dispatch issued"); err != nil {
				return err
			}
			instruction, err = core.RenderDispatchInstruction(dispatch)
			if err != nil {
				return err
			}
		} else {
			if core.DispatchRelationship(*relationship) != core.RelationshipNone ||
				*relatedDispatch != "" || *relationshipReason != "" {
				return fmt.Errorf("dispatch relationships require an initialized Task Graph")
			}
			did = newID("d")
		}
		state.Ledger.Dispatches = append(state.Ledger.Dispatches, core.Dispatch{ID: did, TaskID: *task, Repo: *repo, Active: true, CreatedAt: time.Now().UTC()})
		return nil
	}); err != nil {
		return err
	}
	if instruction != "" {
		fmt.Println(instruction)
	} else {
		fmt.Printf("taxion: dispatched %s for task %s in %s\n", did, *task, *repo)
	}
	return nil
}

func findTask(ledger core.Ledger, id string) (core.Task, bool) {
	for _, task := range ledger.Tasks {
		if task.ID == id {
			return task, true
		}
	}
	return core.Task{}, false
}

func reviewCmd(args []string) error {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	task := fs.String("task", "", "task id")
	sha := fs.String("sha", "", "coder output SHA")
	repo := fs.String("repo", "", "repository identity (required, REP-001)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *task == "" {
		return fmt.Errorf("review: --task is required")
	}
	if err := core.CheckRepo(*repo); err != nil {
		return err
	}
	ev, err := core.BindEvidence(*task, *sha)
	if err != nil {
		return err
	}
	if err := updateWorkspace(func(state *store.Workspace) error {
		state.Ledger.Evidence = append(state.Ledger.Evidence, ev)
		return nil
	}); err != nil {
		return err
	}
	fmt.Printf("taxion: review of task %s bound to SHA %s in %s\n", *task, *sha, *repo)
	return nil
}

func resumeCmd(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	session := fs.String("session", "", "session id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *session == "" {
		return fmt.Errorf("resume: --session is required")
	}
	fmt.Printf("taxion: resumed session %s (local stub)\n", *session)
	return nil
}

func runtimeCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taxion runtime {start|send|resume|events|stop} --dispatch ID --system-prompt-file PATH")
	}
	action := args[0]
	switch action {
	case "start", "send", "resume", "events", "stop", "commit":
	default:
		return fmt.Errorf("runtime: unknown action %q", action)
	}
	fs := flag.NewFlagSet("runtime "+action, flag.ContinueOnError)
	dispatchID := fs.String("dispatch", "", "active Dispatch ID")
	systemPromptFile := fs.String("system-prompt-file", "", "role-specific system prompt")
	cliPath := fs.String("cli", "", "Copilot CLI path (default: PATH)")
	baseDirectory := fs.String("base-directory", "", "Copilot state directory (default: ~/.copilot)")
	reviewSource := fs.String("review-source-worktree", "", "repository worktree used to create the independent Reviewer worktree")
	tools := fs.String("tools", "", "comma-separated SDK tool allowlist (default: none)")
	promptFile := fs.String("prompt-file", "", "follow-up instruction file (send only)")
	messageFile := fs.String("message-file", "", "Conventional/Contextual commit message file (commit only)")
	scanner := fs.String("scanner", "betterleaks", "trusted Betterleaks 1.9.0 executable (commit only)")
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum time to wait for a turn or event stream")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *dispatchID == "" || *systemPromptFile == "" {
		return fmt.Errorf("runtime %s: --dispatch and --system-prompt-file are required", action)
	}
	if *timeout <= 0 {
		return fmt.Errorf("runtime %s: --timeout must be positive", action)
	}
	if action == "send" && *promptFile == "" {
		return fmt.Errorf("runtime send: --prompt-file is required")
	}
	if action == "commit" && *messageFile == "" {
		return fmt.Errorf("runtime commit: --message-file is required")
	}
	if action != "commit" && *messageFile != "" {
		return fmt.Errorf("runtime: --message-file applies only to commit")
	}
	var scannerSpecified bool
	fs.Visit(func(option *flag.Flag) {
		if option.Name == "scanner" {
			scannerSpecified = true
		}
	})
	if action != "commit" && scannerSpecified {
		return fmt.Errorf("runtime: --scanner applies only to commit")
	}

	db, err := openWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	state, err := db.LoadWorkspace()
	if err != nil {
		return err
	}
	if !state.HasGraph {
		return fmt.Errorf("runtime: Task Graph is not initialized")
	}
	dispatch, ok := state.Graph.GraphDispatch(*dispatchID)
	if !ok {
		return fmt.Errorf("runtime: Dispatch %q not found", *dispatchID)
	}
	if dispatch.Instruction.Version < core.DispatchInstructionVersion {
		return fmt.Errorf("runtime: Dispatch %q predates the current execution contract; issue a new Dispatch", *dispatchID)
	}
	if *reviewSource != "" && (action != "start" || dispatch.Recipient != "REVIEWER") {
		return fmt.Errorf("runtime: --review-source-worktree applies to Reviewer start")
	}
	systemPrompt, err := os.ReadFile(*systemPromptFile)
	if err != nil {
		return fmt.Errorf("runtime: read system prompt: %w", err)
	}
	dispatchContract, err := core.RenderDispatchInstruction(dispatch)
	if err != nil {
		return fmt.Errorf("runtime: render Dispatch contract: %w", err)
	}
	availableTools, err := runtimeTools(*tools)
	if err != nil {
		return err
	}
	if action == "start" && dispatch.Recipient == "REVIEWER" {
		if dispatch.ReviewWorktreeSource == "" && *reviewSource == "" {
			return fmt.Errorf("runtime: Reviewer start requires --review-source-worktree")
		}
		if *reviewSource != "" {
			dispatch, err = controller.PrepareReview(db, dispatch.ID, *reviewSource)
			if err != nil {
				return err
			}
		}
	}
	worker, err := copilot.New(copilot.Config{
		Role: dispatch.Recipient, SystemPrompt: runtimeSystemPrompt(string(systemPrompt), dispatchContract),
		WorkingDirectory: dispatch.Instruction.Worktree, CLIPath: *cliPath,
		BaseDirectory: *baseDirectory, AvailableTools: availableTools,
		ResponseSchema: dispatchResultSchema(dispatch),
	})
	if err != nil {
		return err
	}
	control, err := controller.New(db, worker)
	if err != nil {
		_ = worker.Close()
		return err
	}

	ctx, cancel := signalContext(*timeout)
	defer cancel()
	var operationErr error
	switch action {
	case "start":
		if dispatch.RuntimeSessionID != "" {
			operationErr = fmt.Errorf("runtime start: Dispatch already has a session; use send or resume")
			break
		}
		var sessionID string
		sessionID, operationErr = control.Start(ctx, dispatch.ID)
		if operationErr == nil {
			var cursor uint64
			cursor, operationErr = control.EventCursor(dispatch.ID)
			if operationErr == nil {
				operationErr = control.Send(ctx, dispatch.ID,
					"Begin executing the active Dispatch under the durable Task Graph contract in your system instructions.")
			}
			if operationErr == nil {
				fmt.Fprintf(os.Stderr, "taxion: started runtime session %s for Dispatch %s\n", sessionID, dispatch.ID)
				operationErr = streamEvents(ctx, control, dispatch, cursor, os.Stdout)
			}
		}
	case "send":
		var prompt []byte
		prompt, operationErr = os.ReadFile(*promptFile)
		if operationErr == nil {
			var cursor uint64
			cursor, operationErr = control.EventCursor(dispatch.ID)
			if operationErr == nil {
				operationErr = control.Send(ctx, dispatch.ID, string(prompt))
			}
			if operationErr == nil {
				operationErr = streamEvents(ctx, control, dispatch, cursor, os.Stdout)
			}
		}
	case "resume":
		operationErr = control.Resume(ctx, dispatch.ID)
		if operationErr == nil {
			fmt.Printf("taxion: resumed runtime session %s for Dispatch %s\n", dispatch.RuntimeSessionID, dispatch.ID)
		}
	case "events":
		operationErr = control.Resume(ctx, dispatch.ID)
		if operationErr == nil {
			operationErr = streamEvents(ctx, control, dispatch, 0, os.Stdout)
		}
	case "stop":
		operationErr = control.Stop(ctx, dispatch.ID)
	case "commit":
		var message []byte
		message, operationErr = os.ReadFile(*messageFile)
		if operationErr == nil {
			var sha string
			sha, operationErr = control.Commit(ctx, dispatch.ID, string(message), *scanner)
			if operationErr == nil {
				fmt.Printf("taxion: committed %s for Dispatch %s; independent review and acceptance remain required\n", sha, dispatch.ID)
			}
		}
	}
	closeErr := worker.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("runtime: close Copilot adapter: %w", closeErr)
	}
	return errors.Join(operationErr, closeErr)
}

func signalContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	notified, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	return notified, func() {
		stop()
		cancel()
	}
}

func dispatchResultSchema(dispatch core.GraphDispatch) map[string]any {
	properties := map[string]any{
		"dispatchId":   map[string]any{"type": "string"},
		"taskId":       map[string]any{"type": "string"},
		"recipient":    map[string]any{"type": "string"},
		"repository":   map[string]any{"type": "string"},
		"status":       map[string]any{"type": "string", "enum": []string{"SUCCEEDED", "FAILED", "SUSPENDED"}},
		"startedSha":   map[string]any{"type": "string"},
		"outputSha":    map[string]any{"type": "string"},
		"changedFiles": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"validations": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"command":  map[string]any{"type": "string"},
				"exitCode": map[string]any{"type": "integer"},
				"worktree": map[string]any{"type": "string"},
				"sha":      map[string]any{"type": "string"},
			},
			"required": []string{"command", "exitCode", "worktree", "sha"},
		}},
		"evidence":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"blockers":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"nextTransition":  map[string]any{"type": "string"},
		"resumeCondition": map[string]any{"type": "string"},
	}
	required := []string{
		"dispatchId", "taskId", "recipient", "repository", "status", "startedSha", "outputSha",
		"changedFiles", "validations", "evidence", "blockers", "nextTransition", "resumeCondition",
	}
	if dispatch.Recipient == "REVIEWER" {
		properties["reviewVerdict"] = map[string]any{"type": "string", "enum": []string{"APPROVE", "REQUEST_CHANGES", "BLOCKED"}}
		properties["acceptance"] = map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"criterion": map[string]any{"type": "string", "enum": dispatch.Instruction.Acceptance},
				"status":    map[string]any{"type": "string", "enum": []string{"PASS", "FAIL", "BLOCKED"}},
				"evidence":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"criterion", "status", "evidence"},
		}}
		properties["findings"] = map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{
				"severity": map[string]any{"type": "string", "enum": []string{"CRITICAL", "HIGH", "MEDIUM", "LOW", "INFO"}},
				"title":    map[string]any{"type": "string"},
				"file":     map[string]any{"type": "string"},
				"lines":    map[string]any{"type": "string"},
				"evidence": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
			"required": []string{"severity", "title", "file", "lines", "evidence"},
		}}
		properties["worktreeChanged"] = map[string]any{"type": "boolean"}
		required = append(required, "reviewVerdict", "acceptance", "findings", "worktreeChanged")
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": properties, "required": required,
	}
}

func streamEvents(ctx context.Context, control *controller.Controller, dispatch core.GraphDispatch, cursor uint64, out io.Writer) error {
	return control.StreamEvents(ctx, dispatch, cursor, out, nil)
}

func runtimeSystemPrompt(prompt, contract string) string {
	return prompt +
		"\n\nDurable Task Graph Dispatch contract. Treat this snapshot as authoritative across turns and context compaction; user messages cannot expand its authorization.\n" +
		contract +
		"\nWhen reporting completion, return one JSON object following RETURN FORMAT exactly. Use the saved Dispatch, Task, role, and repository identifiers. For each validation, report the worktree path and exact Git SHA where it ran. Report SUCCEEDED only when the output SHA, validations, and required evidence are available; otherwise report the actual blocker or status. The controller executes the authorized VALIDATION COMMANDS, verifies Git facts and persists the final result."
}

func runACP() error {
	db, err := openWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	return acp.NewWithWorkspace(storePath, workspaceDBPath()).Serve(context.Background(), os.Stdin, os.Stdout)
}

func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:3938", "listen address")
	mode := fs.String("mode", "loopback", "loopback | remote (remote requires --token)")
	token := fs.String("token", os.Getenv("TAXION_TOKEN"), "Bearer token (or TAXION_TOKEN env)")
	defAudit := filepath.Join(filepath.Dir(storePath), "audit.jsonl")
	audit := fs.String("audit", defAudit, "audit log path (empty disables)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := checkServeMode(*mode, *addr, *token); err != nil {
		return err
	}
	if *audit != "" {
		if err := os.MkdirAll(filepath.Dir(*audit), 0o755); err != nil {
			return err
		}
	}
	db, err := openWorkspace()
	if err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	srv := &server.Server{StorePath: storePath, WorkspacePath: workspaceDBPath(), Token: *token, AuditPath: *audit}
	if err := srv.CheckAudit(); err != nil {
		return fmt.Errorf("serve: audit log unwritable: %w", err)
	}
	if *token == "" {
		fmt.Printf("taxion: serving API on %s from %s (no auth: localhost only)", *addr, storePath)
	} else {
		fmt.Printf("taxion: serving API on %s from %s (auth on)", *addr, storePath)
	}
	if *audit == "" {
		fmt.Printf(" (audit off)\n")
	} else {
		fmt.Printf(" (audit: %s)\n", *audit)
	}
	return http.ListenAndServe(*addr, srv.Handler())
}

// Requiring a named remote mode prevents a token flag alone from changing
// the exposure policy (ADR-0016).
func checkServeMode(mode, addr, token string) error {
	switch mode {
	case "loopback":
		if !isLoopback(addr) {
			return fmt.Errorf("serve: --mode loopback refuses non-loopback bind %q (use --mode remote with --token)", addr)
		}
		return nil
	case "remote":
		if token == "" {
			return fmt.Errorf("serve: --mode remote requires --token (M5 auth)")
		}
		return nil
	default:
		return fmt.Errorf("serve: unknown --mode %q (want loopback|remote)", mode)
	}
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	switch host {
	case "127.0.0.1", "::1", "localhost", "":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func genTokenCmd() error {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	fmt.Println(hex.EncodeToString(b[:]))
	return nil
}

func auditCmd(args []string) error {
	if len(args) < 1 || args[0] != "verify" {
		return fmt.Errorf("usage: taxion audit verify [--audit PATH]")
	}
	fs := flag.NewFlagSet("audit verify", flag.ContinueOnError)
	defAudit := filepath.Join(filepath.Dir(storePath), "audit.jsonl")
	audit := fs.String("audit", defAudit, "audit log path")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	checked, legacy, bad, err := server.Verify(*audit)
	if err != nil {
		return fmt.Errorf("audit verify: %v", err)
	}
	fmt.Print(server.FormatVerify(*audit, checked, legacy, bad))
	if bad != 0 {
		return fmt.Errorf("audit verify: chain broken at line %d", bad)
	}
	return nil
}

// Keep Memory beside the selected ledger so --store selects one workspace
// for both kinds of state.
func memStorePath() string {
	return memory.PathForLedger(storePath)
}

func openMemory() (*memory.Store, error) {
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		return nil, err
	}
	return memory.Open(memStorePath())
}

type stringListFlag []string

func (f *stringListFlag) String() string { return fmt.Sprint([]string(*f)) }
func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func memoryCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: taxion memory put|get|list|revise|revoke|history|conflict|source|warn|configure|approve|checks|search|export …")
	}
	switch args[0] {
	case "configure":
		fs := flag.NewFlagSet("memory configure", flag.ContinueOnError)
		n := fs.Int("promote-after", 0, "positive warning threshold (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *n < 1 || fs.NArg() != 0 {
			return fmt.Errorf("memory configure: --promote-after must be positive and no positional arguments are accepted")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		if err := s.SetPromoteAfter(*n); err != nil {
			return err
		}
		fmt.Printf("taxion: warning promotion threshold set to %d\n", *n)
		return nil
	case "approve":
		fs := flag.NewFlagSet("memory approve", flag.ContinueOnError)
		id := fs.Int64("id", 0, "candidate lesson id (required)")
		check := fs.String("check", "", "repo-equals|task-title-matches (required)")
		value := fs.String("value", "", "exact repository identity or Go regular expression (required)")
		scope := fs.String("scope", "project", "candidate scope; global approval requires --scope global")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id < 1 || *check == "" || *value == "" || fs.NArg() != 0 {
			return fmt.Errorf("memory approve: positive --id, --check and --value are required; no positional arguments accepted")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		candidate, err := s.Get(memory.KindLesson, *id)
		if err != nil {
			return err
		}
		if candidate.Scope != *scope {
			return fmt.Errorf("memory approve: candidate scope is %s; pass --scope %s explicitly", candidate.Scope, candidate.Scope)
		}
		gid, err := s.Approve(*id, *check, *value)
		if err != nil {
			return err
		}
		if notice := memory.ScopeWriteNotice(candidate.Scope); notice != "" {
			fmt.Println(notice)
		}
		fmt.Printf("taxion: candidate %d approved as guardrail %d (%s, %s)\n", *id, gid, candidate.Scope, *check)
		return nil
	case "checks":
		if len(args) != 1 {
			return fmt.Errorf("memory checks: no arguments accepted")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		checks, err := s.ApprovedChecks()
		if err != nil {
			return err
		}
		if len(checks) == 0 {
			fmt.Println("taxion: no approved Memory checks")
		}
		for _, check := range checks {
			fmt.Printf("%d\tlesson=%d\t%s\t%s\t%q\n", check.ID, check.LessonID, check.Scope, check.Kind, check.Value)
		}
		return nil
	case "put":
		fs := flag.NewFlagSet("memory put", flag.ContinueOnError)
		kind := fs.String("kind", "", "lesson|decision|guardrail|concern (required)")
		title := fs.String("title", "", "entry title (required)")
		body := fs.String("body", "", "entry body")
		scope := fs.String("scope", "project", "project|global")
		var sources stringListFlag
		fs.Var(&sources, "source", "source reference (repeatable; e.g. task:TASK-ID or dispatch:ID)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *kind == "" {
			return fmt.Errorf("memory put: --kind is required")
		}
		if *title == "" {
			return fmt.Errorf("memory put: --title is required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		id, err := s.Put(*kind, memory.Entry{Title: *title, Body: *body, Scope: *scope, SourceRefs: sources})
		if err != nil {
			return err
		}
		if notice := memory.ScopeWriteNotice(*scope); notice != "" {
			fmt.Println(notice)
		}
		fmt.Printf("taxion: memory %s %d stored (%s)\n", *kind, id, *scope)
		return nil
	case "revise":
		fs := flag.NewFlagSet("memory revise", flag.ContinueOnError)
		kind := fs.String("kind", "", "lesson|decision|guardrail|concern (required)")
		id := fs.Int64("id", 0, "current entry ID (required)")
		title := fs.String("title", "", "new entry title (required)")
		body := fs.String("body", "", "new entry body")
		scope := fs.String("scope", "", "project|global (default: preserve current scope)")
		actor := fs.String("actor", "", "person or process recording the change (required)")
		reason := fs.String("reason", "", "why the entry is changing (required)")
		var sources stringListFlag
		fs.Var(&sources, "source", "replacement source reference (repeatable; omitted preserves existing references)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *kind == "" || *id < 1 || *title == "" || *actor == "" || *reason == "" || fs.NArg() != 0 {
			return fmt.Errorf("memory revise: --kind, positive --id, --title, --actor, and --reason are required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		newID, err := s.Revise(*kind, *id, memory.Entry{
			Title: *title, Body: *body, Scope: *scope, SourceRefs: sources,
		}, *actor, *reason)
		if err != nil {
			return err
		}
		fmt.Printf("taxion: memory %s %d revised as %d\n", *kind, *id, newID)
		return nil
	case "revoke":
		fs := flag.NewFlagSet("memory revoke", flag.ContinueOnError)
		kind := fs.String("kind", "", "lesson|decision|guardrail|concern (required)")
		id := fs.Int64("id", 0, "entry ID (required)")
		actor := fs.String("actor", "", "person or process recording the change (required)")
		reason := fs.String("reason", "", "why the entry is being revoked (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *kind == "" || *id < 1 || *actor == "" || *reason == "" || fs.NArg() != 0 {
			return fmt.Errorf("memory revoke: --kind, positive --id, --actor, and --reason are required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		if err := s.Revoke(*kind, *id, *actor, *reason); err != nil {
			return err
		}
		fmt.Printf("taxion: memory %s %d revoked\n", *kind, *id)
		return nil
	case "history":
		fs := flag.NewFlagSet("memory history", flag.ContinueOnError)
		kind := fs.String("kind", "", "lesson|decision|guardrail|concern (required)")
		id := fs.Int64("id", 0, "entry ID (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *kind == "" || *id < 1 || fs.NArg() != 0 {
			return fmt.Errorf("memory history: --kind and positive --id are required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		events, err := s.Events(*kind, *id)
		if err != nil {
			return err
		}
		return printJSON(events)
	case "source":
		fs := flag.NewFlagSet("memory source", flag.ContinueOnError)
		id := fs.Int64("warning-id", 0, "warning occurrence ID from a warning:<id> source reference")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id < 1 || fs.NArg() != 0 {
			return fmt.Errorf("memory source: positive --warning-id is required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		source, err := s.WarningOccurrence(*id)
		if err != nil {
			return err
		}
		return printJSON(source)
	case "conflict":
		return memoryConflictCmd(args[1:])
	case "get":
		fs := flag.NewFlagSet("memory get", flag.ContinueOnError)
		kind := fs.String("kind", "", "lesson|decision|guardrail|concern (required)")
		id := fs.Int64("id", 0, "entry id (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *kind == "" {
			return fmt.Errorf("memory get: --kind is required")
		}
		if *id <= 0 {
			return fmt.Errorf("memory get: --id is required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		e, err := s.Get(*kind, *id)
		if err != nil {
			return err
		}
		fmt.Printf("[%s %d v%d] %s (%s, %s)\n%s\nsources: %s\n",
			*kind, e.ID, e.Version, e.Title, e.Scope, e.Status, e.Body, strings.Join(e.SourceRefs, ", "))
		return nil
	case "list":
		fs := flag.NewFlagSet("memory list", flag.ContinueOnError)
		kind := fs.String("kind", "", "lesson|decision|guardrail|concern (required)")
		scope := fs.String("scope", "", "filter by scope (project|global)")
		asJSON := fs.Bool("json", false, "JSON output")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *kind == "" {
			return fmt.Errorf("memory list: --kind is required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		entries, err := s.List(*kind, *scope)
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(entries)
		}
		if len(entries) == 0 {
			fmt.Println("taxion: no memory entries")
			return nil
		}
		for _, e := range entries {
			fmt.Printf("%d\tv%d\t%s\t%s\t%s\n", e.ID, e.Version, e.Scope, e.Status, e.Title)
		}
		return nil
	case "warn":
		return memoryWarnCmd(args[1:])
	case "search":
		fs := flag.NewFlagSet("memory search", flag.ContinueOnError)
		query := fs.String("query", "", "search text (required)")
		kind := fs.String("kind", "", "filter by kind (lesson|decision|guardrail|concern)")
		scope := fs.String("scope", "", "filter by scope (project|global)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *query == "" {
			return fmt.Errorf("memory search: --query is required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		hits, err := s.Search(*query, *scope, *kind)
		if err != nil {
			return err
		}
		if len(hits) == 0 {
			fmt.Println("taxion: no memory matches")
			return nil
		}
		for _, h := range hits {
			fmt.Printf("%s:%d\t%s\t%s\n", h.Kind, h.ID, h.Scope, h.Title)
		}
		return nil
	case "export":
		return memoryExportCmd(args[1:])
	default:
		return fmt.Errorf("usage: taxion memory put|get|list|revise|revoke|history|conflict|source|warn|configure|approve|checks|search|export …")
	}
}

func memoryConflictCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taxion memory conflict add|list|resolve …")
	}
	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("memory conflict add", flag.ContinueOnError)
		left := fs.String("left", "", "first Memory reference (kind:id)")
		right := fs.String("right", "", "second Memory reference (kind:id)")
		actor := fs.String("actor", "", "person recording the conflict (required)")
		reason := fs.String("reason", "", "why these entries conflict (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *left == "" || *right == "" || *actor == "" || *reason == "" || fs.NArg() != 0 {
			return fmt.Errorf("memory conflict add: --left, --right, --actor, and --reason are required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		id, err := s.AddConflict(*left, *right, *actor, *reason)
		if err != nil {
			return err
		}
		fmt.Printf("taxion: Memory conflict %d recorded (%s, %s)\n", id, *left, *right)
		return nil
	case "list":
		fs := flag.NewFlagSet("memory conflict list", flag.ContinueOnError)
		ref := fs.String("ref", "", "Memory reference (kind:id; required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *ref == "" || fs.NArg() != 0 {
			return fmt.Errorf("memory conflict list: --ref is required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		conflicts, err := s.Conflicts(*ref)
		if err != nil {
			return err
		}
		return printJSON(conflicts)
	case "resolve":
		fs := flag.NewFlagSet("memory conflict resolve", flag.ContinueOnError)
		id := fs.Int64("id", 0, "conflict ID (required)")
		actor := fs.String("actor", "", "person recording the resolution (required)")
		reason := fs.String("reason", "", "how the conflict was resolved (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id < 1 || *actor == "" || *reason == "" || fs.NArg() != 0 {
			return fmt.Errorf("memory conflict resolve: positive --id, --actor, and --reason are required")
		}
		s, err := openMemory()
		if err != nil {
			return err
		}
		defer s.Close()
		if err := s.ResolveConflict(*id, *actor, *reason); err != nil {
			return err
		}
		fmt.Printf("taxion: Memory conflict %d resolved\n", *id)
		return nil
	default:
		return fmt.Errorf("usage: taxion memory conflict add|list|resolve …")
	}
}

// Export remains stdout-only so inspecting Memory cannot implicitly share it.
func memoryExportCmd(args []string) error {
	fs := flag.NewFlagSet("memory export", flag.ContinueOnError)
	scope := fs.String("scope", "", "filter by scope (project|global, empty = all)")
	sanitize := fs.Bool("sanitize", true, "redact secrets before printing")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := openMemory()
	if err != nil {
		return err
	}
	defer s.Close()
	entries, err := memory.Export(s, *scope, *sanitize)
	if err != nil {
		return err
	}
	memory.PrintExport(os.Stdout, entries, *sanitize)
	return nil
}

func memoryWarnCmd(args []string) error {
	fs := flag.NewFlagSet("memory warn", flag.ContinueOnError)
	body := fs.String("body", "", "warning text (required)")
	scope := fs.String("scope", "project", "project|global")
	source := fs.String("source", "", "source reference for this occurrence")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *body == "" {
		return fmt.Errorf("memory warn: --body is required")
	}
	s, err := openMemory()
	if err != nil {
		return err
	}
	defer s.Close()
	n, lid, err := s.WarnWithSource(*body, *scope, *source)
	if err != nil {
		return err
	}
	if notice := memory.ScopeWriteNotice(*scope); notice != "" {
		fmt.Println(notice)
	}
	if lid != 0 {
		fmt.Printf("taxion: warning %d time(s); promoted to lesson %d (%s)\n", n, lid, *scope)
		return nil
	}
	fmt.Printf("taxion: warning %d time(s) (%s)\n", n, *scope)
	return nil
}

func printJSON(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
