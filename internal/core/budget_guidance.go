package core

import (
	"fmt"
	"slices"

	"github.com/hiono/taxion/internal/billing"
)

type BudgetGuidancePolicy struct {
	Mode                string            `json:"mode"`
	Source              string            `json:"source"`
	AllowedModes        []string          `json:"allowedModes"`
	AccountReadApproved bool              `json:"accountReadApproved,omitempty"`
	AccountAllocation   string            `json:"accountAllocation,omitempty"`
	Billing             *billing.Settings `json:"billing,omitempty"`
}

func (p *BudgetGuidancePolicy) Validate() error {
	if p == nil {
		return nil
	}
	if p.Source != "default" && p.Source != "configuration" && p.Source != "request" {
		return fmt.Errorf("budget guidance requires default, configuration or request source")
	}
	if len(p.AllowedModes) == 0 || !slices.Contains(p.AllowedModes, p.Mode) {
		return fmt.Errorf("budget guidance selected mode must be explicitly allowed")
	}
	seen := map[string]bool{}
	for _, mode := range p.AllowedModes {
		if (mode != "off" && mode != "flow" && mode != "account" && mode != "provider") || seen[mode] {
			return fmt.Errorf("budget guidance requires unique off, flow, account or provider modes")
		}
		seen[mode] = true
	}
	if slices.Contains(p.AllowedModes, "account") != p.AccountReadApproved {
		return fmt.Errorf("account guidance requires explicit account read approval")
	}
	if p.AccountAllocation != "" && (p.AccountAllocation != "reset-daily" || !p.AccountReadApproved) {
		return fmt.Errorf("account allocation requires reset-daily and approved account guidance")
	}
	if p.Billing != nil && !p.Billing.Complete() {
		return fmt.Errorf("budget guidance requires a complete billing declaration")
	}
	if slices.Contains(p.AllowedModes, "provider") && (p.Billing == nil || !p.Billing.ProviderStops()) {
		return fmt.Errorf("provider guidance requires declared provider-limit, denied overage, disabled recharge and paid fallback")
	}
	return nil
}

func CloneBudgetGuidance(p *BudgetGuidancePolicy) *BudgetGuidancePolicy {
	if p == nil {
		return nil
	}
	copy := *p
	copy.AllowedModes = slices.Clone(p.AllowedModes)
	if p.Billing != nil {
		settings := *p.Billing
		copy.Billing = &settings
	}
	return &copy
}

func AccountQuotaBudget(p *BudgetGuidancePolicy) bool {
	return p != nil && p.Validate() == nil && p.Mode == "account" &&
		p.AccountReadApproved && p.AccountAllocation == "reset-daily"
}

func NewBudgetGuidance(configured, override string) (*BudgetGuidancePolicy, error) {
	return NewBillingBudgetGuidance(configured, override, billing.Settings{})
}

func ProviderBudget(p *BudgetGuidancePolicy) bool {
	return p != nil && p.Validate() == nil && p.Mode == "provider" && p.Billing != nil && p.Billing.ProviderStops()
}

func ExternalBudget(p *BudgetGuidancePolicy) bool {
	return AccountQuotaBudget(p) || ProviderBudget(p)
}

func (p *BudgetGuidancePolicy) CheckBillingContract(selected billing.Settings) error {
	if p == nil {
		return nil
	}
	if err := p.Validate(); err != nil {
		return err
	}
	if p.Billing != nil {
		return p.Billing.CheckSelection(selected)
	}
	return nil
}

func NewBillingBudgetGuidance(configured, override string, selected billing.Settings) (*BudgetGuidancePolicy, error) {
	settings, err := selected.Resolve()
	if err != nil {
		return nil, err
	}
	mode, source := configured, "configuration"
	if mode == "" {
		mode, source = "provider", "default"
		if !settings.ProviderStops() {
			mode, source = "flow", "configuration"
		}
	}
	if override != "" {
		mode, source = override, "request"
	}
	p := &BudgetGuidancePolicy{Mode: mode, Source: source, AllowedModes: []string{"off", "flow"}, Billing: &settings}
	if mode == "account" {
		p.AllowedModes = append(p.AllowedModes, "account")
		p.AccountReadApproved = true
		p.AccountAllocation = "reset-daily"
	}
	if mode == "provider" {
		p.AllowedModes = append(p.AllowedModes, "provider")
	}
	return p, p.CheckBillingContract(selected)
}
