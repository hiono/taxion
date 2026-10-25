package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"

	executionconfig "github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/runtime/openacp"
	"github.com/hiono/taxion/internal/store"
	"github.com/hiono/taxion/internal/tools"
)

type configuredWorker interface {
	runtime.ConfigurationIdentity
	io.Closer
}

func requiredRuntimeCeiling(provider string, credits float64) error {
	if provider == "opencode" {
		if credits != 0 {
			return fmt.Errorf("runtime: OpenCode does not enforce Copilot AI-credit limits; omit --max-ai-credits and approve an explicit deadline")
		}
		return nil
	}
	if err := copilot.ValidateAICreditLimit(credits); err != nil {
		return err
	}
	if credits == 0 {
		return fmt.Errorf("runtime: Copilot requires --max-ai-credits of at least 30")
	}
	return nil
}
func newRoleWorker(config copilot.Config) (configuredWorker, error) {
	if len(config.Configuration) == 0 {
		return copilot.New(config)
	}
	var selected executionconfig.Config
	if err := json.Unmarshal(config.Configuration, &selected); err != nil {
		return nil, fmt.Errorf("runtime: invalid configuration selection")
	}
	if err := selected.Validate(); err != nil {
		return nil, err
	}
	if selected.Runtime.Provider != "opencode" {
		return copilot.New(config)
	}
	if config.MaxAICredits != 0 || config.ManagedCommit != nil || config.ManagedSearch != nil || config.EnableReviewProgress ||
		config.CLIPath != "" || config.BaseDirectory != "" {
		return nil, fmt.Errorf("runtime: OpenCode uses approved acpCommand/acpConfig; Copilot credit caps, CLI overrides, managed commit, managed SDK search and review progress are unsupported")
	}
	if config.FileAccess == nil {
		config.FileAccess = core.DefaultFileAccessPolicy()
	}
	return openacp.New(openacp.Config{
		Role: config.Role, SystemPrompt: config.SystemPrompt, WorkingDirectory: config.WorkingDirectory,
		CLIPath: selected.Runtime.ACPCommand, CLIHash: selected.Runtime.ACPCommandHash,
		ServerConfig: selected.Runtime.ACPConfig, ServerConfigHash: selected.Runtime.ACPConfigHash,
		Configuration: config.Configuration, FileAccess: config.FileAccess, TrackedFiles: config.TrackedFiles,
		ResponseSchema: config.ResponseSchema, BudgetGuidance: config.BudgetGuidance,
		ToolPolicy: config.ToolPolicy, AvailableTools: config.AvailableTools,
	})
}

func newDispatchWorker(hostContext context.Context, db *store.Store, dispatch core.GraphDispatch, config copilot.Config, managedCommit, managedStage bool, scanner string) (configuredWorker, error) {
	if managedStage && !managedCommit {
		return nil, fmt.Errorf("runtime: --managed-stage requires --managed-commit")
	}
	var err error
	config.Configuration = nil
	config.FileAccess = core.CloneFileAccessPolicy(dispatch.Instruction.FileAccess)
	config.BudgetGuidance = core.CloneBudgetGuidance(dispatch.Instruction.BudgetGuidance)
	config.TrackedFiles = nil
	if config.FileAccess != nil && config.FileAccess.Level == core.FileAccessCommitted {
		config.TrackedFiles, err = gitproof.TrackedPathsAt(dispatch.Instruction.Worktree, dispatch.ExpectedStartSHA())
		if err != nil {
			return nil, fmt.Errorf("runtime: build committed file-access snapshot: %w", err)
		}
	}
	if dispatch.Configuration != nil {
		config.Configuration = dispatch.Configuration.Selection
	}
	var worker configuredWorker
	if config.EnableReviewProgress && dispatch.Recipient == "REVIEWER" {
		if db == nil {
			return nil, fmt.Errorf("runtime: review progress requires a saved workspace")
		}
		config.ManagedReviewProgress = &runtime.ManagedReviewProgress{
			Basis: core.ReviewProgress{
				Version: core.ReviewProgressVersion, DispatchID: dispatch.ID,
				InstructionHash: dispatch.InstructionHash, CoderDispatchID: dispatch.RelatedID,
				TargetSHA: dispatch.Instruction.TargetSHA, TargetTreeSHA: dispatch.Instruction.TargetTreeSHA,
			},
			Save: func(ctx context.Context, sessionID string, progress core.ReviewProgress) (string, error) {
				ctx, cancel := context.WithCancel(ctx)
				stop := context.AfterFunc(hostContext, cancel)
				defer stop()
				defer cancel()
				if err := hostContext.Err(); err != nil {
					return "", err
				}
				control, err := controller.New(db, worker)
				if err != nil {
					return "", err
				}
				return control.SaveReviewProgressForSession(ctx, dispatch.ID, sessionID, progress)
			},
		}
	}
	var graph core.TaskGraph
	if db != nil {
		state, err := db.LoadWorkspace()
		if err != nil {
			return nil, err
		}
		graph = state.Graph
	}
	config.ToolPolicy, err = tools.ResolveForTask(graph, dispatch.Instruction.Decisions, dispatch.TaskID, dispatch.Recipient)
	if err != nil {
		return nil, err
	}
	if config.ToolPolicy != nil && db == nil {
		return nil, fmt.Errorf("taxion: approved tools require a saved workspace")
	}
	if config.ToolPolicy != nil {
		config.AuthorizeTools = func(ctx context.Context, sessionID string) error {
			ctx, cancel := context.WithCancel(ctx)
			stop := context.AfterFunc(hostContext, cancel)
			defer stop()
			defer cancel()
			control, err := controller.New(db, worker)
			if err != nil {
				return err
			}
			return control.AuthorizeTools(ctx, dispatch.ID, sessionID)
		}
	}
	if managedCommit {
		if dispatch.Recipient != "CODER" || dispatch.Instruction.CommitMessagePolicy != core.CommitMessagePolicy {
			return nil, fmt.Errorf("runtime: managed commit requires a Coder Dispatch with the current commit policy")
		}
		path := scanner
		if !filepath.IsAbs(path) {
			var err error
			path, err = exec.LookPath(scanner)
			if err != nil {
				return nil, fmt.Errorf("runtime: required managed commit scanner is unavailable; use the saved absolute path when attaching for safe stop")
			}
		}
		path, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("runtime: resolve managed commit scanner: %w", err)
		}
		config.ManagedCommit = &runtime.ManagedCommit{
			DispatchID: dispatch.ID, Scanner: path,
			Commit: func(ctx context.Context, sessionID, message string) (string, error) {
				ctx, cancel := context.WithCancel(ctx)
				stop := context.AfterFunc(hostContext, cancel)
				defer stop()
				defer cancel()
				if err := hostContext.Err(); err != nil {
					return "", fmt.Errorf("runtime: managed commit host interrupted: %w", err)
				}
				control, err := controller.New(db, worker)
				if err != nil {
					return "", err
				}
				return control.CommitForSession(ctx, dispatch.ID, sessionID, message, path)
			},
		}
		if managedStage {
			config.ManagedCommit.Stage = func(ctx context.Context, sessionID string, paths []string) (string, error) {
				ctx, cancel := context.WithCancel(ctx)
				stop := context.AfterFunc(hostContext, cancel)
				defer stop()
				defer cancel()
				if err := hostContext.Err(); err != nil {
					return "", fmt.Errorf("runtime: managed stage host interrupted: %w", err)
				}
				control, err := controller.New(db, worker)
				if err != nil {
					return "", err
				}
				return control.StageForSession(ctx, dispatch.ID, sessionID, paths, path)
			}
		}
	}
	worker, err = newRoleWorker(config)
	return worker, err
}

func selectDispatchConfiguration(db *store.Store, dispatch core.GraphDispatch) (core.GraphDispatch, error) {
	resolved, err := resolveConfiguration()
	if err != nil {
		return core.GraphDispatch{}, err
	}
	if err := controller.BindConfiguration(db, dispatch.ID, resolved); err != nil {
		return core.GraphDispatch{}, err
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return core.GraphDispatch{}, err
	}
	selected, ok := state.Graph.GraphDispatch(dispatch.ID)
	if !state.HasGraph || !ok || selected.Configuration == nil {
		return core.GraphDispatch{}, fmt.Errorf("runtime: saved Dispatch configuration is unavailable")
	}
	if _, err := controller.CheckDispatchConfiguration(db, dispatch.ID, resolved.Config); err != nil {
		return core.GraphDispatch{}, err
	}
	return selected, nil
}
