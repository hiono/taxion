package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

// ReadSavedTurn uses the nonexecuting attachment also used for cancellation.
// In particular it never sends, aborts, repairs transcripts or continues work.
func (a *Adapter) ReadSavedTurn(ctx context.Context, sessionID string) (runtime.SavedTurn, error) {
	if a.stopSessionID == "" || sessionID != a.stopSessionID {
		return runtime.SavedTurn{}, fmt.Errorf("taxion: saved result observation requires a bound nonexecuting attachment")
	}
	if err := a.ResumeForStop(ctx, sessionID); err != nil {
		return runtime.SavedTurn{}, err
	}
	handle, err := a.lookup(sessionID)
	if err != nil {
		return runtime.SavedTurn{}, err
	}
	handle.mu.Lock()
	session := handle.sdk
	handle.mu.Unlock()
	events, err := session.GetEvents(ctx)
	if err != nil {
		return runtime.SavedTurn{}, err
	}
	return savedTurn(events, sessionID, a.Worktree())
}

func savedTurn(events []sdk.SessionEvent, sessionID, worktree string) (runtime.SavedTurn, error) {
	fail := func() (runtime.SavedTurn, error) {
		return runtime.SavedTurn{}, fmt.Errorf("taxion: saved session has no unambiguous completed main-agent result")
	}
	var result runtime.SavedTurn
	result.SessionID = sessionID
	started, ended, shutdown := false, false, false
	var messageID, turnID string
	seen := map[string]bool{}
	for _, event := range events {
		if event.ID == "" || seen[event.ID] || event.Timestamp.IsZero() {
			return fail()
		}
		seen[event.ID] = true
		if event.AgentID != nil && *event.AgentID != "" {
			switch event.Data.(type) {
			case *sdk.UserMessageData, *sdk.AssistantMessageData, *sdk.AssistantTurnStartData,
				*sdk.AssistantTurnEndData, *sdk.ToolExecutionStartData, *sdk.ToolExecutionCompleteData:
				result.Payload, ended, shutdown = "", false, false
			}
			continue
		}
		switch data := event.Data.(type) {
		case *sdk.SessionStartData:
			if started || data.SessionID != sessionID || data.Context == nil ||
				!filepath.IsAbs(data.Context.Cwd) || filepath.Clean(data.Context.Cwd) != filepath.Clean(worktree) {
				return fail()
			}
			started = true
		case *sdk.UserMessageData:
			shutdown = false
			if !started || result.Payload != "" || messageID != "" || data.MessageID == nil ||
				strings.TrimSpace(*data.MessageID) == "" {
				return fail()
			}
			messageID = *data.MessageID
		case *sdk.AssistantMessageData:
			shutdown = false
			if !started || messageID == "" {
				return fail()
			}
			if len(data.ToolRequests) != 0 {
				result.Payload, ended = "", false
				break
			}
			if strings.TrimSpace(data.Content) == "" {
				break
			}
			if data.ParentToolCallID != nil || data.OriginatingMessageID == nil ||
				*data.OriginatingMessageID != messageID || data.TurnID == nil || strings.TrimSpace(*data.TurnID) == "" {
				return fail()
			}
			result.Payload, result.SubmittedAt = data.Content, event.Timestamp
			turnID, ended = *data.TurnID, false
		case *sdk.AssistantTurnEndData:
			if result.Payload != "" && data.TurnID == turnID {
				ended, result.FinishedAt = true, event.Timestamp
			}
		case *sdk.SessionShutdownData:
			shutdown = data.ShutdownType == "routine" && ended && result.Payload != ""
			result.ShutdownAt = event.Timestamp
			result.UsageTotals = nil
			models := make([]string, 0, len(data.ModelMetrics))
			for model := range data.ModelMetrics {
				models = append(models, model)
			}
			slices.Sort(models)
			for _, model := range models {
				metric := data.ModelMetrics[model]
				usage := core.DispatchUsage{
					Model: model, InputTokens: &metric.Usage.InputTokens, OutputTokens: &metric.Usage.OutputTokens,
					CacheReadTokens: &metric.Usage.CacheReadTokens, CacheWriteTokens: &metric.Usage.CacheWriteTokens,
					TotalNanoAIU: metric.TotalNanoAiu,
				}
				if metric.TotalNanoAiu != nil {
					complete := "complete"
					usage.AICreditsStatus = &complete
				}
				result.UsageTotals = append(result.UsageTotals, usage)
			}
		case *sdk.SessionErrorData:
			return fail()
		case *sdk.AssistantTurnStartData, *sdk.ToolExecutionStartData, *sdk.ToolExecutionCompleteData:
			result.Payload, ended, shutdown = "", false, false
		}
		if normalized, ok := normalizeEvent(event); ok {
			switch normalized.Type {
			case runtime.EventExecution, runtime.EventUsage, runtime.EventOutput:
				result.Events = append(result.Events, normalized)
			}
		}
	}
	if !started || !ended || !shutdown || messageID == "" ||
		result.FinishedAt.Before(result.SubmittedAt) || result.ShutdownAt.Before(result.FinishedAt) {
		return fail()
	}
	body, err := json.Marshal(events)
	if err != nil {
		return runtime.SavedTurn{}, err
	}
	result.HistoryHash = fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	return result, nil
}
