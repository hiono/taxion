package runtime

import (
	"encoding/json"
	"testing"

	"github.com/hiono/taxion/internal/core"
)

func TestUsageCheckpointSeparatesCoveredReceiptsFromLaterCalls(t *testing.T) {
	var calls []core.DispatchUsage
	if err := json.Unmarshal([]byte(`[
		{"model":"test","inputTokens":10,"outputTokens":5,"cacheReadTokens":0,"cacheWriteTokens":0,"totalNanoAIU":3,"aiCreditsStatus":"complete","accounting":{"sourceSessionId":"session","sequence":1,"usageId":"first"}},
		{"model":"test","inputTokens":2,"outputTokens":1,"cacheReadTokens":0,"cacheWriteTokens":0,"totalNanoAIU":2,"aiCreditsStatus":"complete","accounting":{"sourceSessionId":"session","sequence":2,"usageId":"later"}}
	]`), &calls); err != nil {
		t.Fatal(err)
	}

	payload := `{"usageTotals":[{"model":"test","inputTokens":10,"outputTokens":5,"cacheReadTokens":0,"cacheWriteTokens":0,"totalNanoAIU":3,"aiCreditsStatus":"complete"}],"totalNanoAIU":3,"aiCreditsStatus":"complete","watermarks":{"session":1}}`
	checkpoint, complete, err := DecodeUsageCheckpoint(payload, "session", calls)
	if err != nil || !complete || checkpoint.KnownNanoAIU(calls) != 5 {
		t.Fatal("late snapshot lost or double-counted the subsequent call", checkpoint, complete, err)
	}
	calls[1].TotalNanoAIU = nil
	if _, complete, err := DecodeUsageCheckpoint(payload, "session", calls); err != nil || complete {
		t.Fatal("unknown tail was rejected or certified complete", complete, err)
	}
	calls[0].InputTokens = calls[0].OutputTokens
	*calls[0].InputTokens = 20
	if _, _, err := DecodeUsageCheckpoint(payload, "session", calls); err == nil {
		t.Fatal("contradictory covered receipt was ignored")
	}
}

func TestUsageSummaryReconcilesScopedSnapshotsWithoutInventingCalls(t *testing.T) {
	call := `{"model":"test","inputTokens":10,"outputTokens":5,"cacheReadTokens":0,"cacheWriteTokens":0,"totalNanoAIU":3,"aiCreditsStatus":"complete","accounting":{"sourceSessionId":"session","sequence":1,"usageId":"first"}}`
	checkpoint := `{"usageTotals":[{"model":"test","inputTokens":10,"outputTokens":5,"cacheReadTokens":0,"cacheWriteTokens":0,"totalNanoAIU":3,"aiCreditsStatus":"complete"}],"totalNanoAIU":3,"aiCreditsStatus":"complete","watermarks":{"session":1}}`
	for _, mode := range []string{"snapshot-only", "both", "duplicate", "missing-cost", "missing-input", "unknown-provider", "unknown-zero", "explicit-zero", "partial-earlier", "partial-later"} {
		t.Run(mode, func(t *testing.T) {
			var calls []core.DispatchUsage
			var snapshots []core.UsageCheckpoint
			if mode != "snapshot-only" && mode != "unknown-zero" && mode != "explicit-zero" {
				if err := json.Unmarshal([]byte("["+call+"]"), &calls); err != nil {
					t.Fatal(err)
				}
			}
			if err := json.Unmarshal([]byte("["+checkpoint+"]"), &snapshots); err != nil {
				t.Fatal(err)
			}
			cost, tokens, status := 3.0, int64(15), "complete"
			switch mode {
			case "duplicate":
				calls = append(calls, calls[0])
			case "missing-cost":
				calls[0].TotalNanoAIU = nil
			case "missing-input":
				calls[0].InputTokens = nil
			case "unknown-provider":
				calls[0].Provider = &core.UsageModelProvider{ID: "not-in-snapshot", Kind: "openai"}
				status = "partial"
			case "unknown-zero":
				snapshots = []core.UsageCheckpoint{{}}
				status, tokens = "unavailable", 0
			case "explicit-zero":
				zero, complete := 0.0, "complete"
				snapshots = []core.UsageCheckpoint{{TotalNanoAIU: &zero, AICreditsStatus: &complete}}
				cost, tokens = 0, 0
			case "partial-earlier", "partial-later":
				partial := "partial"
				watermark := int64(0)
				if mode == "partial-later" {
					watermark, status = 2, "partial"
				}
				snapshots = append(snapshots, core.UsageCheckpoint{
					AICreditsStatus: &partial, Watermarks: map[string]int64{"session": watermark},
				})
			}
			before, _ := json.Marshal(calls)
			summary, err := core.SummarizeUsage("session", calls, snapshots)
			if err != nil || summary.CreditsStatus != status {
				t.Fatal("snapshot completeness was misclassified", mode, summary.CreditsStatus, err)
			}
			if mode == "unknown-zero" {
				if summary.NanoAIU != nil || summary.InputOutputTokens != nil {
					t.Fatal("missing usage was replaced with zero")
				}
			} else if summary.NanoAIU == nil || *summary.NanoAIU != cost {
				t.Fatal("snapshot and receipts were lost or counted twice", summary.NanoAIU)
			}
			if tokens != 0 && (summary.InputOutputTokens == nil || *summary.InputOutputTokens != tokens) {
				t.Fatal("snapshot tokens did not subsume individual tokens", summary.InputOutputTokens)
			}
			after, _ := json.Marshal(calls)
			if string(before) != string(after) {
				t.Fatal("derived summary changed the observed calls")
			}
		})
	}
}
