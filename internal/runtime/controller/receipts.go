package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

func receiptHash(payload string) string {
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func (c *Controller) recordEvent(dispatchID, streamID string, event runtime.Event) error {
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		if !ok || dispatch.Status != core.DispatchRunning || dispatch.RuntimeSessionID == "" ||
			dispatch.Recipient != c.runtime.Role() || dispatch.RuntimeConfigHash != c.runtime.ConfigHash() {
			return fmt.Errorf("taxion: event requires a running owned session")
		}
		for _, previous := range state.Records.Events {
			if previous.DispatchID == dispatchID && previous.Type != "verified-result" && previous.Type != "budget-guidance" && previous.Type != reviewProgressReceipt && previous.StreamID != streamID {
				return fmt.Errorf("taxion: reconnect event identity is uncertain; reconcile the saved stream before adopting new events")
			}
			if previous.DispatchID == dispatchID && previous.Type != "budget-guidance" && previous.Type != "verified-result" && previous.Type != reviewProgressReceipt && previous.StreamID == streamID &&
				event.Sequence != 0 && previous.Sequence == event.Sequence {
				if previous.Type == event.Type && previous.Hash == receiptHash(event.Payload) {
					return nil
				}
				return fmt.Errorf("taxion: conflicting runtime event replay")
			}
		}
		now := time.Now().UTC()
		if event.Type == runtime.EventUsage {
			var usage core.DispatchUsage
			if err := core.DecodeGraphInput(strings.NewReader(event.Payload), &usage); err != nil {
				return err
			}
			if !nonnegativeUsage(usage) {
				return fmt.Errorf("taxion: invalid runtime event usage")
			}
			usage.ObservedAt = now
			for i := range state.Graph.Dispatches {
				if state.Graph.Dispatches[i].ID == dispatchID {
					if err := appendRuntimeUsage(&state.Graph.Dispatches[i], usage); err != nil {
						return err
					}
					break
				}
			}
			if flow, found := flowForDispatch(state.Graph, dispatchID); found {
				if err := appendBudgetAudit(state, flow, "USAGE_OBSERVED", taskBudgetReport(state.Graph, flow, now)); err != nil {
					return err
				}
			}
		}
		state.Records.Events = append(state.Records.Events, store.RuntimeReceipt{
			DispatchID: dispatchID, SessionID: dispatch.RuntimeSessionID, StreamID: streamID,
			Sequence: event.Sequence, Type: event.Type, Payload: event.Payload,
			Hash: receiptHash(event.Payload), At: now,
		})
		return nil
	})
}

func (c *Controller) adoptSavedTurn(dispatch core.GraphDispatch, flow core.ReviewFlow, request RecoveryRequest, turn runtime.SavedTurn) error {
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		current, found := state.Graph.GraphDispatch(dispatch.ID)
		owner, _ := state.Graph.ReviewFlow(flow.ID)
		if !found || current.Status != core.DispatchRunning || current.Result != nil ||
			current.RuntimeSessionID != turn.SessionID || current.RuntimeConfigHash != dispatch.RuntimeConfigHash ||
			current.InstructionHash != dispatch.InstructionHash || owner.RunnerID != request.ExpectedRunnerID {
			return fmt.Errorf("taxion: saved finalization binding changed during observation")
		}
		stream := "saved-turn:" + turn.HistoryHash
		matched, err := matchSavedTurn(state.Records, dispatch, turn)
		if err != nil {
			return err
		}
		now := time.Now().UTC()
		for index, event := range turn.Events {
			if matched[index] {
				continue
			}
			if event.Type == runtime.EventUsage {
				var usage core.DispatchUsage
				if err := core.DecodeGraphInput(strings.NewReader(event.Payload), &usage); err != nil || !nonnegativeUsage(usage) {
					return fmt.Errorf("taxion: saved provider usage is invalid")
				}
				usage.ObservedAt = now
				if err := appendRuntimeUsage(&current, usage); err != nil {
					return err
				}
			}
			state.Records.Events = append(state.Records.Events, store.RuntimeReceipt{
				DispatchID: dispatch.ID, SessionID: turn.SessionID, StreamID: stream,
				Sequence: uint64(index + 1), Type: event.Type, Payload: event.Payload,
				Hash: receiptHash(event.Payload), At: now,
			})
		}
		if err := reconcileSavedUsage(&current, turn.UsageTotals, now); err != nil {
			return err
		}
		for index := range state.Graph.Dispatches {
			if state.Graph.Dispatches[index].ID == current.ID {
				state.Graph.Dispatches[index] = current
			}
		}
		if len(current.RuntimeUsage) != len(dispatch.RuntimeUsage) {
			if err := appendBudgetAudit(state, flow, "USAGE_OBSERVED", taskBudgetReport(state.Graph, flow, now)); err != nil {
				return err
			}
		}
		body, err := json.Marshal(struct {
			Request     RecoveryRequest      `json:"request"`
			HistoryHash string               `json:"historyHash"`
			SubmittedAt time.Time            `json:"submittedAt"`
			FinishedAt  time.Time            `json:"finishedAt"`
			UsageTotals []core.DispatchUsage `json:"usageTotals,omitempty"`
		}{request, turn.HistoryHash, turn.SubmittedAt, turn.FinishedAt, turn.UsageTotals})
		if err != nil {
			return err
		}
		state.Records.Events = append(state.Records.Events, store.RuntimeReceipt{
			DispatchID: dispatch.ID, SessionID: turn.SessionID, StreamID: stream, Type: "saved-turn-observed",
			Payload: string(body), Hash: receiptHash(string(body)), At: now,
		})
		return nil
	})
}

// Match durable observations in order against the provider's own history.
// Caller-authored answer files cannot establish this execution binding.
func matchSavedTurn(records store.Records, dispatch core.GraphDispatch, turn runtime.SavedTurn) (map[int]bool, error) {
	stream := "saved-turn:" + turn.HistoryHash
	cursor, anchors := 0, 0
	matched := make(map[int]bool)
	for _, receipt := range records.Events {
		if receipt.DispatchID != dispatch.ID ||
			receipt.Type != runtime.EventExecution && receipt.Type != runtime.EventUsage && receipt.Type != runtime.EventOutput {
			continue
		}
		if receipt.SessionID != turn.SessionID || receipt.Hash != receiptHash(receipt.Payload) {
			return nil, fmt.Errorf("taxion: saved runtime observation is inconsistent")
		}
		// Per-call usage is ephemeral; retain its receipts and compare the
		// provider's shutdown totals separately.
		if receipt.Type == runtime.EventUsage {
			for index, event := range turn.Events {
				if !matched[index] && event.Type == receipt.Type && event.Payload == receipt.Payload {
					matched[index] = true
					break
				}
			}
			continue
		}
		if receipt.StreamID == stream {
			index := int(receipt.Sequence) - 1
			if index < 0 || index >= len(turn.Events) || turn.Events[index].Type != receipt.Type ||
				turn.Events[index].Payload != receipt.Payload {
				return nil, fmt.Errorf("taxion: previously adopted provider history differs")
			}
			matched[index] = true
			continue
		}
		found := false
		for cursor < len(turn.Events) {
			index := cursor
			event := turn.Events[cursor]
			cursor++
			if event.Type == receipt.Type && event.Payload == receipt.Payload {
				matched[index], found = true, true
				if event.Type == runtime.EventExecution {
					anchors++
				}
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("taxion: provider history differs from the durable execution observations")
		}
	}
	if anchors == 0 {
		return nil, fmt.Errorf("taxion: saved finalization requires a matching durable execution observation")
	}
	return matched, nil
}

func reconcileSavedUsage(dispatch *core.GraphDispatch, totals []core.DispatchUsage, at time.Time) error {
	seen := map[string]bool{}
	for _, total := range totals {
		if !nonnegativeUsage(total) || seen[total.Model] {
			return fmt.Errorf("taxion: invalid provider shutdown usage totals")
		}
		seen[total.Model] = true
		delta := total
		delta.InputTokens, delta.OutputTokens = cloneUsageMetric(total.InputTokens), cloneUsageMetric(total.OutputTokens)
		delta.CacheReadTokens, delta.CacheWriteTokens = cloneUsageMetric(total.CacheReadTokens), cloneUsageMetric(total.CacheWriteTokens)
		if total.TotalNanoAIU != nil {
			value := *total.TotalNanoAIU
			delta.TotalNanoAIU = &value
		}
		for _, usage := range dispatch.RuntimeUsage {
			if usage.Model != total.Model {
				continue
			}
			for _, pair := range [][2]*int64{
				{delta.InputTokens, usage.InputTokens}, {delta.OutputTokens, usage.OutputTokens},
				{delta.CacheReadTokens, usage.CacheReadTokens}, {delta.CacheWriteTokens, usage.CacheWriteTokens},
			} {
				if pair[0] != nil && pair[1] != nil {
					*pair[0] -= *pair[1]
				}
			}
			if delta.TotalNanoAIU != nil && usage.TotalNanoAIU != nil {
				*delta.TotalNanoAIU -= *usage.TotalNanoAIU
			}
		}
		if !nonnegativeUsage(delta) {
			return fmt.Errorf("taxion: provider shutdown usage is lower than saved consumption")
		}
		changed := delta.TotalNanoAIU != nil && *delta.TotalNanoAIU != 0
		for _, value := range []*int64{delta.InputTokens, delta.OutputTokens, delta.CacheReadTokens, delta.CacheWriteTokens} {
			changed = changed || value != nil && *value != 0
		}
		if changed {
			delta.ObservedAt = at
			dispatch.RuntimeUsage = append(dispatch.RuntimeUsage, delta)
		}
	}
	return nil
}

func cloneUsageMetric(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func appendRuntimeUsage(dispatch *core.GraphDispatch, usage core.DispatchUsage) error {
	if usage.Accounting != nil {
		for _, previous := range dispatch.RuntimeUsage {
			if previous.Accounting == nil || *previous.Accounting != *usage.Accounting {
				continue
			}
			usage.ObservedAt = previous.ObservedAt
			if !reflect.DeepEqual(previous, usage) {
				return fmt.Errorf("taxion: runtime usage accounting identity was reused with different metrics")
			}
			return nil
		}
	}
	dispatch.RuntimeUsage = append(dispatch.RuntimeUsage, usage)
	return nil
}

type reviewFinalization struct {
	Result          *core.DispatchResult `json:"result"`
	Usage           []core.DispatchUsage `json:"usage"`
	InstructionHash string               `json:"instructionHash"`
	ConfigHash      string               `json:"configHash"`
	RuntimeClosed   bool                 `json:"runtimeClosed"`
}

func savedReviewFinalization(state store.Workspace, dispatch core.GraphDispatch) (*reviewFinalization, error) {
	var saved *reviewFinalization
	for _, receipt := range state.Records.Events {
		if receipt.DispatchID != dispatch.ID || receipt.Type != "review-finalization" {
			continue
		}
		var phase reviewFinalization
		// Validation binds a usage prefix; runtime close freezes the complete usage ledger.
		if receipt.SessionID != dispatch.RuntimeSessionID || receipt.StreamID != "review-finalization" ||
			receipt.Hash != receiptHash(receipt.Payload) ||
			core.DecodeGraphInput(strings.NewReader(receipt.Payload), &phase) != nil ||
			phase.InstructionHash != dispatch.InstructionHash || phase.ConfigHash != dispatch.RuntimeConfigHash ||
			len(phase.Usage) > len(dispatch.RuntimeUsage) ||
			len(phase.Usage) != 0 && !reflect.DeepEqual(phase.Usage, dispatch.RuntimeUsage[:len(phase.Usage)]) ||
			phase.RuntimeClosed && len(phase.Usage) != len(dispatch.RuntimeUsage) || phase.Result == nil ||
			phase.Result.Status != core.DispatchSucceeded || phase.Result.GitEvidence == nil {
			return nil, fmt.Errorf("taxion: saved review finalization differs from its bound validated result")
		}
		if err := dispatch.ValidateResult(phase.Result.Status, phase.Result); err != nil {
			return nil, err
		}
		if saved != nil {
			if saved.RuntimeClosed || !phase.RuntimeClosed || !reflect.DeepEqual(saved.Result, phase.Result) {
				return nil, fmt.Errorf("taxion: saved review finalization phases conflict")
			}
		} else if phase.RuntimeClosed {
			return nil, fmt.Errorf("taxion: saved review finalization is missing validation")
		}
		saved = &phase
	}
	return saved, nil
}

func (c *Controller) recordReviewFinalization(dispatch core.GraphDispatch, result *core.DispatchResult, closed bool) error {
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		current, found := state.Graph.GraphDispatch(dispatch.ID)
		if !found || current.Status != core.DispatchRunning || current.Result != nil ||
			current.RuntimeSessionID != dispatch.RuntimeSessionID ||
			current.RuntimeConfigHash != dispatch.RuntimeConfigHash || current.InstructionHash != dispatch.InstructionHash {
			return fmt.Errorf("taxion: review finalization lost its running session binding")
		}
		previous, err := savedReviewFinalization(*state, current)
		if err != nil {
			return err
		}
		if previous != nil {
			if !reflect.DeepEqual(previous.Result, result) || previous.RuntimeClosed && !closed {
				return fmt.Errorf("taxion: review finalization cannot replace validated evidence")
			}
			if previous.RuntimeClosed == closed {
				return nil
			}
		} else if closed {
			return fmt.Errorf("taxion: runtime close requires saved review validation")
		}
		body, err := json.Marshal(reviewFinalization{
			Result: result, Usage: current.RuntimeUsage, InstructionHash: current.InstructionHash,
			ConfigHash: current.RuntimeConfigHash, RuntimeClosed: closed,
		})
		if err != nil {
			return err
		}
		state.Records.Events = append(state.Records.Events, store.RuntimeReceipt{
			DispatchID: dispatch.ID, SessionID: dispatch.RuntimeSessionID, StreamID: "review-finalization",
			Type: "review-finalization", Payload: string(body), Hash: receiptHash(string(body)), At: time.Now().UTC(),
		})
		return nil
	})
}

func validateResultReceipt(state *store.Workspace, dispatch core.GraphDispatch) error {
	var latest *store.RuntimeReceipt
	var sequence uint64
	for _, receipt := range state.Records.Events {
		if receipt.DispatchID != dispatch.ID {
			continue
		}
		if receipt.SessionID != dispatch.RuntimeSessionID || receipt.Hash != receiptHash(receipt.Payload) {
			return fmt.Errorf("taxion: saved session transcript receipt differs")
		}
		if receipt.Type == "verified-result" {
			if receipt.StreamID != "" || receipt.Sequence != sequence {
				return fmt.Errorf("taxion: saved result receipt history is incomplete or reordered")
			}
			if latest != nil {
				var prior struct {
					Result *core.DispatchResult
					Usage  []core.DispatchUsage
				}
				if err := core.DecodeGraphInput(strings.NewReader(latest.Payload), &prior); err != nil ||
					prior.Result == nil || prior.Result.Status != core.DispatchSuspended ||
					prior.Result.DispatchID != dispatch.ID || prior.Result.TaskID != dispatch.TaskID ||
					prior.Result.Recipient != dispatch.Recipient || prior.Result.Repository != dispatch.Repository ||
					len(prior.Usage) > len(dispatch.RuntimeUsage) {
					return fmt.Errorf("taxion: historical result is not a suspension of the same Dispatch")
				}
				for index, usage := range prior.Usage {
					if !reflect.DeepEqual(usage, dispatch.RuntimeUsage[index]) {
						return fmt.Errorf("taxion: historical result usage differs from the saved session")
					}
				}
			}
			latest = &receipt
			sequence++
		}
	}
	if latest != nil {
		body, err := json.Marshal(struct {
			Result *core.DispatchResult
			Usage  []core.DispatchUsage
		}{dispatch.Result, dispatch.RuntimeUsage})
		if err != nil || latest.Hash != receiptHash(string(body)) {
			return fmt.Errorf("taxion: saved result differs from controller receipt")
		}
	}
	if dispatch.Status == core.DispatchSucceeded && latest == nil {
		return fmt.Errorf("taxion: successful checkpoint has no controller result receipt; explicit evidence reconciliation is required")
	}
	return nil
}
