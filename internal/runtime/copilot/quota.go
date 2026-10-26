package copilot

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/github/copilot-sdk/go/rpc"
	executionconfig "github.com/hiono/taxion/internal/config"
	"github.com/hiono/taxion/internal/runtime"
)

func (a *Adapter) ReadAccountQuota(ctx context.Context, expectedAccount string) (runtime.QuotaSnapshot, error) {
	result := runtime.QuotaSnapshot{
		Provider: "copilot", Account: expectedAccount, Resource: "premium_interactions",
		Scope: "selected account entitlement; cycle not established", Unit: "requests",
		Source: "sdk.account.getQuota", State: "unavailable", ObservedAt: time.Now().UTC(),
	}
	if a.config.BudgetGuidance == nil || !a.config.BudgetGuidance.AccountReadApproved {
		return result, fmt.Errorf("taxion: account quota read is not approved for this adapter")
	}
	var selected executionconfig.Config
	if json.Unmarshal(a.config.Configuration, &selected) != nil || selected.Runtime.Account != expectedAccount {
		return result, fmt.Errorf("taxion: quota account differs from the selected runtime account")
	}
	a.opMu.Lock()
	defer a.opMu.Unlock()
	if a.stopSessionID != "" {
		return result, fmt.Errorf("taxion: stop-only adapter cannot read quota")
	}
	if err := a.ensureStarted(ctx); err != nil {
		result.Reason = "quota client unavailable; provider details withheld"
		return result, nil
	}
	status, err := a.client.GetAuthStatus(ctx)
	if err != nil {
		result.Reason = "authentication status unavailable; provider details withheld"
		return result, nil
	}
	if err := checkAuthenticationStatus(status, expectedAccount); err != nil {
		return result, err
	}
	users, err := a.client.RPC.Account.GetAllUsers(ctx)
	if err != nil || users == nil {
		result.Reason = "selected-account quota refresh credentials unavailable"
		return result, nil
	}
	auth, err := a.client.RPC.Account.GetCurrentAuth(ctx)
	if err != nil || auth == nil {
		result.Reason = "active quota authentication unavailable"
		return result, nil
	}
	request, err := selectedQuotaRequest(*users, expectedAccount, auth.AuthInfo)
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	quota, err := a.client.RPC.Account.GetQuota(ctx, request)
	result.ObservedAt = time.Now().UTC()
	if err != nil || quota == nil {
		result.Reason = "quota read failed or timed out; provider details withheld"
		return result, nil
	}
	snapshot, ok := quota.QuotaSnapshots[result.Resource]
	if !ok {
		result.State, result.Reason = "unsupported", "SDK did not return the selected quota resource"
		return result, nil
	}
	result = mapQuota(result, snapshot)
	stamp := snapshot.ResetDate
	if stamp == nil || stamp.IsZero() {
		result.State, result.Reason = "unavailable", "provider quota observation timestamp unavailable"
	} else {
		result.ObservedAt = stamp.UTC()
		if time.Since(*stamp) > time.Minute || stamp.After(time.Now().Add(time.Second)) {
			result.State, result.Reason = "stale", "provider quota observation is outside its freshness interval"
		}
	}
	// account.getQuota reports the observation time as resetDate; the subscription reset
	// is only exposed on the authenticated user's Copilot snapshot.
	result.ResetAt = nil
	result.ResetAt = quotaResetAt(auth, result.ObservedAt)
	return result, nil
}

// Explicit token selection refreshes quota; the default and selection-ID routes
// may return a cached snapshot. Tokens remain inside the local SDK request.
func selectedQuotaRequest(users rpc.AccountGetAllUsersResult, expectedAccount string, active rpc.AuthInfo) (*rpc.AccountGetQuotaRequest, error) {
	activeType, activeLogin, err := quotaAuthIdentity(active)
	if err != nil || !strings.EqualFold(activeLogin, expectedAccount) {
		return nil, fmt.Errorf("active quota authentication does not match the selected account")
	}
	var request *rpc.AccountGetQuotaRequest
	for _, user := range users {
		kind, login, err := quotaAuthIdentity(user.AuthInfo)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(login, expectedAccount) || kind != activeType {
			continue
		}
		if request != nil || user.Token == nil || *user.Token == "" {
			return nil, fmt.Errorf("selected-account quota refresh credentials missing or ambiguous")
		}
		request = &rpc.AccountGetQuotaRequest{GitHubToken: user.Token}
	}
	if request == nil {
		return nil, fmt.Errorf("selected-account quota refresh identity not found")
	}
	return request, nil
}

func quotaAuthIdentity(info rpc.AuthInfo) (kind, login string, err error) {
	body, err := json.Marshal(info)
	if raw, ok := info.(rpc.RawAuthInfoData); ok {
		body, err = raw.Raw, nil
	}
	var identity struct {
		Type  string `json:"type"`
		Host  string `json:"host"`
		Login string `json:"login"`
		User  *struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	if err != nil || json.Unmarshal(body, &identity) != nil || identity.Type == "" || !githubAuthenticationHost(identity.Host) {
		return "", "", fmt.Errorf("selected-account quota authentication identity is unavailable")
	}
	login = identity.Login
	if identity.User != nil {
		if login != "" && !strings.EqualFold(login, identity.User.Login) {
			return "", "", fmt.Errorf("selected-account quota identity is ambiguous")
		}
		login = identity.User.Login
	}
	if login == "" {
		return "", "", fmt.Errorf("selected-account quota login is unavailable")
	}
	return identity.Type, login, nil
}

func quotaResetAt(auth *rpc.AccountGetCurrentAuthResult, observedAt time.Time) *time.Time {
	if auth == nil || auth.AuthInfo == nil {
		return nil
	}
	body, err := json.Marshal(auth.AuthInfo)
	if raw, ok := auth.AuthInfo.(rpc.RawAuthInfoData); ok {
		body, err = raw.Raw, nil
	}
	if err != nil {
		return nil
	}
	var info struct {
		CopilotUser *struct {
			QuotaResetDateUTC *string `json:"quota_reset_date_utc"`
		} `json:"copilotUser"`
	}
	if json.Unmarshal(body, &info) != nil || info.CopilotUser == nil || info.CopilotUser.QuotaResetDateUTC == nil {
		return nil
	}
	reset, err := time.Parse(time.RFC3339, *info.CopilotUser.QuotaResetDateUTC)
	if err != nil || !reset.After(observedAt) {
		return nil
	}
	reset = reset.UTC()
	return &reset
}

func (a *Adapter) SessionCreditCeiling() float64 {
	return a.config.MaxAICredits
}

func mapQuota(result runtime.QuotaSnapshot, snapshot rpc.AccountQuotaSnapshot) runtime.QuotaSnapshot {
	if snapshot.UsedRequests < 0 || snapshot.EntitlementRequests < -1 ||
		math.IsNaN(snapshot.RemainingPercentage) || math.IsInf(snapshot.RemainingPercentage, 0) ||
		snapshot.RemainingPercentage < 0 || snapshot.RemainingPercentage > 100 {
		result.State, result.Reason = "unavailable", "SDK returned invalid quota values"
		return result
	}
	result.State = "available"
	result.Unlimited = snapshot.IsUnlimitedEntitlement || snapshot.EntitlementRequests == -1
	result.Entitlement, result.Used = &snapshot.EntitlementRequests, &snapshot.UsedRequests
	if !result.Unlimited {
		remaining := snapshot.EntitlementRequests - snapshot.UsedRequests
		result.Remaining = &remaining
	}
	result.RemainingPercentage = &snapshot.RemainingPercentage
	// Overage availability is not user approval to use paid overage.
	result.ExecutionDenied = !result.Unlimited && (snapshot.RemainingPercentage == 0 ||
		snapshot.EntitlementRequests == 0 || snapshot.UsedRequests >= snapshot.EntitlementRequests)
	return result
}
