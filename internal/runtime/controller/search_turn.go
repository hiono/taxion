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
	SessionID         string               `json:"sessionId"`
	Usage             []core.DispatchUsage `json:"usage"`
	BudgetGuidance    *budgetObservation   `json:"budgetGuidance,omitempty"`
	QuotaObservations []*budgetObservation `json:"quotaObservations,omitempty"`
	BudgetSupplement  string               `json:"budgetSupplement,omitempty"`
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
	var selected config.Config
	if identity, ok := worker.(runtime.BudgetGuidanceIdentity); ok && identity.BudgetGuidancePolicy() != nil {
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
	lastQuotaCheck := time.Now()
	if turn.BudgetGuidance != nil && turn.BudgetGuidance.Account != nil {
		lastQuotaCheck = turn.BudgetGuidance.Account.ObservedAt
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	checkQuota := func() error {
		observation, quotaErr := monitorIsolatedQuota(ctx, worker, selected, turn.BudgetGuidance, time.Now().UTC())
		lastQuotaCheck = time.Now()
		if observation != nil {
			turn.QuotaObservations = append(turn.QuotaObservations, observation)
		}
		return quotaErr
	}
	var messageID, turnID string
	var sequence uint64
	started, answered := false, false
	for {
		select {
		case <-ctx.Done():
			return turn, ctx.Err()
		case <-ticker.C:
			if time.Since(lastQuotaCheck) >= time.Minute {
				if err := checkQuota(); err != nil {
					return turn, err
				}
			}
		case event, ok := <-events:
			if !ok {
				return turn, fmt.Errorf("taxion: search stream ended before completion")
			}
			if event.AgentID != "" || event.Sequence != 0 && event.Sequence <= sequence {
				return turn, fmt.Errorf("taxion: search main-agent lifecycle is ambiguous")
			}
			if event.Sequence != 0 {
				sequence = event.Sequence
			}
			switch event.Type {
			case runtime.EventInput:
				if messageID != "" || event.MessageID == "" {
					return turn, fmt.Errorf("taxion: search input identity is ambiguous")
				}
				messageID = event.MessageID
			case runtime.EventTurnStarted:
				if started || messageID == "" || event.TurnID == "" {
					return turn, fmt.Errorf("taxion: search turn identity is ambiguous")
				}
				started, turnID = true, event.TurnID
			case runtime.EventOutput:
				if !started || event.MessageID != messageID || event.TurnID != turnID || strings.TrimSpace(event.Payload) == "" {
					return turn, fmt.Errorf("taxion: search answer is outside its main turn")
				}
				answered = true
			case runtime.EventUsage:
				var usage core.DispatchUsage
				if err := core.DecodeGraphInput(strings.NewReader(event.Payload), &usage); err != nil {
					return turn, fmt.Errorf("taxion: search usage is invalid")
				}
				turn.Usage = append(turn.Usage, usage)
			case runtime.EventFailed, runtime.EventCancelled:
				return turn, fmt.Errorf("taxion: search runtime failed or was cancelled; provider details withheld")
			case runtime.EventTurnEnded:
				if !started || !answered || event.TurnID != turnID || sequence == 0 {
					return turn, fmt.Errorf("taxion: search ended without a bound main-agent answer")
				}
				if err := drainCompletedTurn(events, &turn.Usage, &sequence); err != nil {
					return turn, err
				}
				return turn, checkQuota()
			case runtime.EventTurnFinished:
				return turn, fmt.Errorf("taxion: search became idle without a bound main-agent turn end")
			}
		}
	}
}
