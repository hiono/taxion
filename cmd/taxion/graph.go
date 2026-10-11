package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
)

func taskGraphCmd(command string, args []string) error {
	fs := flag.NewFlagSet("task "+command, flag.ContinueOnError)
	file := fs.String("file", "", "JSON request file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected task graph arguments")
	}
	db, state, err := loadWorkspace()
	if err != nil {
		return err
	}
	defer db.Close()
	if command == "graph" {
		if !state.HasGraph {
			return fmt.Errorf("task graph is not initialized; use task track")
		}
		if err := state.Graph.CheckLedger(state.Ledger); err != nil {
			return err
		}
		return printJSON(state.Graph)
	}
	if *file == "" {
		return fmt.Errorf("task %s: --file is required", command)
	}
	input, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer input.Close()
	switch command {
	case "track":
		var request struct {
			Task    core.GraphTask `json:"task"`
			Actor   string         `json:"actor"`
			Reason  string         `json:"reason"`
			Concern *core.Concern  `json:"concern,omitempty"`
		}
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		return updateWorkspace(func(current *store.Workspace) error {
			if !current.HasGraph {
				if err := core.GuardActiveDispatch(current.Ledger.Dispatches); err != nil {
					return fmt.Errorf("resolve legacy active dispatches before graph migration: %w", err)
				}
				current.Graph = core.TaskGraph{Version: 1}
				current.HasGraph = true
			}
			if err := current.Graph.Track(request.Task, request.Actor, request.Reason); err != nil {
				return err
			}
			if request.Concern != nil {
				if err := current.Graph.AddConcern(*request.Concern); err != nil {
					return err
				}
			}
			return current.Graph.CheckLedger(current.Ledger)
		})
	case "transition":
		var request struct {
			TaskID string               `json:"taskId"`
			Status core.TaskStatus      `json:"status"`
			Actor  string               `json:"actor"`
			Reason string               `json:"reason"`
			Proof  core.TransitionProof `json:"proof"`
		}
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		return updateWorkspace(func(current *store.Workspace) error {
			if !current.HasGraph {
				return fmt.Errorf("task graph is not initialized; use task track")
			}
			return current.Graph.Transition(request.TaskID, request.Status, request.Actor, request.Reason, request.Proof)
		})
	default:
		return fmt.Errorf("unknown task graph command %s", command)
	}
}

func dispatchTransitionCmd(args []string) error {
	fs := flag.NewFlagSet("dispatch transition", flag.ContinueOnError)
	id := fs.String("id", "", "dispatch ID")
	status := fs.String("status", "", "target status")
	actor := fs.String("actor", "", "actor")
	reason := fs.String("reason", "", "reason")
	resultFile := fs.String("result-file", "", "JSON result file required when ending a Dispatch")
	validationTimeout := fs.Duration("validation-timeout", 10*time.Minute, "controller validation deadline")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" || *status == "" || *actor == "" || *reason == "" {
		return fmt.Errorf("dispatch transition: --id, --status, --actor and --reason are required")
	}
	if *validationTimeout <= 0 {
		return fmt.Errorf("dispatch transition: --validation-timeout must be positive")
	}
	ctx, cancel := signalContext(*validationTimeout)
	defer cancel()
	var result *core.DispatchResult
	if *resultFile != "" {
		file, err := os.Open(*resultFile)
		if err != nil {
			return err
		}
		defer file.Close()
		var decoded core.DispatchResult
		if err := core.DecodeGraphInput(file, &decoded); err != nil {
			return fmt.Errorf("dispatch transition result: %w", err)
		}
		result = &decoded
	}
	return updateWorkspace(func(state *store.Workspace) error {
		if !state.HasGraph {
			return fmt.Errorf("dispatch lifecycle requires a Task Graph")
		}
		target := core.DispatchStatus(*status)
		if target == core.DispatchRunning {
			dispatch, ok := state.Graph.GraphDispatch(*id)
			if !ok {
				return fmt.Errorf("dispatch %q not found", *id)
			}
			if dispatch.Instruction.Version >= 4 && dispatch.ExecutionStartSHA == "" {
				start, err := gitproof.ObserveStart(dispatch)
				if err != nil {
					return err
				}
				for index := range state.Graph.Dispatches {
					if state.Graph.Dispatches[index].ID == *id {
						state.Graph.Dispatches[index].ExecutionStartSHA = start
					}
				}
			}
		}
		if result != nil {
			result.GitEvidence = nil
		}
		if target == core.DispatchSucceeded {
			dispatch, ok := state.Graph.GraphDispatch(*id)
			if !ok {
				return fmt.Errorf("dispatch %q not found", *id)
			}
			if dispatch.Status != core.DispatchRunning || !dispatch.ExecutionStarted {
				return fmt.Errorf("successful result requires a running Dispatch")
			}
			if dispatch.Recipient == "REVIEWER" {
				return fmt.Errorf("Reviewer completion requires runtime finalization and managed worktree release")
			}
			if result == nil {
				return fmt.Errorf("successful dispatch requires a result file")
			}
			if err := gitproof.ExecuteValidations(ctx, dispatch, result); err != nil {
				return fmt.Errorf("dispatch Git verification: %w", err)
			}
		}
		ack := target == core.DispatchAcknowledged || target == core.DispatchRunning
		started := target == core.DispatchRunning
		if err := state.Graph.TransitionDispatch(*id, target, *actor, *reason, ack, started, result); err != nil {
			return err
		}
		for i := range state.Ledger.Dispatches {
			if state.Ledger.Dispatches[i].ID == *id {
				state.Ledger.Dispatches[i].Active = target == core.DispatchIssued || target == core.DispatchAcknowledged ||
					target == core.DispatchRunning || target == core.DispatchCancelRequested || target == core.DispatchSuspended
			}
		}
		return nil
	})
}
