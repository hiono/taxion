package copilot

import (
	"context"
	"fmt"

	"github.com/github/copilot-sdk/go/rpc"
)

// CheckServiceAccess reads provider-owned access/capacity, not balances or a
// contractual payer. It neither sends a prompt nor changes the selected account.
func (a *Adapter) CheckServiceAccess(ctx context.Context, sessionID string) error {
	handle, err := a.lookup(sessionID)
	if err != nil {
		return err
	}
	handle.opMu.Lock()
	defer handle.opMu.Unlock()
	handle.mu.Lock()
	session, stopped := handle.sdk, handle.stopped
	handle.mu.Unlock()
	if stopped || session == nil {
		return ErrStoppedSession
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	quota, err := session.RPC.Quota.Get(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return fmt.Errorf("taxion: provider service access unavailable; provider details withheld")
	}
	return checkServiceAccess(quota)
}

func checkServiceAccess(quota *rpc.SessionQuotaGetResult) error {
	if quota == nil {
		return fmt.Errorf("taxion: provider service access is unconfirmed")
	}
	confirmed := false
	for _, observation := range quota.ProviderQuotas {
		if observation.Provider.Kind != rpc.ModelProviderKindCopilot {
			continue
		}
		if observation.AcquisitionStatus == nil || *observation.AcquisitionStatus != rpc.ProviderQuotaAcquisitionStatusSucceeded ||
			observation.ObservationKind == nil || *observation.ObservationKind != rpc.ProviderQuotaObservationKindAccountSnapshot {
			return fmt.Errorf("taxion: provider service access observation is unavailable or unconfirmed")
		}
		if observation.HasQuota != nil && !*observation.HasQuota {
			return fmt.Errorf("taxion: provider denied service quota")
		}
		switch observation.AccessState {
		case rpc.ProviderQuotaAccessStateAllowed, rpc.ProviderQuotaAccessStateNotRequired:
		case rpc.ProviderQuotaAccessStateDenied:
			return fmt.Errorf("taxion: provider denied service access")
		default:
			return fmt.Errorf("taxion: provider service access is unconfirmed")
		}
		switch observation.CapacityState {
		case rpc.ProviderQuotaCapacityStateAvailable, rpc.ProviderQuotaCapacityStateUnlimited,
			rpc.ProviderQuotaCapacityStateNotRequired, rpc.ProviderQuotaCapacityStateNotApplicable:
		case rpc.ProviderQuotaCapacityStateExhausted:
			return fmt.Errorf("taxion: provider service capacity is exhausted")
		default:
			return fmt.Errorf("taxion: provider service capacity is unconfirmed")
		}
		confirmed = true
	}
	if !confirmed {
		return fmt.Errorf("taxion: provider service access has no Copilot observation")
	}
	return nil
}
