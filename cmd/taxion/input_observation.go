package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/store"
)

func inputObservationCommand(action string, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("workflow "+action, flag.ContinueOnError)
	fs.SetOutput(output)
	file := fs.String("file", "", "explicit observation or input assessment JSON")
	preview := false
	if action == "observe-input" {
		fs.BoolVar(&preview, "preview", false, "inspect exact input and observation authority; no database or retained-ref writes")
	}
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage of workflow "+action+":")
		fs.PrintDefaults()
		fmt.Fprintln(fs.Output(), `observe-input JSON:
taskId, optional flowId and dispatchId, scope (explicit read/capture paths),
targetSha (exact current HEAD), actor, source, reason, evidence (redacted strings),
noActiveExecutionConfirmed:true, acceptDeliveryUncertainty:true.
configuration may supply the exact non-secret resolved repository configuration;
when omitted the saved Dispatch selection is reused. Without either, observation
is blocked. Existing forbidden paths and file-access restrictions are never relaxed.
If flowId is supplied it must be stopped/held with no runner or unresolved feedback;
dispatchId must be its latest terminal Dispatch with an exact controller result receipt.
Preview returns observation.hash. To record the exact observation, include expectedHash.
This acquires evidence NOW; it never rewrites the old result, declares Coder success,
proves authorship or stop-time identity, replays a session, or grants execution.
Old forbidden paths and file-access restrictions still apply. Git objects may be
created using a private index; files, real index and branches are unchanged.
assess-input JSON: taskId. Returns the full typed decision for the saved proposal.
Use observationHash in BOTH conversation intake and confirm to bind a new
purpose-specific contract to the recorded input. For source Flows retain supersedes,
createdFrom and replacesFlow, plus the exact feedback target when a Dispatch exists.
Ordinary plan/start/validate-input/run follow only after exact contract approval.
No Coder/Reviewer model or validation command starts in either operation.`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || fs.NArg() != 0 {
		return fmt.Errorf("workflow %s requires --file; --preview is only for observe-input", action)
	}
	input, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer input.Close()
	if _, err := os.Stat(workspaceDBPath()); err != nil {
		return err
	}
	var db *store.Store
	if preview || action == "assess-input" {
		db, err = store.OpenReadOnly(workspaceDBPath())
	} else {
		db, err = store.Open(workspaceDBPath())
	}
	if err != nil {
		return err
	}
	defer db.Close()
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if action == "assess-input" {
		var request struct {
			TaskID string `json:"taskId"`
		}
		if err := core.DecodeGraphInput(input, &request); err != nil {
			return err
		}
		report, err := controller.AssessInput(db, request.TaskID, nil)
		if err != nil {
			return err
		}
		if err := encoder.Encode(report); err != nil {
			return err
		}
		return report.Err()
	}
	var request controller.InputObservationRequest
	if err := core.DecodeGraphInput(input, &request); err != nil {
		return err
	}
	report, err := controller.ObserveInput(db, request, preview)
	var decisionError *core.InputDecisionError
	if err != nil && !errors.As(err, &decisionError) {
		return err
	}
	if encodeErr := encoder.Encode(report); encodeErr != nil {
		return encodeErr
	}
	return err
}
