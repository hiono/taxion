package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime/controller"
	"github.com/hiono/taxion/internal/runtime/copilot"
	"github.com/hiono/taxion/internal/store"
)

func recordFailedReviewCommand(ctx context.Context, args []string, output io.Writer) (resultErr error) {
	phase := "cli-setup"
	persistence := "NOT_WRITTEN"
	var closers []func() error
	diagnosticWritten := false
	defer func() {
		if err := closeFailedReviewResources(&closers); err != nil {
			resultErr = failedReviewCleanupFailure(resultErr, phase, persistence, err)
			diagnosticWritten = false
		}
		if resultErr != nil && !errors.Is(resultErr, flag.ErrHelp) && !diagnosticWritten {
			var failure *controller.FailedReviewOperationError
			if !errors.As(resultErr, &failure) {
				resultErr = controller.NewFailedReviewOperationError(phase, persistence, resultErr)
			}
			resultErr = writeFailedReviewReport(output, controller.FailedReviewReport{}, resultErr)
		}
	}()
	fs := flag.NewFlagSet("workflow record-failed-review", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := fs.String("file", "", "JSON sourced decision for the exact failed Reviewer")
	preview := fs.Bool("preview", false, "observe and verify without changing the workspace")
	timeout := fs.Duration("observation-timeout", controller.DefaultFailedReviewObservationTimeout, "maximum saved-review observation time (positive, at most 5m)")
	cli := fs.String("cli", "", "original Copilot CLI path")
	base := fs.String("base-directory", "", "original Copilot base directory")
	fs.Usage = func() {
		fmt.Fprintln(output, "Usage of workflow record-failed-review:")
		fs.SetOutput(output)
		fs.PrintDefaults()
		fs.SetOutput(io.Discard)
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
Answer and turn end must not follow the original verified FAILED result receipt.
Routine shutdown may follow that failure, but must precede the saved submission
deadline and not be in the future, with no later main-agent or tool execution.
--observation-timeout bounds Controller observation (default 30s; positive, at
most 5m) and never extends an earlier caller deadline. The bound starts before
the Controller lock and covers cancellable native, Git and SQLite transaction
work. Controller/store mutex waits may exceed it. CLI file/store reads, store open/migration,
Copilot runtime setup, operation lease acquisition and cleanup/output are outside this bound; no exact
full-command wall-clock cap is promised. Setup and operation failures emit
versioned, redacted phase and persistence diagnostics. UNKNOWN requires record
inspection; matching expectedHash alone does not establish delivery.
Persistence codes are NOT_WRITTEN, UNKNOWN and COMMITTED; a committed append
remains COMMITTED if later output fails.
Timing rejection prints non-secret JSON with the effective submission deadline,
its source, reserved seconds, failed-result receipt time and failedChecks.
remainingEligibility:NOT_EVALUATED means later identity/answer/tree checks did not run.
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
	if *timeout <= 0 || *timeout > controller.MaxFailedReviewObservationTimeout {
		phase = "configuration"
		return fmt.Errorf("observation timeout must be positive and at most five minutes")
	}
	if *file == "" || fs.NArg() != 0 {
		return fmt.Errorf("workflow record-failed-review requires --file and no positional arguments")
	}
	phase = "file-open"
	input, err := os.Open(*file)
	if err != nil {
		return err
	}
	phase = "input-decode"
	var request controller.FailedReviewRequest
	decodeErr := core.DecodeGraphInput(input, &request)
	closeErr := input.Close()
	if decodeErr != nil {
		return decodeErr
	}
	if closeErr != nil {
		phase = "cleanup"
		return closeErr
	}
	if !*preview && request.ExpectedHash == "" {
		return fmt.Errorf("workflow record-failed-review requires expectedHash from --preview")
	}
	var db *store.Store
	phase = "store-open"
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
	closers = append(closers, db.Close)
	if !*preview {
		phase = "operation-lease"
		lease, err := db.LockOperation()
		if err != nil {
			return err
		}
		closers = append(closers, lease.Close)
	}
	phase = "initial-read"
	state, err := db.LoadWorkspace()
	if err != nil {
		return err
	}
	dispatch, ok := state.Graph.GraphDispatch(request.DispatchID)
	if !ok {
		return fmt.Errorf("workflow record-failed-review requires an existing Dispatch")
	}
	phase = "runtime-setup"
	worker, err := copilot.NewForFailedReview(dispatch, *cli, *base)
	if err != nil {
		return err
	}
	closers = append(closers, worker.Close)
	control, err := controller.New(db, worker)
	if err != nil {
		return err
	}
	phase = "observation"
	report, err := control.RecordFailedReviewWithTimeout(ctx, request, *preview, *timeout)
	if report.StateChanged {
		persistence = string(store.FailedReviewCommitted)
	}
	var failure *controller.FailedReviewOperationError
	if errors.As(err, &failure) {
		persistence = failure.Persistence
	}
	if cleanupErr := closeFailedReviewResources(&closers); cleanupErr != nil {
		return failedReviewCleanupFailure(err, phase, persistence, cleanupErr)
	}
	resultErr = writeFailedReviewReport(output, report, err)
	diagnosticWritten = err != nil
	return resultErr
}

func failedReviewCleanupFailure(primaryErr error, phase, persistence string, cleanupErr error) error {
	cleanup := controller.NewFailedReviewOperationError("cleanup", persistence, cleanupErr)
	if primaryErr == nil {
		return cleanup
	}
	var primary *controller.FailedReviewOperationError
	if !errors.As(primaryErr, &primary) {
		primary = controller.NewFailedReviewOperationError(phase, persistence, primaryErr)
	}
	copy := *primary
	copy.CleanupFailed = true
	return errors.Join(&copy, cleanup)
}

func closeFailedReviewResources(closers *[]func() error) error {
	var errs []error
	for index := len(*closers) - 1; index >= 0; index-- {
		errs = append(errs, (*closers)[index]())
	}
	*closers = nil
	return errors.Join(errs...)
}

func writeFailedReviewReport(output io.Writer, report controller.FailedReviewReport, err error) error {
	var timing *controller.FailedReviewTimingError
	if errors.As(err, &timing) {
		copy := *timing
		var failure *controller.FailedReviewOperationError
		if !errors.As(err, &failure) {
			failure = controller.NewFailedReviewOperationError("native-read", "NOT_WRITTEN", err)
		}
		copy.Phase = failure.Phase
		copy.Cause = "timing-rejected"
		copy.Persistence = failure.Persistence
		copy.Message = "The saved reviewer timing is not eligible for this append."
		copy.CleanupFailed = copy.CleanupFailed || failure.CleanupFailed
		copy.ObservationTimeout = failure.ObservationTimeout
		copy.EffectiveDeadline = failure.EffectiveDeadline
		copy.ElapsedMilliseconds = failure.ElapsedMilliseconds
		if encodeErr := json.NewEncoder(output).Encode(&copy); encodeErr != nil {
			return errors.Join(err, controller.NewFailedReviewOperationError("output", "NOT_WRITTEN", encodeErr))
		}
		return err
	}
	if err != nil {
		var failure *controller.FailedReviewOperationError
		if !errors.As(err, &failure) {
			failure = controller.NewFailedReviewOperationError("observation", "NOT_WRITTEN", err)
		}
		return writeFailedReviewDiagnostic(output, failure)
	}
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		persistence := "NOT_WRITTEN"
		if report.StateChanged {
			persistence = "COMMITTED"
		}
		return controller.NewFailedReviewOperationError("output", persistence, err)
	}
	return nil
}

func writeFailedReviewDiagnostic(output io.Writer, failure *controller.FailedReviewOperationError) error {
	if err := json.NewEncoder(output).Encode(failure); err != nil {
		return errors.Join(failure, controller.NewFailedReviewOperationError("output", failure.Persistence, err))
	}
	return failure
}
