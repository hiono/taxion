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
	executionconfig "github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/server"
	"github.com/hiono/taxion/internal/store"
)

const usage = `taxion — Run approved coding tasks with saved results and independent review

Usage:
  taxion [--store PATH] <command> [options]

Workspace: defaults to .taxion/ under the Git worktree root (cwd outside Git).
--store selects a legacy ledger path for explicit overrides, not workspace.db.
--version prints the installed build identity without reading configuration.

Commands:
Setup and discovery:
  init                Prepare Project configuration, Git protection and workspace ([--set FIELD=VALUE], [--remote NAME], [--dry-run], [--yes])
  doctor              Diagnose setup; with valid settings, inspect the selected backend's native paths and customizations ([--json])
  doctor fix          Preview and explicitly repair missing Project settings and Git protection ([--set FIELD=VALUE], [--remote NAME], [--dry-run], [--yes])
  completion bash     Print offline bash command and option completion
  ready               Print coding-agent usage instructions; optional --install-skill / --update-skill / --check-skill
  config check        Validate fixed Global/Project YAML and environment references ([--sources], [--capabilities], [--task], [--dispatch])
  provenance configure  Bind an explicit development/product store policy (--file POLICY.json; never overwrites)
  provenance verify   Verify the operation mode and pinned release artifact without changing state

Requests, planning and Task contracts:
  request             Refine a natural-language request with Planner, then approve coding/review without JSON or prompt files (--repo, --timeout, --max-ai-credits)
  task create         Create a task (--title and --repo required)
  task list           List unarchived tasks ([--repo] filter, [--json], [--include-archived])
  task archive        Remove stopped tasks and associated work from normal views, keeping history (--tasks, --actor, --source, --reason; --dry-run then --expected-hash)
  task track          Register/update a durable Task Graph contract (--file JSON)
  task transition     Validate and record a Task Graph transition (--file JSON)
  task graph          Print the versioned Task Graph and history
  task validate       Check a Task Graph request without storing it (--manifest JSON; all violations, canonical hash)
  task authorize      Validate and bundle a Task; fill a fresh READY bundle without changing its contract (--manifest JSON)
  task authorize-packets  Preview/confirm a READY Task sequential packet lifecycle (--request JSON)
  task plan           Rebuild a follow-up plan from durable contracts and evidence (--repo required; --include-archived for history)
  gate <name>         Report the contract, resume, execution or publication gate without changing state (--task/--flow, --json)
  workflow repair-status Report whether the next repair needs Human action, read-only (--id required)

Dispatch and configuration binding:
  dispatch            Dispatch a task (--task and --repo required; revised Dispatches need an explicit relationship)
  dispatch transition Record a Dispatch lifecycle transition (--id, --status, --reason)
  dispatch list       List unarchived tasks' dispatches ([--repo] filter, [--json], [--include-archived])
  config bind         Save an approved fixed-file configuration selection for an unbound Dispatch

Conversation, investigation and source attachment:
  conversation watch  Hold handoffs before reading Human input; select one role step or bounded run (--flow, --actor, --source)
  conversation ask    Answer a bounded side question from saved Task records without changing the main workflow
  conversation plan   Recommend a next action from compact Memory and results; no execution or inferred approval
  conversation intake Record an original request and unapproved scope/acceptance contract (--file JSON)
  conversation proposal Read a saved proposed contract and its exact hash (--task ID)
  conversation confirm Explicitly approve that exact contract/configuration without issuing work (--file JSON)
  conversation investigate Read bounded scoped committed files for an isolated question with explicit ceilings
  conversation search Search and attach sourced excerpts to an editable Task (--scope, --task, --question)

Runtime sessions and ACP:
  runtime auth        Check the effective Copilot account against approved Dispatch configuration without creating a session
  runtime start       Start the Copilot session bound to an active Dispatch and send its authorized instruction
  runtime send        Send a follow-up prompt to the Dispatch session (--prompt-file required)
  runtime resume      Resume the Dispatch session
  runtime events      Stream events from the Dispatch session
  runtime stop        Cancel the saved Dispatch session without execution prompts or tools
  runtime commit      Publish scanned staged changes for the bound running Coder (--message-file, --scanner)
  runtime <action> --managed-commit  Opt into the bound Coder SDK commit tool; use the same option and scanner on resume
  runtime <action> --managed-stage   Also enable scoped file staging; requires --managed-commit
  acp                 Serve approved Dispatches over ACP (--coder-system-prompt-file, --max-ai-credits)

Review flows and recovery:
  workflow start      Authorize a bounded review flow and issue its Coder Dispatch (--file JSON)
  workflow terminate  Cancel an untouched Dispatch and hold it for a new approved contract (--file JSON)
  workflow advance    Issue review/repair from the latest verified result (--id required)
  workflow run        Execute review/repair continuously with saved ceilings (--id; role prompt files or --builtin-prompts)
  workflow step       Execute one role turn from a saved review flow (--id; role prompt files or --builtin-prompts)
  workflow show       Read the saved review flow and ceilings (--id required)
  workflow evaluate   Report saved quality gates and measured/unknown costs; no reward score (--id required)
  workflow resume     Verify the saved unstarted Coder boundary, then run within its original contract (--id required)
  workflow lanes      Run read-only controller lanes for one Task and report a machine-readable gate
  workflow recover    Release interrupted runner ownership after recorded execution stop (--file JSON)
  workflow interrupt Request a targeted stop for the active flow (--file JSON)
  workflow handoff    Hold or release the next handoff with a sourced compare-and-swap request (--file JSON)
  workflow feedback-target Read the latest saved instruction/result target without changing execution (--id required)
  workflow feedback   Record target-bound Human feedback and hold the next handoff (--file JSON)
  workflow feedback-resolve Record an explicit KEEP_CONTRACT disposition before handoff release (--file JSON)
  workflow draft      Save or revise a versioned, unissued instruction proposal while held (--file JSON)
  workflow draft-discard Explicitly discard the latest proposal without applying it (--file JSON)
  workflow draft-approve Approve the latest supplement within the unchanged flow contract (--file JSON)
  workflow planning-context Read bounded Planner input from saved Task records (--id required)
  workflow planner-draft Record a selected planning proposal (--file JSON)
  workflow planner-action Record selected non-execution advice (--file JSON)
  workflow question-draft Record selected findings from a bounded side question (--file JSON)
  review              Bind coder-output evidence (--task, --sha and --repo required)
  resume              Print a resume acknowledgement (local stub; --session required)

Evidence and external records:
  role-packet create  Emit an immutable reference-only role packet (explicit task, tree and contract digests)
  role-packet verify  Verify and read back a role packet (--file, --expected-hash); no execution
  role-packet read    Read and verify a persisted canonical packet (--expected-hash)
  role-packet list    Read lifecycle authorizations and packet audit references (--task)
  records proposal    Display exact external publication content and its approval hash (--file JSON)
  records approve     Save an explicitly approved Issue/PR publication after local secret checks (--file JSON)
  records send        Deliver one approved publication with a durable sender claim (--id, --scanner)
  records cancel      Invalidate one exact unsent publication intent with sourced authorization (--file JSON)
  records reconcile   Reconcile an uncertain stopped sender against remote records (--id, --claim, --sender-stopped)
  records show        Show saved runtime receipts and external publication status
  records finding     Record cause, correction, lesson candidate and Human decision for an exact result (--file JSON)

Git publication protection:
  git check-index     Reject local Taxion and dotenv paths in the actual commit index
  git check-range     Scan every unpublished commit tree and message (--base SHA, --head SHA, --scanner PATH)
  git hook            Install (--scanner PATH, or --paths-only), inspect, or remove the Taxion pre-commit hook

Workspace portability:
  workspace snapshot  Collect stopped workspace, Memory and required Git objects into one private SQLite file (--output)
  workspace inspect-snapshot  Verify portable contents without contacting a provider (--file)
  workspace restore   Restore an exact approved snapshot into a fresh directory (--file, --output, --expected-hash, --actor, --source)

HTTP service and audit:
  serve               Serve HTTP API over the ledger ([--addr], [--mode], [--token]/TAXION_TOKEN, [--audit])
  gen-token           Print a random Bearer token for serve (M5 auth)
  audit verify        Verify the audit log hash chain ([--audit])

Memory and reusable decisions:
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
  memory snapshot     Create a complete private SQLite snapshot (--output)
  memory inspect-snapshot  Review row counts and sanitizer findings (--file)
  memory restore      Install an exact approved snapshot into a fresh local destination

Specification sources and tools:
  specification hash  Hash a sourced specification for separate use approvals (--file)
  specification scope-hash Hash an exact public/synthetic local corpus (--scope)
  specification search Search approved Office documents or selected private chat messages; print metadata only (--scope, --query)
  specification serve Serve the approved local search corpus over read-only MCP stdio (--scope)
  tools hash          Pin exact local extension files or directory contents (--path)

Identity gate (REP-001): task create, dispatch and review require an explicit --repo.
The target is never inferred from the working directory.

Bash completion: source <(taxion completion bash)
Add that line to ~/.bashrc for persistence, then open a new shell or run source ~/.bashrc.
`

var storePath = ".taxion/ledger.json"

func main() {
	args := os.Args[1:]
	explicitStore := false
	if len(args) >= 2 && args[0] == "--store" {
		storePath = args[1]
		explicitStore = true
		args = args[2:]
	}
	if len(args) < 1 {
		_ = printHelp(os.Stderr)
		os.Exit(2)
	}
	var err error
	shortHelp := provenanceHelpOnly(args)
	if !explicitStore && !shortHelp &&
		!slices.Contains([]string{"ready", "completion", "-h", "--help", "help", "--version", "gen-token", "git"}, args[0]) {
		storePath, err = defaultWorkspaceStorePath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "taxion: %v\n", err)
			os.Exit(1)
		}
	}
	if !shortHelp && !slices.Contains([]string{"--version", "help", "-h", "--help", "provenance", "completion", "gen-token"}, args[0]) &&
		args[0] != "git" && !(args[0] == "doctor" && (len(args) == 1 || args[1] != "fix")) &&
		!(args[0] == "ready" && len(args) == 1) {
		if _, err := verifyOperationProvenance(); err != nil {
			_ = provenanceDiagnostic(err)
			fmt.Fprintln(os.Stderr, "taxion: operation blocked by provenance policy")
			os.Exit(1)
		}
	}
	switch args[0] {
	case "provenance":
		err = provenanceCmd(args[1:])
	case "ready":
		err = readyCmd(args[1:], os.Stdout)
	case "request":
		err = requestCmd(args[1:], os.Stdin, os.Stdout)
	case "config":
		err = configCmd(args[1:])
	case "doctor":
		err = doctorCmd(args[1:], os.Stdout)
	case "git":
		err = checkGitIndex(args[1:], os.Stdout)
	case "init":
		err = setupCmd(args[1:], os.Stdin, os.Stdout, terminalInput(os.Stdin), true)
	case "completion":
		err = completionCmd(args[1:], os.Stdout)
	case "workspace":
		err = workspaceMigrationCmd(args[1:])
	case "task":
		err = taskCmd(args[1:])
	case "dispatch":
		err = dispatchCmd(args[1:])
	case "runtime":
		err = runtimeCmd(args[1:])
	case "workflow":
		err = workflowCmd(args[1:])
	case "gate":
		err = gateCmd(args[1:])
	case "role-packet":
		err = rolePacketCommand(args[1:], os.Stdout)
	case "records":
		err = recordsCmd(args[1:])
	case "conversation":
		err = conversationCmd(args[1:], os.Stdout)
	case "review":
		err = reviewCmd(args[1:])
	case "resume":
		err = resumeCmd(args[1:])
	case "acp":
		err = runACP(args[1:])
	case "serve":
		err = serveCmd(args[1:])
	case "gen-token":
		err = genTokenCmd()
	case "audit":
		err = auditCmd(args[1:])
	case "memory":
		err = memoryCmd(args[1:])
	case "specification":
		err = specificationCmd(args[1:])
	case "tools":
		err = toolsCmd(args[1:])
	case "-h", "--help", "help":
		err = printHelp(os.Stdout)
	case "--version":
		if len(args) != 1 {
			err = fmt.Errorf("--version accepts no additional arguments")
		} else {
			_, err = fmt.Fprintf(os.Stdout, "taxion %s\n", currentBuildVersion())
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
		_ = printHelp(os.Stderr)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "taxion: %v\n", err)
		var exit interface{ ExitCode() int }
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
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
	if err := validateWorkspaceStorePath(); err != nil {
		return err
	}
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
		return fmt.Errorf("usage: taxion task create|list|archive|track|transition|graph|plan|validate|authorize|reauthorize|authorize-packets …")
	}
	switch args[0] {
	case "archive":
		return taskArchiveCmd(args[1:])
	case "reauthorize":
		return taskReauthorizeCmd(args[1:])
	case "authorize-packets":
		return taskAuthorizePacketsCmd(args[1:])
	case "track", "transition", "graph", "plan", "validate", "authorize":
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
		includeArchived := fs.Bool("include-archived", false, "include archived Tasks")
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
			if (*repo == "" || t.Repo == *repo) && (*includeArchived || !state.Graph.IsArchived(t.ID)) {
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
		return fmt.Errorf("usage: taxion task create|list|archive|track|transition|graph|plan …")
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
		includeArchived := fs.Bool("include-archived", false, "include Dispatches for archived Tasks")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		db, state, err := loadWorkspace()
		if err != nil {
			return err
		}
		defer db.Close()
		l := state.Ledger
		var ds []core.Dispatch
		for _, d := range l.Dispatches {
			if (*repo == "" || d.Repo == *repo) && (*includeArchived || !state.Graph.IsArchived(d.TaskID)) {
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
			instruction, err = controller.RenderWorkspaceDispatchContext(*state, dispatch, false)
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
		return fmt.Errorf("usage: taxion runtime {auth|start|send|resume|events|stop|commit} --dispatch ID [--system-prompt-file PATH; not used for stop]")
	}
	action := args[0]
	switch action {
	case "auth", "start", "send", "resume", "events", "stop", "commit":
	default:
		return fmt.Errorf("runtime: unknown action %q", action)
	}
	fs := flag.NewFlagSet("runtime "+action, flag.ContinueOnError)
	dispatchID := fs.String("dispatch", "", "active Dispatch ID")
	systemPromptFile := fs.String("system-prompt-file", "", "role-specific system prompt")
	cliPath := fs.String("cli", "", "Copilot CLI path (default: PATH)")
	baseDirectory := fs.String("base-directory", "", "Copilot state directory (default: ~/.copilot)")
	maxAICredits := fs.Float64("max-ai-credits", 0, "per-session soft AI-credit ceiling (zero: unset; minimum: 30)")
	reviewSource := fs.String("review-source-worktree", "", "repository worktree used to create the independent Reviewer worktree")
	tools := fs.String("tools", "", "comma-separated SDK tool allowlist (default: none)")
	promptFile := fs.String("prompt-file", "", "follow-up instruction file (send only)")
	budgetGuidance := fs.String("budget-guidance", "", "approved per-send guidance mode: off, flow or account (start/send only)")
	messageFile := fs.String("message-file", "", "Conventional/Contextual commit message file (commit only)")
	scanner := fs.String("scanner", "betterleaks", "trusted Betterleaks 1.9.0 executable (commit or --managed-commit)")
	managedCommit := fs.Bool("managed-commit", false, "enable the bound Coder's taxion_commit SDK tool")
	managedStage := fs.Bool("managed-stage", false, "enable taxion_stage alongside --managed-commit")
	strictResult := fs.Bool("strict-result-identities", false, "constrain final-result identities and start SHA to the saved Dispatch; repeat on session attachment")
	compactContext := fs.Bool("compact-context", false, "include exact prior results without nested instructions; repeat on session attachment")
	validationSubmission := fs.Bool("controller-validation-submission", false, "separate model submission from controller-run checks; repeat on session attachment")
	reviewProgress := fs.Bool("review-progress", false, "enable bounded Reviewer progress receipts (Copilot only); repeat on session attachment")
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum time to wait for a turn or event stream")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := copilot.ValidateAICreditLimit(*maxAICredits); err != nil {
		return err
	}
	if *dispatchID == "" {
		return fmt.Errorf("runtime %s: --dispatch is required", action)
	}
	if action != "stop" && *systemPromptFile == "" {
		return fmt.Errorf("runtime %s: --dispatch and --system-prompt-file are required", action)
	}
	if *timeout <= 0 {
		return fmt.Errorf("runtime %s: --timeout must be positive", action)
	}
	if *budgetGuidance != "" && action != "start" && action != "send" {
		return fmt.Errorf("runtime: --budget-guidance applies only to start/send")
	}
	if *budgetGuidance != "" && *budgetGuidance != "off" && *budgetGuidance != "flow" && *budgetGuidance != "account" {
		return fmt.Errorf("runtime: --budget-guidance requires off, flow or account")
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
	if action != "commit" && !*managedCommit && scannerSpecified {
		return fmt.Errorf("runtime: --scanner applies only to commit or --managed-commit")
	}
	if *managedStage && !*managedCommit {
		return fmt.Errorf("runtime: --managed-stage requires --managed-commit")
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
	if *budgetGuidance != "" && (dispatch.Instruction.BudgetGuidance == nil ||
		!slices.Contains(dispatch.Instruction.BudgetGuidance.AllowedModes, *budgetGuidance)) {
		return fmt.Errorf("runtime: --budget-guidance is outside the saved Dispatch approval")
	}
	if action == "stop" {
		if dispatch.Configuration != nil {
			var selected executionconfig.Config
			if err := json.Unmarshal(dispatch.Configuration.Selection, &selected); err != nil {
				return fmt.Errorf("runtime stop: invalid saved configuration")
			}
			if selected.Runtime.Provider == "opencode" {
				return fmt.Errorf("runtime stop: an isolated OpenCode session cannot attach across processes; stop through its owning runner, then verify exit and authorize a successor Task; do not reuse the session ID")
			}
		}
		var invalidOption string
		fs.Visit(func(option *flag.Flag) {
			switch option.Name {
			case "dispatch", "cli", "base-directory", "timeout":
			default:
				invalidOption = option.Name
			}
		})
		if invalidOption != "" {
			return fmt.Errorf("runtime stop: --%s is an execution option; stop uses only the saved session binding, --cli, --base-directory and --timeout", invalidOption)
		}
		ctx, cancel := signalContext(*timeout)
		defer cancel()
		worker, err := copilot.NewForStop(dispatch, *cliPath, *baseDirectory)
		if err != nil {
			return err
		}
		control, err := controller.New(db, worker)
		if err != nil {
			return errors.Join(err, worker.Close())
		}
		return errors.Join(control.Stop(ctx, dispatch.ID), worker.Close())
	}
	if dispatch.Instruction.Version < core.DispatchInstructionVersion {
		return fmt.Errorf("runtime: Dispatch %q predates the current execution contract; issue a new Dispatch", *dispatchID)
	}
	var expectedAccount string
	if action == "auth" {
		resolved, err := resolveConfiguration()
		if err != nil {
			return err
		}
		if _, err := controller.CheckDispatchConfiguration(db, dispatch.ID, resolved.Config); err != nil {
			return err
		}
		expectedAccount = resolved.Config.Runtime.Account
	}
	if action != "auth" {
		dispatch, err = selectDispatchConfiguration(db, dispatch)
		if err != nil {
			return err
		}
	}
	if *reviewSource != "" && (action != "start" || dispatch.Recipient != "REVIEWER") {
		return fmt.Errorf("runtime: --review-source-worktree applies to Reviewer start")
	}
	systemPrompt, err := os.ReadFile(*systemPromptFile)
	if err != nil {
		return fmt.Errorf("runtime: read system prompt: %w", err)
	}
	availableTools, err := runtimeTools(*tools)
	if err != nil {
		return err
	}
	if err := controller.CheckRolePacket(db, dispatch.ID); err != nil {
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
	dispatchContract, err := controller.RenderDispatchContext(db, dispatch, *compactContext)
	if err != nil {
		return fmt.Errorf("runtime: render Dispatch contract: %w", err)
	}
	ctx, cancel := signalContext(*timeout)
	defer cancel()
	responseSchema := dispatchResultSchema(dispatch)
	if *strictResult {
		responseSchema = strictDispatchResultSchema(dispatch)
	}
	if *validationSubmission {
		responseSchema = controller.BoundDispatchResultSchema(dispatch)
	}
	worker, err := newDispatchWorker(ctx, db, dispatch, copilot.Config{
		Role: dispatch.Recipient, SystemPrompt: runtimeSubmissionPrompt(string(systemPrompt), dispatchContract, *validationSubmission),
		WorkingDirectory: dispatch.Instruction.Worktree, CLIPath: *cliPath,
		BaseDirectory: *baseDirectory, AvailableTools: availableTools,
		MaxAICredits:         *maxAICredits,
		ResponseSchema:       responseSchema,
		EnableReviewProgress: *reviewProgress,
	}, *managedCommit, *managedStage, *scanner)
	if err != nil {
		return err
	}
	control, err := controller.New(db, worker)
	if err != nil {
		_ = worker.Close()
		return err
	}

	var operationErr error
	switch action {
	case "auth":
		operationErr = worker.CheckAuthentication(ctx, expectedAccount)
		if operationErr == nil {
			if runtime.ProviderName(worker) == "opencode" {
				fmt.Println("taxion: approved OpenCode executable and server configuration hashes match. Provider authentication, repository access, entitlement and quota remain unverified and OpenCode-managed. No session was created.")
			} else {
				fmt.Println("taxion: effective Copilot account matches the approved Dispatch configuration on github.com. Explicit credential policies apply; legacy reference resolution, repository actor and contract eligibility remain unverified. No session was created or resumed.")
			}
		}
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
				operationErr = control.SendWithBudgetGuidance(ctx, dispatch.ID,
					"Begin executing the active Dispatch under the durable Task Graph contract in your system instructions.", *budgetGuidance)
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
				operationErr = control.SendWithBudgetGuidance(ctx, dispatch.ID, string(prompt), *budgetGuidance)
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
	return controller.DispatchResultSchema(dispatch, false)
}

func strictDispatchResultSchema(dispatch core.GraphDispatch) map[string]any {
	return controller.DispatchResultSchema(dispatch, true)
}

func streamEvents(ctx context.Context, control *controller.Controller, dispatch core.GraphDispatch, cursor uint64, out io.Writer) error {
	return control.StreamEvents(ctx, dispatch, cursor, out, nil)
}

func runtimeSystemPrompt(prompt, contract string) string {
	return runtimeSubmissionPrompt(prompt, contract, false)
}

func runtimeSubmissionPrompt(prompt, contract string, submission bool) string {
	prompt += "\n" + goalExecutionPolicy + "\n" + goalRolePolicy + "\n" + roleVerificationPrompt
	prompt += "\nResult evidence contains identifiers. Include each required evidence identifier exactly as a separate array item, without a description suffix (for example, \"work-report\", not \"work-report: changes made\"). Additional work descriptions may be separate evidence items. Do not invent evidence merely to satisfy an identifier."
	prompt += "\nFor Reviewer, changedFiles must be []. The reproduced Coder changes are review input, not Reviewer edits. worktreeChanged describes changes since this Reviewer started, not differences from the baseline commit. Report an actual Reviewer mutation as a blocker; do not hide it."
	if !submission {
		return prompt +
			"\n\nDurable Task Graph Dispatch contract. Treat this snapshot as authoritative across turns and context compaction; user messages cannot expand its authorization.\n" +
			contract +
			"\nWhen reporting completion, return one JSON object following RETURN FORMAT exactly. Use the saved Dispatch, Task, role, and repository identifiers. For each validation, report the worktree path and exact Git SHA where it ran. Report SUCCEEDED only when the output SHA, validations, and required evidence are available; otherwise report the actual blocker or status. The controller executes the authorized VALIDATION COMMANDS, verifies Git facts and persists the final result."
	}
	return prompt +
		"\n\nDurable Task Graph Dispatch contract. Treat this snapshot as authoritative across turns and context compaction; user messages cannot expand its authorization.\n" +
		contract +
		"\nWhen reporting completion, return one JSON object following the response schema exactly. Omit fields absent from that schema: in the bound result protocol, the host supplies Dispatch, Task, role, repository and observed Git identifiers from this session, not from model transcription. For validations you actually ran, report their worktree and exact Git SHA; never invent execution. If the authored output and role evidence are ready for controller verification, submit status SUCCEEDED and validations [] for checks the controller has not yet run. Pending controller validation alone is not a reason to report SUSPENDED or BLOCKED. The controller executes the authorized VALIDATION COMMANDS and verifies Git after this submission, before persisting a successful result. A real missing output, failed check, missing role evidence or required Human decision remains a blocker; report it rather than guessing success. A Reviewer must still independently determine its verdict and acceptance from actual evidence."
}

func runACP(args []string) error {
	fs := flag.NewFlagSet("acp", flag.ContinueOnError)
	coderPromptFile := fs.String("coder-system-prompt-file", "", "Coder system prompt file")
	reviewerPromptFile := fs.String("reviewer-system-prompt-file", "", "Reviewer system prompt file (optional)")
	cliPath := fs.String("cli", "", "Copilot CLI path (default: PATH)")
	baseDirectory := fs.String("base-directory", "", "Copilot state directory (default: ~/.copilot)")
	tools := fs.String("tools", "", "comma-separated SDK tool allowlist (default: none)")
	maxAICredits := fs.Float64("max-ai-credits", 0, "required per-session soft AI-credit ceiling (minimum: 30)")
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum time to wait for a turn")
	compactContext := fs.Bool("compact-context", false, "include exact prior results without nested instructions")
	validationSubmission := fs.Bool("controller-validation-submission", false, "separate model submission from controller-run checks")
	scanner := fs.String("scanner", "betterleaks", "trusted Betterleaks 1.9.0 executable for managed commit")
	managedCommit := fs.Bool("managed-commit", false, "enable the bound Coder's taxion_commit SDK tool")
	managedStage := fs.Bool("managed-stage", false, "enable taxion_stage alongside --managed-commit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *coderPromptFile == "" {
		return fmt.Errorf("acp: --coder-system-prompt-file is required")
	}
	if err := copilot.ValidateAICreditLimit(*maxAICredits); err != nil {
		return err
	}
	if *timeout <= 0 {
		return fmt.Errorf("acp: --timeout must be positive")
	}
	var scannerSpecified bool
	fs.Visit(func(option *flag.Flag) {
		if option.Name == "scanner" {
			scannerSpecified = true
		}
	})
	if scannerSpecified && !*managedCommit {
		return fmt.Errorf("acp: --scanner applies only with --managed-commit")
	}
	if *managedStage && !*managedCommit {
		return fmt.Errorf("acp: --managed-stage requires --managed-commit")
	}
	resolved, err := resolveConfiguration()
	if err != nil {
		return err
	}
	if err := requiredRuntimeCeiling(resolved.Config.Runtime.Provider, *maxAICredits); err != nil {
		return err
	}
	prompts := make(map[string]string, 2)
	for role, path := range map[string]string{"CODER": *coderPromptFile, "REVIEWER": *reviewerPromptFile} {
		if path == "" {
			continue
		}
		prompt, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("acp: read %s system prompt: %w", role, err)
		}
		prompts[role] = string(prompt)
	}
	availableTools, err := runtimeTools(*tools)
	if err != nil {
		return err
	}
	db, err := openWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	factory := func(dispatch core.GraphDispatch) (runtime.Identity, error) {
		dispatch, err := selectDispatchConfiguration(db, dispatch)
		if err != nil {
			return nil, err
		}
		prompt, ok := prompts[dispatch.Recipient]
		if !ok {
			return nil, fmt.Errorf("acp: no system prompt configured for role %s", dispatch.Recipient)
		}
		contract, err := controller.RenderDispatchContext(db, dispatch, *compactContext)
		if err != nil {
			return nil, err
		}
		responseSchema := strictDispatchResultSchema(dispatch)
		if *validationSubmission {
			responseSchema = controller.BoundDispatchResultSchema(dispatch)
		}
		return newDispatchWorker(ctx, db, dispatch, copilot.Config{
			Role:             dispatch.Recipient,
			SystemPrompt:     runtimeSubmissionPrompt(prompt, contract, *validationSubmission),
			WorkingDirectory: dispatch.Instruction.Worktree,
			CLIPath:          *cliPath, BaseDirectory: *baseDirectory, AvailableTools: availableTools,
			MaxAICredits: *maxAICredits, ResponseSchema: responseSchema,
		}, *managedCommit, *managedStage, *scanner)
	}
	return acp.NewRuntime(storePath, db, *timeout, factory).Serve(ctx, os.Stdin, os.Stdout)
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
	if err := validateWorkspaceStorePath(); err != nil {
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
	if err := validateWorkspaceStorePath(); err != nil {
		return nil, err
	}
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
		return fmt.Errorf("usage: taxion memory put|get|list|revise|revoke|history|conflict|source|warn|configure|approve|checks|search|export|snapshot|inspect-snapshot|restore …")
	}
	switch args[0] {
	case "snapshot", "inspect-snapshot", "restore":
		return memoryMigrationCmd(args[0], args[1:])
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
		return fmt.Errorf("usage: taxion memory put|get|list|revise|revoke|history|conflict|source|warn|configure|approve|checks|search|export|snapshot|inspect-snapshot|restore …")
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
