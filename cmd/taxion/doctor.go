package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/store"
	"github.com/hiono/taxion/internal/tools"
)

func doctorCmd(args []string, output io.Writer) (err error) {
	if len(args) > 0 && args[0] == "fix" {
		return setupCmd(args[1:], os.Stdin, output, terminalInput(os.Stdin), false)
	}
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	asJSON := flags.Bool("json", false, "print the same diagnostic inventory as JSON")
	worktree := flags.String("worktree", ".", "execution worktree whose native configuration is inspected")
	cli := flags.String("cli", "", "same Copilot executable as runtime execution")
	base := flags.String("base-directory", "", "same Copilot state/config directory as runtime execution")
	dispatchID := flags.String("dispatch", "", "inspect an existing Dispatch's worktree and tools policy without changing it")
	toolList := flags.String("tools", "", "same explicit role tool list as runtime execution")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("doctor accepts options only")
	}
	resolved, err := resolveConfiguration()
	if err != nil {
		return fmt.Errorf("configuration diagnosis: %w; next: taxion doctor fix (or taxion doctor fix --dry-run with --set FIELD=VALUE for missing references); no files changed", err)
	}
	selection, err := json.Marshal(resolved.Config)
	if err != nil {
		return err
	}
	available, err := runtimeTools(*toolList)
	if err != nil {
		return err
	}
	config := copilot.Config{
		Role: "PLANNER", SystemPrompt: "Read-only configuration inventory; no session or model turn.",
		WorkingDirectory: *worktree, CLIPath: *cli, BaseDirectory: *base,
		Configuration: selection, AvailableTools: available,
	}
	if *dispatchID != "" {
		if *worktree != "." {
			return fmt.Errorf("doctor --dispatch uses the saved worktree; do not also specify --worktree")
		}
		db, err := store.OpenReadOnly(workspaceDBPath())
		if err != nil {
			return err
		}
		state, loadErr := db.LoadWorkspace()
		if loadErr == nil {
			_, loadErr = controller.CheckDispatchConfiguration(db, *dispatchID, resolved.Config)
		}
		if err := errors.Join(loadErr, db.Close()); err != nil {
			return err
		}
		dispatch, ok := state.Graph.GraphDispatch(*dispatchID)
		if !state.HasGraph || !ok {
			return fmt.Errorf("doctor requires an existing saved Dispatch")
		}
		config.WorkingDirectory, config.Role = dispatch.Instruction.Worktree, dispatch.Recipient
		if dispatch.Configuration != nil {
			config.Configuration = dispatch.Configuration.Selection
		}
		config.ToolPolicy, err = tools.ResolveForTask(state.Graph, dispatch.Instruction.Decisions, dispatch.TaskID, dispatch.Recipient)
		if err != nil {
			return err
		}
	}
	if resolved.Config.Runtime.Provider == "opencode" {
		worker, err := newRoleWorker(config)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, worker.Close()) }()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		checkErr := worker.CheckAuthentication(ctx, resolved.Config.Runtime.Account)
		report := struct {
			Backend        string   `json:"backend"`
			CLI            string   `json:"cli"`
			Version        string   `json:"version"`
			Worktree       string   `json:"worktree"`
			Isolation      string   `json:"isolation"`
			AssetsVerified bool     `json:"assetsVerified"`
			Limitations    []string `json:"limitations"`
		}{
			Backend: "opencode", CLI: resolved.Config.Runtime.ACPCommand, Version: "1.19.0 required; not connected",
			Worktree: config.WorkingDirectory, Isolation: "Linux user/mount/PID namespaces; role-scoped copied inputs",
			AssetsVerified: checkErr == nil,
			Limitations: []string{
				"No process, session, model prompt, credential or provider quota read was performed.",
				"Only approved skill directories enter the isolated configuration; executable plugins, MCP, RTK and shell are unsupported.",
				"Provider authentication, remote repository permission, entitlement and quota remain unverified.",
				"Credential injection is unsupported; the isolated process does not inherit OpenCode logins or provider API keys.",
				"Connection verifies version and agent before any prompt; unavailable Linux isolation fails closed.",
				"ACP token usage covers only the latest assistant message, not the complete task or turn.",
			},
		}
		if *asJSON {
			return errors.Join(checkErr, json.NewEncoder(output).Encode(report))
		}
		_, printErr := fmt.Fprintf(output, "Backend: %s\nCLI: %s\nVersion: %s\nWorktree: %s\nIsolation: %s\nAssets verified: %t\n",
			report.Backend, report.CLI, report.Version, report.Worktree, report.Isolation, report.AssetsVerified)
		for _, limitation := range report.Limitations {
			_, err := fmt.Fprintln(output, limitation)
			printErr = errors.Join(printErr, err)
		}
		return errors.Join(checkErr, printErr)
	}
	adapter, err := copilot.New(config)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, adapter.Close()) }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	report, inspectErr := adapter.Inspect(ctx)
	if *asJSON {
		return errors.Join(inspectErr, json.NewEncoder(output).Encode(report))
	}
	if _, err := fmt.Fprintf(output, "Backend: %s\nCLI: %s\nVersion: %s\nWorktree: %s\nCopilot home: %s\nDiscovery: %s\n\n検索先:\n",
		report.Backend, report.CLI, report.Version, report.Worktree, report.BaseDirectory, report.Mode); err != nil {
		return errors.Join(inspectErr, err)
	}
	for _, path := range report.Paths {
		if _, err := fmt.Fprintf(output, "  [%s] %s (%s; %s)\n", path.State, path.Path, path.Kind, path.Source); err != nil {
			return errors.Join(inspectErr, err)
		}
	}
	if _, err := fmt.Fprintln(output, "\n検出された設定:"); err != nil {
		return errors.Join(inspectErr, err)
	}
	for _, asset := range report.Assets {
		if _, err := fmt.Fprintf(output, "  [%s] %s %s (%s) %s\n", asset.State, asset.Kind, asset.Name, asset.Source, asset.Path); err != nil {
			return errors.Join(inspectErr, err)
		}
		if asset.Reason != "" {
			if _, err := fmt.Fprintf(output, "    %s\n", asset.Reason); err != nil {
				return errors.Join(inspectErr, err)
			}
		}
	}
	for _, problem := range report.Problems {
		if _, err := fmt.Fprintf(output, "ERROR: %s\n", problem); err != nil {
			return errors.Join(inspectErr, err)
		}
	}
	for _, limitation := range report.Limitations {
		if _, err := fmt.Fprintf(output, "確認範囲: %s\n", limitation); err != nil {
			return errors.Join(inspectErr, err)
		}
	}
	return inspectErr
}
