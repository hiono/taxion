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
		return fmt.Errorf("usage: taxion workflow {start|advance|run|recover|handoff|feedback-target|feedback|show}")
	}
	action := args[0]
	fs := flag.NewFlagSet("workflow "+action, flag.ContinueOnError)
	var file, id string
	var coderPrompt, reviewerPrompt, cli, base, coderTools, reviewerTools string
	var managedCommit bool
	var managedStage bool
	var scanner string
	switch action {
	case "start", "recover", "handoff", "feedback":
		fs.StringVar(&file, "file", "", "JSON authorization and explicit review-flow ceilings")
	case "advance", "show", "feedback-target":
		fs.StringVar(&id, "id", "", "review flow ID")
	case "run":
		fs.StringVar(&id, "id", "", "review flow ID")
		fs.StringVar(&coderPrompt, "coder-prompt-file", "", "Coder system prompt")
		fs.StringVar(&reviewerPrompt, "reviewer-prompt-file", "", "Reviewer system prompt")
		fs.StringVar(&cli, "cli", "", "Copilot CLI path")
		fs.StringVar(&base, "base-directory", "", "Copilot state directory")
		fs.StringVar(&coderTools, "coder-tools", "", "comma-separated Coder SDK tool allowlist")
		fs.StringVar(&reviewerTools, "reviewer-tools", "", "comma-separated Reviewer SDK tool allowlist")
		fs.BoolVar(&managedCommit, "managed-commit", false, "enable the Coder's bound taxion_commit SDK tool")
		fs.BoolVar(&managedStage, "managed-stage", false, "enable taxion_stage alongside --managed-commit")
		fs.StringVar(&scanner, "scanner", "betterleaks", "trusted Betterleaks executable (--managed-commit only)")
	default:
		return fmt.Errorf("unknown workflow action %q", action)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	var scannerSpecified bool
	fs.Visit(func(option *flag.Flag) {
		scannerSpecified = scannerSpecified || option.Name == "scanner"
	})
	if scannerSpecified && !managedCommit {
		return fmt.Errorf("workflow: --scanner requires --managed-commit")
	}
	if managedStage && !managedCommit {
		return fmt.Errorf("workflow: --managed-stage requires --managed-commit")
	}
	fileAction := action == "start" || action == "recover" || action == "handoff" || action == "feedback"
	if len(fs.Args()) != 0 || fileAction && file == "" || !fileAction && id == "" {
		return fmt.Errorf("workflow start, recover, handoff and feedback require --file; advance, run, show and feedback-target require --id")
	}
	db, state, err := loadWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	switch action {
	case "feedback-target":
		target, err := controller.ObserveFeedback(db, id)
		if err != nil {
			return err
		}
		return printJSON(target)
	case "feedback":
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.FeedbackRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		flow, err := controller.RecordFeedback(db, request)
		if err != nil {
			return err
		}
		return printJSON(flow)
	case "handoff":
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.HandoffRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		flow, err := controller.ChangeHandoff(db, request)
		if err != nil {
			return err
		}
		return printJSON(flow)
	case "recover":
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.RecoveryRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		flow, err := controller.RecoverReviewFlow(db, request)
		if err != nil {
			return err
		}
		return printJSON(flow)
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
			dispatch, err := selectDispatchConfiguration(db, dispatch)
			if err != nil {
				return nil, err
			}
			contract, err := core.RenderDispatchInstruction(dispatch)
			if err != nil {
				return nil, err
			}
			return newDispatchWorker(ctx, db, dispatch, copilot.Config{
				Role: dispatch.Recipient, SystemPrompt: runtimeSystemPrompt(prompts[dispatch.Recipient], contract),
				WorkingDirectory: dispatch.Instruction.Worktree, CLIPath: cli, BaseDirectory: base,
				AvailableTools: tools[dispatch.Recipient], ResponseSchema: dispatchResultSchema(dispatch),
			}, managedCommit && dispatch.Recipient == "CODER", managedStage && dispatch.Recipient == "CODER", scanner)
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
		if err != nil && !errors.Is(err, controller.ErrFlowStopped) && !errors.Is(err, core.ErrHandoffHeld) {
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
