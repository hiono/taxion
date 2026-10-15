package copilot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"testing"
)

func TestCreditLimitValidationBindingAndSDKConfiguration(t *testing.T) {
	for _, limit := range []float64{-1, 1, 29.99, math.NaN(), math.Inf(1), math.Inf(-1)} {
		cfg := testConfig(t)
		cfg.MaxAICredits = limit
		if _, err := New(cfg); err == nil {
			t.Fatalf("invalid credit limit %v accepted", limit)
		}
	}
	cfg := testConfig(t)
	unlimited, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer unlimited.Close()
	if unlimited.sessionConfig(nil).SessionLimits != nil || unlimited.resumeConfig(nil).SessionLimits != nil {
		t.Fatal("legacy configuration acquired a limit")
	}
	legacy, err := json.Marshal(struct {
		Role             string
		SystemPrompt     string
		WorkingDirectory string
		CLIPath          string
		BaseDirectory    string
		AvailableTools   []string
		ResponseSchema   map[string]any
		PermissionPolicy string
		DiscoveryPolicy  string
	}{
		unlimited.config.Role, unlimited.config.SystemPrompt, unlimited.config.WorkingDirectory,
		unlimited.config.CLIPath, unlimited.config.BaseDirectory, unlimited.config.AvailableTools,
		unlimited.config.ResponseSchema, "coder-worktree-write-v1", "native-cli-discovery-v2",
	})
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(legacy)
	if unlimited.ConfigHash() != hex.EncodeToString(hash[:]) {
		t.Fatal("zero limit changed the native-default session hash")
	}
	for _, limit := range []float64{30, 30.5, 60} {
		cfg.MaxAICredits = limit
		limited, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer limited.Close()
		if limited.ConfigHash() == unlimited.ConfigHash() {
			t.Fatal("credit policy omitted from durable session binding")
		}
		for _, actual := range []*float64{
			limited.sessionConfig(nil).SessionLimits.MaxAiCredits,
			limited.resumeConfig(nil).SessionLimits.MaxAiCredits,
		} {
			if actual == nil || *actual != limit {
				t.Fatal("start/resume lost configured credit ceiling")
			}
			*actual = 900
		}
		if limited.config.MaxAICredits != limit || *limited.sessionLimits().MaxAiCredits != limit {
			t.Fatal("caller mutation changed adapter credit policy")
		}
		if err := limited.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
