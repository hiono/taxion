package copilot

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

// ReadSavedTurn uses the nonexecuting attachment also used for cancellation.
// In particular it never sends, aborts, repairs transcripts or continues work.
func (a *Adapter) ReadSavedTurn(ctx context.Context, sessionID string) (runtime.SavedTurn, error) {
	return a.readSavedTurn(ctx, sessionID, false, false)
}

func (a *Adapter) ReadSavedQuestionTurn(ctx context.Context, sessionID string, stoppedConfirmed bool) (runtime.SavedTurn, error) {
	return a.readSavedTurn(ctx, sessionID, stoppedConfirmed, true)
}

func (a *Adapter) readSavedTurn(ctx context.Context, sessionID string, stoppedConfirmed, advisory bool) (runtime.SavedTurn, error) {
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
	return savedSessionTurn(events, sessionID, a.Worktree(), stoppedConfirmed, advisory)
}

func savedTurn(events []sdk.SessionEvent, sessionID, worktree string) (runtime.SavedTurn, error) {
	return savedSessionTurn(events, sessionID, worktree, false, false)
}

func savedQuestionTurn(events []sdk.SessionEvent, sessionID, worktree string, stoppedConfirmed bool) (runtime.SavedTurn, error) {
	return savedSessionTurn(events, sessionID, worktree, stoppedConfirmed, true)
}

func savedSessionTurn(events []sdk.SessionEvent, sessionID, worktree string, stoppedConfirmed, advisory bool) (runtime.SavedTurn, error) {
	fail := func() (runtime.SavedTurn, error) {
		return runtime.SavedTurn{}, fmt.Errorf("taxion: saved session has no unambiguous completed main-agent result")
	}
	var result runtime.SavedTurn
	result.SessionID = sessionID
	started, turnStarted, ended, shutdown, shutdownSeen := false, false, false, false, false
	var messageID, turnID string
	seen := map[string]bool{}
	var lastAt time.Time
	for _, event := range events {
		if shutdownSeen {
			return fail()
		}
		if event.ID == "" || seen[event.ID] || event.Timestamp.IsZero() ||
			!lastAt.IsZero() && event.Timestamp.Before(lastAt) {
			return fail()
		}
		seen[event.ID] = true
		lastAt = event.Timestamp
		if event.AgentID != nil && *event.AgentID != "" {
			return fail()
		}
		normalized, recognized := normalizeEvent(event)
		if advisory && recognized && !runtime.QuestionEventAllowed(normalized.Type) {
			return fail()
		}
		// Keep the existing indexed Reviewer receipt separate from advisory admission.
		projected := recognized && normalized.Type != runtime.EventUnclassified &&
			normalized.Type != runtime.EventUsageCheckpoint
		if ended && projected {
			switch normalized.Type {
			case runtime.EventUsage, runtime.EventContextUsage, runtime.EventTurnFinished:
			default:
				return fail()
			}
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
			if !started || turnStarted || result.Payload != "" || messageID != "" || data.MessageID == nil ||
				strings.TrimSpace(*data.MessageID) == "" {
				return fail()
			}
			inputHash, err := savedUserInputHash(data)
			if err != nil {
				return fail()
			}
			result.InputHash = inputHash
			result.Input, err = savedUserInput(data)
			if err != nil {
				return fail()
			}
			result.SubmittedAt = event.Timestamp
			messageID = *data.MessageID
			result.MessageID = messageID
		case *sdk.AssistantMessageData:
			shutdown = false
			if !started || !turnStarted || messageID == "" {
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
				*data.OriginatingMessageID != messageID || data.TurnID == nil ||
				strings.TrimSpace(*data.TurnID) == "" || *data.TurnID != turnID {
				return fail()
			}
			if result.Payload != "" {
				return fail()
			}
			result.Payload = data.Content
			result.TurnID = turnID
		case *sdk.AssistantMessageDeltaData:
			if !started || !turnStarted || messageID == "" {
				return fail()
			}
		case *sdk.AssistantTurnEndData:
			if !turnStarted || result.Payload == "" || data.TurnID != turnID {
				return fail()
			}
			turnStarted, ended, result.FinishedAt = false, true, event.Timestamp
		case *sdk.SessionShutdownData:
			if shutdownSeen {
				return fail()
			}
			shutdownSeen = true
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
		case *sdk.AssistantTurnStartData:
			if !started || messageID == "" || turnStarted || ended {
				return fail()
			}
			activeTurnID, err := savedEventTurnID(data)
			if err != nil {
				return fail()
			}
			turnID = activeTurnID
			turnStarted, shutdown = true, false
		case *sdk.ToolExecutionStartData, *sdk.ToolExecutionCompleteData:
			result.Payload, turnStarted, ended, shutdown = "", false, false, false
		}
		if projected {
			switch normalized.Type {
			case runtime.EventExecution, runtime.EventUsage, runtime.EventOutput:
				result.Events = append(result.Events, normalized)
			}
		}
		if advisory && normalized.Type == runtime.EventUsageCheckpoint {
			result.Events = append(result.Events, normalized)
		}
	}
	if !started || !ended || (!shutdown && !(stoppedConfirmed && !shutdownSeen)) || messageID == "" || result.InputHash == "" ||
		result.MessageID != messageID || result.TurnID == "" ||
		result.FinishedAt.Before(result.SubmittedAt) || shutdown && result.ShutdownAt.Before(result.FinishedAt) {
		return fail()
	}
	body, err := json.Marshal(events)
	if err != nil {
		return runtime.SavedTurn{}, err
	}
	result.HistoryHash = fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	return result, nil
}

func savedUserInputHash(data *sdk.UserMessageData) (string, error) {
	content, err := savedUserInput(data)
	if err != nil {
		return "", err
	}
	return core.SHA256Hex([]byte(content)), nil
}

func savedUserInput(data *sdk.UserMessageData) (string, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return "", err
	}
	for _, key := range []string{"content", "text"} {
		var content string
		if json.Unmarshal(fields[key], &content) == nil && content != "" {
			return content, nil
		}
	}
	return "", fmt.Errorf("taxion: saved user input is unavailable")
}

func savedEventTurnID(data any) (string, error) {
	encoded, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return "", err
	}
	var id string
	if err := json.Unmarshal(fields["turnId"], &id); err != nil || strings.TrimSpace(id) == "" {
		return "", fmt.Errorf("taxion: saved turn identity is unavailable")
	}
	return id, nil
}
