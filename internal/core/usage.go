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
