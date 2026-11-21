package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrUnsupportedCapability   = errors.New("Provider capability is not verified for this configuration")
	ErrCredentialUnavailable   = errors.New("Provider credential is unavailable")
	ErrProviderOperationFailed = errors.New("Provider operation failed")
	ErrProviderInvalidResult   = errors.New("Provider returned an invalid result")
	ErrProviderInvalidRequest  = errors.New("Provider request is invalid")
	ErrStopUnconfirmed         = errors.New("Provider stop is not confirmed")
	ErrResumeUnconfirmed       = errors.New("Provider Session reattachment is not confirmed")
)

type Runtime struct {
	adapter  Adapter
	broker   CredentialBroker
	identity Identity
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
	if err := request.Validate(); err != nil {
		return StartResult{}, ErrProviderInvalidRequest
	}
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return StartResult{}, err
	}
	if !capabilities.SupportsSafeStart() {
		return StartResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (StartResult, error) {
		result, err := r.adapter.Start(ctx, request, lease)
		if err != nil {
			return StartResult{}, err
		}
		leaked, err := containsCredential(lease, result.SessionID, result.ReceiptID)
		if err != nil {
			return StartResult{}, err
		}
		if leaked {
			return StartResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return result, nil
	})
	if err != nil {
		return StartResult{}, safeAdapterError(err)
	}
	if result.OperationID != request.OperationID || !validIdentifier(result.SessionID) ||
		!validIdentifier(result.ReceiptID) || !validSHA256(result.EvidenceSHA256) {
		return StartResult{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Send(ctx context.Context, request TurnRequest) (TurnResult, error) {
	if err := request.Validate(); err != nil {
		return TurnResult{}, ErrProviderInvalidRequest
	}
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return TurnResult{}, err
	}
	if !capabilities.SupportsSafeSend() {
		return TurnResult{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (TurnResult, error) {
		result, err := r.adapter.Send(ctx, request, lease)
		if err != nil {
			return TurnResult{}, err
		}
		leaked, err := containsCredential(lease, result.ReceiptID, result.Output)
		if err != nil {
			return TurnResult{}, err
		}
		if leaked {
			return TurnResult{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return result, nil
	})
	if err != nil {
		return TurnResult{}, safeAdapterError(err)
	}
	if result.OperationID != request.OperationID || result.TurnID != request.TurnID ||
		result.SessionID != request.SessionID ||
		(result.Status != "COMPLETED" && result.Status != "FAILED" && result.Status != "UNKNOWN") ||
		result.TokensUsed > request.MaxTokens {
		return TurnResult{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	if result.Status == "UNKNOWN" {
		if result.TokensUsed != 0 || result.Output != "" {
			return TurnResult{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
		}
		return result, nil
	}
	if !validIdentifier(result.ReceiptID) || !validSHA256(result.EvidenceSHA256) {
		return TurnResult{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Stop(ctx context.Context, request StopRequest) (StopResult, error) {
	if !validIdentifier(request.OperationID) || !validIdentifier(request.SessionID) {
		return StopResult{}, ErrProviderInvalidRequest
	}
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
	if result.OperationID != request.OperationID || result.SessionID != request.SessionID ||
		!result.Stopped || !validSHA256(result.EvidenceSHA256) {
		return StopResult{}, errors.Join(ErrStopUnconfirmed, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Resume(ctx context.Context, request ResumeRequest) (ResumeResult, error) {
	if !validIdentifier(request.OperationID) || !validIdentifier(request.SessionID) {
		return ResumeResult{}, ErrProviderInvalidRequest
	}
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
	if result.OperationID != request.OperationID || result.SessionID != request.SessionID ||
		!result.Attached || !validSHA256(result.EvidenceSHA256) {
		return ResumeResult{}, errors.Join(ErrResumeUnconfirmed, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Lookup(ctx context.Context, request LookupRequest) (LookupResult, error) {
	if err := request.Validate(); err != nil {
		return LookupResult{}, ErrProviderInvalidRequest
	}
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
	if result.OperationID != request.OperationID || !validIdentifier(result.Status) ||
		!validSHA256(result.EvidenceSHA256) ||
		(request.SessionID != "" && result.SessionID != request.SessionID) ||
		(request.TurnID != "" && result.TurnID != request.TurnID) ||
		(result.Found && (!validIdentifier(result.SessionID) || !validIdentifier(result.ReceiptID))) {
		return LookupResult{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Result(ctx context.Context, request ResultRequest) (Result, error) {
	if !validIdentifier(request.OperationID) || !validIdentifier(request.SessionID) ||
		(request.TurnID != "" && !validIdentifier(request.TurnID)) {
		return Result{}, ErrProviderInvalidRequest
	}
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return Result{}, err
	}
	if !capabilities.HasEvidenceAtLeast(CapabilityFinalOutput, EvidenceLive) {
		return Result{}, ErrUnsupportedCapability
	}
	result, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (Result, error) {
		result, err := r.adapter.Result(ctx, request, lease)
		if err != nil {
			return Result{}, err
		}
		leaked, err := containsCredential(lease, result.Content)
		if err != nil {
			return Result{}, err
		}
		if leaked {
			return Result{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
		}
		return result, nil
	})
	if err != nil {
		return Result{}, safeAdapterError(err)
	}
	if result.OperationID != request.OperationID || result.SessionID != request.SessionID ||
		result.TurnID != request.TurnID || sha256Hex(result.Content) != result.ResultSHA256 ||
		!validSHA256(result.EvidenceSHA256) {
		return Result{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) Usage(ctx context.Context, request UsageRequest) (Usage, error) {
	if err := request.Validate(); err != nil {
		return Usage{}, ErrProviderInvalidRequest
	}
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
	if result.OperationID != request.OperationID || result.SessionID != request.SessionID ||
		result.TurnID != request.TurnID || !validIdentifier(result.UsageID) ||
		!validSHA256(result.EvidenceSHA256) {
		return Usage{}, errors.Join(ErrProviderInvalidResult, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown})
	}
	return result, nil
}

func (r *Runtime) ReplayEvents(ctx context.Context, request ReplayEventsRequest) (EventPage, error) {
	if err := request.Validate(); err != nil {
		return EventPage{}, ErrProviderInvalidRequest
	}
	capabilities, err := r.liveCapabilities(ctx)
	if err != nil {
		return EventPage{}, err
	}
	if !capabilities.SupportsEventReplay() {
		return EventPage{}, ErrUnsupportedCapability
	}
	page, err := withCredential(ctx, r.broker, r.identity, func(lease CredentialLease) (EventPage, error) {
		return r.adapter.ReplayEvents(ctx, request, lease)
	})
	if err != nil {
		return EventPage{}, safeAdapterError(err)
	}
	if err := page.Validate(request); err != nil {
		return EventPage{}, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	return page, nil
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

func containsCredential(lease CredentialLease, values ...string) (bool, error) {
	found := false
	err := lease.WithSecret(func(secret []byte) error {
		if len(secret) == 0 {
			return nil
		}
		for _, value := range values {
			if bytes.Contains([]byte(value), secret) {
				found = true
				break
			}
		}
		return nil
	})
	if err != nil {
		return false, ErrCredentialUnavailable
	}
	return found, nil
}

func withCredential[T any](ctx context.Context, broker CredentialBroker, identity Identity, call func(CredentialLease) (T, error)) (result T, err error) {
	lease, acquireErr := broker.Acquire(ctx, identity)
	if acquireErr != nil || lease == nil {
		if lease != nil {
			_ = lease.Close()
		}
		return result, ErrCredentialUnavailable
	}
	result, callErr := call(lease)
	if closeErr := lease.Close(); closeErr != nil {
		var zero T
		return zero, &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
	}
	return result, callErr
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
	return &OperationError{Class: FailureAmbiguous, Code: OperationCodeUnknown}
}
