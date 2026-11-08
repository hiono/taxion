package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func drainCompletedTurn(ctx context.Context, events <-chan runtime.Event, usage *[]core.DispatchUsage, sequence *uint64, sessionID string) error {
	finished := false
	for {
		var event runtime.Event
		var open bool
		if finished {
			select {
			case event, open = <-events:
			default:
				return nil
			}
		} else {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case event, open = <-events:
			}
		}
		if !open {
			return nil
		}
		if event.AgentID != "" || event.Sequence == 0 || event.Sequence <= *sequence {
			return fmt.Errorf("taxion: completed main turn has ambiguous or child activity")
		}
		*sequence = event.Sequence
		switch event.Type {
		case runtime.EventUsage:
			var call core.DispatchUsage
			if err := core.DecodeGraphInput(strings.NewReader(event.Payload), &call); err != nil {
				return fmt.Errorf("taxion: completed main turn has invalid usage")
			}
			call.ObservedAt = time.Now().UTC()
			*usage = append(*usage, call)
		case runtime.EventUsageCheckpoint:
			if _, matches, err := runtime.DecodeUsageCheckpoint(event.Payload, sessionID, *usage); err != nil || !matches {
				return fmt.Errorf("taxion: completed main turn has incomplete or conflicting checkpoint accounting")
			}
		case runtime.EventTurnFinished:
			if finished {
				return fmt.Errorf("taxion: completed main turn has duplicate terminal boundaries")
			}
			finished = true
		case runtime.EventContextUsage, "acp_connection", "acp_capabilities":
		default:
			return fmt.Errorf("taxion: completed main turn has later activity; receipt acceptance withheld")
		}
	}
}
