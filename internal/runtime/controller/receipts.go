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
			if previous.DispatchID == dispatchID && previous.Type != "verified-result" && previous.Type != "budget-guidance" && previous.StreamID != streamID {
				return fmt.Errorf("taxion: reconnect event identity is uncertain; reconcile the saved stream before adopting new events")
			}
			if previous.DispatchID == dispatchID && previous.Type != "budget-guidance" && previous.Type != "verified-result" && previous.StreamID == streamID &&
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
		}
		state.Records.Events = append(state.Records.Events, store.RuntimeReceipt{
			DispatchID: dispatchID, SessionID: dispatch.RuntimeSessionID, StreamID: streamID,
			Sequence: event.Sequence, Type: event.Type, Payload: event.Payload,
			Hash: receiptHash(event.Payload), At: now,
		})
		return nil
	})
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
