package core

import (
	"math"
	"testing"
)

func TestSettingsResolveAndProviderStopConditions(t *testing.T) {
	for _, test := range []struct {
		name     string
		settings BillingSettings
		stops    bool
	}{
		{"default", BillingSettings{}, true},
		{"explicit", BillingSettings{"provider-limit", "denied", "disabled", "disabled"}, true},
		{"metered", BillingSettings{Contract: "metered"}, false},
		{"unknown", BillingSettings{Contract: "unknown"}, false},
		{"overage", BillingSettings{Overage: "allowed"}, false},
		{"overage-unknown", BillingSettings{Overage: "unknown"}, false},
		{"recharge", BillingSettings{AutoRecharge: "enabled"}, false},
		{"recharge-unknown", BillingSettings{AutoRecharge: "unknown"}, false},
		{"fallback", BillingSettings{PaidFallback: "enabled"}, false},
		{"fallback-unknown", BillingSettings{PaidFallback: "unknown"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := test.settings.Resolve()
			if err != nil || !resolved.Complete() || resolved.ProviderStops() != test.stops {
				t.Fatal(resolved, err)
			}
			again, err := resolved.Resolve()
			if err != nil || again != resolved || resolved.CheckSelection(test.settings) != nil {
				t.Fatal("billing resolution is not stable", err)
			}
			for _, changed := range []BillingSettings{
				{Contract: "metered"}, {Overage: "allowed"},
				{AutoRecharge: "enabled"}, {PaidFallback: "enabled"},
			} {
				next, _ := changed.Resolve()
				if next != resolved && resolved.CheckSelection(changed) == nil {
					t.Fatal("changed declaration reused approval", next)
				}
			}
		})
	}
	for _, invalid := range []BillingSettings{
		{Contract: "unlimited"}, {Overage: "true"}, {AutoRecharge: "off"}, {PaidFallback: "auto"},
	} {
		if _, err := invalid.Resolve(); err == nil {
			t.Fatal("invalid billing setting accepted", invalid)
		}
	}
}

func TestBudgetSourcesPreserveLegacyAndExplicitCaps(t *testing.T) {
	for _, test := range []struct {
		source                                   string
		account                                  bool
		cap                                      float64
		accountApproved, providerApproved, valid bool
	}{
		{"", false, 10, false, false, true},
		{"", false, 0, false, false, false},
		{"", true, 0, true, false, true},
		{"", true, 1, true, false, false},
		{"", true, 0, false, true, false},
		{ProviderStopBudgetSource, false, 0, false, true, true},
		{ProviderStopBudgetSource, false, 1, false, true, false},
		{ProviderStopBudgetSource, true, 0, true, true, false},
		{ProviderStopBudgetSource, false, 0, true, false, false},
		{"unknown", false, 0, true, true, false},
		{"", false, math.NaN(), false, false, false},
		{"", false, math.Inf(1), false, false, false},
		{"", false, -1, false, false, false},
	} {
		if (ValidateBillingBudget(test.source, test.account, test.cap, test.accountApproved, test.providerApproved) == nil) != test.valid {
			t.Fatal("incorrect budget source validation", test)
		}
	}
}
