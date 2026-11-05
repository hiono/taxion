package store

import (
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
		deadline := flow.ExecutionDeadline()
		if flow.StagePlan != nil {
			deadline = flow.StagePlan.SubmissionDeadline(flow, dispatch.Recipient)
		}
		if supplement.Version != 1 || !found || !ok || seen[dispatch.ID] ||
			flow.LastDispatchID() != dispatch.ID || hash != supplement.DispatchHash || err != nil ||
			receiptErr != nil || receiptsHash != supplement.ReceiptHash ||
			!recordHash(supplement.PreviewHash) ||
			!strings.HasPrefix(supplement.HistoryHash, "sha256:") ||
			!recordHash(strings.TrimPrefix(supplement.HistoryHash, "sha256:")) ||
			supplement.At.IsZero() || supplement.SubmittedAt.Before(dispatch.CreatedAt) ||
			supplement.FinishedAt.Before(supplement.SubmittedAt) ||
			supplement.FinishedAt.After(supplement.At) || len(supplement.Evidence) == 0 ||
			!supplement.FinishedAt.Before(deadline) ||
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
