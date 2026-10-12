package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
			if previous.DispatchID == dispatchID && previous.Type != "verified-result" && previous.StreamID != streamID {
				return fmt.Errorf("taxion: reconnect event identity is uncertain; reconcile the saved stream before adopting new events")
			}
			if previous.DispatchID == dispatchID && previous.StreamID == streamID &&
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
					state.Graph.Dispatches[i].RuntimeUsage = append(state.Graph.Dispatches[i].RuntimeUsage, usage)
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

func validateResultReceipt(state *store.Workspace, dispatch core.GraphDispatch) error {
	verified := false
	for _, receipt := range state.Records.Events {
		if receipt.DispatchID != dispatch.ID {
			continue
		}
		if receipt.SessionID != dispatch.RuntimeSessionID || receipt.Hash != receiptHash(receipt.Payload) {
			return fmt.Errorf("taxion: saved session transcript receipt differs")
		}
		if receipt.Type == "verified-result" {
			verified = true
			body, err := json.Marshal(struct {
				Result *core.DispatchResult
				Usage  []core.DispatchUsage
			}{dispatch.Result, dispatch.RuntimeUsage})
			if err != nil || receipt.Hash != receiptHash(string(body)) {
				return fmt.Errorf("taxion: saved result differs from controller receipt")
			}
		}
	}
	if dispatch.Status == core.DispatchSucceeded && !verified {
		return fmt.Errorf("taxion: successful checkpoint has no controller result receipt; explicit evidence reconciliation is required")
	}
	return nil
}
