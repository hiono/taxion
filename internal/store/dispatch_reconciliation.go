package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
)

// StoppedDispatchReconciliation retains the original stop and input contract,
// not a claim that the cancelled Dispatch produced or never executed that input.
type StoppedDispatchReconciliation struct {
	Kind                       string                    `json:"kind,omitempty"`
	DeadlineRecontract         *ReviewDeadlineRecontract `json:"deadlineRecontract,omitempty"`
	PreviewHash                string                    `json:"previewHash"`
	InputContractHash          string                    `json:"inputContractHash"`
	Actor                      string                    `json:"actor"`
	Source                     string                    `json:"source"`
	Reason                     string                    `json:"reason"`
	Evidence                   []string                  `json:"evidence"`
	NoActiveExecutionConfirmed bool                      `json:"noActiveExecutionConfirmed"`
	AcceptDeliveryUncertainty  bool                      `json:"acceptDeliveryUncertainty"`
	DeliveryStatus             string                    `json:"deliveryStatus"`
	PreviousFlow               core.ReviewFlow           `json:"previousFlow"`
	PreviousDispatch           core.GraphDispatch        `json:"previousDispatch"`
	Target                     core.FeedbackTarget       `json:"target"`
	At                         time.Time                 `json:"at"`
}

// ReviewDeadlineRecontract authorizes a successor deadline, never a rewrite of
// the stopped Flow's deadline or authority to replay its execution.
type ReviewDeadlineRecontract struct {
	PreviousDeadline time.Time `json:"previousDeadline"`
	Deadline         time.Time `json:"deadline"`
	Reason           string    `json:"reason"`
}

func (d ReviewDeadlineRecontract) Validate(previous, at time.Time) error {
	if !d.PreviousDeadline.Equal(previous) || !d.Deadline.After(previous) || !d.Deadline.After(at) ||
		strings.TrimSpace(d.Reason) == "" || strings.TrimSpace(d.Reason) != d.Reason || core.Redact(d.Reason) != d.Reason {
		return fmt.Errorf("invalid review deadline recontract: require exact previous deadline, future later deadline and sourced non-secret reason")
	}
	return nil
}

func (a StoppedDispatchReconciliation) ArtifactReviewer() bool {
	return a.Kind == "" && a.PreviousFlow.StoppedArtifactReviewer(a.PreviousDispatch) ||
		a.Kind == "executed-artifact-review-continuation-v1" && a.PreviousFlow.StoppedExecutedArtifactReviewer(a.PreviousDispatch)
}

func (r Records) VerifyDispatchReconciliations(graph core.TaskGraph) error {
	seen := map[string]bool{}
	cancelled := map[string]bool{}
	for _, audit := range r.DispatchReconciliations {
		prior := audit.PreviousFlow
		before := audit.PreviousDispatch
		artifactReviewer := prior.StoppedArtifactReviewer(before)
		coderReviewer := graph.StoppedCoderReviewer(prior, before)
		firstCoder := graph.StoppedFirstCoder(prior, before)
		reviewer := artifactReviewer || coderReviewer
		flow, found := graph.ReviewFlow(prior.ID)
		dispatch, exists := graph.GraphDispatch(before.ID)
		inputHash, err := core.CanonicalSHA256(prior.Contract)
		target, targetErr := core.ObserveFeedbackTarget(dispatch)
		if audit.DeadlineRecontract != nil {
			if !audit.ArtifactReviewer() || audit.DeadlineRecontract.Validate(prior.ExecutionDeadline(), audit.At) != nil {
				return fmt.Errorf("invalid artifact Reviewer deadline recontract audit")
			}
		}
		if audit.Kind != "" {
			if audit.Kind != "executed-artifact-review-continuation-v1" || seen[prior.ID] ||
				!found || !exists || err != nil || targetErr != nil || audit.InputContractHash != inputHash ||
				!recordHash(audit.PreviewHash) || audit.Actor == "" || audit.Source == "" || audit.Reason == "" ||
				len(audit.Evidence) == 0 || !audit.NoActiveExecutionConfirmed || !audit.AcceptDeliveryUncertainty ||
				audit.DeliveryStatus != "execution-observed-stop-confirmed-usage-may-be-incomplete" ||
				audit.At.IsZero() || !audit.At.Before(prior.ExecutionDeadline()) && audit.DeadlineRecontract == nil ||
				!prior.StoppedExecutedArtifactReviewer(before) || !reflect.DeepEqual(flow, prior) ||
				!reflect.DeepEqual(dispatch, before) || target != audit.Target {
				return fmt.Errorf("invalid executed artifact Reviewer continuation audit")
			}
			verifiedStop := false
			for _, event := range r.Events {
				if event.DispatchID != before.ID || event.Type != "verified-result" {
					continue
				}
				var receipt struct {
					Result *core.DispatchResult
					Usage  []core.DispatchUsage
				}
				if json.Unmarshal([]byte(event.Payload), &receipt) == nil &&
					event.SessionID == before.RuntimeSessionID &&
					event.Hash == fmt.Sprintf("%x", sha256.Sum256([]byte(event.Payload))) &&
					!event.At.After(audit.At) && reflect.DeepEqual(receipt.Result, before.Result) &&
					reflect.DeepEqual(receipt.Usage, before.RuntimeUsage) {
					verifiedStop = true
				}
			}
			if !verifiedStop {
				return fmt.Errorf("executed Reviewer continuation lacks its exact controller stop receipt and usage")
			}
			seen[prior.ID] = true
			continue
		}
		if seen[prior.ID] || !found || !exists || err != nil || targetErr != nil ||
			!recordHash(audit.PreviewHash) ||
			audit.InputContractHash != inputHash || audit.Actor == "" || audit.Source == "" ||
			audit.Reason == "" || len(audit.Evidence) == 0 || !audit.NoActiveExecutionConfirmed || !audit.AcceptDeliveryUncertainty ||
			audit.DeliveryStatus != "unknown" || audit.At.IsZero() ||
			prior.Status != "STOPPED" || (!reviewer && !firstCoder) || !prior.HandoffHeld() ||
			prior.RunnerID != "" || (!coderReviewer && (len(prior.DispatchIDs) != 1 || prior.DispatchIDs[0] != before.ID)) ||
			(!reviewer && (before.TaskID != prior.TaskID || before.Recipient != "CODER")) ||
			before.Status != core.DispatchIssued ||
			before.Acknowledged || before.ExecutionStarted || before.RuntimeSessionID != "" || before.Result != nil ||
			before.RuntimeConfigHash != "" || before.ExecutionStartSHA != "" || len(before.RuntimeUsage) != 0 ||
			prior.Contract.OutputMode != core.OutputModeWorktreeTreeSnapshot ||
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
		cancelled[before.ID] = true
	}
	for _, dispatch := range graph.Dispatches {
		if dispatch.Result != nil && !cancelled[dispatch.ID] &&
			slices.ContainsFunc(dispatch.Result.Evidence, func(e string) bool { return strings.HasPrefix(e, "reconciled-stop:") }) {
			return fmt.Errorf("reconciled cancellation lacks its exact saved reconciliation audit")
		}
	}
	return nil
}
