package config

import (
	"strings"
	"testing"
)

func TestResolvePriorityAndFieldOrigins(t *testing.T) {
	resolved, err := Resolve([]Layer{
		{Source: "user", Input: []byte(validConfig)},
		{Source: "project", Input: []byte("version: 1\nruntime:\n  account: project-account\n  contractRef: project-contract\n")},
		{Source: "invocation", Input: []byte("version: 1\nruntime:\n  account: selected-account\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Config.Runtime.Account != "selected-account" ||
		resolved.Config.Runtime.ContractRef != "project-contract" ||
		resolved.Config.Repository.Account != "developer-a" {
		t.Fatal("overrides replaced omitted values or crossed identity domains")
	}
	for field, source := range map[string]string{
		"runtime.account": "invocation", "runtime.contractRef": "project",
		"repository.account": "user", "execution.approvalRef": "user",
	} {
		if resolved.Sources[field] != source {
			t.Fatalf("%s has wrong source", field)
		}
	}
	if len(resolved.Sources) != len(resolved.Config.fields()) {
		t.Fatal("required field provenance is incomplete")
	}
	if resolved.Config.Execution.BudgetGuidance != "" || resolved.Sources["execution.budgetGuidance"] != "" {
		t.Fatal("optional guidance must preserve legacy configuration identity")
	}
}

func TestResolveRejectsInvalidLayersBeforeOverrides(t *testing.T) {
	for _, input := range []string{
		"version: 2\n", "runtime:\n  account: selected\n",
		"version: 1\nruntime:\n  account: \"\"\n",
		"version: 1\nruntime:\n  account: null\n",
		"version: 1\nruntime:\n  provider: unknown\n",
		"version: 1\nrepository:\n  host: user:secret@example.com\n",
		"version: 1\nrepository:\n  path: owner/../repo\n",
		"version: 1\ntoken: secret-value\n",
	} {
		_, err := Resolve([]Layer{
			{Source: "user", Input: []byte(input)},
			{Source: "invocation", Input: []byte(validConfig)},
		})
		if err == nil || strings.Contains(err.Error(), "secret") {
			t.Fatalf("invalid lower layer hidden or leaked: %v", err)
		}
	}
	for _, layers := range [][]Layer{
		nil,
		{{Source: "user", Input: []byte("version: 1\n")}},
		{{Input: []byte(validConfig)}},
		{{Source: "same", Input: []byte(validConfig)}, {Source: "same", Input: []byte(validConfig)}},
	} {
		if _, err := Resolve(layers); err == nil {
			t.Fatal("invalid or incomplete resolution accepted")
		}
	}
}
