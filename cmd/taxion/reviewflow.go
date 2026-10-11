package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	return workflowCommand(context.Background(), args, os.Stdout)
}

func workflowCommand(parent context.Context, args []string, output io.Writer) error {
	printJSON := func(value any) error {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		return encoder.Encode(value)
	}
	if len(args) == 0 {
		return fmt.Errorf("usage: taxion workflow {start|advance|run|step|recover|handoff|interrupt|feedback-target|planning-context|planner-draft|planner-action|question-draft|feedback|feedback-resolve|draft|draft-discard|draft-approve|show}")
	}
	action := args[0]
	fs := flag.NewFlagSet("workflow "+action, flag.ContinueOnError)
	var file, id string
	var coderPrompt, reviewerPrompt, cli, base, coderTools, reviewerTools string
	var managedCommit bool
	var managedStage bool
	var strictResult bool
	var compactContext bool
	var validationSubmission bool
	var scanner string
	var maxAICredits float64
	switch action {
	case "start", "recover", "handoff", "interrupt", "feedback", "feedback-resolve", "draft", "draft-discard", "draft-approve", "planner-draft", "planner-action", "question-draft":
		fs.StringVar(&file, "file", "", "JSON authorization and explicit review-flow ceilings")
	case "advance", "show", "feedback-target", "planning-context":
		fs.StringVar(&id, "id", "", "review flow ID")
	case "run", "step":
		fs.StringVar(&id, "id", "", "review flow ID")
		fs.StringVar(&coderPrompt, "coder-prompt-file", "", "Coder system prompt")
		fs.StringVar(&reviewerPrompt, "reviewer-prompt-file", "", "Reviewer system prompt")
		fs.StringVar(&cli, "cli", "", "Copilot CLI path")
		fs.StringVar(&base, "base-directory", "", "Copilot state directory")
		fs.Float64Var(&maxAICredits, "max-ai-credits", 0, "per-session soft AI-credit ceiling, not a flow-wide budget (zero: unset; minimum: 30)")
		fs.StringVar(&coderTools, "coder-tools", "", "comma-separated Coder SDK tool allowlist")
		fs.StringVar(&reviewerTools, "reviewer-tools", "", "comma-separated Reviewer SDK tool allowlist")
		fs.BoolVar(&managedCommit, "managed-commit", false, "enable the Coder's bound taxion_commit SDK tool")
		fs.BoolVar(&managedStage, "managed-stage", false, "enable taxion_stage alongside --managed-commit")
		fs.BoolVar(&strictResult, "strict-result-identities", false, "constrain final identities and start SHA to the saved Dispatch")
		fs.BoolVar(&compactContext, "compact-context", false, "include exact prior results without nested instructions")
		fs.BoolVar(&validationSubmission, "controller-validation-submission", false, "separate submission from controller-run checks; repeat on session attachment")
		fs.StringVar(&scanner, "scanner", "betterleaks", "trusted Betterleaks executable (--managed-commit only)")
	default:
		return fmt.Errorf("unknown workflow action %q", action)
	}
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := copilot.ValidateAICreditLimit(maxAICredits); err != nil {
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
	fileAction := action == "start" || action == "recover" || action == "handoff" || action == "interrupt" || action == "feedback" || action == "feedback-resolve" || action == "draft" || action == "draft-discard" || action == "draft-approve" || action == "planner-draft" || action == "planner-action" || action == "question-draft"
	if len(fs.Args()) != 0 || fileAction && file == "" || !fileAction && id == "" {
		return fmt.Errorf("workflow start, recover, handoff, interrupt, feedback, feedback-resolve, draft, draft-discard, draft-approve, planner-draft, planner-action and question-draft require --file; advance, run, step, show, planning-context and feedback-target require --id")
	}
	db, state, err := loadWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	if action == "planner-draft" || action == "planner-action" || action == "question-draft" {
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var flow core.ReviewFlow
		if action == "question-draft" {
			var request controller.QuestionDraftRequest
			if err := core.DecodeGraphInput(input, &request); err != nil {
				return err
			}
			flow, err = controller.RecordQuestionDraft(db, storePath, request)
		} else {
			var request controller.PlannerDraftRequest
			if err := core.DecodeGraphInput(input, &request); err != nil {
				return err
			}
			adopt := controller.RecordPlannerDraft
			if action == "planner-action" {
				adopt = controller.RecordPlannerAction
			}
			flow, err = adopt(db, storePath, request)
		}
		if err != nil {
			return err
		}
		return printJSON(flow)
	}
	if action == "planning-context" {
		flow, ok := state.Graph.ReviewFlow(id)
		if !state.HasGraph || !ok {
			return fmt.Errorf("workflow planning-context requires an existing flow")
		}
		context, err := controller.ObservePlannerContext(db, storePath, flow.Contract.Repo, flow.TaskID)
		if err != nil {
			return err
		}
		return printJSON(context)
	}
	switch action {
	case "interrupt":
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.InterruptRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		flow, err := controller.RequestFlowInterrupt(db, request)
		if err != nil {
			return err
		}
		return printJSON(flow)
	case "draft-approve":
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.DraftApprovalRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		flow, err := controller.ApproveInstructionDraft(db, request)
		if err != nil {
			return err
		}
		return printJSON(flow)
	case "draft":
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.InstructionDraftRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		flow, err := controller.RecordInstructionDraft(db, request)
		if err != nil {
			return err
		}
		return printJSON(flow)
	case "draft-discard":
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.DraftDiscardRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		flow, err := controller.DiscardInstructionDraft(db, request)
		if err != nil {
			return err
		}
		return printJSON(flow)
	case "feedback-resolve":
		input, err := os.Open(file)
		if err != nil {
			return err
		}
		defer input.Close()
		var request controller.FeedbackResolutionRequest
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		flow, err := controller.ResolveFeedback(db, request)
		if err != nil {
			return err
		}
		return printJSON(flow)
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
	case "run", "step":
		if coderPrompt == "" || reviewerPrompt == "" {
			return fmt.Errorf("workflow run/step requires --coder-prompt-file and --reviewer-prompt-file")
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
		ctx, cancel := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		run := controller.RunReviewFlow
		if action == "step" {
			run = controller.StepReviewFlow
		}
		flow, err := run(ctx, db, storePath, id, func(dispatch core.GraphDispatch) (runtime.Identity, error) {
			dispatch, err := selectDispatchConfiguration(db, dispatch)
			if err != nil {
				return nil, err
			}
			contract, err := controller.RenderDispatchContext(db, dispatch, compactContext)
			if err != nil {
				return nil, err
			}
			responseSchema := dispatchResultSchema(dispatch)
			if strictResult {
				responseSchema = strictDispatchResultSchema(dispatch)
			}
			return newDispatchWorker(ctx, db, dispatch, copilot.Config{
				Role: dispatch.Recipient, SystemPrompt: runtimeSubmissionPrompt(prompts[dispatch.Recipient], contract, validationSubmission),
				WorkingDirectory: dispatch.Instruction.Worktree, CLIPath: cli, BaseDirectory: base,
				MaxAICredits:   maxAICredits,
				AvailableTools: tools[dispatch.Recipient], ResponseSchema: responseSchema,
			}, managedCommit && dispatch.Recipient == "CODER", managedStage && dispatch.Recipient == "CODER", scanner)
		}, output)
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
