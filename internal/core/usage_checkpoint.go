package core

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// UsageSummary is a derived view, not another persisted usage observation.
type UsageSummary struct {
	NanoAIU           *float64
	CreditsStatus     string
	InputOutputTokens *int64
	TokenStatus       string
}

func SummarizeUsage(sessionID string, calls []DispatchUsage, checkpoints []UsageCheckpoint) (UsageSummary, error) {
	summary := UsageSummary{CreditsStatus: "unavailable", TokenStatus: "unavailable"}
	var unique []DispatchUsage
	for _, call := range calls {
		if err := AppendUsage(&unique, call); err != nil {
			return summary, err
		}
	}
	calls = unique
	cost, tokens := 0.0, int64(0)
	costSeen, tokensSeen := false, false
	tokensOverflow := false
	costComplete, tokensComplete := len(calls) != 0, len(calls) != 0
	addTokens := func(total *int64, values ...*int64) error {
		for _, value := range values {
			if value == nil {
				continue
			}
			if *value < 0 {
				return fmt.Errorf("taxion: invalid usage tokens")
			}
			if *value > math.MaxInt64-*total {
				*total = math.MaxInt64
				tokensOverflow = true
				continue
			}
			*total += *value
		}
		return nil
	}
	for _, call := range calls {
		if call.Accounting != nil && sessionID != "" && call.Accounting.SourceSessionID != sessionID {
			return summary, fmt.Errorf("taxion: usage belongs to another session")
		}
		if call.TotalNanoAIU != nil {
			if math.IsNaN(*call.TotalNanoAIU) || math.IsInf(*call.TotalNanoAIU, 0) || *call.TotalNanoAIU < 0 {
				return summary, fmt.Errorf("taxion: invalid usage cost")
			}
			cost += *call.TotalNanoAIU
			costSeen = true
		}
		costComplete = costComplete && call.Scope == "" && call.TotalNanoAIU != nil &&
			call.AICreditsStatus != nil && *call.AICreditsStatus == "complete"
		if err := addTokens(&tokens, call.InputTokens, call.OutputTokens); err != nil {
			return summary, err
		}
		tokensSeen = tokensSeen || call.InputTokens != nil || call.OutputTokens != nil
		tokensComplete = tokensComplete && call.Scope == "" && call.InputTokens != nil && call.OutputTokens != nil
	}
	type observation struct {
		checkpoint    UsageCheckpoint
		cost          float64
		tokens        int64
		complete      bool
		tokensSeen    bool
		tokensCovered bool
	}
	var observations []observation
	for _, checkpoint := range checkpoints {
		payload, err := json.Marshal(checkpoint)
		if err != nil {
			return summary, err
		}
		checkpoint, complete, err := DecodeUsageCheckpoint(string(payload), sessionID, calls)
		if err != nil {
			return summary, err
		}
		current := observation{
			checkpoint: checkpoint, cost: checkpoint.KnownNanoAIU(calls), complete: complete,
			tokensCovered: len(checkpoint.UsageTotals) != 0 || len(checkpoint.ProviderModelMetrics) != 0,
		}
		var modelTokens, providerTokens int64
		for _, total := range checkpoint.UsageTotals {
			if err := addTokens(&modelTokens, total.InputTokens, total.OutputTokens); err != nil {
				return summary, err
			}
			costSeen = costSeen || total.TotalNanoAIU != nil
			current.tokensSeen = true
		}
		for _, total := range checkpoint.ProviderModelMetrics {
			if err := addTokens(&providerTokens, &total.Metrics.Usage.InputTokens, &total.Metrics.Usage.OutputTokens); err != nil {
				return summary, err
			}
			costSeen = costSeen || total.Metrics.TotalNanoAIU != nil
			current.tokensSeen = true
		}
		current.tokens = max(modelTokens, providerTokens)
		for _, call := range calls {
			if call.Accounting == nil {
				continue
			}
			if watermark, exists := checkpoint.Watermarks[call.Accounting.SourceSessionID]; exists &&
				call.Accounting.Sequence > watermark {
				if err := addTokens(&current.tokens, call.InputTokens, call.OutputTokens); err != nil {
					return summary, err
				}
				current.tokensSeen = current.tokensSeen || call.InputTokens != nil || call.OutputTokens != nil
			}
		}
		costSeen = costSeen || checkpoint.TotalNanoAIU != nil
		tokensSeen = tokensSeen || current.tokensSeen
		cost, tokens = max(cost, current.cost), max(tokens, current.tokens)
		observations = append(observations, current)
	}
	if math.IsInf(cost, 0) || math.IsNaN(cost) {
		return summary, fmt.Errorf("taxion: usage cost overflowed")
	}
	if len(observations) != 0 {
		costComplete, tokensComplete = false, false
		for _, candidate := range observations {
			if !candidate.complete {
				continue
			}
			covers := true
			for _, other := range observations {
				for source, watermark := range other.checkpoint.Watermarks {
					if candidate.checkpoint.Watermarks[source] < watermark {
						covers = false
					}
				}
			}
			costComplete = costComplete || covers && candidate.cost+1e-6 >= cost
			tokensComplete = tokensComplete || covers && candidate.tokensCovered && candidate.tokensSeen && candidate.tokens >= tokens
		}
	}
	if costSeen {
		summary.NanoAIU, summary.CreditsStatus = &cost, "partial"
		if costComplete {
			summary.CreditsStatus = "complete"
		}
	}
	if tokensSeen {
		summary.InputOutputTokens, summary.TokenStatus = &tokens, "partial"
		if tokensComplete && !tokensOverflow {
			summary.TokenStatus = "complete"
		}
	}
	return summary, nil
}

// UsageCheckpoint subsumes preceding receipts; it is never another API call.
type UsageCheckpoint struct {
	UsageTotals          []DispatchUsage       `json:"usageTotals,omitempty"`
	ProviderModelMetrics []ProviderModelMetric `json:"providerModelMetrics,omitempty"`
	TotalNanoAIU         *float64              `json:"totalNanoAIU,omitempty"`
	AICreditsStatus      *string               `json:"aiCreditsStatus,omitempty"`
	TotalPremiumRequests *float64              `json:"totalPremiumRequests,omitempty"`
	Watermarks           map[string]int64      `json:"watermarks,omitempty"`
}

func DecodeUsageCheckpoint(payload, sessionID string, calls []DispatchUsage) (UsageCheckpoint, bool, error) {
	fail := func() (UsageCheckpoint, bool, error) {
		return UsageCheckpoint{}, false, fmt.Errorf("taxion: invalid or conflicting usage checkpoint")
	}
	var checkpoint UsageCheckpoint
	if DecodeGraphInput(strings.NewReader(payload), &checkpoint) != nil {
		return fail()
	}
	var unique []DispatchUsage
	for _, call := range calls {
		if AppendUsage(&unique, call) != nil {
			return fail()
		}
	}
	calls = unique
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
	var covered []DispatchUsage
	tailComplete := true
	for _, call := range calls {
		if !ValidUsageMetadata(call) {
			return fail()
		}
		if call.TotalNanoAIU != nil {
			if math.IsNaN(*call.TotalNanoAIU) || math.IsInf(*call.TotalNanoAIU, 0) || *call.TotalNanoAIU < 0 {
				return fail()
			}
		}
		if call.Accounting != nil {
			if sessionID != "" && call.Accounting.SourceSessionID != sessionID || call.Accounting.Sequence <= 0 {
				return fail()
			}
		}
		if call.Accounting != nil {
			if watermark, exists := checkpoint.Watermarks[call.Accounting.SourceSessionID]; exists &&
				call.Accounting.Sequence > watermark {
				tailComplete = tailComplete && call.TotalNanoAIU != nil && call.AICreditsStatus != nil &&
					*call.AICreditsStatus == "complete" && call.Scope == "" &&
					call.InputTokens != nil && call.OutputTokens != nil &&
					*call.InputTokens >= 0 && *call.OutputTokens >= 0
				continue
			}
			last = max(last, call.Accounting.Sequence)
		}
		covered = append(covered, call)
		if call.TotalNanoAIU != nil {
			known += *call.TotalNanoAIU
		}
	}
	calls = covered
	modelKnown := 0.0
	seen := map[string]bool{}
	for _, total := range checkpoint.UsageTotals {
		if !ValidUsageMetadata(total) || seen[total.Model] || total.Accounting != nil || total.Scope != "" ||
			total.InputTokens == nil || total.OutputTokens == nil {
			return fail()
		}
		seen[total.Model] = true
		if total.TotalNanoAIU != nil {
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
		math.IsInf(providerKnown, 0) || UsageBelowKnown(calls, checkpoint.UsageTotals) {
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
		return checkpoint, tailComplete, nil
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
			var providerTotals []DispatchUsage
			for _, total := range checkpoint.ProviderModelMetrics {
				if total.ModelID == nil {
					complete = false
					continue
				}
				metric := total.Metrics
				providerTotals = append(providerTotals, DispatchUsage{
					Model: *total.ModelID, InputTokens: &metric.Usage.InputTokens,
					OutputTokens: &metric.Usage.OutputTokens, CacheReadTokens: &metric.Usage.CacheReadTokens,
					CacheWriteTokens: &metric.Usage.CacheWriteTokens, TotalNanoAIU: metric.TotalNanoAIU,
				})
			}
			if len(providerTotals) == len(checkpoint.ProviderModelMetrics) {
				matches, conflict := CompareUsage(providerTotals, checkpoint.UsageTotals)
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
		return checkpoint, complete && tailComplete, nil
	}
	matches, _ := CompareUsage(calls, checkpoint.UsageTotals)
	hasProviderCalls := false
	for _, call := range calls {
		hasProviderCalls = hasProviderCalls || call.Provider != nil
	}
	if hasProviderCalls || len(checkpoint.ProviderModelMetrics) != 0 {
		providerMatches, conflict := CompareProviderModelUsage(calls, checkpoint.ProviderModelMetrics)
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
	if !matches && len(calls) != 0 && sessionID != "" {
		watermark, scoped := checkpoint.Watermarks[sessionID]
		scoped = scoped && watermark >= last
		for _, call := range calls {
			scoped = scoped && call.Accounting != nil && call.Scope == ""
			modelCovered := len(checkpoint.UsageTotals) == 0
			for _, total := range checkpoint.UsageTotals {
				modelCovered = modelCovered || total.Model == call.Model
			}
			providerCovered := len(checkpoint.ProviderModelMetrics) == 0 && call.Provider == nil
			for _, total := range checkpoint.ProviderModelMetrics {
				if total.ModelID == nil || *total.ModelID != call.Model {
					continue
				}
				providerCovered = providerCovered || call.Provider == nil && total.Provider == nil ||
					call.Provider != nil && total.Provider != nil &&
						call.Provider.ID == total.Provider.ID && call.Provider.Kind == total.Provider.Kind
			}
			scoped = scoped && modelCovered && providerCovered
		}
		if scoped {
			_, complete, err := DecodeUsageCheckpoint(payload, sessionID, nil)
			if err != nil {
				return fail()
			}
			matches = complete
		}
	}
	return checkpoint, matches && tailComplete, nil
}

// KnownNanoAIU counts only costs known to be outside the snapshot's watermarks.
func (checkpoint UsageCheckpoint) KnownNanoAIU(calls []DispatchUsage) float64 {
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
	seen := map[UsageAccountingIdentity]bool{}
	for _, call := range calls {
		if call.TotalNanoAIU == nil || call.Accounting == nil {
			continue
		}
		if seen[*call.Accounting] {
			continue
		}
		seen[*call.Accounting] = true
		watermark, exists := checkpoint.Watermarks[call.Accounting.SourceSessionID]
		if exists && call.Accounting.Sequence > watermark {
			total += *call.TotalNanoAIU
		}
	}
	return total
}
