package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/hiono/taxion/internal/billing"
	"github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/core"
	"github.com/hiono/taxion/internal/runtime"
	"github.com/hiono/taxion/internal/store"
)

type budgetObservation struct {
	Mode                 string                 `json:"mode"`
	Source               string                 `json:"source"`
	ObservedAt           time.Time              `json:"observedAt"`
	FlowID               string                 `json:"flowId,omitempty"`
	FlowState            string                 `json:"flowState"`
	UsedTokens           *int64                 `json:"usedTokens,omitempty"`
	ReportedNanoAIU      *float64               `json:"reportedNanoAIU,omitempty"`
	AICreditsStatus      string                 `json:"aiCreditsStatus"`
	Deadline             *time.Time             `json:"deadline,omitempty"`
	UnfinishedRoles      []string               `json:"unfinishedRoles,omitempty"`
	SessionCreditCeiling *float64               `json:"sessionCreditCeiling,omitempty"`
	Account              *runtime.QuotaSnapshot `json:"account,omitempty"`
	AccountAllocation    string                 `json:"accountAllocation,omitempty"`
	AllocationBasis      *quotaAllocationBasis  `json:"allocationBasis,omitempty"`
	RemainingFloor       *float64               `json:"remainingFloor,omitempty"`
	Billing              *billing.Settings      `json:"billing,omitempty"`
	BillingVerification  string                 `json:"billingVerification,omitempty"`
}

func (c *Controller) prepareBudgetGuidance(ctx context.Context, dispatch core.GraphDispatch, override string) (*budgetObservation, error) {
	policy := dispatch.Instruction.BudgetGuidance
	if policy == nil {
		if override != "" {
			return nil, fmt.Errorf("taxion: legacy Dispatch has no approved budget guidance; reapproval is required")
		}
		return nil, nil
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	mode, source := policy.Mode, policy.Source
	if override != "" {
		if !slices.Contains(policy.AllowedModes, override) {
			return nil, fmt.Errorf("taxion: budget guidance override is outside the approved modes")
		}
		mode, source = override, "request"
	}
	observation := &budgetObservation{Mode: mode, Source: source, ObservedAt: time.Now().UTC(), FlowState: "not-applicable"}
	if policy.Billing != nil {
		settings := *policy.Billing
		observation.Billing, observation.BillingVerification = &settings, "human-declaration"
	}
	state, err := c.store.LoadWorkspace()
	if err != nil {
		return nil, err
	}
	for _, flow := range state.Graph.ReviewFlows {
		if !slices.Contains(flow.DispatchIDs, dispatch.ID) {
			continue
		}
		if flow.Budget.AccountQuotaOnly && (mode != "account" || !core.AccountQuotaBudget(policy)) {
			return nil, fmt.Errorf("taxion: account-quota-only flow cannot disable its approved residual-quota control")
		}
		if flow.Budget.BudgetSource == billing.ProviderStop && (mode != "provider" || !core.ProviderBudget(policy)) {
			return nil, fmt.Errorf("taxion: provider-budget-only flow cannot change its approved billing policy")
		}
		if flow.Status != "RUNNING" || !time.Now().Before(flow.Deadline) {
			return nil, fmt.Errorf("taxion: budget guidance cannot send after flow stop or deadline")
		}
		used, known := flowTokens(state.Graph, flow)
		if mode != "off" {
			observation.FlowID, observation.FlowState = flow.ID, "usage-unavailable"
			observation.Deadline = &flow.Deadline
			if known {
				observation.FlowState = "available"
				observation.UsedTokens = &used
			}
			nanoAIU, status := flowNanoAIU(state.Graph, flow)
			observation.ReportedNanoAIU, observation.AICreditsStatus = nanoAIU, status
			observation.UnfinishedRoles = []string{dispatch.Recipient}
			if dispatch.Recipient == "CODER" {
				observation.UnfinishedRoles = append(observation.UnfinishedRoles, "REVIEWER")
			}
		}
		break
	}
	if provider, ok := c.runtime.(interface{ SessionCreditCeiling() float64 }); ok && mode != "off" {
		if value := provider.SessionCreditCeiling(); value > 0 {
			observation.SessionCreditCeiling = &value
		}
	}
	if mode != "account" {
		return observation, nil
	}
	if !policy.AccountReadApproved || dispatch.Configuration == nil {
		return nil, fmt.Errorf("taxion: account quota read requires approved account guidance and bound configuration")
	}
	var selected config.Config
	if err := core.DecodeGraphInput(strings.NewReader(string(dispatch.Configuration.Selection)), &selected); err != nil {
		return nil, err
	}
	observation, err = readBudgetQuota(ctx, c.runtime, selected, observation)
	if err == nil && observation != nil && observation.Account != nil {
		now := time.Now().UTC()
		if policy.AccountAllocation == "reset-daily" {
			refreshQuotaState(observation, now)
			err = allocateResetDaily(observation, accountAllocationReceipts(state.Records), now)
		} else {
			allocateAccountQuota(observation.Account, accountDayStart(state.Records.Events, observation.Account, now), now)
		}
	}
	return observation, err
}

func accountAllocationReceipts(records store.Records) []store.RuntimeReceipt {
	receipts := slices.Clone(records.Events)
	for _, record := range records.Requests {
		if record.Kind == "planning" {
			receipts = append(receipts, store.RuntimeReceipt{
				Type: "budget-guidance", Payload: record.Payload, Hash: record.Hash,
			})
		}
	}
	return receipts
}

func readBudgetQuota(ctx context.Context, worker runtime.Identity, selected config.Config,
	observation *budgetObservation) (*budgetObservation, error) {
	quota := runtime.QuotaSnapshot{Provider: selected.Runtime.Provider, Account: selected.Runtime.Account,
		State: "unsupported", Source: "runtime-capability", Scope: "selected account", Unit: "unknown",
		ObservedAt: time.Now().UTC(), Reason: "runtime does not expose account quota"}
	if reader, ok := worker.(runtime.AccountQuota); ok {
		readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		if observation.Deadline != nil {
			var end context.CancelFunc
			readCtx, end = context.WithDeadline(readCtx, *observation.Deadline)
			defer end()
		}
		var err error
		quota, err = reader.ReadAccountQuota(readCtx, selected.Runtime.Account)
		cancel()
		if err != nil {
			quota.State, quota.Reason = "unavailable", "runtime rejected the selected-account quota read; details withheld"
			observation.Account = &quota
			return observation, err
		}
	}
	if quota.Account != selected.Runtime.Account || quota.Provider != selected.Runtime.Provider {
		quota.State, quota.Reason = "unavailable", "quota account/provider mismatch; details withheld"
		observation.Account = &quota
		return observation, fmt.Errorf("taxion: quota observation account/provider mismatch")
	}
	switch quota.State {
	case "available":
		if quota.ObservedAt.IsZero() || quota.Resource == "" || quota.Scope == "" || quota.Unit == "" || quota.Source == "" ||
			quota.Used != nil && *quota.Used < 0 ||
			quota.Entitlement != nil && *quota.Entitlement < -1 ||
			quota.RemainingPercentage != nil && (math.IsNaN(*quota.RemainingPercentage) ||
				math.IsInf(*quota.RemainingPercentage, 0) || *quota.RemainingPercentage < 0 || *quota.RemainingPercentage > 100) {
			quota.State, quota.Reason = "unavailable", "runtime returned incomplete or invalid quota values"
			quota.Used, quota.Entitlement, quota.Remaining, quota.RemainingPercentage = nil, nil, nil, nil
			quota.Unlimited, quota.ExecutionDenied = false, false
		}
	case "unsupported", "unavailable", "stale":
		if quota.Reason == "" {
			return nil, fmt.Errorf("taxion: nonavailable quota requires an explicit reason")
		}
	default:
		return nil, fmt.Errorf("taxion: runtime returned an invalid quota observation state")
	}
	observation.Account = &quota
	return observation, nil
}

func refreshQuotaState(observation *budgetObservation, now time.Time) {
	if observation == nil || observation.Account == nil || observation.Account.State != "available" {
		return
	}
	quota := observation.Account
	if now.Sub(quota.ObservedAt) > time.Minute || quota.ObservedAt.After(now.Add(time.Second)) ||
		quota.ExpiresAt != nil && !now.Before(*quota.ExpiresAt) {
		quota.State, quota.Reason = "stale", "quota observation is outside its freshness interval"
	}
}

func (c *Controller) recordBudgetGuidance(dispatch core.GraphDispatch, observation *budgetObservation, phase string, sendErr error) error {
	if observation == nil {
		return nil
	}
	supplement, err := budgetSupplement(observation)
	if err != nil {
		return err
	}
	body, err := json.Marshal(struct {
		Observation *budgetObservation `json:"observation"`
		Phase       string             `json:"phase"`
		SendError   bool               `json:"sendError,omitempty"`
		Supplement  string             `json:"supplement"`
	}{observation, phase, sendErr != nil, supplement})
	if err != nil {
		return err
	}
	return c.store.UpdateWorkspace(func(state *store.Workspace) error {
		current, ok := state.Graph.GraphDispatch(dispatch.ID)
		if !ok || current.RuntimeSessionID != dispatch.RuntimeSessionID ||
			current.RuntimeConfigHash != c.runtime.ConfigHash() ||
			!reflect.DeepEqual(current.Instruction.BudgetGuidance, dispatch.Instruction.BudgetGuidance) {
			return fmt.Errorf("taxion: budget observation binding changed")
		}
		var sequence uint64 = 1
		for _, receipt := range state.Records.Events {
			if receipt.DispatchID == dispatch.ID && receipt.Type == "budget-guidance" {
				sequence++
			}
		}
		state.Records.Events = append(state.Records.Events, store.RuntimeReceipt{
			DispatchID: dispatch.ID, SessionID: dispatch.RuntimeSessionID, Type: "budget-guidance", StreamID: "budget-guidance",
			Sequence: sequence, At: time.Now().UTC(), Payload: string(body), Hash: receiptHash(string(body)),
		})
		return nil
	})
}

func budgetSupplement(observation *budgetObservation) (string, error) {
	if observation == nil || observation.Mode == "off" {
		return "", nil
	}
	body, err := json.Marshal(observation)
	if err != nil {
		return "", err
	}
	return "\n\nTaxion resource observation (advisory, not new authorization or a token-count spending cap):\n" +
		string(body) + "\nThe approved goal and all mandatory acceptance criteria are the priority. Complete them while using as few AI credits as practical: reuse saved results and investigation, avoid optional work and repeated exploration, and preserve resources for remaining essential roles. " +
		"Never trade away required validation or independent review. Treat reported per-call nano-AIU, account quota requests, and session credit ceilings as separate native units; do not estimate one from token counts or convert between them. " +
		"Unknown/stale quota or unavailable credit reporting is not zero, free, unlimited or proof that work is unaffordable.", nil
}

func isolatedBudgetGuidance(ctx context.Context, worker runtime.Identity, policy *core.BudgetGuidancePolicy,
	selected config.Config) (*budgetObservation, string, error) {
	if policy == nil {
		return nil, "", nil
	}
	if err := policy.Validate(); err != nil {
		return nil, "", err
	}
	if err := policy.CheckBillingContract(selected.Billing); err != nil {
		return nil, "", err
	}
	observation := &budgetObservation{Mode: policy.Mode, Source: policy.Source,
		ObservedAt: time.Now().UTC(), FlowState: "not-applicable"}
	if policy.Billing != nil {
		settings := *policy.Billing
		observation.Billing, observation.BillingVerification = &settings, "human-declaration"
	}
	if policy.Mode != "off" {
		observation.AICreditsStatus = "unavailable"
		observation.UnfinishedRoles = []string{"ORCHESTRATOR"}
		if deadline, ok := ctx.Deadline(); ok {
			observation.Deadline = &deadline
		}
		if ceiling, ok := worker.(interface{ SessionCreditCeiling() float64 }); ok {
			if value := ceiling.SessionCreditCeiling(); value > 0 {
				observation.SessionCreditCeiling = &value
			}
		}
	}
	if identity, ok := worker.(runtime.BudgetGuidanceIdentity); ok &&
		!reflect.DeepEqual(identity.BudgetGuidancePolicy(), policy) {
		return observation, "", fmt.Errorf("taxion: isolated runtime guidance differs from approved Task")
	}
	if policy.Mode == "account" {
		var err error
		observation, err = readBudgetQuota(ctx, worker, selected, observation)
		if err != nil {
			return observation, "", err
		}
		if policy.AccountAllocation == "reset-daily" {
			refreshQuotaState(observation, time.Now().UTC())
			if err := allocateResetDaily(observation, nil, time.Now().UTC()); err != nil {
				return observation, "", err
			}
		} else {
			allocateAccountQuota(observation.Account, nil, time.Now().UTC())
		}
	}
	refreshQuotaState(observation, time.Now().UTC())
	text, err := budgetSupplement(observation)
	if policy.Mode == "account" {
		if reason := quotaExecutionStop(observation.Account); reason != "" {
			return observation, text, &budgetStopError{reason: reason}
		}
	}
	return observation, text, err
}

func monitorIsolatedQuota(ctx context.Context, worker runtime.Identity, selected config.Config,
	initial *budgetObservation, now time.Time) (*budgetObservation, error) {
	if initial == nil || initial.Mode != "account" {
		return nil, nil
	}
	current := &budgetObservation{Mode: initial.Mode, Source: initial.Source,
		ObservedAt: now, FlowState: initial.FlowState, Deadline: initial.Deadline,
		AccountAllocation: initial.AccountAllocation}
	current, err := readBudgetQuota(ctx, worker, selected, current)
	if err != nil {
		return current, &budgetStopError{reason: "isolated account quota read failed; remaining allowance is unverified"}
	}
	refreshQuotaState(current, now)
	if reason := quotaExecutionStop(current.Account); reason != "" {
		return current, &budgetStopError{reason: reason}
	}
	before, after := initial.Account, current.Account
	if before == nil || after == nil || before.State == "available" && after.State != "available" ||
		before.Resource != after.Resource || before.Unit != after.Unit ||
		before.Scope != after.Scope || before.Unlimited != after.Unlimited ||
		(before.ResetAt == nil) != (after.ResetAt == nil) ||
		before.ResetAt != nil && (!before.ResetAt.Equal(*after.ResetAt) || !now.Before(*before.ResetAt)) {
		return current, &budgetStopError{reason: "isolated account quota basis or cycle changed"}
	}
	if initial.AccountAllocation == "reset-daily" {
		payload, err := json.Marshal(struct {
			Observation *budgetObservation `json:"observation"`
		}{initial})
		if err != nil {
			return current, err
		}
		err = allocateResetDaily(current, []store.RuntimeReceipt{{
			Type: "budget-guidance", Payload: string(payload), Hash: receiptHash(string(payload)),
		}}, now)
		if err != nil {
			return current, &budgetStopError{reason: "isolated account allocation basis is invalid"}
		}
	} else {
		allocateAccountQuota(current.Account, before.RemainingPercentage, now)
	}
	if reason := quotaExecutionStop(current.Account); reason != "" {
		return current, &budgetStopError{reason: reason}
	}
	return current, nil
}
