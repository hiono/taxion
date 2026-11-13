package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

type gateExitError struct {
	report controller.GateReport
}

func (e gateExitError) Error() string {
	if len(e.report.BlockingConditions) != 0 {
		condition := e.report.BlockingConditions[0]
		return fmt.Sprintf("gate %s: %s (%s: %s)", e.report.Gate, e.report.Verdict, condition.Code, condition.Message)
	}
	return fmt.Sprintf("gate %s: %s", e.report.Gate, e.report.Verdict)
}

func (e gateExitError) ExitCode() int { return controller.GateExitCode(e.report.Verdict) }

func gateCmd(args []string) error { return gateCommand(args, os.Stdout) }

func gateCommand(args []string, output io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taxion gate {contract|resume|execution|publication} [--task ID|--flow ID] [--json]")
	}
	gate := args[0]
	switch gate {
	case controller.GateContract, controller.GateResume, controller.GateExecution, controller.GatePublication:
	default:
		return fmt.Errorf("unknown gate %q; use contract, resume, execution or publication", gate)
	}
	fs := flag.NewFlagSet("gate "+gate, flag.ContinueOnError)
	var taskID, flowID string
	var asJSON bool
	fs.StringVar(&taskID, "task", "", "Task ID for the contract gate")
	fs.StringVar(&flowID, "flow", "", "review flow ID for the execution or publication gate")
	fs.BoolVar(&asJSON, "json", false, "emit the versioned machine-readable report")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return fmt.Errorf("gate %s accepts no positional arguments", gate)
	}
	if gate == controller.GateContract && taskID == "" {
		return fmt.Errorf("gate contract requires --task")
	}
	if gate != controller.GateContract && flowID == "" {
		return fmt.Errorf("gate %s requires --flow", gate)
	}
	if gate == controller.GateContract && flowID != "" {
		return fmt.Errorf("gate contract accepts --task, not --flow")
	}
	if gate != controller.GateContract && taskID != "" {
		return fmt.Errorf("gate %s accepts --flow, not --task", gate)
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
	var report controller.GateReport
	switch gate {
	case controller.GateContract, controller.GateResume:
		configuration := core.ApprovalConfiguration{}
		if resolved, resolveErr := resolveConfiguration(); resolveErr == nil {
			configuration, err = approvalConfigurationFor(resolved.Config, resolved.Sources)
			if err != nil {
				return err
			}
		}
		if gate == controller.GateResume {
			report, err = controller.ResumeContractGateReport(context.Background(), state, flowID, configuration)
		} else {
			report, err = controller.ContractGateReport(state.Graph, taskID, configuration)
		}
	case controller.GateExecution:
		report, err = controller.ExecutionGateReport(state.Graph, flowID)
	case controller.GatePublication:
		report, err = controller.PublicationGateReport(state.Graph, flowID)
	}
	if err != nil {
		return err
	}
	if asJSON {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			return err
		}
	} else if err := printGateReport(output, report); err != nil {
		return err
	}
	if report.Verdict != controller.GateVerdictPass {
		return gateExitError{report: report}
	}
	return nil
}

func printGateReport(output io.Writer, report controller.GateReport) error {
	if _, err := fmt.Fprintf(output, "gate: %s\ntask: %s\n", report.Gate, report.TaskID); err != nil {
		return err
	}
	if report.FlowID != "" {
		if _, err := fmt.Fprintf(output, "flow: %s\n", report.FlowID); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(output, "verdict: %s\n", report.Verdict); err != nil {
		return err
	}
	for _, hash := range []struct{ name, value string }{
		{"task", report.Hashes.Task}, {"proposal", report.Hashes.Proposal},
		{"record", report.Hashes.Record}, {"bundle", report.Hashes.Bundle},
		{"evidence", report.Hashes.Evidence},
	} {
		if hash.value == "" {
			continue
		}
		if _, err := fmt.Fprintf(output, "%s hash: %s\n", hash.name, hash.value); err != nil {
			return err
		}
	}
	for _, condition := range report.BlockingConditions {
		if _, err := fmt.Fprintf(output, "blocking: %s: %s\n", condition.Code, condition.Message); err != nil {
			return err
		}
	}
	if report.AuthorizationImpact != "" {
		if _, err := fmt.Fprintf(output, "authorization impact: %s\n", report.AuthorizationImpact); err != nil {
			return err
		}
	}
	for _, drift := range report.Drifts {
		if _, err := fmt.Fprintf(output, "drift: %s (%s)\n", drift.Dimension, drift.AuthorizationImpact); err != nil {
			return err
		}
	}
	if report.NextRequiredApproval != "" {
		if _, err := fmt.Fprintf(output, "next required approval: %s\n", report.NextRequiredApproval); err != nil {
			return err
		}
	}
	if report.NextAuthorizedTransition != "" {
		if _, err := fmt.Fprintf(output, "next authorized transition: %s\n", report.NextAuthorizedTransition); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(output, "observed at: %s\n", report.ObservedAt.Format(time.RFC3339)); err != nil {
		return err
	}
	_, err := fmt.Fprintln(output, "state changed: false")
	return err
}
