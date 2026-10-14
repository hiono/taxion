package config

import "testing"

func TestBudgetGuidanceLayersAndEnvironment(t *testing.T) {
	environment, present, err := EnvironmentLayer(func(key string) (string, bool) {
		return "account", key == "TAXION_EXECUTION_BUDGET_GUIDANCE"
	})
	if err != nil || !present {
		t.Fatal(err)
	}
	resolved, err := Resolve([]Layer{{Source: "user", Input: []byte(validConfig)},
		{Source: "project", Input: []byte("version: 1\nexecution:\n  budgetGuidance: off\n")}, environment})
	if err != nil || resolved.Config.Execution.BudgetGuidance != "account" ||
		resolved.Sources["execution.budgetGuidance"] != "environment" {
		t.Fatal("guidance did not follow existing configuration precedence", resolved, err)
	}
	for _, text := range []string{"auto", "\"\"", "true", "null", "\" flow\""} {
		_, err := Resolve([]Layer{{Source: "user", Input: []byte(validConfig)},
			{Source: "project", Input: []byte("version: 1\nexecution:\n  budgetGuidance: " + text + "\n")}})
		if err == nil {
			t.Fatal("invalid guidance accepted", text)
		}
	}
}
