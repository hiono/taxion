package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/hiono/taxion/internal/core"
)

func taskGraphCmd(command string, args []string) (retErr error) {
	fs := flag.NewFlagSet("task "+command, flag.ContinueOnError)
	file := fs.String("file", "", "JSON request file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("unexpected task graph arguments")
	}
	ledger, err := loadStore()
	if err != nil {
		return err
	}
	if command != "graph" {
		lockPath := core.GraphPath(storePath) + ".lock"
		lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return fmt.Errorf("cannot acquire task graph writer lock %s: %w", lockPath, err)
		}
		if err := lock.Close(); err != nil {
			if removeErr := os.Remove(lockPath); removeErr != nil {
				return errors.Join(err, removeErr)
			}
			return err
		}
		defer func() {
			if err := os.Remove(lockPath); err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("cannot release graph lock: %w", err))
			}
		}()
	}
	g, err := core.LoadTaskGraph(core.GraphPath(storePath))
	if errors.Is(err, os.ErrNotExist) {
		if _, statErr := os.Lstat(core.GraphPath(storePath)); !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("task graph exists but cannot be loaded: %w", err)
		}
		if command == "graph" {
			return fmt.Errorf("task graph is not initialized; use task track")
		}
		if err := core.GuardActiveDispatch(ledger.Dispatches); err != nil {
			return fmt.Errorf("resolve legacy active dispatches before graph migration: %w", err)
		}
		g = core.TaskGraph{Version: 1}
	} else if err != nil {
		return err
	}
	if command == "graph" {
		if err := g.CheckLedger(ledger); err != nil {
			return err
		}
		return printJSON(g)
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
		found := false
		for _, t := range ledger.Tasks {
			if t.ID == request.Task.ID {
				found = true
				if t.Title != request.Task.Title || t.Repo != request.Task.Repo {
					return fmt.Errorf("task graph must match ledger task identity")
				}
			}
		}
		if !found {
			return fmt.Errorf("create the ledger task before tracking %s", request.Task.ID)
		}
		if err := g.Track(request.Task, request.Actor, request.Reason); err != nil {
			return err
		}
		if request.Concern != nil {
			if err := g.AddConcern(*request.Concern); err != nil {
				return err
			}
		}
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
		if err := g.Transition(request.TaskID, request.Status, request.Actor, request.Reason, request.Proof); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown task graph command %s", command)
	}
	if err := g.CheckLedger(ledger); err != nil {
		return err
	}
	if err := g.Save(core.GraphPath(storePath)); err != nil {
		return err
	}
	return printJSON(g)
}
