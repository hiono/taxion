package controller

import (
	"fmt"
	"math"
	"slices"

	"github.com/hiono/taxion/internal/core"
)

// FlowEvaluation reports saved evidence, not an independent judgment or reward.
type FlowEvaluation struct {
	FlowID                 string              `json:"flowId"`
	QualityGate            string              `json:"qualityGate"`
	CostComparisonEligible bool                `json:"costComparisonEligible"`
	Unmet                  []string            `json:"unmet"`
	Dispatches             []EvaluatedDispatch `json:"dispatches"`
	Repairs                int                 `json:"repairs"`
	InputOutputTokens      *int64              `json:"inputOutputTokens"`
	TokenStatus            string              `json:"tokenStatus"`
	CostScope              string              `json:"costScope"`
	ElapsedMilliseconds    *int64              `json:"elapsedMilliseconds"`
	ExecutionObservations  int                 `json:"executionObservations"`
	Unmeasured             []string            `json:"unmeasured"`
}

type EvaluatedDispatch struct {
	ID              string `json:"id"`
	Role            string `json:"role"`
	InstructionHash string `json:"instructionHash"`
	OutputSHA       string `json:"outputSha,omitempty"`
	OutputTreeSHA   string `json:"outputTreeSha,omitempty"`
}

func EvaluateReviewFlow(graph core.TaskGraph, id string) (FlowEvaluation, error) {
	if err := graph.Validate(); err != nil {
		return FlowEvaluation{}, fmt.Errorf("taxion: evaluation requires a valid saved graph: %w", err)
	}
	flow, ok := graph.ReviewFlow(id)
	if !ok {
		return FlowEvaluation{}, fmt.Errorf("taxion: evaluation requires an existing flow")
	}
	report := FlowEvaluation{
		FlowID: id, QualityGate: "UNMET", Unmet: []string{}, Dispatches: []EvaluatedDispatch{},
		Repairs: flow.Repairs, TokenStatus: "unavailable",
		CostScope: "Listed Flow Dispatches only; excludes planning, predecessor flows and outer-agent work.",
		Unmeasured: []string{
			"Actual goal achievement and missed defects require independent reference outcomes.",
			"Question information value and unnecessary communication require labeled observations; counts are not penalties.",
			"Execution observations are recorded tool/skill observations, not a complete tool-call or communication count.",
			"Token completeness describes recorded usage fields, not independently verified provider billing coverage.",
			"Elapsed time includes waiting and host work, not only model execution.",
			"Money, comparable-condition cost improvement and model judgment improvement are not established.",
			"Saved approval is not proof of current Git, authentication, publication permission or semantic documentation truth.",
		},
	}
	if flow.Status != "COMPLETED" {
		report.Unmet = append(report.Unmet, "Flow has not completed with independent same-target acceptance.")
	}
	if !slices.Contains(flow.Contract.Acceptance, core.DocumentationAcceptance) {
		report.Unmet = append(report.Unmet, "Approved contract lacks the documentation-consistency criterion; legacy approvals are not upgraded retroactively.")
	}
	var total int64
	complete, observed := true, false
	seenUsage := map[core.UsageAccountingIdentity]bool{}
	for _, dispatchID := range flow.DispatchIDs {
		dispatch, _ := graph.GraphDispatch(dispatchID)
		entry := EvaluatedDispatch{ID: dispatch.ID, Role: dispatch.Recipient, InstructionHash: dispatch.InstructionHash}
		if dispatch.Result != nil {
			entry.OutputSHA, entry.OutputTreeSHA = dispatch.Result.OutputSHA, dispatch.Result.OutputTreeSHA
			report.ExecutionObservations += len(dispatch.Result.ExecutionEvidence)
		}
		report.Dispatches = append(report.Dispatches, entry)
		if len(dispatch.RuntimeUsage) == 0 {
			complete = false
		}
		for _, usage := range dispatch.RuntimeUsage {
			observed = true
			if usage.Accounting != nil {
				if seenUsage[*usage.Accounting] {
					complete = false
					continue
				}
				seenUsage[*usage.Accounting] = true
			}
			if usage.Scope != "" || usage.InputTokens == nil || usage.OutputTokens == nil {
				complete = false
				continue
			}
			for _, value := range []int64{*usage.InputTokens, *usage.OutputTokens} {
				if value < 0 || value > math.MaxInt64-total {
					complete = false
					continue
				}
				total += value
			}
		}
	}
	if observed {
		report.TokenStatus = "partial"
		if complete {
			report.InputOutputTokens, report.TokenStatus = &total, "complete"
		}
	}
	if flow.Status == "COMPLETED" && len(flow.DispatchIDs) != 0 {
		last, _ := graph.GraphDispatch(flow.DispatchIDs[len(flow.DispatchIDs)-1])
		if !last.UpdatedAt.Before(flow.StartedAt) {
			elapsed := last.UpdatedAt.Sub(flow.StartedAt).Milliseconds()
			report.ElapsedMilliseconds = &elapsed
		}
	}
	if len(report.Unmet) == 0 {
		report.QualityGate, report.CostComparisonEligible = "RECORDED_PASS", true
	}
	return report, nil
}
