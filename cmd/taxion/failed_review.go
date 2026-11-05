package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/store"
)

func recordFailedReviewCommand(ctx context.Context, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("workflow record-failed-review", flag.ContinueOnError)
	fs.SetOutput(output)
	file := fs.String("file", "", "JSON sourced decision for the exact failed Reviewer")
	preview := fs.Bool("preview", false, "observe and verify without changing the workspace")
	cli := fs.String("cli", "", "original Copilot CLI path")
	base := fs.String("base-directory", "", "original Copilot base directory")
	fs.Usage = func() {
		fmt.Fprintln(output, "Usage of workflow record-failed-review:")
		fs.PrintDefaults()
		fmt.Fprintln(output, `
JSON requires flowId, dispatchId, expectedRunnerId (exact saved value, possibly ""),
previousProcessStopped:true, actor, source, reason and unique non-secret evidence.
Confirm the old process has stopped; its saved owner is not a liveness observation.
Use --preview first. After approving that exact supplement, add its hash as
expectedHash to the same JSON and invoke without --preview.
Only the latest FAILED managed snapshot Reviewer is supported. The original
Copilot session must have a bound final REQUEST_CHANGES answer, matching turn end
and routine shutdown before its original submission deadline, plus matching
durable execution observations. Caller-authored answer files are not accepted.
The exact prepared Reviewer tree must remain available and unchanged.
This appends a formal negative-answer supplement only. FAILED result, Flow, Task,
owner, usage and existing receipts remain unchanged. No model send, host validation
replay, worktree release, repair, quality approval or completion is authorized.
Model-reported validations and shutdown totals are retained as supplemental
evidence, not accepted host checks or a reconciled spending allowance.
RUNNING recovery uses recover; CANCELLED artifact successors use recover-review.
APPROVE, BLOCKED, restored workspaces and released/changed review trees are rejected.`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || fs.NArg() != 0 {
		return fmt.Errorf("workflow record-failed-review requires --file and no positional arguments")
	}
	input, err := os.Open(*file)
	if err != nil {
		return err
	}
	defer input.Close()
	var request controller.FailedReviewRequest
	if err := core.DecodeGraphInput(input, &request); err != nil {
		return err
	}
	if !*preview && request.ExpectedHash == "" {
		return fmt.Errorf("workflow record-failed-review requires expectedHash from --preview")
	}
	var db *store.Store
	if *preview {
		db, err = store.OpenReadOnly(workspaceDBPath())
	} else {
		if _, err := os.Stat(workspaceDBPath()); err != nil {
			return err
		}
		db, err = store.Open(workspaceDBPath())
	}
	if err != nil {
		return err
	}
	defer db.Close()
	if !*preview {
		lease, err := db.LockOperation()
		if err != nil {
			return err
		}
		defer lease.Close()
	}
	state, err := db.LoadWorkspace()
	if err != nil {
		return err
	}
	dispatch, ok := state.Graph.GraphDispatch(request.DispatchID)
	if !ok {
		return fmt.Errorf("workflow record-failed-review requires an existing Dispatch")
	}
	worker, err := copilot.NewForFailedReview(dispatch, *cli, *base)
	if err != nil {
		return err
	}
	defer worker.Close()
	control, err := controller.New(db, worker)
	if err != nil {
		return err
	}
	report, err := control.RecordFailedReview(ctx, request, *preview)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
