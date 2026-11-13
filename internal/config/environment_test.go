package config

import (
	"strings"
	"testing"
)

func TestEnvironmentOverridesPreserveTypesAndSources(t *testing.T) {
	values := map[string]string{
		"TAXION_RUNTIME_ACCOUNT":      "123",
		"TAXION_RUNTIME_CONTRACT_REF": "selected-contract",
		"GH_TOKEN":                    "private-secret",
		"TAXION_TOKEN":                "private-secret",
	}
	lookup := func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}
	layer, present, err := EnvironmentLayer(lookup)
	if err != nil || !present {
		t.Fatal("environment layer missing", err)
	}
	if strings.Contains(string(layer.Input), "private-secret") {
		t.Fatal("credential input copied into configuration")
	}
	result, err := Resolve([]Layer{{Source: "project", Input: []byte(validConfig)}, layer})
	if err != nil || result.Config.Runtime.Account != "123" ||
		result.Sources["runtime.account"] != "environment" ||
		result.Config.Repository.Account != "developer-a" {
		t.Fatal("environment values coerced or unrelated fields replaced", err)
	}
	result, err = Resolve([]Layer{
		{Source: "project", Input: []byte(validConfig)}, layer,
		{Source: "invocation", Input: []byte("version: 1\nruntime:\n  account: final-account\n")},
	})
	if err != nil || result.Config.Runtime.Account != "final-account" ||
		result.Sources["runtime.contractRef"] != "environment" {
		t.Fatal("invocation priority or inherited environment source lost", err)
	}
}

func TestEnvironmentRejectsInvalidExplicitValues(t *testing.T) {
	for _, values := range []map[string]string{
		{"TAXION_RUNTIME_ACCOUNT": ""},
		{"TAXION_RUNTIME_ACCOUNT": "has whitespace"},
		{"TAXION_RUNTIME_PROVIDER": "unknown"},
		{"TAXION_REPOSITORY_HOST": "user:private-secret@example.com"},
		{"TAXION_REPOSITORY_PATH": "owner/../repo"},
	} {
		lookup := func(name string) (string, bool) {
			value, found := values[name]
			return value, found
		}
		if _, _, err := EnvironmentLayer(lookup); err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatal("invalid environment accepted or leaked")
		}
	}
	if _, present, err := EnvironmentLayer(func(string) (string, bool) { return "", false }); err != nil || present {
		t.Fatal("absent environment created an overriding layer", err)
	}
}
