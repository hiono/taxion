package copilot

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"

	sdk "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
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
			if data.TotalNanoAiu > 0 && math.Abs(*snapshot.TotalNanoAiu-data.TotalNanoAiu) > 1e-6 {
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
			checkpoint.UsageTotals = append(checkpoint.UsageTotals, normalizeModelUsage(model, metric, status))
		}
	}
	providers := data.ProviderModelMetrics
	if snapshot := data.AccountingSnapshot; snapshot != nil && snapshot.ProviderModelMetrics != nil {
		if providers != nil && !sameProviderMetrics(providers, snapshot.ProviderModelMetrics) {
			return fail
		}
		providers = snapshot.ProviderModelMetrics
	}
	checkpoint.ProviderModelMetrics = normalizeProviderMetrics(providers)
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return fail
	}
	if _, _, err := runtime.DecodeUsageCheckpoint(string(payload), "", nil); err != nil {
		return fail
	}
	return runtime.Event{Type: runtime.EventUsageCheckpoint, Payload: string(payload)}
}

func normalizeModelUsage(model string, metric rpc.UsageMetricsModelMetric, status *string) core.DispatchUsage {
	details := make(map[string]int64, len(metric.TokenDetails))
	for name, detail := range metric.TokenDetails {
		details[name] = detail.TokenCount
	}
	usage := core.DispatchUsage{
		Model: model, InputTokens: &metric.Usage.InputTokens, OutputTokens: &metric.Usage.OutputTokens,
		CacheReadTokens: &metric.Usage.CacheReadTokens, CacheWriteTokens: &metric.Usage.CacheWriteTokens,
		ReasoningTokens: metric.Usage.ReasoningTokens, RequestCount: &metric.Requests.Count,
		RequestCost: &metric.Requests.Cost, TokenDetails: details, CacheExpiresAt: metric.CacheExpiresAt,
		TotalNanoAIU: metric.TotalNanoAiu, AICreditsStatus: status,
	}
	return usage
}

func normalizeUsageShutdown(data *sdk.SessionShutdownData) (runtime.UsageCheckpoint, error) {
	if data == nil {
		return runtime.UsageCheckpoint{}, fmt.Errorf("taxion: missing native usage shutdown")
	}
	checkpoint := runtime.UsageCheckpoint{
		TotalNanoAIU: data.TotalNanoAiu, TotalPremiumRequests: data.TotalPremiumRequests,
		Watermarks: data.UsageAccountingWatermarks,
	}
	for agent := range data.AgentMetrics {
		if agent != "main" {
			return runtime.UsageCheckpoint{}, fmt.Errorf("taxion: shutdown contains child-agent accounting")
		}
	}
	if snapshot := data.AccountingSnapshot; snapshot != nil {
		for agent := range snapshot.AgentMetrics {
			if agent != "main" {
				return runtime.UsageCheckpoint{}, fmt.Errorf("taxion: shutdown snapshot contains child-agent accounting")
			}
		}
		if snapshot.TotalNanoAiu != nil {
			if data.TotalNanoAiu != nil && math.Abs(*data.TotalNanoAiu-*snapshot.TotalNanoAiu) > 1e-6 {
				return runtime.UsageCheckpoint{}, fmt.Errorf("taxion: conflicting shutdown cumulative usage")
			}
			checkpoint.TotalNanoAIU = snapshot.TotalNanoAiu
		}
		if snapshot.AiCreditsStatus != nil {
			status := string(*snapshot.AiCreditsStatus)
			checkpoint.AICreditsStatus = &status
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
			checkpoint.UsageTotals = append(checkpoint.UsageTotals, normalizeModelUsage(model, metric, status))
		}
	}
	if len(checkpoint.UsageTotals) == 0 {
		models := make([]string, 0, len(data.ModelMetrics))
		for model := range data.ModelMetrics {
			models = append(models, model)
		}
		slices.Sort(models)
		for _, model := range models {
			metric := data.ModelMetrics[model]
			usage := core.DispatchUsage{
				Model: model, InputTokens: &metric.Usage.InputTokens, OutputTokens: &metric.Usage.OutputTokens,
				CacheReadTokens: &metric.Usage.CacheReadTokens, CacheWriteTokens: &metric.Usage.CacheWriteTokens,
				ReasoningTokens: metric.Usage.ReasoningTokens, TotalNanoAIU: metric.TotalNanoAiu,
				TokenDetails: map[string]int64{},
			}
			if metric.Requests.Count != nil {
				usage.RequestCount = metric.Requests.Count
			}
			if metric.Requests.Cost != nil {
				usage.RequestCost = metric.Requests.Cost
			}
			for name, detail := range metric.TokenDetails {
				usage.TokenDetails[name] = detail.TokenCount
			}
			checkpoint.UsageTotals = append(checkpoint.UsageTotals, usage)
		}
	}
	providers := data.ProviderModelMetrics
	if snapshot := data.AccountingSnapshot; snapshot != nil && snapshot.ProviderModelMetrics != nil {
		if providers != nil && !sameProviderMetrics(providers, snapshot.ProviderModelMetrics) {
			return runtime.UsageCheckpoint{}, fmt.Errorf("taxion: conflicting shutdown provider/model accounting")
		}
		providers = snapshot.ProviderModelMetrics
	}
	checkpoint.ProviderModelMetrics = normalizeProviderMetrics(providers)
	payload, err := json.Marshal(checkpoint)
	if err != nil {
		return runtime.UsageCheckpoint{}, err
	}
	if _, _, err := runtime.DecodeUsageCheckpoint(string(payload), "", nil); err != nil {
		return runtime.UsageCheckpoint{}, err
	}
	return checkpoint, nil
}

func normalizeProviderMetrics(metrics []rpc.UsageMetricsProviderModelMetric) []core.ProviderModelMetric {
	if len(metrics) == 0 {
		return nil
	}
	result := make([]core.ProviderModelMetric, 0, len(metrics))
	for _, metric := range metrics {
		status := (*string)(nil)
		if metric.Metrics.AiCreditsStatus != nil {
			value := string(*metric.Metrics.AiCreditsStatus)
			status = &value
		}
		provider := (*core.UsageModelProvider)(nil)
		if metric.Provider != nil {
			provider = &core.UsageModelProvider{
				ID: metric.Provider.ID, Kind: string(metric.Provider.Kind), Label: metric.Provider.Label,
			}
		}
		result = append(result, core.ProviderModelMetric{
			ModelID: metric.ModelID, ModelDisplayName: metric.ModelDisplayName, Provider: provider,
			Metrics: core.ProviderModelUsageMetrics{
				AICreditsStatus: status, CacheExpiresAt: metric.Metrics.CacheExpiresAt,
				Requests: core.ProviderModelRequests{
					Cost: metric.Metrics.Requests.Cost, Count: metric.Metrics.Requests.Count,
				},
				TokenDetails: map[string]int64{}, TotalNanoAIU: metric.Metrics.TotalNanoAiu,
				Usage: core.ProviderModelTokenUsage{
					CacheReadTokens:  metric.Metrics.Usage.CacheReadTokens,
					CacheWriteTokens: metric.Metrics.Usage.CacheWriteTokens,
					InputTokens:      metric.Metrics.Usage.InputTokens,
					OutputTokens:     metric.Metrics.Usage.OutputTokens,
					ReasoningTokens:  metric.Metrics.Usage.ReasoningTokens,
				},
			},
		})
		for name, detail := range metric.Metrics.TokenDetails {
			result[len(result)-1].Metrics.TokenDetails[name] = detail.TokenCount
		}
	}
	return result
}

func sameProviderMetrics(left, right []rpc.UsageMetricsProviderModelMetric) bool {
	a, b := normalizeProviderMetrics(left), normalizeProviderMetrics(right)
	sortProviderMetrics(a)
	sortProviderMetrics(b)
	return reflect.DeepEqual(a, b)
}

func sortProviderMetrics(metrics []core.ProviderModelMetric) {
	slices.SortFunc(metrics, func(a, b core.ProviderModelMetric) int {
		return strings.Compare(providerMetricKey(a), providerMetricKey(b))
	})
}

func providerMetricKey(metric core.ProviderModelMetric) string {
	model := ""
	if metric.ModelID != nil {
		model = *metric.ModelID
	}
	provider := ""
	if metric.Provider != nil {
		provider = strings.Join([]string{metric.Provider.ID, metric.Provider.Kind}, "\x00")
	}
	return model + "\x00" + provider
}
