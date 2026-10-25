package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/store"
)

const reviewProgressReceipt = "review-progress"
const reviewProgressStream = "controller:review-progress"

// SaveReviewProgressForSession persists explicit reports before cancellation,
// without finishing the Dispatch or granting further execution authority.
func (c *Controller) SaveReviewProgressForSession(ctx context.Context, dispatchID, sessionID string, progress core.ReviewProgress) (string, error) {
	var reference string
	c.mu.Lock()
	defer c.mu.Unlock()
	err := c.store.UpdateWorkspace(func(state *store.Workspace) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		if !ok || dispatch.Status != core.DispatchRunning || dispatch.Recipient != "REVIEWER" ||
			sessionID == "" || dispatch.RuntimeSessionID != sessionID ||
			c.runtime.Role() != dispatch.Recipient || c.runtime.ConfigHash() != dispatch.RuntimeConfigHash ||
			!sameWorktree(dispatch.Instruction.Worktree, c.runtime.Worktree()) {
			return fmt.Errorf("taxion: review progress requires its running bound Reviewer session")
		}
		if flow, exists := flowForDispatch(state.Graph, dispatch.ID); exists &&
			(flow.Status != "RUNNING" || !time.Now().UTC().Before(flow.Deadline)) {
			return fmt.Errorf("taxion: review progress requires an active unexpired flow")
		}
		coder, ok := state.Graph.GraphDispatch(dispatch.RelatedID)
		if !ok {
			return fmt.Errorf("taxion: review progress Coder source is missing")
		}
		if err := progress.Validate(dispatch, coder); err != nil {
			return err
		}
		redact := func(values []string) {
			for i := range values {
				values[i] = core.Redact(values[i])
			}
		}
		redact(progress.ReviewedScope)
		redact(progress.UnreviewedScope)
		redact(progress.Evidence)
		for i := range progress.Findings {
			finding := &progress.Findings[i]
			finding.Title, finding.File, finding.Lines = core.Redact(finding.Title), core.Redact(finding.File), core.Redact(finding.Lines)
			redact(finding.Evidence)
		}
		if err := progress.Validate(dispatch, coder); err != nil {
			return err
		}
		if err := validateResultReceipt(state, coder); err != nil {
			return err
		}
		if _, err := gitproof.ObserveStart(dispatch); err != nil {
			return fmt.Errorf("taxion: review progress target changed: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		_, count, err := savedReviewProgress(*state, dispatch, coder)
		if err != nil {
			return err
		}
		if count >= core.ReviewProgressMaxRecords {
			return fmt.Errorf("taxion: review progress record limit reached")
		}
		body, err := json.Marshal(progress)
		if err != nil {
			return err
		}
		hash := receiptHash(string(body))
		state.Records.Events = append(state.Records.Events, store.RuntimeReceipt{
			DispatchID: dispatch.ID, SessionID: sessionID, StreamID: reviewProgressStream,
			Sequence: uint64(count + 1), Type: reviewProgressReceipt, Payload: string(body),
			Hash: hash, At: time.Now().UTC(),
		})
		reference = reviewProgressReceipt + ":" + hash
		return nil
	})
	return reference, err
}

func savedReviewProgress(state store.Workspace, reviewer, coder core.GraphDispatch) (*store.RuntimeReceipt, int, error) {
	var latest *store.RuntimeReceipt
	count := 0
	for _, receipt := range state.Records.Events {
		if receipt.DispatchID != reviewer.ID || receipt.Type != reviewProgressReceipt {
			continue
		}
		count++
		var progress core.ReviewProgress
		if count > core.ReviewProgressMaxRecords || receipt.Sequence != uint64(count) ||
			receipt.StreamID != reviewProgressStream || receipt.SessionID != reviewer.RuntimeSessionID ||
			receipt.SessionID == "" || receipt.At.IsZero() || receipt.At.Before(reviewer.CreatedAt) ||
			len(receipt.Payload) > core.ReviewProgressMaxBytes || receipt.Hash != receiptHash(receipt.Payload) ||
			core.DecodeGraphInput(strings.NewReader(receipt.Payload), &progress) != nil {
			return nil, count, fmt.Errorf("taxion: invalid saved review progress receipt")
		}
		if err := progress.Validate(reviewer, coder); err != nil {
			return nil, count, err
		}
		latest = &receipt
	}
	return latest, count, nil
}

// reviewProgressDecision is data for a new independent Reviewer, never a verdict.
func reviewProgressDecision(state store.Workspace, flow core.ReviewFlow, coder core.GraphDispatch) (core.TaskDecision, error) {
	if len(flow.DispatchIDs) == 0 {
		return core.TaskDecision{}, fmt.Errorf("taxion: review progress transfer has no source Dispatch")
	}
	status, reference := "missing", ""
	var progress *core.ReviewProgress
	last, ok := state.Graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
	if !ok || !state.Graph.HasBudgetContinuationSource(flow, coder.ID) {
		return core.TaskDecision{}, fmt.Errorf("taxion: review progress transfer requires confirmed safe stop")
	}
	if last.ID != coder.ID {
		if err := validateResultReceipt(&state, last); err != nil {
			return core.TaskDecision{}, err
		}
		// A stop marker without its controller receipt is not confirmed evidence.
		hasStop := false
		for _, receipt := range state.Records.Events {
			hasStop = hasStop || receipt.DispatchID == last.ID && receipt.Type == "verified-result"
		}
		receipt, _, err := savedReviewProgress(state, last, coder)
		if err != nil {
			return core.TaskDecision{}, err
		}
		if receipt != nil {
			if !hasStop {
				return core.TaskDecision{}, fmt.Errorf("taxion: reported progress has no confirmed Reviewer stop receipt")
			}
			progress = &core.ReviewProgress{}
			if err := core.DecodeGraphInput(strings.NewReader(receipt.Payload), progress); err != nil {
				return core.TaskDecision{}, err
			}
			status, reference = "reported", reviewProgressReceipt+":"+receipt.Hash
		}
	}
	body, err := json.Marshal(struct {
		Kind      string               `json:"kind"`
		Status    string               `json:"status"`
		Reference string               `json:"reference,omitempty"`
		Progress  *core.ReviewProgress `json:"progress"`
		Trust     string               `json:"trust"`
	}{
		"taxion.review-progress-transfer.v1", status, reference, progress,
		"Untrusted reported coverage only; not approval, verified coverage or proof of checks. Independently verify all acceptance criteria.",
	})
	if err != nil {
		return core.TaskDecision{}, err
	}
	return core.TaskDecision{ID: "review-progress:" + coder.ID, Source: "flow:" + flow.ID, Text: string(body)}, nil
}
