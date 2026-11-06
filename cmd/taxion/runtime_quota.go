package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/store"
)

func runtimeQuotaCmd(args []string, output io.Writer) error {
	fs := flag.NewFlagSet("runtime quota", flag.ContinueOnError)
	fs.SetOutput(output)
	dispatchID := fs.String("dispatch", "", "Dispatch with approved account guidance and saved configuration")
	account := fs.String("account", "", "outer operator's Copilot account (instead of --dispatch)")
	approveRead := fs.Bool("approve-account-read", false, "use existing permission for an operator account read; no execution approval")
	cli := fs.String("cli", "", "Copilot CLI path (default: PATH)")
	baseDirectory := fs.String("base-directory", "", "Copilot state directory (default: ~/.copilot)")
	timeout := fs.Duration("timeout", 5*time.Second, "read-only observation timeout (at most 5s)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if (*dispatchID == "") == (*account == "") || fs.NArg() != 0 || *timeout <= 0 || *timeout > 5*time.Second ||
		(*account != "" && (!*approveRead || strings.TrimSpace(*account) != *account)) || (*dispatchID != "" && *approveRead) {
		return fmt.Errorf("runtime quota: use --dispatch ID or --account ACCOUNT --approve-account-read; --timeout must be positive and at most 5s; positional arguments are not accepted")
	}
	if *account != "" {
		ctx, cancel := signalContext(*timeout)
		defer cancel()
		quota, err := copilot.ReadLoggedInAccountQuota(ctx, *account, *cli, *baseDirectory)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		outputErr := writeQuota(output, &quota)
		if quota.State != "available" || quota.ExecutionDenied {
			return errors.Join(fmt.Errorf("runtime quota: operator quota is unavailable, stale or denied"), outputErr)
		}
		return outputErr
	}
	if err := validateWorkspaceStorePath(); err != nil {
		return err
	}
	db, err := store.OpenReadOnly(workspaceDBPath())
	if err != nil {
		return err
	}
	defer db.Close()
	state, err := db.LoadWorkspace()
	if err != nil {
		return err
	}
	dispatch, ok := state.Graph.GraphDispatch(*dispatchID)
	if !state.HasGraph || !ok || dispatch.Configuration == nil {
		return fmt.Errorf("runtime quota: a saved Dispatch configuration is required")
	}
	policy := dispatch.Instruction.BudgetGuidance
	if policy == nil || policy.Mode != "account" || !policy.AccountReadApproved {
		return fmt.Errorf("runtime quota: approved account guidance is required; provider-stop does not require numeric balance")
	}
	resolved, err := resolveConfiguration()
	if err != nil {
		return err
	}
	if _, err := controller.CheckDispatchConfiguration(db, dispatch.ID, resolved.Config); err != nil {
		return err
	}
	if resolved.Config.Runtime.Provider != "copilot" {
		return fmt.Errorf("runtime quota: selected runtime does not expose account quota; no paid route or billing-mode change was attempted")
	}
	ctx, cancel := signalContext(*timeout)
	defer cancel()
	worker, err := copilot.NewForQuota(dispatch, *cli, *baseDirectory)
	if err != nil {
		return err
	}
	control, err := controller.New(db, worker)
	if err != nil {
		return errors.Join(err, worker.Close())
	}
	quota, observationErr := control.ObserveAccountQuota(ctx, dispatch.ID)
	var outputErr error
	if quota != nil {
		outputErr = writeQuota(output, quota)
	}
	return errors.Join(observationErr, outputErr, worker.Close())
}

func writeQuota(output io.Writer, quota *runtime.QuotaSnapshot) error {
	return json.NewEncoder(output).Encode(struct {
		Quota               *runtime.QuotaSnapshot `json:"quota"`
		ExecutionAuthorized bool                   `json:"executionAuthorized"`
	}{Quota: quota})
}
