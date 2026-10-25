package core

import (
	"fmt"
	"slices"
)

type BudgetGuidancePolicy struct {
	Mode                string   `json:"mode"`
	Source              string   `json:"source"`
	AllowedModes        []string `json:"allowedModes"`
	AccountReadApproved bool     `json:"accountReadApproved,omitempty"`
	AccountAllocation   string   `json:"accountAllocation,omitempty"`
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
		if (mode != "off" && mode != "flow" && mode != "account") || seen[mode] {
			return fmt.Errorf("budget guidance requires unique off, flow or account modes")
		}
		seen[mode] = true
	}
	if slices.Contains(p.AllowedModes, "account") != p.AccountReadApproved {
		return fmt.Errorf("account guidance requires explicit account read approval")
	}
	if p.AccountAllocation != "" && (p.AccountAllocation != "reset-daily" || !p.AccountReadApproved) {
		return fmt.Errorf("account allocation requires reset-daily and approved account guidance")
	}
	return nil
}

func CloneBudgetGuidance(p *BudgetGuidancePolicy) *BudgetGuidancePolicy {
	if p == nil {
		return nil
	}
	copy := *p
	copy.AllowedModes = slices.Clone(p.AllowedModes)
	return &copy
}

func AccountQuotaBudget(p *BudgetGuidancePolicy) bool {
	return p != nil && p.Validate() == nil && p.Mode == "account" &&
		p.AccountReadApproved && p.AccountAllocation == "reset-daily"
}

func NewBudgetGuidance(configured, override string) (*BudgetGuidancePolicy, error) {
	mode, source := configured, "configuration"
	if mode == "" {
		mode, source = "flow", "default"
	}
	if override != "" {
		mode, source = override, "request"
	}
	p := &BudgetGuidancePolicy{Mode: mode, Source: source, AllowedModes: []string{"off", "flow"}}
	if mode == "account" {
		p.AllowedModes = append(p.AllowedModes, "account")
		p.AccountReadApproved = true
	}
	return p, p.Validate()
}
