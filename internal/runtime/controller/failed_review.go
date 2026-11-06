package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

const (
	DefaultFailedReviewObservationTimeout = 30 * time.Second
	MaxFailedReviewObservationTimeout     = 5 * time.Minute
)

type FailedReviewRequest struct {
	FlowID                 string   `json:"flowId"`
	DispatchID             string   `json:"dispatchId"`
	ExpectedRunnerID       string   `json:"expectedRunnerId"`
	PreviousProcessStopped bool     `json:"previousProcessStopped"`
	Actor                  string   `json:"actor"`
	Source                 string   `json:"source"`
	Reason                 string   `json:"reason"`
	Evidence               []string `json:"evidence"`
	ExpectedHash           string   `json:"expectedHash,omitempty"`
}

type FailedReviewReport struct {
	Version              int                          `json:"version"`
	StateChanged         bool                         `json:"stateChanged"`
	DispatchStatus       core.DispatchStatus          `json:"dispatchStatus"`
	QualityAccepted      bool                         `json:"qualityAccepted"`
	ExecutionAuthorized  bool                         `json:"executionAuthorized"`
	ValidationStatus     string                       `json:"validationStatus"`
	AccountingReconciled bool                         `json:"accountingReconciled"`
	Supplement           store.FailedReviewSupplement `json:"supplement"`
	Hash                 string                       `json:"hash"`
}

type FailedReviewTimingError struct {
	Version               int                     `json:"version"`
	Code                  string                  `json:"code"`
	Phase                 string                  `json:"phase"`
	Cause                 string                  `json:"cause"`
	Persistence           string                  `json:"persistence"`
	Message               string                  `json:"message"`
	ObservationTimeout    string                  `json:"observationTimeout,omitempty"`
	EffectiveDeadline     time.Time               `json:"effectiveDeadline,omitzero"`
	ElapsedMilliseconds   int64                   `json:"elapsedMilliseconds,omitempty"`
	StateChanged          bool                    `json:"stateChanged"`
	QualityAccepted       bool                    `json:"qualityAccepted"`
	ExecutionAuthorized   bool                    `json:"executionAuthorized"`
	CleanupFailed         bool                    `json:"cleanupFailed,omitempty"`
	Timing                core.FailedReviewTiming `json:"timing"`
	OriginalFlowDeadline  time.Time               `json:"originalFlowDeadline"`
	EffectiveFlowDeadline time.Time               `json:"effectiveFlowDeadline"`
	DeadlineSource        string                  `json:"deadlineSource"`
	ReservedSeconds       int64                   `json:"reservedSeconds"`
	FailedResultSource    string                  `json:"failedResultSource"`
	FailedChecks          []string                `json:"failedChecks"`
	RemainingEligibility  string                  `json:"remainingEligibility"`
}

type FailedReviewOperationError struct {
	Version             int       `json:"version"`
	Code                string    `json:"code"`
	Phase               string    `json:"phase"`
	Cause               string    `json:"cause"`
	Persistence         string    `json:"persistence"`
	Message             string    `json:"message"`
	CleanupFailed       bool      `json:"cleanupFailed,omitempty"`
	ObservationTimeout  string    `json:"observationTimeout,omitempty"`
	EffectiveDeadline   time.Time `json:"effectiveDeadline,omitzero"`
	ElapsedMilliseconds int64     `json:"elapsedMilliseconds,omitempty"`
	err                 error
}

func (e *FailedReviewOperationError) Error() string {
	return fmt.Sprintf("taxion: failed review observation: phase=%s cause=%s persistence=%s: %s",
		e.Phase, e.Cause, e.Persistence, e.Message)
}

func (e *FailedReviewOperationError) Unwrap() error { return e.err }

type failedReviewRejection struct{ reason string }

func (e *failedReviewRejection) Error() string { return e.reason }

func NewFailedReviewOperationError(phase string, persistence string, err error) *FailedReviewOperationError {
	code, message := "failed-review-operation", "The saved review could not be observed safely."
	if errors.Is(err, context.DeadlineExceeded) {
		code, message = "failed-review-observation-timeout", "The observation deadline expired."
	} else if errors.Is(err, context.Canceled) {
		code, message = "failed-review-observation-cancelled", "The observation was cancelled."
	} else {
		var rejected *failedReviewRejection
		if errors.As(err, &rejected) {
			code, message = "failed-review-rejected", rejected.reason
		}
	}
	switch persistence {
	case "NOT_WRITTEN":
		message += " No supplement was written."
	case "UNKNOWN":
		message += " Supplement persistence is unknown; inspect the saved record before continuing."
	case "COMMITTED":
		message += " The supplement committed."
	}
	return &FailedReviewOperationError{
		Version: 1, Code: code, Phase: phase, Cause: code, Persistence: persistence,
		Message: message, err: err,
	}
}

func (e *FailedReviewTimingError) Error() string {
	return "taxion: failed review supplement: native timing rejected: " + strings.Join(e.FailedChecks, ", ")
}

// RecordFailedReview appends only a sourced negative answer. It never finishes
// the Dispatch, changes usage, releases its owner, or executes host validations.
func (c *Controller) RecordFailedReview(ctx context.Context, request FailedReviewRequest, previewOnly bool) (FailedReviewReport, error) {
	return c.RecordFailedReviewWithTimeout(ctx, request, previewOnly, DefaultFailedReviewObservationTimeout)
}

func (c *Controller) RecordFailedReviewWithTimeout(ctx context.Context, request FailedReviewRequest, previewOnly bool, timeout time.Duration) (FailedReviewReport, error) {
	if timeout <= 0 || timeout > MaxFailedReviewObservationTimeout {
		return FailedReviewReport{}, NewFailedReviewOperationError("configuration", "NOT_WRITTEN",
			fmt.Errorf("observation timeout must be positive and at most five minutes"))
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	phase, persistence := "controller-lock", "NOT_WRITTEN"
	report, err := c.recordFailedReview(ctx, request, previewOnly, &phase, &persistence)
	if err != nil {
		failure := NewFailedReviewOperationError(phase, persistence, err)
		failure.ObservationTimeout = timeout.String()
		failure.EffectiveDeadline, _ = ctx.Deadline()
		failure.ElapsedMilliseconds = time.Since(started).Milliseconds()
		return FailedReviewReport{}, failure
	}
	return report, nil
}

func (c *Controller) recordFailedReview(ctx context.Context, request FailedReviewRequest, previewOnly bool, phase *string, persistence *string) (FailedReviewReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return FailedReviewReport{}, err
	}
	*phase = "request-validation"
	reject := func(message string) (FailedReviewReport, error) {
		return FailedReviewReport{}, &failedReviewRejection{reason: message}
	}
	for _, text := range append([]string{request.FlowID, request.DispatchID, request.Actor, request.Source, request.Reason}, request.Evidence...) {
		if text == "" || strings.TrimSpace(text) != text || core.Redact(text) != text {
			return reject("requires nonempty, trimmed, non-secret identities and evidence")
		}
	}
	if !request.PreviousProcessStopped || len(request.Evidence) == 0 {
		return reject("confirm the original process has stopped and supply its evidence")
	}
	seen := map[string]bool{}
	for _, evidence := range request.Evidence {
		if seen[evidence] {
			return reject("evidence references must be unique")
		}
		seen[evidence] = true
	}
	*phase = "initial-read"
	state, err := c.store.LoadWorkspaceContext(ctx)
	if err != nil {
		return FailedReviewReport{}, err
	}
	flow, found := state.Graph.ReviewFlow(request.FlowID)
	dispatch, ok := state.Graph.GraphDispatch(request.DispatchID)
	if !state.HasGraph || !found || !ok || flow.LastDispatchID() != dispatch.ID ||
		flow.Status != "RUNNING" && flow.Status != "STOPPED" ||
		flow.RunnerID != request.ExpectedRunnerID || dispatch.Status != core.DispatchFailed ||
		dispatch.Result == nil || dispatch.RuntimeSessionID == "" || dispatch.Recipient != "REVIEWER" ||
		dispatch.ReviewWorktreeSource == "" || dispatch.Configuration == nil ||
		c.runtime.Role() != dispatch.Recipient || c.runtime.ConfigHash() != dispatch.RuntimeConfigHash ||
		!sameWorktree(c.runtime.Worktree(), dispatch.Instruction.Worktree) || state.Records.Restore != nil {
		return reject("requires the exact latest FAILED managed snapshot Reviewer, saved owner, configuration and original workspace")
	}
	for _, supplement := range state.Records.FailedReviewSupplements {
		if supplement.DispatchID == dispatch.ID {
			return reject("a supplement is already recorded; inspect it instead of processing twice")
		}
	}
	task, _ := state.Graph.GraphTask(flow.TaskID)
	if state.Graph.IsArchived(task.ID) || state.Graph.IsArchived(dispatch.TaskID) ||
		!flowContractMatches(state.Graph, flow, task) {
		return reject("the original Task contract must remain unchanged and unarchived")
	}
	if err := validateResultReceipt(&state, dispatch); err != nil {
		return FailedReviewReport{}, err
	}
	failureAt, err := state.Records.FailedReviewReceiptAt(dispatch)
	if err != nil {
		return FailedReviewReport{}, err
	}
	if err := verifyStagePlanBinding(state.Records, flow); err != nil {
		return FailedReviewReport{}, err
	}
	identity, ok := c.runtime.(runtime.ConfigurationIdentity)
	if !ok || !reflect.DeepEqual(identity.ExecutionConfiguration(), dispatch.Configuration.Selection) {
		return reject("observation configuration differs from the saved execution")
	}
	reader, ok := c.runtime.(interface {
		ReadSavedTurn(context.Context, string) (runtime.SavedTurn, error)
	})
	if !ok {
		return reject("runtime cannot observe a saved native turn without execution")
	}
	*phase = "native-read"
	turn, err := reader.ReadSavedTurn(ctx, dispatch.RuntimeSessionID)
	if err != nil {
		return FailedReviewReport{}, err
	}
	if turn.SessionID != dispatch.RuntimeSessionID {
		return reject("native history belongs to a different session")
	}
	timing := core.FailedReviewTiming{
		DispatchCreatedAt: dispatch.CreatedAt, SubmittedAt: turn.SubmittedAt, FinishedAt: turn.FinishedAt,
		ShutdownAt: turn.ShutdownAt, FailedResultAt: failureAt,
		SubmissionDeadline: stageDeadline(flow, dispatch, false), ObservedAt: time.Now().UTC(),
	}
	if failed := timing.FailedChecks(); len(failed) != 0 {
		source := "flow.deadline"
		if len(flow.Extensions) != 0 {
			source = "flow.extensions[last].deadline"
		}
		var reserved int64
		if flow.StagePlan != nil && flow.StagePlan.Version == 1 {
			reserved = flow.StagePlan.ReservedSeconds(flow.Repairs, dispatch.Recipient)
		}
		return FailedReviewReport{}, &FailedReviewTimingError{
			Version: 1, Code: "failed-review-timing-rejected", Timing: timing,
			OriginalFlowDeadline: flow.Deadline, EffectiveFlowDeadline: flow.ExecutionDeadline(),
			DeadlineSource: source, ReservedSeconds: reserved, FailedResultSource: "verified-result.at",
			FailedChecks: failed, RemainingEligibility: "NOT_EVALUATED",
		}
	}
	if _, err := matchSavedTurn(state.Records, dispatch, turn); err != nil {
		return FailedReviewReport{}, err
	}
	usageProbe := dispatch
	usageProbe.RuntimeUsage = slices.Clone(dispatch.RuntimeUsage)
	if err := reconcileSavedUsage(&usageProbe, turn.UsageTotals, time.Now().UTC()); err != nil {
		return FailedReviewReport{}, err
	}
	if core.Redact(turn.Payload) != turn.Payload {
		return reject("saved answer contains credential-like material; preserve the native original without publishing it")
	}
	var answer core.DispatchResult
	if err := core.DecodeGraphInput(strings.NewReader(turn.Payload), &answer); err != nil {
		return FailedReviewReport{}, err
	}
	if bound, ok := c.runtime.(interface{ UsesBoundResultIdentity() bool }); ok && bound.UsesBoundResultIdentity() {
		if err := bindResultIdentity(dispatch, &answer); err != nil {
			return FailedReviewReport{}, err
		}
	}
	if err := dispatch.ValidateFailedReviewSupplement(&answer); err != nil {
		return FailedReviewReport{}, err
	}
	*phase = "prepared-git"
	if err := gitproof.VerifyPreparedReviewWorktreeContext(ctx, dispatch, flow.Contract.Worktree); err != nil {
		return FailedReviewReport{}, err
	}
	hash, err := core.CanonicalSHA256(dispatch)
	if err != nil {
		return FailedReviewReport{}, err
	}
	receiptsHash, err := state.Records.DispatchReceiptsHash(dispatch.ID)
	if err != nil {
		return FailedReviewReport{}, err
	}
	report := FailedReviewReport{
		Version: 1, DispatchStatus: core.DispatchFailed, ValidationStatus: "NOT_REPLAYED_NOT_ACCEPTED",
		Supplement: store.FailedReviewSupplement{
			Version: 1, FlowID: flow.ID, DispatchID: dispatch.ID, DispatchHash: hash, ReceiptHash: receiptsHash,
			HistoryHash: turn.HistoryHash, Answer: answer, NativeUsageTotals: turn.UsageTotals,
			SubmittedAt: turn.SubmittedAt, FinishedAt: turn.FinishedAt, ShutdownAt: turn.ShutdownAt,
			Actor: request.Actor, Source: request.Source, Reason: request.Reason, Evidence: request.Evidence,
			ExpectedRunnerID: request.ExpectedRunnerID,
		},
	}
	expected := request.ExpectedHash
	request.ExpectedHash = ""
	roleTask, _ := state.Graph.GraphTask(dispatch.TaskID)
	report.Hash, err = core.CanonicalSHA256(struct {
		Flow     core.ReviewFlow
		Task     core.GraphTask
		RoleTask core.GraphTask
		Request  FailedReviewRequest
		Report   FailedReviewReport
	}{flow, task, roleTask, request, report})
	if err != nil {
		return FailedReviewReport{}, err
	}
	report.Supplement.PreviewHash = report.Hash
	// Validate the record even for preview; At is assigned only on commit.
	probe := report.Supplement
	probe.At = time.Now().UTC()
	records := state.Records
	records.FailedReviewSupplements = append(slices.Clone(records.FailedReviewSupplements), probe)
	if err := records.VerifyFailedReviewSupplements(state.Graph); err != nil {
		return FailedReviewReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return FailedReviewReport{}, err
	}
	if previewOnly {
		return report, nil
	}
	if expected == "" || expected != report.Hash {
		return reject("preview changed; inspect --preview again and provide its expectedHash")
	}
	before, err := core.CanonicalSHA256(state)
	if err != nil {
		return FailedReviewReport{}, err
	}
	*phase = "persistence"
	outcome, err := c.store.AppendFailedReviewSupplementContext(ctx, before, &report.Supplement)
	*persistence = string(outcome)
	report.StateChanged = err == nil
	return report, err
}
