package controller

import (
	"encoding/json"
	"math"
	"time"

	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

// The warning requests a human decision; it never pauses or cancels a turn.
func resourceWarning(state store.Workspace, dispatch core.GraphDispatch, now time.Time) (*runtime.Event, string, error) {
	flow, found := flowForDispatch(state.Graph, dispatch.ID)
	if !found || flow.Status != "RUNNING" {
		return nil, "", nil
	}
	key, err := core.CanonicalSHA256(struct {
		Deadline time.Time `json:"deadline"`
		Limit    float64   `json:"limit"`
	}{flow.ExecutionDeadline(), flow.NativeLimit()})
	if err != nil {
		return nil, "", err
	}
	roleSeconds, reserved := int64(600), int64(0)
	if flow.StagePlan != nil {
		roleSeconds = flow.StagePlan.ReviewSeconds
		if dispatch.Recipient == "CODER" {
			roleSeconds = flow.StagePlan.RepairSeconds
		}
		reserved = flow.StagePlan.ReservedSeconds(flow.Repairs, dispatch.Recipient) + flow.StagePlan.ValidationSeconds
	}
	// Conservative remaining-work estimates are not a claim about model progress.
	needed := time.Duration(min(roleSeconds+reserved+300+60, math.MaxInt64/int64(time.Second))) * time.Second
	report := taskBudgetReport(state.Graph, flow, now)
	timeShort := flow.ExecutionDeadline().Sub(now) <= needed
	usageShort := flow.NativeLimit() > 0 && report.CreditsStatus == "complete" &&
		report.NanoAIURemaining <= flow.NativeLimit()*0.2
	if !timeShort && !usageShort {
		return nil, key, nil
	}
	deadline := flow.ExecutionDeadline()
	if timeShort {
		deadline = now.Add(needed)
	}
	payload, err := json.Marshal(struct {
		Version              int              `json:"version"`
		FlowID               string           `json:"flowId"`
		DispatchID           string           `json:"dispatchId"`
		SessionID            string           `json:"sessionId"`
		TimeShortfall        bool             `json:"timeShortfall"`
		NativeUsageNearLimit bool             `json:"nativeUsageNearLimit"`
		Budget               TaskBudgetReport `json:"budget"`
		ProposedDeadline     time.Time        `json:"proposedDeadline"`
		AssessmentRequired   bool             `json:"assessmentRequired"`
		ExecutionAuthorized  bool             `json:"executionAuthorized"`
		Action               string           `json:"action"`
	}{1, flow.ID, dispatch.ID, dispatch.RuntimeSessionID, timeShort, usageShort, report, deadline, true, false,
		"Assess actual progress and remaining mandatory work. For a version 2 plan, first reallocate forecasts within the approved outer envelope; an estimate alone does not require new consent. If additional resources are still needed, ask once now for time and any observed same-unit budget shortfall, including validation/review, repair, preservation and response wait. Use workflow budget --file for external cumulative spending and workflow extend --preview for exact live Flow approval. Do not cancel valid execution while awaiting consent. No automatic top-up, usage reset, unit conversion or caller-deadline override. Stop safely only at an actual unextended hard boundary."})
	if err != nil {
		return nil, "", err
	}
	return &runtime.Event{Type: "resource_approval_required", Payload: string(payload)}, key, nil
}
