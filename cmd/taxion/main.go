// Command taxion is the human-facing CLI for the Taxion control plane.
//
// Local phase scope: local-first, deterministic, no network, no SDK dependency.
// State lives in a JSON ledger file (default .taxion/ledger.json).
// Future: thin client over Taxion Server (Server phase+) with ACP support (Platform phase).
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/hiono/taxion/internal/acp"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/server"
)

const usage = `taxion — Evidence-governed control plane for coding agents

Usage:
  taxion [--store PATH] <command> [options]

Commands:
  init                Initialize a local workspace ledger
  task create         Create a task (--title and --repo required)
  task list           List tasks ([--repo] filter, [--json])
  dispatch            Dispatch a task (--task and --repo required)
  dispatch list       List dispatches ([--repo] filter, [--json])
  review              Review a coder output (--task, --sha and --repo required)
  resume              Resume a session (--session required)
  acp                 Serve ACP v1 over stdio (spike)
  serve               Serve HTTP API over the ledger ([--addr], [--token]/TAXION_TOKEN, [--audit])
  gen-token           Print a random Bearer token for serve (M5 auth)

Identity gate (REP-001): mutating commands require an explicit --repo.
The target is never inferred from the working directory.
`

var storePath = ".taxion/ledger.json"

func main() {
	args := os.Args[1:]
	// Optional global --store before the command.
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
		err = acp.New(storePath).Serve(context.Background(), os.Stdin, os.Stdout)
	case "serve":
		err = serveCmd(args[1:])
	case "gen-token":
		err = genTokenCmd()
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
	l, err := core.LoadLedger(storePath)
	if err != nil {
		return core.Ledger{}, fmt.Errorf("cannot load %s (run 'taxion init' first): %w", storePath, err)
	}
	return l, nil
}

func initCmd() error {
	if err := os.MkdirAll(filepath.Dir(storePath), 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(storePath); err == nil {
		fmt.Printf("taxion: workspace already initialized at %s\n", storePath)
		return nil
	}
	if err := (core.Ledger{}).Save(storePath); err != nil {
		return err
	}
	fmt.Printf("taxion: initialized workspace at %s\n", storePath)
	return nil
}

func newID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func taskCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: taxion task create|list …")
	}
	switch args[0] {
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
		l, err := loadStore()
		if err != nil {
			return err
		}
		t := core.Task{ID: newID("t"), Title: *title, Repo: *repo, CreatedAt: time.Now().UTC()}
		l.Tasks = append(l.Tasks, t)
		if err := l.Save(storePath); err != nil {
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
		l, err := loadStore()
		if err != nil {
			return err
		}
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
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *task == "" {
		return fmt.Errorf("dispatch: --task is required")
	}
	if err := core.CheckRepo(*repo); err != nil {
		return err
	}
	l, err := loadStore()
	if err != nil {
		return err
	}
	found := false
	for _, t := range l.Tasks {
		if t.ID == *task {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("dispatch: unknown task %q", *task)
	}
	if err := core.GuardActiveDispatch(l.Dispatches); err != nil {
		return err
	}
	d := core.Dispatch{ID: newID("d"), TaskID: *task, Repo: *repo, Active: true, CreatedAt: time.Now().UTC()}
	l.Dispatches = append(l.Dispatches, d)
	if err := l.Save(storePath); err != nil {
		return err
	}
	fmt.Printf("taxion: dispatched %s for task %s in %s\n", d.ID, *task, *repo)
	return nil
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
	ev, err := core.BindEvidence(*task, *sha) // SHA-001: --sha required, never "latest"
	if err != nil {
		return err
	}
	l, err := loadStore()
	if err != nil {
		return err
	}
	l.Evidence = append(l.Evidence, ev)
	if err := l.Save(storePath); err != nil {
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

// serveCmd runs the HTTP API (M5: optional Bearer auth, M5-2 audit log).
// Localhost only by default; binding elsewhere requires --token (or
// TAXION_TOKEN) — serving beyond loopback without auth is refused.
// The audit log defaults next to the ledger; empty --audit disables it.
func serveCmd(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:3938", "listen address")
	token := fs.String("token", os.Getenv("TAXION_TOKEN"), "Bearer token (or TAXION_TOKEN env)")
	defAudit := filepath.Join(filepath.Dir(storePath), "audit.jsonl")
	audit := fs.String("audit", defAudit, "audit log path (empty disables)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !isLoopback(*addr) && *token == "" {
		return fmt.Errorf("serve: binding beyond loopback requires --token (M5 auth)")
	}
	if *audit != "" {
		if err := os.MkdirAll(filepath.Dir(*audit), 0o755); err != nil {
			return err
		}
	}
	srv := &server.Server{StorePath: storePath, Token: *token, AuditPath: *audit}
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

// isLoopback reports whether addr binds a loopback interface only.
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

// genTokenCmd prints a fresh random token for `serve --token`. Keep it in an
// env file or secret store — never in the repo.
func genTokenCmd() error {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return err
	}
	fmt.Println(hex.EncodeToString(b[:]))
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
