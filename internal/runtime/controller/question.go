package controller

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/gitproof"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

// QuestionContext is an observation of saved records, not live execution.
// It is deliberately separate from Dispatch instructions and main-role history.
type QuestionContext struct {
	Task          PlanningTask       `json:"task"`
	Flows         []core.ReviewFlow  `json:"flows"`
	Observation   *PlanObservation   `json:"observation"`
	Planner       *PlannerContext    `json:"planner,omitempty"`
	Investigation *FileInvestigation `json:"investigation,omitempty"`
	Projection    string             `json:"projection,omitempty"`
}

type InvestigationRequest struct {
	Paths    []string `json:"paths"`
	MaxBytes int      `json:"maxBytes"`
}

type FileInvestigation struct {
	SHA         string                      `json:"sha"`
	Files       []gitproof.InvestigatedFile `json:"files"`
	Observation PlanObservation             `json:"observation"`
}

type QuestionClaim struct {
	Text    string   `json:"text"`
	Sources []string `json:"sources"`
}

type QuestionAnswer struct {
	Confirmed      []QuestionClaim  `json:"confirmed"`
	Assumptions    []string         `json:"assumptions"`
	Unconfirmed    []string         `json:"unconfirmed"`
	Recommendation string           `json:"recommendation,omitempty"`
	Proposal       *RequestProposal `json:"proposal,omitempty"`
	Questions      []string         `json:"questions,omitempty"`
}

func (answer QuestionAnswer) MarshalJSON() ([]byte, error) {
	type wire QuestionAnswer
	if answer.Questions == nil {
		return json.Marshal(wire(answer))
	}
	// Keep legacy answers unchanged but preserve an explicit empty questions
	// list when the request Planner has no outstanding clarification.
	return json.Marshal(struct {
		wire
		Questions []string `json:"questions"`
	}{wire(answer), answer.Questions})
}

type QuestionResult struct {
	SessionID string               `json:"sessionId"`
	Context   QuestionContext      `json:"context"`
	Answer    *QuestionAnswer      `json:"answer,omitempty"`
	Usage     []core.DispatchUsage `json:"usage"`
	Changed   bool                 `json:"recordsChangedDuringAnswer"`
}

func questionContextsEqual(current, supplied QuestionContext) (bool, error) {
	current.Observation = supplied.Observation
	current.Investigation, supplied.Investigation = nil, nil
	currentJSON, err := json.Marshal(current)
	if err != nil {
		return false, err
	}
	suppliedJSON, err := json.Marshal(supplied)
	if err != nil {
		return false, err
	}
	// RawMessage configuration whitespace changes when CLI input is formatted.
	// Marshal compacts it while retaining every recorded field and source hash.
	return bytes.Equal(currentJSON, suppliedJSON), nil
}

func ObserveQuestion(db *store.Store, ledgerPath, repository, taskID string) (QuestionContext, error) {
	plan, err := Plan(db, ledgerPath, repository)
	if err != nil {
		return QuestionContext{}, err
	}
	for _, task := range plan.Tasks {
		if task.Task.ID != taskID {
			continue
		}
		context := QuestionContext{Task: task, Flows: []core.ReviewFlow{}, Observation: plan.Observation}
		state, err := db.LoadWorkspace()
		if err != nil {
			return QuestionContext{}, err
		}
		current, ok := state.Graph.GraphTask(taskID)
		if !state.HasGraph || !ok || !reflect.DeepEqual(current, task.Task) {
			return QuestionContext{}, core.ErrFeedbackStale
		}
		context.Task.Task.Decisions, err = compactResultDecisions(state.Graph, task.Task.Decisions)
		if err != nil {
			return QuestionContext{}, err
		}
		projected := !reflect.DeepEqual(context.Task.Task.Decisions, task.Task.Decisions)
		history := []core.GraphEvent{}
		for _, event := range state.Graph.History {
			if event.TaskID == taskID {
				history = append(history, event)
			}
		}
		if !reflect.DeepEqual(history, context.Task.History) {
			return QuestionContext{}, core.ErrFeedbackStale
		}
		for i, event := range context.Task.History {
			decisions, err := compactResultDecisions(state.Graph, event.Task.Decisions)
			if err != nil {
				return QuestionContext{}, err
			}
			projected = projected || !reflect.DeepEqual(decisions, event.Task.Decisions)
			context.Task.History[i].Task.Decisions = decisions
		}
		for i, dispatch := range context.Task.Dispatches {
			saved, ok := state.Graph.GraphDispatch(dispatch.ID)
			if !ok || !reflect.DeepEqual(saved, dispatch) {
				return QuestionContext{}, core.ErrFeedbackStale
			}
			decisions, err := compactResultDecisions(state.Graph, dispatch.Instruction.Decisions)
			if err != nil {
				return QuestionContext{}, err
			}
			projected = projected || !reflect.DeepEqual(decisions, dispatch.Instruction.Decisions)
			context.Task.Dispatches[i].Instruction.Decisions = decisions
		}
		for _, flow := range plan.ReviewFlows {
			if flow.TaskID == taskID || slices.ContainsFunc(flow.DispatchIDs, func(id string) bool {
				return slices.ContainsFunc(task.Dispatches, func(d core.GraphDispatch) bool { return d.ID == id })
			}) {
				saved, ok := state.Graph.ReviewFlow(flow.ID)
				if !ok || !reflect.DeepEqual(saved, flow) {
					return QuestionContext{}, core.ErrFeedbackStale
				}
				decisions, err := compactResultDecisions(state.Graph, flow.Contract.Decisions)
				if err != nil {
					return QuestionContext{}, err
				}
				projected = projected || !reflect.DeepEqual(decisions, flow.Contract.Decisions)
				flow.Contract.Decisions = decisions
				context.Flows = append(context.Flows, flow)
			}
		}
		if projected {
			context.Projection = "Prior result decisions retain exact saved results and source hashes, but omit recursively repeated instructions. Instruction hashes identify the original saved instruction, not this projection; task graph retains the full source."
		}
		context.Observation = nil
		content, err := json.Marshal(context)
		if err != nil {
			return QuestionContext{}, err
		}
		context.Observation = &PlanObservation{
			StartedAt: plan.Observation.StartedAt, CompletedAt: plan.Observation.CompletedAt,
			ContentHash: fmt.Sprintf("sha256:%x", sha256.Sum256(content)),
		}
		return context, nil
	}
	return QuestionContext{}, fmt.Errorf("taxion: side question requires a tracked Task in the selected repository")
}

// AskQuestion is model-only: no tools, Dispatch binding, graph update or inferred
// Human approval. The caller supplies a separately bounded, isolated runtime.
func AskQuestion(ctx context.Context, db *store.Store, ledgerPath, taskID, text string, selected config.Config,
	maxTokens int64, worker runtime.ConfigurationIdentity) (result QuestionResult, err error) {
	return askQuestion(ctx, db, ledgerPath, taskID, text, selected, maxTokens, worker, false, nil)
}

// AskPlanner recommends an action from saved Memory and results in an isolated
// session. It cannot release a handoff, approve work or issue a Dispatch.
func AskPlanner(ctx context.Context, db *store.Store, ledgerPath, taskID, text string, selected config.Config,
	maxTokens int64, worker runtime.ConfigurationIdentity) (QuestionResult, error) {
	return askQuestion(ctx, db, ledgerPath, taskID, text, selected, maxTokens, worker, true, nil)
}

// AskInvestigation supplies bounded committed files to a tool-free isolated
// question. Its new Git observation is not evidence of prior consideration.
func AskInvestigation(ctx context.Context, db *store.Store, ledgerPath, taskID, text string, selected config.Config,
	maxTokens int64, worker runtime.ConfigurationIdentity, request InvestigationRequest) (QuestionResult, error) {
	return askQuestion(ctx, db, ledgerPath, taskID, text, selected, maxTokens, worker, false, &request)
}

func askQuestion(ctx context.Context, db *store.Store, ledgerPath, taskID, text string, selected config.Config,
	maxTokens int64, worker runtime.ConfigurationIdentity, planner bool, investigation *InvestigationRequest) (result QuestionResult, err error) {
	result.Usage = []core.DispatchUsage{}
	if worker == nil || worker.Role() != "ORCHESTRATOR" || strings.TrimSpace(text) == "" || maxTokens <= 0 {
		return result, fmt.Errorf("taxion: side question requires an isolated Orchestrator, text and explicit token ceiling")
	}
	var actual config.Config
	if err := core.DecodeGraphInput(strings.NewReader(string(worker.ExecutionConfiguration())), &actual); err != nil || actual != selected {
		return result, fmt.Errorf("taxion: side question runtime differs from the selected configuration")
	}
	approval, err := CheckConfiguration(db, taskID, selected)
	if err != nil {
		return result, err
	}
	result.Context, err = ObserveQuestion(db, ledgerPath, selected.Repository.Path, taskID)
	if err != nil {
		return result, err
	}
	for _, dispatch := range result.Context.Task.Dispatches {
		if dispatch.Configuration == nil {
			continue
		}
		var saved config.Config
		if err := core.DecodeGraphInput(strings.NewReader(string(dispatch.Configuration.Selection)), &saved); err != nil || saved != selected {
			return result, fmt.Errorf("taxion: question configuration differs from the Task's issued selection")
		}
	}
	if planner {
		result.Context, err = observePlannerQuestion(db, ledgerPath, selected.Repository.Path, taskID)
		if err != nil {
			return result, err
		}
	}
	if err := worker.CheckAuthentication(ctx, selected.Runtime.Account); err != nil {
		return result, err
	}
	if err := worker.CheckRepositoryAccess(ctx); err != nil {
		return result, err
	}
	recheck := func() error {
		current, err := CheckConfiguration(db, taskID, selected)
		if err != nil {
			return err
		}
		if current != approval {
			return fmt.Errorf("taxion: side question approval changed; obtain renewed authorization")
		}
		state, err := db.LoadWorkspace()
		if err != nil {
			return err
		}
		if err := state.Graph.CheckTaskSpecificationUse(taskID, "copilot"); err != nil {
			return err
		}
		return ctx.Err()
	}
	if err := recheck(); err != nil {
		return result, err
	}
	if investigation != nil {
		result.Context.Investigation, err = investigateFiles(ctx, result.Context, *investigation)
		if err != nil {
			return result, err
		}
	}
	input, err := json.Marshal(struct {
		Question string          `json:"question"`
		Context  QuestionContext `json:"context"`
	}{text, result.Context})
	if err != nil {
		return result, err
	}
	if len(input) > 128*1024 {
		return result, fmt.Errorf("taxion: question and context exceed the 128 KiB input ceiling")
	}
	result.SessionID, err = worker.Start(ctx, taskID)
	if err != nil {
		return result, questionProviderError(ctx, "session creation", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		err = errors.Join(err, questionProviderError(stopCtx, "session cleanup", worker.Stop(stopCtx, result.SessionID)))
	}()
	if err := worker.CheckServiceAccess(ctx, result.SessionID); err != nil {
		return result, err
	}
	if err := recheck(); err != nil {
		return result, err
	}
	events, err := worker.Events(ctx, result.SessionID)
	if err != nil {
		return result, questionProviderError(ctx, "event subscription", err)
	}
	if err := worker.Send(ctx, result.SessionID, string(input)); err != nil {
		return result, questionProviderError(ctx, "instruction send", err)
	}
	var output string
	var tokens int64
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-ticker.C:
			if err := recheck(); err != nil {
				return result, err
			}
		case event, ok := <-events:
			if !ok {
				return result, fmt.Errorf("taxion: question stream ended without a terminal turn")
			}
			switch event.Type {
			case runtime.EventOutput:
				if len(event.Payload) > 64*1024 {
					return result, fmt.Errorf("taxion: question answer exceeds the 64 KiB output ceiling")
				}
				output = event.Payload
			case runtime.EventUsage:
				var usage core.DispatchUsage
				if err := core.DecodeGraphInput(strings.NewReader(event.Payload), &usage); err != nil {
					return result, fmt.Errorf("taxion: question usage is invalid")
				}
				usage.ObservedAt = time.Now().UTC()
				result.Usage = append(result.Usage, usage)
				if usage.InputTokens == nil || usage.OutputTokens == nil || *usage.InputTokens < 0 || *usage.OutputTokens < 0 {
					return result, fmt.Errorf("taxion: question token usage is unconfirmed")
				}
				// Subtract before adding so provider counters cannot overflow.
				if *usage.InputTokens >= maxTokens-tokens {
					return result, fmt.Errorf("taxion: question token ceiling reached")
				}
				tokens += *usage.InputTokens
				if *usage.OutputTokens >= maxTokens-tokens {
					return result, fmt.Errorf("taxion: question token ceiling reached")
				}
				tokens += *usage.OutputTokens
			case runtime.EventFailed, runtime.EventCancelled:
				return result, fmt.Errorf("taxion: question runtime failed or was cancelled; provider details withheld")
			case runtime.EventTurnFinished:
				if err := recheck(); err != nil {
					return result, err
				}
				if len(result.Usage) == 0 {
					return result, fmt.Errorf("taxion: question ended without token usage evidence")
				}
				var answer QuestionAnswer
				if err := core.DecodeGraphInput(strings.NewReader(output), &answer); err != nil {
					return result, fmt.Errorf("taxion: question answer has an invalid shape")
				}
				if err := validateQuestionAnswer(answer, result.Context); err != nil {
					return result, err
				}
				observe := ObserveQuestion
				if planner {
					observe = observePlannerQuestion
				}
				now, err := observe(db, ledgerPath, selected.Repository.Path, taskID)
				if err != nil {
					return result, err
				}
				result.Changed = now.Observation.ContentHash != result.Context.Observation.ContentHash
				// The independently hashed Git observation is attached separately
				// from the saved-record observation and never uses mutable HEAD.
				result.Answer = &answer
				return result, nil
			}
		}
	}
}

func questionProviderError(ctx context.Context, operation string, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("taxion: question %s interrupted: %w", operation, ctx.Err())
	}
	return fmt.Errorf("taxion: question %s failed; provider details withheld", operation)
}

func validateQuestionAnswer(answer QuestionAnswer, context QuestionContext) error {
	if answer.Proposal != nil || answer.Questions != nil {
		if context.Planner == nil {
			return fmt.Errorf("taxion: request proposals require a Planner context")
		}
		if err := ValidateRequestPlan(answer); err != nil {
			return err
		}
	}
	if context.Planner != nil {
		if !slices.Contains([]string{"REVIEW", "REPAIR", "REPLAN", "INVESTIGATE", "HOLD", "HUMAN_REQUIRED", "REPORT"}, answer.Recommendation) ||
			len(answer.Confirmed) == 0 {
			return fmt.Errorf("taxion: planner answer requires a sourced recommendation from the allowed actions")
		}
	} else if answer.Recommendation != "" {
		return fmt.Errorf("taxion: a side question cannot contain a planner recommendation")
	}
	if answer.Confirmed == nil || answer.Assumptions == nil || answer.Unconfirmed == nil ||
		len(answer.Confirmed)+len(answer.Assumptions)+len(answer.Unconfirmed) == 0 {
		return fmt.Errorf("taxion: question answer requires explicit confirmed, assumed and unconfirmed lists")
	}
	content, err := json.Marshal(context)
	if err != nil {
		return err
	}
	var records any
	if err := json.Unmarshal(content, &records); err != nil {
		return err
	}
	for _, claim := range answer.Confirmed {
		if strings.TrimSpace(claim.Text) == "" || len(claim.Sources) == 0 {
			return fmt.Errorf("taxion: confirmed question claim requires text and record sources")
		}
		for _, source := range claim.Sources {
			// JSON pointers name the exact fields within the supplied context.
			if !strings.HasPrefix(source, "/task/") && !strings.HasPrefix(source, "/flows/") && !strings.HasPrefix(source, "/observation/") &&
				!(context.Planner != nil && strings.HasPrefix(source, "/planner/")) &&
				!(context.Investigation != nil && strings.HasPrefix(source, "/investigation/")) {
				return fmt.Errorf("taxion: question source must identify a supplied saved record")
			}
			current := records
			for _, part := range strings.Split(source[1:], "/") {
				part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
				switch node := current.(type) {
				case map[string]any:
					current = node[part]
				case []any:
					index, err := strconv.Atoi(part)
					if err != nil || index < 0 || index >= len(node) || strconv.Itoa(index) != part {
						return fmt.Errorf("taxion: question source array index is invalid")
					}
					current = node[index]
				default:
					return fmt.Errorf("taxion: question source does not resolve to a saved record")
				}
				if current == nil {
					return fmt.Errorf("taxion: question source does not resolve to a saved record")
				}
			}
		}
	}
	for _, text := range append(slices.Clone(answer.Assumptions), answer.Unconfirmed...) {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("taxion: question uncertainty cannot be empty")
		}
	}
	return nil
}
