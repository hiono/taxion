package core

import (
	"fmt"
	"math"
)

const ProviderStopBudgetSource = "provider-stop"

type BillingSettings struct {
	Contract     string `yaml:"contract,omitempty" json:"contract,omitempty"`
	Overage      string `yaml:"overage,omitempty" json:"overage,omitempty"`
	AutoRecharge string `yaml:"autoRecharge,omitempty" json:"autoRecharge,omitempty"`
	PaidFallback string `yaml:"paidFallback,omitempty" json:"paidFallback,omitempty"`
}

func (s BillingSettings) Validate() error {
	for _, field := range []struct {
		name, value string
		allowed     []string
	}{
		{"contract", s.Contract, []string{"", "provider-limit", "metered", "unknown"}},
		{"overage", s.Overage, []string{"", "allowed", "denied", "unknown"}},
		{"autoRecharge", s.AutoRecharge, []string{"", "enabled", "disabled", "unknown"}},
		{"paidFallback", s.PaidFallback, []string{"", "enabled", "disabled", "unknown"}},
	} {
		valid := false
		for _, allowed := range field.allowed {
			valid = valid || field.value == allowed
		}
		if !valid {
			return fmt.Errorf("billing.%s has an unsupported value", field.name)
		}
	}
	return nil
}

// Resolve applies defaults only to new proposals. Stored contracts stay untouched.
func (s BillingSettings) Resolve() (BillingSettings, error) {
	if err := s.Validate(); err != nil {
		return BillingSettings{}, err
	}
	if s.Contract == "" {
		s.Contract = "provider-limit"
	}
	if s.Overage == "" {
		s.Overage = "unknown"
		if s.Contract == "provider-limit" {
			s.Overage = "denied"
		}
	}
	if s.AutoRecharge == "" {
		s.AutoRecharge = "disabled"
	}
	if s.PaidFallback == "" {
		s.PaidFallback = "disabled"
	}
	return s, nil
}

func (s BillingSettings) ProviderStops() bool {
	return s.Contract == "provider-limit" && s.Overage == "denied" &&
		s.AutoRecharge == "disabled" && s.PaidFallback == "disabled"
}

func (s BillingSettings) Complete() bool {
	return s.Validate() == nil && s.Contract != "" && s.Overage != "" &&
		s.AutoRecharge != "" && s.PaidFallback != ""
}

func (s BillingSettings) CheckSelection(selected BillingSettings) error {
	resolved, err := selected.Resolve()
	if err != nil {
		return err
	}
	if !s.Complete() || s != resolved {
		return fmt.Errorf("billing settings differ from the approved declaration")
	}
	return nil
}

// BillingExternalBudget preserves the legacy account flag without migrating saved data.
func BillingExternalBudget(source string, accountQuotaOnly bool) bool {
	return source == ProviderStopBudgetSource || accountQuotaOnly
}

func ValidateBillingBudget(source string, accountQuotaOnly bool, ceiling float64, accountApproved, providerApproved bool) error {
	if math.IsNaN(ceiling) || math.IsInf(ceiling, 0) || ceiling < 0 ||
		source != "" && source != ProviderStopBudgetSource ||
		source == ProviderStopBudgetSource && (!providerApproved || accountQuotaOnly || ceiling != 0) ||
		accountQuotaOnly && (!accountApproved || ceiling != 0) ||
		!BillingExternalBudget(source, accountQuotaOnly) && ceiling <= 0 {
		return fmt.Errorf("budget source must match its approved billing or account policy and native ceiling")
	}
	return nil
}
