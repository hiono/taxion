package core

import (
	"math"
	"strings"
)

// CompareUsage distinguishes an exact observation from missing or conflicting totals.
func CompareUsage(calls, totals []DispatchUsage) (bool, bool) {
	return compareUsage(calls, totals, true)
}

// UsageBelowKnown rejects only contradictions with observed subtotals.
func UsageBelowKnown(calls, totals []DispatchUsage) bool {
	_, conflict := compareUsage(calls, totals, false)
	return conflict
}

func CompareProviderModelUsage(calls []DispatchUsage, totals []ProviderModelMetric) (bool, bool) {
	type usage struct {
		input, output, cacheRead, cacheWrite int64
		cost                                 float64
		costKnown, complete                  bool
	}
	key := func(model string, provider *UsageModelProvider) string {
		if strings.TrimSpace(model) == "" {
			return ""
		}
		if provider == nil {
			return model + "\x00"
		}
		if provider.ID == "" || provider.Kind == "" {
			return ""
		}
		return strings.Join([]string{model, provider.ID, provider.Kind}, "\x00")
	}
	sums := make(map[string]usage)
	complete := len(calls) != 0 && len(totals) != 0
	for _, call := range calls {
		id := key(call.Model, call.Provider)
		if id == "" || call.InputTokens == nil || call.OutputTokens == nil {
			complete = false
			continue
		}
		value, exists := sums[id]
		if !exists {
			value.complete = true
		}
		if call.InputTokens != nil {
			if *call.InputTokens < 0 || *call.InputTokens > math.MaxInt64-value.input {
				return false, true
			}
			value.input += *call.InputTokens
		}
		if call.OutputTokens != nil {
			if *call.OutputTokens < 0 || *call.OutputTokens > math.MaxInt64-value.output {
				return false, true
			}
			value.output += *call.OutputTokens
		}
		if call.CacheReadTokens == nil {
			value.complete = false
		} else if *call.CacheReadTokens < 0 || *call.CacheReadTokens > math.MaxInt64-value.cacheRead {
			return false, true
		} else {
			value.cacheRead += *call.CacheReadTokens
		}
		if call.CacheWriteTokens == nil {
			value.complete = false
		} else if *call.CacheWriteTokens < 0 || *call.CacheWriteTokens > math.MaxInt64-value.cacheWrite {
			return false, true
		} else {
			value.cacheWrite += *call.CacheWriteTokens
		}
		if call.TotalNanoAIU == nil {
			value.complete = false
		} else if math.IsNaN(*call.TotalNanoAIU) || math.IsInf(*call.TotalNanoAIU, 0) || *call.TotalNanoAIU < 0 {
			return false, true
		} else {
			value.cost += *call.TotalNanoAIU
			value.costKnown = true
		}
		if math.IsInf(value.cost, 0) {
			return false, true
		}
		sums[id] = value
	}
	seen := make(map[string]bool)
	matched := 0
	for _, total := range totals {
		if total.ModelID == nil {
			complete = false
			continue
		}
		id := key(*total.ModelID, total.Provider)
		if id == "" || seen[id] {
			complete = false
			continue
		}
		seen[id] = true
		value, ok := sums[id]
		if !ok {
			continue
		}
		metric := total.Metrics
		if metric.Usage.InputTokens < value.input || metric.Usage.OutputTokens < value.output ||
			metric.Usage.CacheReadTokens < value.cacheRead || metric.Usage.CacheWriteTokens < value.cacheWrite ||
			value.costKnown && (metric.TotalNanoAIU == nil || *metric.TotalNanoAIU+1e-6 < value.cost) {
			return false, true
		}
		matched++
		if metric.Usage.InputTokens != value.input || metric.Usage.OutputTokens != value.output ||
			metric.Usage.CacheReadTokens != value.cacheRead || metric.Usage.CacheWriteTokens != value.cacheWrite ||
			value.costKnown && (metric.TotalNanoAIU == nil || math.Abs(*metric.TotalNanoAIU-value.cost) > 1e-6) ||
			!value.complete {
			complete = false
		}
	}
	return complete && matched == len(sums) && matched == len(totals), false
}

func compareUsage(calls, totals []DispatchUsage, completeCalls bool) (bool, bool) {
	if len(calls) == 0 || len(totals) == 0 {
		return false, completeCalls && len(calls) == 0 && len(totals) != 0
	}
	type modelUsage struct {
		input, output, cacheRead, cacheWrite                     int64
		inputKnown, outputKnown, cacheReadKnown, cacheWriteKnown bool
		cacheReadComplete, cacheWriteComplete                    bool
		nanoAIU                                                  float64
		nanoAIUKnown, nanoAIUComplete                            bool
	}
	sum := map[string]modelUsage{}
	for _, call := range calls {
		if strings.TrimSpace(call.Model) == "" || call.InputTokens == nil || call.OutputTokens == nil ||
			*call.InputTokens < 0 || *call.OutputTokens < 0 {
			return false, true
		}
		value := sum[call.Model]
		if _, exists := sum[call.Model]; !exists {
			value.cacheReadComplete, value.cacheWriteComplete, value.nanoAIUComplete = true, true, true
		}
		if *call.InputTokens > math.MaxInt64-value.input || *call.OutputTokens > math.MaxInt64-value.output {
			return false, true
		}
		value.input += *call.InputTokens
		value.output += *call.OutputTokens
		value.inputKnown, value.outputKnown = true, true
		if call.CacheReadTokens != nil {
			if *call.CacheReadTokens < 0 || *call.CacheReadTokens > math.MaxInt64-value.cacheRead {
				return false, true
			}
			value.cacheRead += *call.CacheReadTokens
			value.cacheReadKnown = true
		} else {
			value.cacheReadComplete = false
		}
		if call.CacheWriteTokens != nil {
			if *call.CacheWriteTokens < 0 || *call.CacheWriteTokens > math.MaxInt64-value.cacheWrite {
				return false, true
			}
			value.cacheWrite += *call.CacheWriteTokens
			value.cacheWriteKnown = true
		} else {
			value.cacheWriteComplete = false
		}
		if call.TotalNanoAIU != nil {
			if math.IsNaN(*call.TotalNanoAIU) || math.IsInf(*call.TotalNanoAIU, 0) || *call.TotalNanoAIU < 0 {
				return false, true
			}
			value.nanoAIU += *call.TotalNanoAIU
			if math.IsInf(value.nanoAIU, 0) {
				return false, true
			}
			value.nanoAIUKnown = true
		} else {
			value.nanoAIUComplete = false
		}
		sum[call.Model] = value
	}
	matches := completeCalls && len(sum) == len(totals)
	seen := map[string]bool{}
	for _, total := range totals {
		if strings.TrimSpace(total.Model) == "" || seen[total.Model] {
			return false, true
		}
		seen[total.Model] = true
		value, ok := sum[total.Model]
		if !ok {
			if completeCalls {
				return false, true
			}
			value = modelUsage{}
		}
		if total.InputTokens != nil && *total.InputTokens < 0 || total.OutputTokens != nil && *total.OutputTokens < 0 ||
			total.CacheReadTokens != nil && *total.CacheReadTokens < 0 ||
			total.CacheWriteTokens != nil && *total.CacheWriteTokens < 0 ||
			total.TotalNanoAIU != nil && (math.IsNaN(*total.TotalNanoAIU) || math.IsInf(*total.TotalNanoAIU, 0) || *total.TotalNanoAIU < 0) {
			return false, true
		}
		if total.InputTokens == nil || total.OutputTokens == nil || !value.inputKnown || !value.outputKnown {
			matches = false
		} else if *total.InputTokens < value.input || *total.OutputTokens < value.output ||
			completeCalls && (*total.InputTokens != value.input || *total.OutputTokens != value.output) {
			return false, true
		}
		if value.cacheReadKnown && total.CacheReadTokens == nil ||
			value.cacheWriteKnown && total.CacheWriteTokens == nil ||
			value.nanoAIUKnown != (total.TotalNanoAIU != nil) ||
			total.CacheReadTokens != nil && !value.cacheReadComplete ||
			total.CacheWriteTokens != nil && !value.cacheWriteComplete ||
			total.TotalNanoAIU != nil && !value.nanoAIUComplete {
			matches = false
		}
		if total.CacheReadTokens != nil && (*total.CacheReadTokens < value.cacheRead ||
			completeCalls && value.cacheReadComplete && *total.CacheReadTokens != value.cacheRead) ||
			total.CacheWriteTokens != nil && (*total.CacheWriteTokens < value.cacheWrite ||
				completeCalls && value.cacheWriteComplete && *total.CacheWriteTokens != value.cacheWrite) ||
			total.TotalNanoAIU != nil && (value.nanoAIU-*total.TotalNanoAIU > 1e-6 ||
				completeCalls && value.nanoAIUComplete && math.Abs(*total.TotalNanoAIU-value.nanoAIU) > 1e-6) {
			return false, true
		}
	}
	return matches, false
}
