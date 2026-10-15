package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

type SearchTurn struct {
	SessionID        string               `json:"sessionId"`
	Usage            []core.DispatchUsage `json:"usage"`
	BudgetGuidance   *budgetObservation   `json:"budgetGuidance,omitempty"`
	BudgetSupplement string               `json:"budgetSupplement,omitempty"`
}

// RunSearchTurn observes one bounded acquisition turn. Model prose is not the
// evidence of acquisition; the host's saved sourced snapshots are.
func RunSearchTurn(ctx context.Context, worker runtime.Identity, taskID, query string) (turn SearchTurn, returnErr error) {
	if worker.Role() != "ORCHESTRATOR" {
		return turn, fmt.Errorf("taxion: search turn requires an Orchestrator")
	}
	if _, ok := ctx.Deadline(); !ok {
		return turn, fmt.Errorf("taxion: search turn requires an explicit deadline")
	}
	session, err := worker.Start(ctx, taskID)
	if err != nil {
		return turn, err
	}
	turn.SessionID = session
	defer func() {
		stopContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := worker.Stop(stopContext, session); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("taxion: stop isolated search session: %w", err))
		}
	}()
	events, err := worker.Events(ctx, session)
	if err != nil {
		return turn, err
	}
	prompt, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return turn, err
	}
	if identity, ok := worker.(runtime.BudgetGuidanceIdentity); ok && identity.BudgetGuidancePolicy() != nil {
		var selected config.Config
		configured, ok := worker.(runtime.ConfigurationIdentity)
		if !ok || core.DecodeGraphInput(strings.NewReader(string(configured.ExecutionConfiguration())), &selected) != nil {
			return turn, fmt.Errorf("taxion: guided search requires the approved runtime configuration")
		}
		if err := configured.CheckServiceAccess(ctx, session); err != nil {
			return turn, err
		}
		turn.BudgetGuidance, turn.BudgetSupplement, err = isolatedBudgetGuidance(ctx, worker, identity.BudgetGuidancePolicy(), selected)
		if err != nil {
			return turn, err
		}
	}
	if err := worker.Send(ctx, session, string(prompt)+turn.BudgetSupplement); err != nil {
		return turn, err
	}
	for {
		select {
		case <-ctx.Done():
			return turn, ctx.Err()
		case event, ok := <-events:
			if !ok {
				return turn, fmt.Errorf("taxion: search stream ended before completion")
			}
			switch event.Type {
			case runtime.EventUsage:
				var usage core.DispatchUsage
				if err := core.DecodeGraphInput(strings.NewReader(event.Payload), &usage); err != nil {
					return turn, fmt.Errorf("taxion: search usage is invalid")
				}
				turn.Usage = append(turn.Usage, usage)
			case runtime.EventFailed, runtime.EventCancelled:
				return turn, fmt.Errorf("taxion: search runtime failed or was cancelled; provider details withheld")
			case runtime.EventTurnFinished:
				return turn, nil
			}
		}
	}
}
