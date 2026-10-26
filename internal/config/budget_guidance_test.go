package config

import (
	"encoding/json"
	"strings"
	"testing"
)

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

func TestBillingContractConfigurationAndEnvironment(t *testing.T) {
	for _, billing := range []string{"provider-limit", "metered", "unknown"} {
		env, present, err := EnvironmentLayer(func(key string) (string, bool) {
			return billing, key == "TAXION_BILLING_CONTRACT"
		})
		if err != nil || !present {
			t.Fatal(err)
		}

		resolved, err := Resolve([]Layer{{Source: "user", Input: []byte(validConfig)},
			{Source: "project", Input: []byte("version: 1\nbilling:\n  contract: unknown\n")}, env})
		if err != nil || resolved.Config.Billing.Contract != billing ||
			resolved.Sources["billing.contract"] != "environment" {
			t.Fatal("billing contract did not follow configuration precedence", resolved, err)
		}
	}
	for _, input := range []string{
		"version: 1\nbilling:\n  contract: unlimited\n",
		"version: 1\nbilling:\n  contract: metered\nexecution:\n  budgetGuidance: provider\n",
		"version: 1\nbilling:\n  contract: false\n",
	} {
		if _, err := ResolvePartial([]Layer{{Source: "project", Input: []byte(input)}}); err == nil {
			t.Fatal("invalid or contradictory billing configuration accepted", input)
		}
	}
	c, err := Load([]byte(validConfig))
	body, marshalErr := json.Marshal(c)
	if err != nil || marshalErr != nil || strings.Contains(string(body), "billing") {
		t.Fatal("legacy configuration acquired a billing declaration", err, marshalErr)
	}
}

func TestAllBillingFieldsFollowConfigurationPrecedence(t *testing.T) {
	values := map[string]string{
		"TAXION_BILLING_CONTRACT": "metered", "TAXION_BILLING_OVERAGE": "allowed",
		"TAXION_BILLING_AUTO_RECHARGE": "enabled", "TAXION_BILLING_PAID_FALLBACK": "unknown",
	}
	env, present, err := EnvironmentLayer(func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	})
	if err != nil || !present {
		t.Fatal(err)
	}
	resolved, err := Resolve([]Layer{{Source: "user", Input: []byte(validConfig)},
		{Source: "project", Input: []byte("version: 1\nbilling:\n  contract: provider-limit\n  overage: denied\n  autoRecharge: disabled\n  paidFallback: disabled\n")}, env})
	if err != nil {
		t.Fatal(err)
	}
	for field, want := range map[string]string{
		"billing.contract": "metered", "billing.overage": "allowed",
		"billing.autoRecharge": "enabled", "billing.paidFallback": "unknown",
	} {
		if resolved.Config.Values()[field] != want || resolved.Sources[field] != "environment" {
			t.Fatal("billing override ignored", field, resolved)
		}
		if _, err := ValuesLayer("invalid", map[string]string{field: "invalid"}); err == nil {
			t.Fatal("invalid billing field accepted", field)
		}
	}
}

func TestLegacyConfigJSONRemainsByteCompatible(t *testing.T) {
	c, err := Load([]byte(validConfig))
	if err != nil {
		t.Fatal(err)
	}
	const legacy = `{"version":1,"project":{"id":"project-a"},"repository":{"provider":"gitlab","host":"gitlab.example.com","path":"group/subgroup/application","account":"developer-a","credentialRef":"scm-credential"},"runtime":{"provider":"copilot","account":"developer-b","credentialRef":"ai-credential","contractRef":"company-contract"},"execution":{"requester":"developer-a","worker":"local-worker","approvalRef":"human-decision-1"}}`
	for _, endpoints := range []bool{false, true} {
		want := legacy
		if endpoints {
			c.Endpoints = Endpoints{"https://gitlab.example.com/api/v4", "https://gitlab.example.com", "https://gitlab.example.com", "gitlab.example.com", "22", "7"}
			want = strings.TrimSuffix(legacy, "}") + `,"endpoints":{"apiBase":"https://gitlab.example.com/api/v4","webBase":"https://gitlab.example.com","httpsCloneBase":"https://gitlab.example.com","sshHost":"gitlab.example.com","sshPort":"22","projectId":"7"}}`
		}
		got, err := json.Marshal(c)
		if err != nil || string(got) != want {
			t.Fatal("legacy configuration bytes changed", string(got), err)
		}
	}
}
