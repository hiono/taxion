package controller

import (
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func drainCompletedTurn(events <-chan runtime.Event, usage *[]core.DispatchUsage, sequence *uint64) error {
	for {
		select {
		case event, open := <-events:
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
			case runtime.EventTurnFinished, runtime.EventContextUsage:
			default:
				return fmt.Errorf("taxion: completed main turn has later activity; receipt acceptance withheld")
			}
		default:
			return nil
		}
	}
}
