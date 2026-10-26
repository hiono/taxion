package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type StagePlanPreview struct {
	StateChanged bool            `json:"stateChanged"`
	FlowID       string          `json:"flowId"`
	TaskID       string          `json:"taskId"`
	MaxNanoAIU   float64         `json:"maxNanoAiu"`
	Plan         *core.StagePlan `json:"stagePlan"`
	Limitations  []string        `json:"limitations"`
}

// PreviewStagePlan does not create a Flow, a Dispatch or an account query.
func PreviewStagePlan(graph core.TaskGraph, request ReviewFlowRequest) (StagePlanPreview, error) {
	task, ok := graph.GraphTask(request.TaskID)
	if !ok || task.Status != core.Ready || request.StageBudget == nil {
		return StagePlanPreview{}, fmt.Errorf("taxion: stage plan requires a READY Task and stageBudget version 1")
	}
	if err := request.StageBudget.Validate(); err != nil {
		return StagePlanPreview{}, err
	}
	for _, previous := range graph.ReviewFlows {
		if task.Supersedes == previous.TaskID && task.CreatedFrom == "flow:"+previous.ID &&
			previous.Status == "STOPPED" &&
			(strings.TrimSpace(request.ContinuationChange) == "" || strings.TrimSpace(request.SavedOutputDecision) == "") {
			return StagePlanPreview{}, fmt.Errorf("taxion: successor stage plan requires continuationChange and savedOutputDecision; explain feasibility changes and reuse or baseline restart")
		}
	}
	if request.TimeoutSeconds == 0 {
		request.TimeoutSeconds = int64(DefaultFlowTimeout / time.Second)
	}
	if request.MaxRepairs == nil || *request.MaxRepairs < 0 || *request.MaxRepairs > 1000 ||
		request.TimeoutSeconds <= 0 || request.TimeoutSeconds > int64(365*24*time.Hour/time.Second) ||
		math.IsNaN(request.MaxNanoAIU) || math.IsInf(request.MaxNanoAIU, 0) || request.MaxNanoAIU < 0 {
		return StagePlanPreview{}, fmt.Errorf("taxion: stage plan requires finite limits, at most 1000 repairs and a deadline within one year")
	}
	policy := *request.StageBudget
	plan := &core.StagePlan{
		Version: 1, TimeoutSeconds: request.TimeoutSeconds, MaxRepairs: *request.MaxRepairs,
		ReviewOnly:        request.ContinueFromDispatchID != "",
		ValidationSeconds: policy.ValidationSeconds, ReviewSeconds: policy.ReviewSeconds,
		RepairSeconds:       policy.RepairSeconds,
		CoderSessionCredits: policy.CoderSessionCredits, ReviewerSessionCredits: policy.ReviewerSessionCredits,
		ReviewNanoAIU: policy.ReviewNanoAIU, RepairNanoAIU: policy.RepairNanoAIU,
		Basis: []string{},
	}
	// Cold estimates are durations, not ratios or completion guarantees.
	validation, review, repair := int64(max(len(task.ValidationCommands), 1))*60, int64(600), int64(600)
	var reviewNative, repairNative float64
	observed := 0
	for index := len(graph.ReviewFlows) - 1; index >= 0 && observed < 20; index-- {
		prior := graph.ReviewFlows[index]
		if prior.Contract.Repo != task.Repo || !slices.Equal(prior.Contract.ValidationCommands, task.ValidationCommands) {
			continue
		}
		for _, id := range prior.DispatchIDs {
			if observed >= 20 {
				break
			}
			dispatch, exists := graph.GraphDispatch(id)
			if !exists || dispatch.Status != core.DispatchSucceeded || dispatch.Result == nil {
				continue
			}
			var validationTime time.Duration
			for _, check := range dispatch.Result.Validations {
				if !check.StartedAt.IsZero() && check.FinishedAt.After(check.StartedAt) {
					validationTime += check.FinishedAt.Sub(check.StartedAt)
				}
			}
			validation = max(validation, int64(math.Ceil(validationTime.Seconds()*1.2)))
			// UpdatedAt includes setup and validation: conservative, not model time.
			duration := int64(math.Ceil(dispatch.UpdatedAt.Sub(dispatch.CreatedAt).Seconds() * 1.2))
			if dispatch.Recipient == "REVIEWER" {
				review = max(review, duration)
			} else if dispatch.Relationship == core.RelationshipAmends {
				repair = max(repair, duration)
			}
			sameConfiguration := false
			if dispatch.Configuration != nil {
				selected, err := core.SelectApprovalConfiguration(dispatch.Configuration.Selection, dispatch.Configuration.Sources)
				sameConfiguration = err == nil && selected.Digest == request.Configuration.Digest
			}
			if sameConfiguration {
				value, status := flowNanoAIU(graph, core.ReviewFlow{DispatchIDs: []string{id}})
				if status == "complete" && value != nil {
					if dispatch.Recipient == "REVIEWER" {
						reviewNative = max(reviewNative, *value*1.2)
					} else if dispatch.Relationship == core.RelationshipAmends {
						repairNative = max(repairNative, *value*1.2)
					}
				}
			}
			observed++
			plan.Basis = append(plan.Basis, "saved-duration-with-20-percent-margin:"+dispatch.ID)
		}
	}
	if observed == 0 {
		plan.Basis = append(plan.Basis, "cold-estimate:validation-60s-per-command,review-600s,repair-600s;uncertain")
	}
	for _, field := range []struct {
		name  string
		value *int64
		cold  int64
	}{{"validation", &plan.ValidationSeconds, validation}, {"review", &plan.ReviewSeconds, review}, {"repair", &plan.RepairSeconds, repair}} {
		if *field.value == 0 {
			*field.value = field.cold
		} else {
			plan.Basis = append(plan.Basis, "explicit-duration-override:"+field.name)
		}
	}
	if request.MaxNanoAIU > 0 {
		if plan.ReviewNanoAIU == 0 {
			plan.ReviewNanoAIU = reviewNative
		}
		if plan.RepairNanoAIU == 0 {
			plan.RepairNanoAIU = repairNative
		}
	}
	if err := plan.ValidateNativeReservations(request.MaxNanoAIU); err != nil {
		return StagePlanPreview{}, err
	}
	role := "CODER"
	if plan.ReviewOnly {
		role = "REVIEWER"
	}
	minimum := plan.ReviewSeconds
	if role == "CODER" {
		minimum = 60
	}
	if plan.ReservedSeconds(0, role)+minimum > plan.TimeoutSeconds {
		return StagePlanPreview{}, fmt.Errorf("taxion: planned mandatory stages leave insufficient room; split scope or obtain approval for a new outer envelope")
	}
	request.StagePlanHash = ""
	hash, err := core.CanonicalSHA256(struct {
		Request ReviewFlowRequest `json:"request"`
		Task    core.GraphTask    `json:"task"`
		Plan    *core.StagePlan   `json:"plan"`
	}{request, task, plan})
	if err != nil {
		return StagePlanPreview{}, err
	}
	plan.Hash = "sha256:" + hash
	return StagePlanPreview{FlowID: request.ID, TaskID: task.ID, MaxNanoAIU: request.MaxNanoAIU, Plan: plan, Limitations: []string{
		"Time reservations are controller deadlines, not a completion guarantee or provider credit reservation.",
		"Session credit limits are soft; no token-to-credit or nano-AIU-to-credit conversion is performed.",
		"Positive native Flow ceilings require estimates for every reserved role from complete same-configuration usage or an explicit approval; missing estimates block planning, never imply free work.",
		"Saved duration includes host work and waiting. Cold estimates remain uncertain; overrides require a new exact plan approval.",
	}}, nil
}

func stageDeadline(flow core.ReviewFlow, dispatch core.GraphDispatch, validation bool) time.Time {
	if flow.StagePlan == nil {
		return flow.Deadline
	}
	deadline := flow.StagePlan.SubmissionDeadline(flow, dispatch.Recipient)
	if validation && dispatch.Recipient == "CODER" {
		deadline = deadline.Add(time.Duration(flow.StagePlan.ValidationSeconds) * time.Second)
	}
	return deadline
}

func stageBudgetStop(graph core.TaskGraph, flow core.ReviewFlow, dispatch core.GraphDispatch, now time.Time) string {
	if flow.StagePlan == nil {
		return ""
	}
	if !now.Before(stageDeadline(flow, dispatch, false)) {
		return "stage submission deadline reached; preserve partial output and remaining mandatory stages"
	}
	report := taskBudgetReport(graph, flow, now)
	if flow.Budget.MaxNanoAIU > 0 && (report.CreditsStatus != "complete" ||
		report.NanoAIURemaining <= flow.StagePlan.ReservedNanoAIU(flow.Repairs, dispatch.Recipient)) {
		return "native stage budget reserved for remaining mandatory stages; no provider credit reservation is implied"
	}
	return ""
}

func stageStartStop(flow core.ReviewFlow, role string, now time.Time) string {
	if flow.StagePlan == nil {
		return ""
	}
	minimum := int64(60)
	if role == "REVIEWER" {
		minimum = flow.StagePlan.ReviewSeconds
	} else if flow.Repairs > 0 {
		minimum = flow.StagePlan.RepairSeconds
	}
	if stageDeadline(flow, core.GraphDispatch{Recipient: role}, false).Sub(now) < time.Duration(minimum)*time.Second {
		return "insufficient stage time for the next mandatory role; preserve output and obtain approval for a feasible successor"
	}
	return ""
}

type StageObservation struct {
	DispatchID           string    `json:"dispatchId"`
	Role                 string    `json:"role"`
	Phase                string    `json:"phase"`
	At                   time.Time `json:"at"`
	Deadline             time.Time `json:"deadline"`
	SessionSoftLimit     float64   `json:"sessionSoftLimit"`
	UsageUnit            string    `json:"usageUnit"`
	UsageScope           string    `json:"usageScope"`
	UsageCompleteness    string    `json:"usageCompleteness"`
	Reason               string    `json:"reason,omitempty"`
	DurationMilliseconds *int64    `json:"durationMilliseconds,omitempty"`
	DurationSource       string    `json:"durationSource,omitempty"`
}

func observeStage(db *store.Store, dispatchID, phase, reason string, worker runtime.Identity) error {
	return db.UpdateWorkspace(func(state *store.Workspace) error {
		dispatch, ok := state.Graph.GraphDispatch(dispatchID)
		flow, found := flowForDispatch(state.Graph, dispatchID)
		if !ok || !found {
			return fmt.Errorf("taxion: stage observation requires a saved Flow Dispatch")
		}
		if flow.StagePlan == nil {
			return nil
		}
		ceiling := 0.0
		if provider, ok := worker.(interface{ SessionCreditCeiling() float64 }); ok {
			ceiling = provider.SessionCreditCeiling()
		}
		expected := flow.StagePlan.CoderSessionCredits
		if dispatch.Recipient == "REVIEWER" {
			expected = flow.StagePlan.ReviewerSessionCredits
		}
		if phase == "session-starting" && ceiling != expected {
			return fmt.Errorf("taxion: actual session soft ceiling %g differs from approved %g for %s", ceiling, expected, dispatch.Recipient)
		}
		report := taskBudgetReport(state.Graph, flow, time.Now().UTC())
		completeness := report.CreditsStatus
		if len(dispatch.RuntimeUsage) == 0 {
			completeness = "unavailable"
		}
		report.StageObservation = &StageObservation{
			DispatchID: dispatch.ID, Role: dispatch.Recipient, Phase: phase, At: time.Now().UTC(),
			Deadline:         stageDeadline(flow, dispatch, phase == "validation-starting" || phase == "validation-finished"),
			SessionSoftLimit: ceiling, UsageUnit: "nano-AIU", UsageScope: "saved Flow Dispatches only; excludes predecessor and outer-agent work",
			UsageCompleteness: completeness, Reason: reason,
		}
		startPhase := ""
		if phase == "submitted" || phase == "runtime-stopped" {
			startPhase = "model-starting"
		} else if phase == "validation-finished" {
			startPhase = "validation-starting"
		}
		if startPhase != "" {
			for index := len(state.Records.Budgets) - 1; index >= 0; index-- {
				saved := state.Records.Budgets[index]
				if saved.FlowID != flow.ID || saved.Kind != "STAGE_OBSERVED" {
					continue
				}
				var prior TaskBudgetReport
				if err := json.Unmarshal([]byte(saved.Payload), &prior); err != nil {
					return err
				}
				if prior.StageObservation != nil && prior.StageObservation.DispatchID == dispatch.ID &&
					prior.StageObservation.Phase == startPhase {
					duration := max(report.StageObservation.At.Sub(prior.StageObservation.At).Milliseconds(), 0)
					report.StageObservation.DurationMilliseconds = &duration
					report.StageObservation.DurationSource = "controller-wall-clock; includes tools, host work and waiting, not model-only time"
					break
				}
			}
		}
		return appendBudgetAudit(state, flow, "STAGE_OBSERVED", report)
	})
}

func (c *Controller) observeStageIfFlow(dispatchID, phase, reason string) error {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return err
	}
	if flow, found := flowForDispatch(state.Graph, dispatchID); !found || flow.StagePlan == nil {
		return nil
	}
	return observeStage(c.store, dispatchID, phase, reason, c.runtime)
}

// boundStageContext is shared by direct runtime commands and the Flow runner.
func (c *Controller) boundStageContext(ctx context.Context, dispatch core.GraphDispatch, validation bool) (context.Context, context.CancelFunc, error) {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return nil, nil, err
	}
	flow, found := flowForDispatch(state.Graph, dispatch.ID)
	if !found || flow.StagePlan == nil {
		return ctx, func() {}, nil
	}
	if flow.Status != "RUNNING" {
		return nil, nil, fmt.Errorf("taxion: stage execution requires a running Flow")
	}
	if err := verifyStagePlanBinding(state.Records, flow); err != nil {
		return nil, nil, err
	}
	if !validation {
		if reason := stageBudgetStop(state.Graph, flow, dispatch, time.Now().UTC()); reason != "" {
			return nil, nil, c.stopForStage(flow, dispatch, reason)
		}
	}
	bounded, cancel := context.WithDeadline(ctx, stageDeadline(flow, dispatch, validation))
	if bounded.Err() != nil {
		cancel()
		return nil, nil, c.stopForStage(flow, dispatch, "stage deadline reached before execution")
	}
	return bounded, cancel, nil
}

func (c *Controller) stopForStage(flow core.ReviewFlow, dispatch core.GraphDispatch, reason string) error {
	if dispatch.Status == core.DispatchIssued && dispatch.RuntimeSessionID == "" {
		return errors.Join(&budgetStopError{reason: reason},
			stopUnstartedDispatch(c.store, flow.ID, dispatch.ID, "controller", "stage-budget", reason, "BUDGET_STOP"))
	}
	return errors.Join(&budgetStopError{reason: reason}, stopFlowForBudget(c.store, flow.ID, reason), c.stopIfExecuting(dispatch.ID))
}

func (c *Controller) stageSupplement(dispatch core.GraphDispatch) (string, error) {
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return "", err
	}
	flow, found := flowForDispatch(state.Graph, dispatch.ID)
	if !found || flow.StagePlan == nil {
		return "", nil
	}
	return fmt.Sprintf("\nController stage deadline: %s (%d seconds remaining). Stop optional exploration now if needed and submit the structured final result before this boundary. Do not omit required acceptance or validation. If incomplete, report blockers; never claim success. Controller validation and independent review have separate reserved time; provider credits are not reserved.\n",
		stageDeadline(flow, dispatch, false).Format(time.RFC3339Nano),
		max(int64(time.Until(stageDeadline(flow, dispatch, false)).Seconds()), 0)), nil
}

type FlowBudgetObservation struct {
	StateChanged bool               `json:"stateChanged"`
	Budget       TaskBudgetReport   `json:"budget"`
	Stages       []StageObservation `json:"stages"`
	StopReason   string             `json:"stopReason,omitempty"`
	Continuation ContinuationAdvice `json:"continuation"`
}

type ContinuationAdvice struct {
	RemainingAcceptance []string `json:"remainingAcceptance"`
	SavedTree           string   `json:"savedTree,omitempty"`
	SavedDispatch       string   `json:"savedDispatch,omitempty"`
	IdentityStatus      string   `json:"identityStatus"`
	NextAction          string   `json:"nextAction"`
}

func ObserveFlowBudget(state store.Workspace, id string) (FlowBudgetObservation, error) {
	flow, ok := state.Graph.ReviewFlow(id)
	if !ok {
		return FlowBudgetObservation{}, fmt.Errorf("taxion: budget observation requires an existing Flow")
	}
	report := FlowBudgetObservation{Budget: taskBudgetReport(state.Graph, flow, time.Now().UTC()),
		Stages: []StageObservation{}, StopReason: core.Redact(flow.Reason),
		Continuation: ContinuationAdvice{
			RemainingAcceptance: slices.Clone(flow.Contract.Acceptance), IdentityStatus: "not-checked",
			NextAction: "Preserve worktrees. Reconcile stop evidence, inspect feedback-target and verify saved output before proposing a sourced successor Task. Record what changes make completion feasible; explain any baseline restart. Obtain approval for the successor and its new envelope; this report grants no execution authority.",
		}}
	for _, audit := range state.Records.Budgets {
		if audit.FlowID != id || audit.Kind != "STAGE_OBSERVED" {
			continue
		}
		var saved TaskBudgetReport
		if err := json.Unmarshal([]byte(audit.Payload), &saved); err != nil || saved.StageObservation == nil {
			return FlowBudgetObservation{}, fmt.Errorf("taxion: invalid saved stage observation")
		}
		observation := *saved.StageObservation
		observation.Reason = core.Redact(observation.Reason)
		report.Stages = append(report.Stages, observation)
	}
	for index := len(flow.DispatchIDs) - 1; index >= 0; index-- {
		dispatch, _ := state.Graph.GraphDispatch(flow.DispatchIDs[index])
		if dispatch.Recipient != "CODER" || dispatch.Result == nil {
			continue
		}
		result := dispatch.Result
		report.Continuation.SavedDispatch = dispatch.ID
		if result.PartialGitEvidence != nil {
			report.Continuation.SavedTree = result.PartialGitEvidence.TreeSHA
			report.Continuation.IdentityStatus = "saved-partial-evidence; live identity and quality not verified"
		} else if result.OutputTreeSHA != "" {
			report.Continuation.SavedTree = result.OutputTreeSHA
			report.Continuation.IdentityStatus = "saved-result; live identity not verified"
		}
		break
	}
	if flow.Status == "COMPLETED" {
		report.Continuation.RemainingAcceptance = []string{}
		report.Continuation.NextAction = "No execution is needed. Publication requires separate authorization."
	}
	return report, nil
}
func verifyStagePlanBinding(records store.Records, flow core.ReviewFlow) error {
	if flow.StagePlan == nil {
		return nil
	}
	for _, audit := range records.Budgets {
		if audit.FlowID != flow.ID || audit.Kind != "DISPATCH_ISSUED" {
			continue
		}
		var report TaskBudgetReport
		if err := json.Unmarshal([]byte(audit.Payload), &report); err != nil {
			return err
		}
		if report.ResumeBasis == nil || !reflect.DeepEqual(report.ResumeBasis.StagePlan, flow.StagePlan) {
			return fmt.Errorf("taxion: saved stage plan differs from its immutable issuance approval")
		}
		return nil
	}
	return fmt.Errorf("taxion: stage plan lacks an immutable issuance approval")
}
