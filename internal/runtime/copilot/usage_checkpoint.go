package copilot

import (
	"encoding/json"
	"math"
	"slices"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
)

func normalizeUsageCheckpoint(data *sdk.SessionUsageCheckpointData) runtime.Event {
	fail := runtime.Event{Type: runtime.EventFailed, Payload: "invalid native usage checkpoint"}
	if data == nil || math.IsNaN(data.TotalNanoAiu) || math.IsInf(data.TotalNanoAiu, 0) || data.TotalNanoAiu < 0 {
		return fail
	}
	checkpoint := runtime.UsageCheckpoint{Watermarks: data.UsageAccountingWatermarks,
		TotalPremiumRequests: data.TotalPremiumRequests}
	if data.TotalNanoAiu > 0 {
		checkpoint.TotalNanoAIU = &data.TotalNanoAiu
	}
	if snapshot := data.AccountingSnapshot; snapshot != nil {
		if snapshot.AiCreditsStatus != nil {
			value := string(*snapshot.AiCreditsStatus)
			checkpoint.AICreditsStatus = &value
		}
		for agent := range snapshot.AgentMetrics {
			if agent != "main" {
				return runtime.Event{Type: runtime.EventUnclassified}
			}
		}
		if snapshot.TotalNanoAiu != nil {
			if math.Abs(*snapshot.TotalNanoAiu-data.TotalNanoAiu) > 1e-6 {
				return fail
			}
			checkpoint.TotalNanoAIU = snapshot.TotalNanoAiu
		}
		models := make([]string, 0, len(snapshot.ModelMetrics))
		for model := range snapshot.ModelMetrics {
			models = append(models, model)
		}
		slices.Sort(models)
		for _, model := range models {
			metric := snapshot.ModelMetrics[model]
			var status *string
			if metric.AiCreditsStatus != nil {
				value := string(*metric.AiCreditsStatus)
				status = &value
			}
			checkpoint.UsageTotals = append(checkpoint.UsageTotals, core.DispatchUsage{
				Model: model, InputTokens: &metric.Usage.InputTokens, OutputTokens: &metric.Usage.OutputTokens,
				CacheReadTokens: &metric.Usage.CacheReadTokens, CacheWriteTokens: &metric.Usage.CacheWriteTokens,
				TotalNanoAIU: metric.TotalNanoAiu, AICreditsStatus: status,
			})
		}
	}
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return fail
	}
	if _, _, err := runtime.DecodeUsageCheckpoint(string(payload), "", nil); err != nil {
		return fail
	}
	return runtime.Event{Type: runtime.EventUsageCheckpoint, Payload: string(payload)}
}
