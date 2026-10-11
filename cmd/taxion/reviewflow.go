package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/runtime/copilot"
)

func workflowCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taxion workflow {start|advance|run|show}")
	}
	action := args[0]
	fs := flag.NewFlagSet("workflow "+action, flag.ContinueOnError)
	var file, id string
	var coderPrompt, reviewerPrompt, cli, base, coderTools, reviewerTools string
	switch action {
	case "start":
		fs.StringVar(&file, "file", "", "JSON authorization and explicit review-flow ceilings")
	case "advance", "show":
		fs.StringVar(&id, "id", "", "review flow ID")
	case "run":
		fs.StringVar(&id, "id", "", "review flow ID")
		fs.StringVar(&coderPrompt, "coder-prompt-file", "", "Coder system prompt")
		fs.StringVar(&reviewerPrompt, "reviewer-prompt-file", "", "Reviewer system prompt")
		fs.StringVar(&cli, "cli", "", "Copilot CLI path")
		fs.StringVar(&base, "base-directory", "", "Copilot state directory")
		fs.StringVar(&coderTools, "coder-tools", "", "comma-separated Coder SDK tool allowlist")
		fs.StringVar(&reviewerTools, "reviewer-tools", "", "comma-separated Reviewer SDK tool allowlist")
	default:
		return fmt.Errorf("unknown workflow action %q", action)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 || action == "start" && file == "" || action != "start" && id == "" {
		return fmt.Errorf("workflow start requires --file; advance, run and show require --id")
	}
	db, state, err := loadWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	switch action {
	case "run":
		if coderPrompt == "" || reviewerPrompt == "" {
			return fmt.Errorf("workflow run requires --coder-prompt-file and --reviewer-prompt-file")
		}
		prompts := map[string]string{}
		for role, path := range map[string]string{"CODER": coderPrompt, "REVIEWER": reviewerPrompt} {
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("workflow run: read %s prompt: %w", role, err)
			}
			if strings.TrimSpace(string(data)) == "" {
				return fmt.Errorf("workflow run: %s prompt is empty", role)
			}
			prompts[role] = string(data)
		}
		tools := map[string][]string{}
		for role, list := range map[string]string{"CODER": coderTools, "REVIEWER": reviewerTools} {
			var err error
			tools[role], err = runtimeTools(list)
			if err != nil {
				return err
			}
		}
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		flow, err := controller.RunReviewFlow(ctx, db, storePath, id, func(dispatch core.GraphDispatch) (runtime.Identity, error) {
			contract, err := core.RenderDispatchInstruction(dispatch)
			if err != nil {
				return nil, err
			}
			return copilot.New(copilot.Config{
				Role: dispatch.Recipient, SystemPrompt: runtimeSystemPrompt(prompts[dispatch.Recipient], contract),
				WorkingDirectory: dispatch.Instruction.Worktree, CLIPath: cli, BaseDirectory: base,
				AvailableTools: tools[dispatch.Recipient], ResponseSchema: dispatchResultSchema(dispatch),
			})
		}, os.Stdout)
		if flow.ID == "" {
			return err
		}
		return errors.Join(printJSON(flow), err)
	case "start":
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.ReviewFlowRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		flow, err := controller.StartReviewFlow(db, storePath, request)
		if err != nil {
			return err
		}
		return printJSON(flow)
	case "advance":
		flow, err := controller.AdvanceReviewFlow(db, storePath, id)
		if err != nil && !errors.Is(err, controller.ErrFlowStopped) {
			return err
		}
		return errors.Join(printJSON(flow), err)
	default:
		flow, ok := state.Graph.ReviewFlow(id)
		if !state.HasGraph || !ok {
			return fmt.Errorf("workflow %q not found", id)
		}
		return printJSON(flow)
	}
}

func runtimeTools(list string) ([]string, error) {
	tools := []string{}
	if strings.TrimSpace(list) == "" {
		return tools, nil
	}
	for _, name := range strings.Split(list, ",") {
		name = strings.TrimSpace(name)
		if name == "" || slices.Contains(tools, name) {
			return nil, fmt.Errorf("runtime: tool list contains an empty or duplicate name")
		}
		tools = append(tools, name)
	}
	return tools, nil
}
