package runtime

import (
	"fmt"
	"math"
	"strings"

	"github.com/hiono/taxion/internal/core"
)

// UsageCheckpoint subsumes preceding receipts; it is never another API call.
type UsageCheckpoint struct {
	UsageTotals          []core.DispatchUsage `json:"usageTotals,omitempty"`
	TotalNanoAIU         *float64             `json:"totalNanoAIU,omitempty"`
	AICreditsStatus      *string              `json:"aiCreditsStatus,omitempty"`
	TotalPremiumRequests *float64             `json:"totalPremiumRequests,omitempty"`
	Watermarks           map[string]int64     `json:"watermarks,omitempty"`
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
		if total.TotalNanoAIU != nil {
			if math.IsNaN(*total.TotalNanoAIU) || math.IsInf(*total.TotalNanoAIU, 0) || *total.TotalNanoAIU < 0 {
				return fail()
			}
			modelKnown += *total.TotalNanoAIU
		}
	}
	if math.IsNaN(known) || math.IsInf(known, 0) || math.IsInf(modelKnown, 0) ||
		core.UsageBelowKnown(calls, checkpoint.UsageTotals) {
		return fail()
	}
	if checkpoint.TotalNanoAIU != nil &&
		(math.IsNaN(*checkpoint.TotalNanoAIU) || math.IsInf(*checkpoint.TotalNanoAIU, 0) ||
			*checkpoint.TotalNanoAIU < 0 || *checkpoint.TotalNanoAIU+1e-6 < max(known, modelKnown)) {
		return fail()
	}
	if len(checkpoint.UsageTotals) == 0 && checkpoint.TotalNanoAIU == nil &&
		len(checkpoint.Watermarks) == 0 && checkpoint.AICreditsStatus == nil {
		return checkpoint, true, nil
	}
	if len(calls) == 0 && len(checkpoint.UsageTotals) == 0 &&
		(checkpoint.TotalNanoAIU == nil || *checkpoint.TotalNanoAIU == 0) {
		for _, watermark := range checkpoint.Watermarks {
			if watermark != 0 {
				return checkpoint, false, nil
			}
		}
		return checkpoint, true, nil
	}
	matches, _ := core.CompareUsage(calls, checkpoint.UsageTotals)
	if checkpoint.AICreditsStatus != nil && *checkpoint.AICreditsStatus != "complete" {
		matches = false
	}
	for _, call := range calls {
		if call.Scope != "" || call.AICreditsStatus != nil && *call.AICreditsStatus != "complete" {
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
