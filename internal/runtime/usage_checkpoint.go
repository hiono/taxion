package runtime

import (
	"fmt"
	"math"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

// UsageCheckpoint subsumes preceding receipts; it is never another API call.
type UsageCheckpoint struct {
	UsageTotals          []core.DispatchUsage       `json:"usageTotals,omitempty"`
	ProviderModelMetrics []core.ProviderModelMetric `json:"providerModelMetrics,omitempty"`
	TotalNanoAIU         *float64                   `json:"totalNanoAIU,omitempty"`
	AICreditsStatus      *string                    `json:"aiCreditsStatus,omitempty"`
	TotalPremiumRequests *float64                   `json:"totalPremiumRequests,omitempty"`
	Watermarks           map[string]int64           `json:"watermarks,omitempty"`
}

func DecodeUsageCheckpoint(payload, sessionID string, calls []core.DispatchUsage) (UsageCheckpoint, bool, error) {
	fail := func() (UsageCheckpoint, bool, error) {
		return UsageCheckpoint{}, false, fmt.Errorf("taxion: invalid or conflicting usage checkpoint")
	}
	var checkpoint UsageCheckpoint
	if core.DecodeGraphInput(strings.NewReader(payload), &checkpoint) != nil {
		return fail()
	}
	if checkpoint.TotalPremiumRequests != nil &&
		(math.IsNaN(*checkpoint.TotalPremiumRequests) || math.IsInf(*checkpoint.TotalPremiumRequests, 0) ||
			*checkpoint.TotalPremiumRequests < 0) {
		return fail()
	}
	for source, sequence := range checkpoint.Watermarks {
		if source == "" || sequence < 0 || sessionID != "" && source != sessionID {
			return fail()
		}
	}
	known := 0.0
	var last int64
	for _, call := range calls {
		if call.TotalNanoAIU != nil {
			if math.IsNaN(*call.TotalNanoAIU) || math.IsInf(*call.TotalNanoAIU, 0) || *call.TotalNanoAIU < 0 {
				return fail()
			}
			known += *call.TotalNanoAIU
		}
		if call.Accounting != nil {
			if sessionID != "" && call.Accounting.SourceSessionID != sessionID || call.Accounting.Sequence <= 0 {
				return fail()
			}
			last = max(last, call.Accounting.Sequence)
		}
	}
	modelKnown := 0.0
	seen := map[string]bool{}
	for _, total := range checkpoint.UsageTotals {
		if strings.TrimSpace(total.Model) == "" || seen[total.Model] || total.Accounting != nil || total.Scope != "" ||
			total.InputTokens == nil || total.OutputTokens == nil {
			return fail()
		}
		seen[total.Model] = true
		for _, value := range []*int64{total.InputTokens, total.OutputTokens, total.CacheReadTokens, total.CacheWriteTokens} {
			if value != nil && *value < 0 {
				return fail()
			}
		}
		if total.RequestCount != nil && *total.RequestCount < 0 ||
			total.RequestCost != nil && (math.IsNaN(*total.RequestCost) || math.IsInf(*total.RequestCost, 0) || *total.RequestCost < 0) ||
			total.ReasoningTokens != nil && *total.ReasoningTokens < 0 {
			return fail()
		}
		for _, value := range total.TokenDetails {
			if value < 0 {
				return fail()
			}
		}
		if total.TotalNanoAIU != nil {
			if math.IsNaN(*total.TotalNanoAIU) || math.IsInf(*total.TotalNanoAIU, 0) || *total.TotalNanoAIU < 0 {
				return fail()
			}
			modelKnown += *total.TotalNanoAIU
		}
	}
	providerKnown := 0.0
	providerSeen := map[string]bool{}
	for _, total := range checkpoint.ProviderModelMetrics {
		key := "<nil>"
		if total.ModelID != nil {
			key = *total.ModelID
		}
		if total.Provider != nil {
			key += "\x00" + total.Provider.ID + "\x00" + total.Provider.Kind
			if total.Provider.ID == "" || total.Provider.Kind == "" {
				return fail()
			}
		}
		if total.ModelID != nil && strings.TrimSpace(*total.ModelID) == "" {
			return fail()
		}
		if providerSeen[key] {
			return fail()
		}
		providerSeen[key] = true
		metric := total.Metrics
		if metric.Requests.Count < 0 || math.IsNaN(metric.Requests.Cost) ||
			math.IsInf(metric.Requests.Cost, 0) || metric.Requests.Cost < 0 {
			return fail()
		}
		for _, value := range []int64{
			metric.Usage.InputTokens, metric.Usage.OutputTokens,
			metric.Usage.CacheReadTokens, metric.Usage.CacheWriteTokens,
		} {
			if value < 0 {
				return fail()
			}
		}
		if metric.Usage.ReasoningTokens != nil && *metric.Usage.ReasoningTokens < 0 {
			return fail()
		}
		for _, value := range metric.TokenDetails {
			if value < 0 {
				return fail()
			}
		}
		if metric.TotalNanoAIU != nil {
			if math.IsNaN(*metric.TotalNanoAIU) || math.IsInf(*metric.TotalNanoAIU, 0) || *metric.TotalNanoAIU < 0 {
				return fail()
			}
			providerKnown += *metric.TotalNanoAIU
		}
	}
	if math.IsNaN(known) || math.IsInf(known, 0) || math.IsInf(modelKnown, 0) ||
		math.IsInf(providerKnown, 0) || core.UsageBelowKnown(calls, checkpoint.UsageTotals) {
		return fail()
	}
	if checkpoint.TotalNanoAIU != nil &&
		(math.IsNaN(*checkpoint.TotalNanoAIU) || math.IsInf(*checkpoint.TotalNanoAIU, 0) ||
			*checkpoint.TotalNanoAIU < 0 || *checkpoint.TotalNanoAIU+1e-6 < max(known, modelKnown, providerKnown)) {
		return fail()
	}
	if len(checkpoint.UsageTotals) == 0 && len(checkpoint.ProviderModelMetrics) == 0 && checkpoint.TotalNanoAIU == nil &&
		len(checkpoint.Watermarks) == 0 && checkpoint.AICreditsStatus == nil {
		return checkpoint, false, nil
	}
	if len(calls) == 0 && len(checkpoint.UsageTotals) == 0 &&
		len(checkpoint.ProviderModelMetrics) == 0 && checkpoint.TotalNanoAIU != nil &&
		*checkpoint.TotalNanoAIU == 0 && checkpoint.AICreditsStatus != nil &&
		*checkpoint.AICreditsStatus == "complete" {
		for _, watermark := range checkpoint.Watermarks {
			if watermark != 0 {
				return checkpoint, false, nil
			}
		}
		return checkpoint, true, nil
	}
	if len(calls) == 0 && checkpoint.TotalNanoAIU != nil &&
		checkpoint.AICreditsStatus != nil && *checkpoint.AICreditsStatus == "complete" &&
		sessionID != "" {
		_, scoped := checkpoint.Watermarks[sessionID]
		complete := scoped && (len(checkpoint.UsageTotals) != 0 || len(checkpoint.ProviderModelMetrics) != 0)
		for _, total := range checkpoint.UsageTotals {
			complete = complete && total.TotalNanoAIU != nil &&
				total.AICreditsStatus != nil && *total.AICreditsStatus == "complete"
		}
		for _, total := range checkpoint.ProviderModelMetrics {
			complete = complete && total.Metrics.TotalNanoAIU != nil &&
				total.Metrics.AICreditsStatus != nil && *total.Metrics.AICreditsStatus == "complete"
		}
		if len(checkpoint.UsageTotals) != 0 && len(checkpoint.ProviderModelMetrics) != 0 {
			var providerTotals []core.DispatchUsage
			for _, total := range checkpoint.ProviderModelMetrics {
				if total.ModelID == nil {
					complete = false
					continue
				}
				metric := total.Metrics
				providerTotals = append(providerTotals, core.DispatchUsage{
					Model: *total.ModelID, InputTokens: &metric.Usage.InputTokens,
					OutputTokens: &metric.Usage.OutputTokens, CacheReadTokens: &metric.Usage.CacheReadTokens,
					CacheWriteTokens: &metric.Usage.CacheWriteTokens, TotalNanoAIU: metric.TotalNanoAIU,
				})
			}
			if len(providerTotals) == len(checkpoint.ProviderModelMetrics) {
				matches, conflict := core.CompareUsage(providerTotals, checkpoint.UsageTotals)
				if conflict {
					return fail()
				}
				complete = complete && matches
			}
		}
		if len(checkpoint.UsageTotals) != 0 {
			complete = complete && math.Abs(*checkpoint.TotalNanoAIU-modelKnown) <= 1e-6
		}
		if len(checkpoint.ProviderModelMetrics) != 0 {
			complete = complete && math.Abs(*checkpoint.TotalNanoAIU-providerKnown) <= 1e-6
		}
		return checkpoint, complete, nil
	}
	matches, _ := core.CompareUsage(calls, checkpoint.UsageTotals)
	hasProviderCalls := false
	for _, call := range calls {
		hasProviderCalls = hasProviderCalls || call.Provider != nil
	}
	if hasProviderCalls || len(checkpoint.ProviderModelMetrics) != 0 {
		providerMatches, conflict := core.CompareProviderModelUsage(calls, checkpoint.ProviderModelMetrics)
		if conflict {
			return fail()
		}
		if len(checkpoint.UsageTotals) == 0 {
			matches = providerMatches
		} else {
			matches = matches && providerMatches
		}
		for _, total := range checkpoint.ProviderModelMetrics {
			if total.Metrics.AICreditsStatus == nil || *total.Metrics.AICreditsStatus != "complete" {
				matches = false
			}
		}
	}
	if checkpoint.TotalNanoAIU == nil || checkpoint.AICreditsStatus == nil || *checkpoint.AICreditsStatus != "complete" {
		matches = false
	}
	for _, call := range calls {
		if call.Scope != "" || call.TotalNanoAIU == nil ||
			call.AICreditsStatus != nil && *call.AICreditsStatus != "complete" {
			matches = false
		}
	}
	for _, total := range checkpoint.UsageTotals {
		if total.AICreditsStatus != nil && *total.AICreditsStatus != "complete" {
			matches = false
		}
	}
	if checkpoint.TotalNanoAIU != nil && math.Abs(*checkpoint.TotalNanoAIU-known) > 1e-6 {
		matches = false
	}
	if len(checkpoint.Watermarks) != 0 {
		watermark := checkpoint.Watermarks[sessionID]
		if sessionID != "" && watermark < last {
			return fail()
		}
		if watermark != last {
			matches = false
		}
	}
	return checkpoint, matches, nil
}

// KnownNanoAIU counts only costs known to be outside the snapshot's watermarks.
func (checkpoint UsageCheckpoint) KnownNanoAIU(calls []core.DispatchUsage) float64 {
	total := 0.0
	for _, usage := range checkpoint.UsageTotals {
		if usage.TotalNanoAIU != nil {
			total += *usage.TotalNanoAIU
		}
	}
	providerTotal := 0.0
	for _, usage := range checkpoint.ProviderModelMetrics {
		if usage.Metrics.TotalNanoAIU != nil {
			providerTotal += *usage.Metrics.TotalNanoAIU
		}
	}
	total = max(total, providerTotal)
	if checkpoint.TotalNanoAIU != nil {
		total = max(total, *checkpoint.TotalNanoAIU)
	}
	for _, call := range calls {
		if call.TotalNanoAIU == nil || call.Accounting == nil {
			continue
		}
		watermark, exists := checkpoint.Watermarks[call.Accounting.SourceSessionID]
		if exists && call.Accounting.Sequence > watermark {
			total += *call.TotalNanoAIU
		}
	}
	return total
}
