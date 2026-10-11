package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/store"
)

func newDispatchWorker(hostContext context.Context, db *store.Store, dispatch core.GraphDispatch, config copilot.Config, managedCommit bool, scanner string) (*copilot.Adapter, error) {
	var worker *copilot.Adapter
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
	}
	var err error
	worker, err = copilot.New(config)
	return worker, err
}
