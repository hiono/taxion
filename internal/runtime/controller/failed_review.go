package controller

import (
	"context"
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
	StateChanged          bool                    `json:"stateChanged"`
	QualityAccepted       bool                    `json:"qualityAccepted"`
	ExecutionAuthorized   bool                    `json:"executionAuthorized"`
	Timing                core.FailedReviewTiming `json:"timing"`
	OriginalFlowDeadline  time.Time               `json:"originalFlowDeadline"`
	EffectiveFlowDeadline time.Time               `json:"effectiveFlowDeadline"`
	DeadlineSource        string                  `json:"deadlineSource"`
	ReservedSeconds       int64                   `json:"reservedSeconds"`
	FailedResultSource    string                  `json:"failedResultSource"`
	FailedChecks          []string                `json:"failedChecks"`
	RemainingEligibility  string                  `json:"remainingEligibility"`
}

func (e *FailedReviewTimingError) Error() string {
	return "taxion: failed review supplement: native timing rejected: " + strings.Join(e.FailedChecks, ", ")
}

// RecordFailedReview appends only a sourced negative answer. It never finishes
// the Dispatch, changes usage, releases its owner, or executes host validations.
func (c *Controller) RecordFailedReview(ctx context.Context, request FailedReviewRequest, previewOnly bool) (FailedReviewReport, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, DefaultLaneTimeout)
	defer cancel()
	reject := func(message string) (FailedReviewReport, error) {
		return FailedReviewReport{}, fmt.Errorf("taxion: failed review supplement: %s", message)
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
	state, err := c.store.LoadWorkspace()
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
	if operation := state.Records.Operation; operation != nil && operation.Active() && operation.FlowID != flow.ID {
		return reject("another operation is active")
	}
	for _, other := range state.Graph.Dispatches {
		if active(other.Status) {
			return reject("a Dispatch is still active")
		}
	}
	for _, other := range state.Graph.ReviewFlows {
		if other.ID != flow.ID && (other.Status == "RUNNING" || other.RunnerID != "") {
			return reject("another Flow is running or claimed")
		}
	}
	for _, other := range state.Ledger.Dispatches {
		if other.Active {
			return reject("a legacy Dispatch is still active")
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
		if flow.StagePlan != nil {
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
	if err := gitproof.VerifyPreparedReviewWorktree(dispatch, flow.Contract.Worktree); err != nil {
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
	report.Hash, err = core.CanonicalSHA256(struct {
		State   store.Workspace
		Request FailedReviewRequest
		Report  FailedReviewReport
	}{state, request, report})
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
	err = c.store.UpdateWorkspace(func(current *store.Workspace) error {
		hash, err := core.CanonicalSHA256(*current)
		if err != nil || hash != before {
			return fmt.Errorf("taxion: workspace changed during saved review observation; preview again")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		report.Supplement.At = time.Now().UTC()
		current.Records.FailedReviewSupplements = append(current.Records.FailedReviewSupplements, report.Supplement)
		return nil
	})
	report.StateChanged = err == nil
	return report, err
}
