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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/acp"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/memory"
	"github.com/hiono/taxion/internal/server"
	"github.com/hiono/taxion/internal/store"
)

const usage = `taxion — Evidence-governed control plane for coding agents

Usage:
  taxion [--store PATH] <command> [options]

Commands:
  init                Initialize a local workspace ledger
  task create         Create a task (--title and --repo required)
  task list           List tasks ([--repo] filter, [--json])
  task track          Register/update a durable Task Graph contract (--file JSON)
  task transition     Validate and record a Task Graph transition (--file JSON)
  task graph          Print the versioned Task Graph and history
  dispatch            Dispatch a task (--task and --repo required; revised Dispatches need an explicit relationship)
  dispatch transition Record a Dispatch lifecycle transition (--id, --status, --reason)
  dispatch list       List dispatches ([--repo] filter, [--json])
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
	case "init":
		err = initCmd()
	case "task":
		err = taskCmd(args[1:])
	case "dispatch":
		err = dispatchCmd(args[1:])
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
	case "track", "transition", "graph":
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

			did = newID("d")
			dispatch, err := core.NewGraphDispatch(state.Graph, did, *task, *repo, core.DispatchRelationship(*relationship), *relatedDispatch, *relationshipReason)
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
		return fmt.Errorf("usage: taxion memory put|get|list|revise|revoke|history|source|warn|configure|approve|checks|search|export …")
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
		return fmt.Errorf("usage: taxion memory put|get|list|revise|revoke|history|source|warn|configure|approve|checks|search|export …")
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
