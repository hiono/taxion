package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// FailedReviewSupplement retains a negative answer without replacing the failure
// or claiming that its model-reported checks were independently validated.
type FailedReviewSupplement struct {
	Version           int                  `json:"version"`
	FlowID            string               `json:"flowId"`
	DispatchID        string               `json:"dispatchId"`
	DispatchHash      string               `json:"dispatchHash"`
	ReceiptHash       string               `json:"receiptHash"`
	HistoryHash       string               `json:"historyHash"`
	Answer            core.DispatchResult  `json:"answer"`
	NativeUsageTotals []core.DispatchUsage `json:"nativeUsageTotals,omitempty"`
	SubmittedAt       time.Time            `json:"submittedAt"`
	FinishedAt        time.Time            `json:"finishedAt"`
	ShutdownAt        time.Time            `json:"shutdownAt"`
	Actor             string               `json:"actor"`
	Source            string               `json:"source"`
	Reason            string               `json:"reason"`
	Evidence          []string             `json:"evidence"`
	ExpectedRunnerID  string               `json:"expectedRunnerId"`
	PreviewHash       string               `json:"previewHash"`
	At                time.Time            `json:"at"`
}

func (r Records) VerifyFailedReviewSupplements(graph core.TaskGraph) error {
	seen := map[string]bool{}
	for _, supplement := range r.FailedReviewSupplements {
		flow, found := graph.ReviewFlow(supplement.FlowID)
		dispatch, ok := graph.GraphDispatch(supplement.DispatchID)
		hash, err := core.CanonicalSHA256(dispatch)
		receiptsHash, receiptErr := r.DispatchReceiptsHash(dispatch.ID)
		failureAt, failureErr := r.FailedReviewReceiptAt(dispatch)
		deadline := flow.ExecutionDeadline()
		if flow.StagePlan != nil {
			deadline = flow.StagePlan.SubmissionDeadline(flow, dispatch.Recipient)
		}
		if supplement.Version != 1 || !found || !ok || seen[dispatch.ID] ||
			flow.LastDispatchID() != dispatch.ID || hash != supplement.DispatchHash || err != nil ||
			receiptErr != nil || receiptsHash != supplement.ReceiptHash ||
			failureErr != nil || failureAt.After(supplement.At) ||
			!recordHash(supplement.PreviewHash) ||
			!strings.HasPrefix(supplement.HistoryHash, "sha256:") ||
			!recordHash(strings.TrimPrefix(supplement.HistoryHash, "sha256:")) ||
			supplement.At.IsZero() || supplement.SubmittedAt.Before(dispatch.CreatedAt) ||
			supplement.FinishedAt.Before(supplement.SubmittedAt) ||
			supplement.FinishedAt.After(supplement.At) || len(supplement.Evidence) == 0 ||
			!supplement.FinishedAt.Before(deadline) ||
			supplement.ShutdownAt.IsZero() || supplement.ShutdownAt.Before(supplement.FinishedAt) ||
			!supplement.ShutdownAt.Before(deadline) || supplement.ShutdownAt.After(failureAt) ||
			dispatch.ValidateFailedReviewSupplement(&supplement.Answer) != nil {
			return fmt.Errorf("invalid failed review supplement or changed original Dispatch")
		}
		references := map[string]bool{}
		for _, evidence := range supplement.Evidence {
			if references[evidence] {
				return fmt.Errorf("failed review supplement evidence must be unique")
			}
			references[evidence] = true
		}
		for _, text := range append([]string{supplement.Actor, supplement.Source, supplement.Reason}, supplement.Evidence...) {
			if text == "" || strings.TrimSpace(text) != text || core.Redact(text) != text {
				return fmt.Errorf("failed review supplement requires sourced non-secret evidence")
			}
		}
		seen[dispatch.ID] = true
	}
	return nil
}

func (r Records) DispatchReceiptsHash(dispatchID string) (string, error) {
	var receipts []RuntimeReceipt
	for _, receipt := range r.Events {
		if receipt.DispatchID == dispatchID {
			receipts = append(receipts, receipt)
		}
	}
	return core.CanonicalSHA256(receipts)
}

func (r Records) FailedReviewReceiptAt(dispatch core.GraphDispatch) (time.Time, error) {
	var latest *RuntimeReceipt
	for _, receipt := range r.Events {
		if receipt.DispatchID == dispatch.ID && receipt.Type == "verified-result" {
			latest = &receipt
		}
	}
	body, err := json.Marshal(struct {
		Result *core.DispatchResult
		Usage  []core.DispatchUsage
	}{dispatch.Result, dispatch.RuntimeUsage})
	if err != nil || dispatch.Status != core.DispatchFailed || dispatch.Result == nil ||
		dispatch.Result.Status != core.DispatchFailed || latest == nil || latest.At.IsZero() ||
		latest.SessionID != dispatch.RuntimeSessionID || latest.StreamID != "" ||
		latest.Hash != fmt.Sprintf("%x", sha256.Sum256(body)) || latest.Payload != string(body) {
		return time.Time{}, fmt.Errorf("failed review requires its original bound FAILED result receipt")
	}
	return latest.At, nil
}
