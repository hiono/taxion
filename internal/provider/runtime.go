package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrUnsupportedCapability  = errors.New("Provider capability is not verified for this configuration")
	ErrCredentialUnavailable   = errors.New("Provider credential is unavailable")
	ErrProviderOperationFailed = errors.New("Provider operation failed")
	ErrProviderInvalidResult  = errors.New("Provider returned an invalid result")
	ErrStopUnconfirmed         = errors.New("Provider stop is not confirmed")
	ErrResumeUnconfirmed       = errors.New("Provider Session reattachment is not confirmed")
)

type Runtime struct {
	adapter   Adapter
	broker    CredentialBroker
	identity  Identity
}

func NewRuntime(adapter Adapter, broker CredentialBroker) (*Runtime, error) {
	if adapter == nil || broker == nil {
		return nil, errors.New("Provider Adapter and CredentialBroker are required")
	}
	identity := adapter.Identity()
	if err := identity.Validate(); err != nil {
		return nil, fmt.Errorf("invalid Provider Adapter identity: %w", err)
	}
	return &Runtime{adapter: adapter, broker: broker, identity: identity}, nil
}

func (r *Runtime) Identity() Identity {
	return r.identity
}

func (r *Runtime) Capabilities(ctx context.Context) (Capabilities, error) {
	capabilities, err := r.adapter.Capabilities(ctx)
	if err != nil {
		return Capabilities{}, ErrUnsupportedCapability
	}
	if err := capabilities.Validate(r.identity); err != nil {
		return Capabilities{}, ErrUnsupportedCapability
	}
	return capabilities, nil
}

func (r *Runtime) Start(ctx context.Context, request StartRequest) (StartResult, error) {
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return StartResult{}, err
	}
	if !capabilities.SupportsSafeStart() {
		return StartResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (StartResult, error) {
		return r.adapter.Start(ctx, request, lease)
	})
	if err != nil {
		return StartResult{}, safeAdapterError(err)
	}
	if strings.TrimSpace(result.SessionID) == "" || strings.TrimSpace(result.ReceiptID) == "" || !validSHA256(result.EvidenceSHA256) {
		return StartResult{}, ErrProviderInvalidResult
	}
	return result, nil
}

func (r *Runtime) Send(ctx context.Context, request TurnRequest) (TurnResult, error) {
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return TurnResult{}, err
	}
	if !capabilities.SupportsSafeSend() {
		return TurnResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (TurnResult, error) {
		return r.adapter.Send(ctx, request, lease)
	})
	if err != nil {
		return TurnResult{}, safeAdapterError(err)
	}
	if strings.TrimSpace(result.ReceiptID) == "" || !validSHA256(result.EvidenceSHA256) ||
		(result.Status != "COMPLETED" && result.Status != "FAILED" && result.Status != "UNKNOWN") ||
		result.TokensUsed > request.MaxTokens {
		return TurnResult{}, ErrProviderInvalidResult
	}
	return result, nil
}

func (r *Runtime) Stop(ctx context.Context, request StopRequest) (StopResult, error) {
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return StopResult{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilityStopProof, EvidenceLive) {
		return StopResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (StopResult, error) {
		return r.adapter.Stop(ctx, request, lease)
	})
	if err != nil {
		return StopResult{}, safeAdapterError(err)
	}
	if !result.Stopped || !validSHA256(result.EvidenceSHA256) {
		return StopResult{}, ErrStopUnconfirmed
	}
	return result, nil
}

func (r *Runtime) Resume(ctx context.Context, request ResumeRequest) (ResumeResult, error) {
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return ResumeResult{}, err
	}
	if !capabilities.SupportsCrossProcessReattachment() {
		return ResumeResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (ResumeResult, error) {
		return r.adapter.Resume(ctx, request, lease)
	})
	if err != nil {
		return ResumeResult{}, safeAdapterError(err)
	}
	if !result.Attached || !validSHA256(result.EvidenceSHA256) {
		return ResumeResult{}, ErrResumeUnconfirmed
	}
	return result, nil
}

func (r *Runtime) Lookup(ctx context.Context, request LookupRequest) (LookupResult, error) {
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return LookupResult{}, err
	}
	capability := CapabilityStartLookup
	if request.TurnID != "" {
		capability = CapabilitySendLookup
	}
	if !capabilities.HasEvidenceAtLeast(capability, EvidenceLive) {
		return LookupResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (LookupResult, error) {
		return r.adapter.Lookup(ctx, request, lease)
	})
	if err != nil {
		return LookupResult{}, safeAdapterError(err)
	}
	if result.Found && (strings.TrimSpace(result.Status) == "" || !validSHA256(result.EvidenceSHA256)) {
		return LookupResult{}, ErrProviderInvalidResult
	}
	return result, nil
}

func (r *Runtime) Result(ctx context.Context, request ResultRequest) (Result, error) {
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return Result{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilityFinalOutput, EvidenceLive) {
		return Result{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (Result, error) {
		return r.adapter.Result(ctx, request, lease)
	})
	if err != nil {
		return Result{}, safeAdapterError(err)
	}
	if !validSHA256(result.ResultSHA256) || !validSHA256(result.EvidenceSHA256) {
		return Result{}, ErrProviderInvalidResult
	}
	return result, nil
}

func (r *Runtime) Usage(ctx context.Context, request UsageRequest) (Usage, error) {
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return Usage{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilityUsageIdentity, EvidenceLive) ||
		!capabilities.HasEvidenceAtLeast(CapabilityUsageCompleteness, EvidenceLive) {
		return Usage{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (Usage, error) {
		return r.adapter.Usage(ctx, request, lease)
	})
	if err != nil {
		return Usage{}, safeAdapterError(err)
	}
	if strings.TrimSpace(result.UsageID) == "" || !validSHA256(result.EvidenceSHA256) {
		return Usage{}, ErrProviderInvalidResult
	}
	return result, nil
}

func (r *Runtime) liveCapabilities(ctx context.Context) (Capabilities, error) {
	if r.adapter.Identity() != r.identity {
		return Capabilities{}, ErrUnsupportedCapability
	}
	capabilities, err := r.adapter.Capabilities(ctx)
	if err != nil {
		return Capabilities{}, ErrUnsupportedCapability
	}
	if err := capabilities.ValidateForLiveExecution(r.identity); err != nil {
		return Capabilities{}, ErrUnsupportedCapability
	}
	return capabilities, nil
}

func withCredential[T any](ctx context.Context, broker CredentialBroker, identity Identity, call func(CredentialLease) (T, error)) (result T, err error) {
	lease, err := broker.Acquire(ctx, identity)
	if err != nil || lease == nil {
		return result, ErrCredentialUnavailable
	}
	defer func() {
		if closeErr := lease.Close(); closeErr != nil {
			err = errors.Join(err, ErrCredentialUnavailable)
		}
	}()
	return call(lease)
}

func safeAdapterError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrCredentialUnavailable) {
		return ErrCredentialUnavailable
	}
	var operationError *OperationError
	if errors.As(err, &operationError) && operationError != nil &&
		(operationError.Class == FailureDefinitive || operationError.Class == FailureAmbiguous) &&
		validOperationCode(operationError.Code) {
		return &OperationError{Class: operationError.Class, Code: operationError.Code}
	}
	return ErrProviderOperationFailed
}
