package store

import (
	"fmt"
	"reflect"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// StoppedDispatchReconciliation retains the original stop and input contract,
// not a claim that the cancelled Dispatch produced or never executed that input.
type StoppedDispatchReconciliation struct {
	PreviewHash                string              `json:"previewHash"`
	InputContractHash          string              `json:"inputContractHash"`
	Actor                      string              `json:"actor"`
	Source                     string              `json:"source"`
	Reason                     string              `json:"reason"`
	Evidence                   []string            `json:"evidence"`
	NoActiveExecutionConfirmed bool                `json:"noActiveExecutionConfirmed"`
	AcceptDeliveryUncertainty  bool                `json:"acceptDeliveryUncertainty"`
	DeliveryStatus             string              `json:"deliveryStatus"`
	PreviousFlow               core.ReviewFlow     `json:"previousFlow"`
	PreviousDispatch           core.GraphDispatch  `json:"previousDispatch"`
	Target                     core.FeedbackTarget `json:"target"`
	At                         time.Time           `json:"at"`
}

func (r Records) VerifyDispatchReconciliations(graph core.TaskGraph) error {
	seen := map[string]bool{}
	for _, audit := range r.DispatchReconciliations {
		prior := audit.PreviousFlow
		before := audit.PreviousDispatch
		flow, found := graph.ReviewFlow(prior.ID)
		dispatch, exists := graph.GraphDispatch(before.ID)
		inputHash, err := core.CanonicalSHA256(prior.Contract)
		target, targetErr := core.ObserveFeedbackTarget(dispatch)
		if seen[prior.ID] || !found || !exists || err != nil || targetErr != nil ||
			!recordHash(audit.PreviewHash) ||
			audit.InputContractHash != inputHash || audit.Actor == "" || audit.Source == "" ||
			audit.Reason == "" || len(audit.Evidence) == 0 || !audit.NoActiveExecutionConfirmed || !audit.AcceptDeliveryUncertainty ||
			audit.DeliveryStatus != "unknown" || audit.At.IsZero() ||
			prior.Status != "STOPPED" || prior.StopCode != "SAFETY_STOP" || !prior.HandoffHeld() ||
			prior.RunnerID != "" || len(prior.DispatchIDs) != 1 || prior.DispatchIDs[0] != before.ID ||
			before.TaskID != prior.TaskID || before.Recipient != "CODER" || before.Status != core.DispatchIssued ||
			before.Acknowledged || before.ExecutionStarted || before.RuntimeSessionID != "" || before.Result != nil ||
			before.RuntimeConfigHash != "" || before.ExecutionStartSHA != "" || len(before.RuntimeUsage) != 0 ||
			prior.Contract.OutputMode != core.OutputModeWorktreeTreeSnapshot || prior.Contract.TargetTreeSHA == "" ||
			!reflect.DeepEqual(flow, prior) || target != audit.Target ||
			dispatch.Status != core.DispatchCancelled || dispatch.Result == nil ||
			dispatch.Result.OutputSHA != "" || dispatch.Result.OutputTreeSHA != "" ||
			dispatch.Result.GitEvidence != nil || dispatch.Result.PartialGitEvidence != nil {
			return fmt.Errorf("invalid stopped Dispatch reconciliation audit")
		}
		expectedResult := core.DispatchResult{
			DispatchID: before.ID, TaskID: before.TaskID, Recipient: before.Recipient,
			Repository: before.Repository, Status: core.DispatchCancelled, StartedSHA: before.ExpectedStartSHA(),
			ChangedFiles: []string{},
			Evidence:     append([]string{"reconciled-stop:" + audit.PreviewHash, "delivery:unknown"}, audit.Evidence...),
			Blockers:     []string{audit.Reason}, NextTransition: core.Blocked,
		}
		if !reflect.DeepEqual(*dispatch.Result, expectedResult) {
			return fmt.Errorf("reconciled cancellation differs from its human decision and unknown-delivery evidence")
		}
		expected := before
		expected.Status, expected.Result, expected.UpdatedAt = dispatch.Status, dispatch.Result, dispatch.UpdatedAt
		expected.LastUpdatedBy, expected.LastTransition = audit.Actor, audit.Source+": "+audit.Reason
		if !reflect.DeepEqual(expected, dispatch) {
			return fmt.Errorf("reconciled Dispatch differs from its original issued instruction")
		}
		seen[prior.ID] = true
	}
	return nil
}
